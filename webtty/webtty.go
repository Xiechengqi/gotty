package webtty

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pkg/errors"
)

// WebTTY bridges a PTY slave and its PTY master.
// To support text-based streams and side channel commands such as
// terminal resizing, WebTTY uses an original protocol.
type WebTTY struct {
	// PTY Master, which probably a connection to browser
	masterConn Master
	// PTY Slave
	slave Slave

	windowTitle []byte
	permitWrite bool
	columns     int
	rows        int
	reconnect   int // in seconds
	masterPrefs []byte
	decoder     Decoder

	bufferSize int
	writeMutex sync.Mutex

	// File upload state
	uploadFile     *os.File
	uploadFileName string
	uploadChunks   int
	uploadWorkDir  string
}

// New creates a new instance of WebTTY.
// masterConn is a connection to the PTY master,
// typically it's a websocket connection to a client.
// slave is a PTY slave such as a local command with a PTY.
func New(masterConn Master, slave Slave, options ...Option) (*WebTTY, error) {
	wt := &WebTTY{
		masterConn: masterConn,
		slave:      slave,

		permitWrite: false,
		columns:     0,
		rows:        0,

		bufferSize: 1024,
		decoder:    &NullCodec{},
	}

	for _, option := range options {
		option(wt)
	}

	return wt, nil
}

// Close cleans up resources including any open upload files
func (wt *WebTTY) Close() {
	wt.cleanupUploadFile()
}

// Run starts the main process of the WebTTY.
// This method blocks until the context is canceled.
// Note that the master and slave are left intact even
// after the context is canceled. Closing them is caller's
// responsibility.
// If the connection to one end gets closed, returns ErrSlaveClosed or ErrMasterClosed.
func (wt *WebTTY) Run(ctx context.Context) error {
	err := wt.sendInitializeMessage()
	if err != nil {
		return errors.Wrapf(err, "failed to send initializing message")
	}

	errs := make(chan error, 2)

	go func() {
		errs <- func() error {
			buffer := make([]byte, wt.bufferSize)
			for {
				//base64 length
				effectiveBufferSize := wt.bufferSize - 1
				//max raw data length
				maxChunkSize := int(effectiveBufferSize/4) * 3

				n, err := wt.slave.Read(buffer[:maxChunkSize])
				if err != nil {
					return ErrSlaveClosed
				}

				err = wt.handleSlaveReadEvent(buffer[:n])
				if err != nil {
					return err
				}
			}
		}()
	}()

	go func() {
		errs <- func() error {
			buffer := make([]byte, wt.bufferSize)
			for {
				n, err := wt.masterConn.Read(buffer)
				if err != nil {
					return ErrMasterClosed
				}

				err = wt.handleMasterReadEvent(buffer[:n])
				if err != nil {
					return err
				}
			}
		}()
	}()

	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-errs:
	}

	return err
}

func (wt *WebTTY) sendInitializeMessage() error {
	err := wt.masterWrite(append([]byte{SetWindowTitle}, wt.windowTitle...))
	if err != nil {
		return errors.Wrapf(err, "failed to send window title")
	}

	bufSizeMsg, _ := json.Marshal(wt.bufferSize)
	err = wt.masterWrite(append([]byte{SetBufferSize}, bufSizeMsg...))
	if err != nil {
		return errors.Wrapf(err, "failed to send buffer size")
	}

	if wt.reconnect > 0 {
		reconnect, _ := json.Marshal(wt.reconnect)
		err := wt.masterWrite(append([]byte{SetReconnect}, reconnect...))
		if err != nil {
			return errors.Wrapf(err, "failed to set reconnect")
		}
	}

	if wt.masterPrefs != nil {
		err := wt.masterWrite(append([]byte{SetPreferences}, wt.masterPrefs...))
		if err != nil {
			return errors.Wrapf(err, "failed to set preferences")
		}
	}

	return nil
}

func (wt *WebTTY) handleSlaveReadEvent(data []byte) error {
	safeMessage := base64.StdEncoding.EncodeToString(data)
	err := wt.masterWrite(append([]byte{Output}, []byte(safeMessage)...))
	if err != nil {
		return errors.Wrapf(err, "failed to send message to master")
	}

	return nil
}

func (wt *WebTTY) masterWrite(data []byte) error {
	wt.writeMutex.Lock()
	defer wt.writeMutex.Unlock()

	_, err := wt.masterConn.Write(data)
	if err != nil {
		return errors.Wrapf(err, "failed to write to master")
	}

	return nil
}

