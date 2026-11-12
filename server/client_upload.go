package server

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const maxClientUploadSize = 64 * 1024 * 1024

type clientFileUpload struct {
	id, name, target string
	file             *os.File
	size, total      int
	next, received   int
}

type fileUploadResult struct {
	UploadID string `json:"uploadId"`
	Name     string `json:"name"`
	Chunk    int    `json:"chunk"`
	Received int    `json:"received"`
	Done     bool   `json:"done"`
	Error    string `json:"error,omitempty"`
}

func (client *Client) cleanupFileUpload() {
	if upload := client.upload; upload != nil {
		upload.file.Close()
		os.Remove(upload.file.Name())
		client.upload = nil
	}
}

// An optional uploadId opts into acknowledged uploads. Legacy clients keep
// their existing wire format. Results use a new server-to-client data prefix
// and are sent only to the originating stream, never to the PTY or its history.
func (sm *SessionManager) handleClientFileUpload(client *Client, payload []byte) {
	var msg UploadFileMessage
	if json.Unmarshal(payload, &msg) != nil {
		return
	}
	if msg.UploadID == "" {
		sm.HandleFileUpload(payload)
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if client == nil || client.readOnly || !sm.clients[client] {
		return
	}
	result := fileUploadResult{UploadID: msg.UploadID, Name: msg.Name, Chunk: msg.Chunk}
	defer func() {
		if result.Error != "" && client.upload != nil && client.upload.id == msg.UploadID {
			client.cleanupFileUpload()
		}
		data, _ := json.Marshal(result)
		enqueueClientMessage(client, append([]byte{'d'}, data...))
	}()
	if len(msg.UploadID) > 64 {
		result.Error = "Invalid upload request"
		return
	}
	if msg.Cancel {
		if client.upload != nil && client.upload.id == msg.UploadID {
			client.cleanupFileUpload()
		}
		return
	}
	if validateUploadFileName(msg.Name) != nil || strings.ContainsAny(msg.Name, "/\\\x00") || msg.Name == "." {
		result.Error = "Invalid upload filename"
		return
	}
	if msg.Size < 0 || msg.Size > maxClientUploadSize || msg.TotalChunks < 1 || msg.TotalChunks > maxClientUploadSize || msg.Chunk < 0 || msg.Chunk >= msg.TotalChunks || len(msg.Data) > 32*1024 {
		result.Error = "Invalid upload size or chunk"
		return
	}
	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil || (len(data) == 0 && msg.Size != 0) {
		result.Error = "Invalid upload data"
		return
	}
	if msg.Chunk == 0 {
		if client.upload != nil {
			result.Error = "Another upload is running"
			return
		}
		workDir, err := sm.slave.GetWorkingDir()
		if err != nil {
			result.Error = "Cannot locate terminal working directory"
			return
		}
		target := filepath.Join(workDir, msg.Name)
		for other := range sm.clients {
			if other.upload != nil && other.upload.target == target {
				result.Error = "This file is being uploaded by another client"
				return
			}
		}
		mode := os.FileMode(0644)
		if stat, err := os.Lstat(target); err == nil {
			if !stat.Mode().IsRegular() {
				result.Error = "Upload destination is not a regular file"
				return
			}
			mode = stat.Mode().Perm()
		} else if !os.IsNotExist(err) {
			result.Error = "Cannot access upload destination"
			return
		}
		file, err := os.CreateTemp(workDir, ".gotty-upload-*")
		if err != nil {
			result.Error = "Cannot create upload file; check directory permissions and free space"
			return
		}
		client.upload = &clientFileUpload{id: msg.UploadID, name: msg.Name, target: target, file: file, size: msg.Size, total: msg.TotalChunks}
		if file.Chmod(mode) != nil {
			result.Error = "Cannot set upload file permissions"
			return
		}
	}
	upload := client.upload
	if upload == nil || upload.id != msg.UploadID || upload.name != msg.Name || upload.size != msg.Size || upload.total != msg.TotalChunks || upload.next != msg.Chunk {
		result.Error = "Upload chunks are out of sequence; retry the upload"
		return
	}
	if upload.received+len(data) > upload.size {
		result.Error = "Uploaded size does not match the file"
		return
	}
	n, err := upload.file.Write(data)
	if err != nil || n != len(data) {
		result.Error = "Cannot write upload file; check free space"
		return
	}
	upload.received += n
	upload.next++
	result.Received = upload.received
	if upload.next == upload.total {
		if upload.received != upload.size {
			result.Error = "Uploaded size does not match the file"
			return
		}
		if upload.file.Sync() != nil {
			result.Error = "Cannot save upload file; check free space"
			return
		}
		if upload.file.Close() != nil || os.Rename(upload.file.Name(), upload.target) != nil {
			result.Error = "Cannot finalize upload file; check directory permissions"
			return
		}
		client.upload = nil
		result.Done = true
	}
}