func (wt *WebTTY) handleMasterReadEvent(data []byte) error {
	if len(data) == 0 {
		return errors.New("unexpected zero length read from master")
	}

	switch data[0] {
	case Input:
		if !wt.permitWrite {
			return nil
		}

		if len(data) <= 1 {
			return nil
		}

		var decodedBuffer = make([]byte, len(data))
		n, err := wt.decoder.Decode(decodedBuffer, data[1:])
		if err != nil {
			return errors.Wrapf(err, "failed to decode received data")
		}

		_, err = wt.slave.Write(decodedBuffer[:n])
		if err != nil {
			return errors.Wrapf(err, "failed to write received data to slave")
		}

	case Ping:
		err := wt.masterWrite([]byte{Pong})
		if err != nil {
			return errors.Wrapf(err, "failed to return Pong message to master")
		}

	case SetEncoding:
		switch string(data[1:]) {
		case "base64":
			wt.decoder = base64.StdEncoding
		case "null":
			wt.decoder = NullCodec{}
		}

	case ResizeTerminal:
		if wt.columns != 0 && wt.rows != 0 {
			break
		}

		if len(data) <= 1 {
			return errors.New("received malformed remote command for terminal resize: empty payload")
		}

		var args argResizeTerminal
		err := json.Unmarshal(data[1:], &args)
		if err != nil {
			return errors.Wrapf(err, "received malformed data for terminal resize")
		}
		rows := wt.rows
		if rows == 0 {
			rows = int(args.Rows)
		}

		columns := wt.columns
		if columns == 0 {
			columns = int(args.Columns)
		}

		wt.slave.ResizeTerminal(columns, rows)

	case UploadFile:
		if !wt.permitWrite {
			return nil
		}

		if len(data) <= 1 {
			return nil
		}

		err := wt.handleUploadFile(data[1:])
		if err != nil {
			return errors.Wrapf(err, "failed to handle file upload")
		}

	case UploadCancel:
		// Cancel ongoing upload and clean up partial file
		if wt.uploadFile != nil {
			wt.uploadFile.Close()
			// Delete the partial file
			if wt.uploadFileName != "" && wt.uploadWorkDir != "" {
				filePath := filepath.Join(wt.uploadWorkDir, wt.uploadFileName)
				os.Remove(filePath)
			}
			wt.uploadFile = nil
			wt.uploadFileName = ""
			wt.uploadChunks = 0
			wt.uploadWorkDir = ""
		}

	default:
		return errors.Errorf("unknown message type `%c`", data[0])
	}

	return nil
}

type argResizeTerminal struct {
	Columns float64
	Rows    float64
}

// UploadFileMessage represents a file upload message from the client
type UploadFileMessage struct {
	Name        string `json:"name"`
	Size        int    `json:"size"`
	Chunk       int    `json:"chunk"`
	TotalChunks int    `json:"totalChunks"`
	Data        string `json:"data"`
}

// handleUploadFile handles file upload from the client
func (wt *WebTTY) handleUploadFile(payload []byte) error {
	var msg UploadFileMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return errors.Wrapf(err, "failed to parse upload file message")
	}

	// Validate filename (relative path only)
	if err := validateFileName(msg.Name); err != nil {
		return errors.Wrapf(err, "invalid filename")
	}
	if msg.TotalChunks <= 0 {
		return errors.New("invalid total chunks")
	}
	if msg.Chunk < 0 || msg.Chunk >= msg.TotalChunks {
		return errors.Errorf("invalid chunk index %d", msg.Chunk)
	}

	// Handle first chunk - create/open file
	if msg.Chunk == 0 {
		// Close previous upload file if any
		if wt.uploadFile != nil {
			wt.uploadFile.Close()
			wt.uploadFile = nil
		}

		wt.uploadFileName = msg.Name
		wt.uploadChunks = 0

		// Get the PTY slave's current working directory
		workDir, err := wt.slave.GetWorkingDir()
		if err != nil {
			return errors.Wrapf(err, "failed to get working directory")
		}
		wt.uploadWorkDir = workDir

		filePath := filepath.Join(workDir, msg.Name)
		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return errors.Wrapf(err, "failed to create file %s", filePath)
		}
		wt.uploadFile = file
	}

	// Skip if not the expected chunk
	if msg.Chunk != wt.uploadChunks {
		return errors.Errorf("out-of-order chunk: expected %d, got %d", wt.uploadChunks, msg.Chunk)
	}

	// Decode base64 data
	fileData, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		return errors.Wrapf(err, "failed to decode file data")
	}

	// Write chunk to file
	if wt.uploadFile != nil {
		_, err = wt.uploadFile.Write(fileData)
		if err != nil {
			return errors.Wrapf(err, "failed to write to file")
		}
	}

	wt.uploadChunks++

	// Handle last chunk - close file
	if msg.Chunk == msg.TotalChunks-1 {
		if wt.uploadFile != nil {
			wt.uploadFile.Close()
			wt.uploadFile = nil
		}
		wt.uploadWorkDir = ""
	}

	return nil
}

// validateFileName validates that the filename is safe (relative path only, no path traversal)
func validateFileName(name string) error {
	if name == "" {
		return errors.New("empty filename")
	}

	// Allow relative paths (starting with ./ or just a name)
	// Disallow absolute paths
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return errors.New("absolute paths are not allowed")
	}

	// Check for path traversal attempts (parent directory references)
	if strings.Contains(name, "..") {
		return errors.New("path traversal is not allowed")
	}

	// Check for reserved names on Unix-like systems
	baseName := filepath.Base(name)
	if baseName == "." || baseName == ".." {
		return errors.New("invalid filename")
	}

	return nil
}

// cleanupUploadFile ensures any open upload file is closed
func (wt *WebTTY) cleanupUploadFile() {
	if wt.uploadFile != nil {
		wt.uploadFile.Close()
		wt.uploadFile = nil
	}
}
