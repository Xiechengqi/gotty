package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultCols = 100
	defaultRows = 35
)

type Client struct {
	id              string
	conn            *websocket.Conn
	send            chan []byte
	ready           chan struct{}
	init            InitMessage
	replay          replaySnapshot
	readOnly        bool
	key             string
	lastActivity    time.Time
	cursorQuerySeen [2]int64
}

type replaySnapshot struct {
	Epoch        string
	Mode         string
	FromOffset   int64
	EndOffset    int64
	Data         []byte
	Columns      int
	Rows         int
	Sequence     int64
	FromSequence int64
	Events       []streamEvent
	Truncated    bool
}

type resizeDimensions struct {
	Columns        int
	Rows           int
	DeviceClass    string
	PixelRatio     float64
	ViewportWidth  int
	ViewportHeight int
	UpdatedAt      time.Time
	Visible        bool
}

type terminalState struct {
	ActiveCols    int    `json:"activeCols"`
	ActiveRows    int    `json:"activeRows"`
	Policy        string `json:"policy"`
	LeaderClient  string `json:"leaderClientId"`
	Reason        string `json:"reason"`
	SourceClient  string `json:"sourceClientId"`
	Connected     int    `json:"connectedClients"`
	ResizeEnabled bool   `json:"resizeEnabled"`
	SizeControl   bool   `json:"sizeControl"`
}

type SessionManager struct {
	slave       Slave
	clients     map[*Client]bool
	broadcast   chan broadcastMessage
	register    chan *Client
	unregister  chan *Client
	history     *HistoryBuffer
	lineHistory *LineBuffer
	mu          sync.RWMutex
	ctx         context.Context
	generation  int64

	resizePolicy       string
	leaderSelect       string
	leaderSwitch       string
	leaderIdle         time.Duration
	leaderGrace        time.Duration
	leaderKey          string
	leaderExpires      time.Time
	resizeDue          time.Time
	resizeEvents       chan resizeEvent
	stream             *terminalStream
	minCols            int
	maxCols            int
	minRows            int
	maxRows            int
	resizeDebounce     time.Duration
	historyReplayBytes int
	configuredCols     int
	configuredRows     int
	leaderClientID     string
	lastResizeAt       time.Time
	lastResizeSource   string
	activeCols         int
	activeRows         int
	nextClientSeq      int64
	clientOrder        map[string]int64
	clientSizes        map[string]resizeDimensions

	// File upload state
	uploadFile     interface{}
	uploadFileName string
	uploadChunks   int
	uploadWorkDir  string
}

func NewSessionManager(ctx context.Context, slave Slave, options *Options) *SessionManager {
	configuredCols := options.Width
	configuredRows := options.Height

	if options.ResizePolicy == "fixed" && configuredCols <= 0 {
		configuredCols = defaultCols
	}
	if options.ResizePolicy == "fixed" && configuredRows <= 0 {
		configuredRows = defaultRows
	}

	if configuredCols > 0 {
		configuredCols = clamp(configuredCols, options.MinCols, options.MaxCols)
	}
	if configuredRows > 0 {
		configuredRows = clamp(configuredRows, options.MinRows, options.MaxRows)
	}

	return &SessionManager{
		slave:              slave,
		clients:            make(map[*Client]bool),
		broadcast:          make(chan broadcastMessage, 256),
		register:           make(chan *Client),
		unregister:         make(chan *Client),
		history:            NewHistoryBuffer(10 * 1024 * 1024), // 10MB
		lineHistory:        NewLineBuffer(5000),
		ctx:                ctx,
		resizePolicy:       options.ResizePolicy,
		leaderSelect:       options.LeaderSelect,
		leaderSwitch:       options.LeaderSwitch,
		leaderIdle:         time.Duration(options.LeaderIdleMs) * time.Millisecond,
		leaderGrace:        time.Duration(options.LeaderGraceMs) * time.Millisecond,
		resizeEvents:       make(chan resizeEvent),
		stream:             newTerminalStream(defaultCols, defaultRows, options.HistoryLines),
		minCols:            options.MinCols,
		maxCols:            options.MaxCols,
		minRows:            options.MinRows,
		maxRows:            options.MaxRows,
		resizeDebounce:     time.Duration(options.ResizeDebounceMs) * time.Millisecond,
		historyReplayBytes: options.HistoryReplayBytes,
		configuredCols:     configuredCols,
		configuredRows:     configuredRows,
		clientOrder:        make(map[string]int64),
		clientSizes:        make(map[string]resizeDimensions),
	}
}

func (sm *SessionManager) ReplaceSlave(newSlave Slave) (oldSlave Slave, generation int64, cols int, rows int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	oldSlave = sm.slave
	sm.slave = newSlave
	sm.generation++
	generation = sm.generation
	cols = sm.activeCols
	rows = sm.activeRows

	if sm.history != nil {
		sm.history.Clear()
	}
	if sm.lineHistory != nil {
		sm.lineHistory.Clear()
	}
	if sm.stream != nil {
		sm.stream.Reset(max(cols, 1), max(rows, 1))
	}
	sm.cleanupUploadLocked()

	return oldSlave, generation, cols, rows
}

func (sm *SessionManager) Generation() int64 {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.generation
}

func (sm *SessionManager) IsGeneration(generation int64) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.generation == generation
}

func (sm *SessionManager) CloseSlave() error {
	sm.mu.RLock()
	slave := sm.slave
	sm.mu.RUnlock()
	if slave == nil {
		return nil
	}
	return slave.Close()
}

func (sm *SessionManager) cleanupUploadLocked() {
	if sm.uploadFile != nil {
		if f, ok := sm.uploadFile.(*os.File); ok {
			f.Close()
		}
	}
	sm.uploadFile = nil
	sm.uploadFileName = ""
	sm.uploadChunks = 0
	sm.uploadWorkDir = ""
}

func (sm *SessionManager) InitializeTerminal() error {
	sm.mu.Lock()
	cols, rows := sm.configuredCols, sm.configuredRows
	if cols <= 0 {
		cols = defaultCols
	}
	if rows <= 0 {
		rows = defaultRows
	}
	cols, rows = clamp(cols, sm.minCols, sm.maxCols), clamp(rows, sm.minRows, sm.maxRows)
	if err := sm.slave.ResizeTerminal(cols, rows); err != nil {
		sm.mu.Unlock()
		return err
	}
	sm.activeCols, sm.activeRows = cols, rows
	sm.lastResizeSource = "server-init"
	if sm.stream != nil {
		sm.stream.Resize(cols, rows)
	}
	sm.mu.Unlock()
	sm.broadcastTerminalState("initial-size")
	return nil
}

func (sm *SessionManager) Run() {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	if sm.stream != nil {
		defer sm.stream.Close()
	}
	for {
		select {
		case <-sm.ctx.Done():
			return
		case client := <-sm.register:
			sm.mu.Lock()
			sm.nextClientSeq++
			client.id = fmt.Sprintf("c-%d", sm.nextClientSeq)
			if validClientKey(client.init.ClientKey) {
				client.key = client.init.ClientKey
			}
			client.lastActivity = time.Now()
			if sm.stream != nil {
				for index, query := range sm.stream.cursorQueries {
					client.cursorQuerySeen[index] = query.sequence
				}
			}
			sm.clients[client] = true
			sm.clientOrder[client.id] = sm.nextClientSeq
			// A refresh can recover an unoccupied lease, but a duplicate tab
			// cannot preempt a connection that still holds it.
			if !client.readOnly && client.key != "" && client.key == sm.leaderKey && !sm.leaderConnectedLocked() {
				sm.leaderClientID = client.id
				sm.leaderExpires = time.Time{}
			}
			client.replay = sm.buildReplaySnapshot(client.init)
			if client.ready != nil {
				close(client.ready)
			}
			sm.mu.Unlock()
			sm.sendConnectionCount()
			sm.broadcastTerminalState("client-connect")
		case client := <-sm.unregister:
			sm.mu.Lock()
			removed := sm.removeClientLocked(client, time.Now())
			sm.mu.Unlock()
			if removed {
				sm.sendConnectionCount()
				sm.broadcastTerminalState("client-disconnect")
			}
		case event := <-sm.resizeEvents:
			sm.handleResizeEvent(event.client, event.request, time.Now())
		case now := <-ticker.C:
			sm.flushResize(now)
		case message := <-sm.broadcast:
			sm.mu.Lock()
			if message.hasGeneration && message.generation != sm.generation {
				sm.mu.Unlock()
				continue
			}
			if len(message.raw) > 0 && sm.history != nil {
				sm.history.AppendRaw(message.raw)
			}
			if len(message.wire) == 0 && len(message.raw) > 0 {
				message.wire = encodeOutputMessage(message.raw)
			}
			sm.publishLocked(message.wire, message.raw)
			sm.mu.Unlock()
		}
	}
}

// All persistent stream events are ordered under mu, including PTY resize.
// Slow consumers use the same ownership cleanup as normal disconnects.
func (sm *SessionManager) publishLocked(wire, raw []byte) {
	var event []byte
	if sm.stream != nil {
		event = sm.stream.Append(wire, raw)
	}
	for client := range sm.clients {
		frame := wire
		if client.init.ReplayVersion >= 2 && len(event) > 0 {
			frame = event
		}
		select {
		case client.send <- frame:
		default:
			sm.removeClientLocked(client, time.Now())
		}
	}
}

func (sm *SessionManager) removeClientLocked(client *Client, now time.Time) bool {
	if !sm.clients[client] {
		return false
	}
	delete(sm.clients, client)
	delete(sm.clientOrder, client.id)
	delete(sm.clientSizes, client.id)
	close(client.send)
	if client.id == sm.leaderClientID {
		if client.key != "" {
			sm.leaderExpires = now.Add(sm.leaderGrace)
		} else {
			sm.leaderExpires = now
		}
		// Keep the last grid during refresh; never means hold until takeover.
		if sm.leaderSwitch != "never" {
			sm.resizeDue = sm.leaderExpires
		}
	}
	if sm.resizePolicy == "median" {
		sm.resizeDue = now.Add(sm.resizeDebounce)
	}
	return true
}

func (sm *SessionManager) isStaleBroadcast(message broadcastMessage) bool {
	if !message.hasGeneration {
		return false
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return message.generation != sm.generation
}

func (sm *SessionManager) buildReplaySnapshot(init InitMessage) replaySnapshot {
	if sm.history == nil {
		return replaySnapshot{Mode: "tail"}
	}

	epoch, _, tail := sm.history.Offsets()
	if init.ReplayVersion >= 2 && sm.stream != nil {
		return sm.buildStreamReplay(init, epoch, tail)
	}
	if init.Epoch != "" && init.Epoch == epoch {
		if data, ok := sm.history.GetSince(init.LastOffset); ok {
			return replaySnapshot{
				Epoch:      epoch,
				Mode:       "resume",
				FromOffset: init.LastOffset,
				EndOffset:  tail,
				Data:       data,
			}
		}
	}

	data, fromOffset := sm.history.GetTail(sm.historyReplayBytes)
	epoch, _, tail = sm.history.Offsets()
	return replaySnapshot{
		Epoch:      epoch,
		Mode:       "tail",
		FromOffset: fromOffset,
		EndOffset:  tail,
		Data:       data,
	}
}

func (sm *SessionManager) sendConnectionCount() {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	count := len(sm.clients)
	msg := []byte{'7'}
	msg = append(msg, []byte(fmt.Sprintf("%d", count))...)

	for client := range sm.clients {
		select {
		case client.send <- msg:
		default:
		}
	}
}

func (sm *SessionManager) broadcastTerminalState(reason string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.publishLocked(sm.terminalStateMessageLocked(reason), nil)
}

func (sm *SessionManager) terminalStateMessageLocked(reason string) []byte {
	state := terminalState{ActiveCols: sm.activeCols, ActiveRows: sm.activeRows,
		Policy: sm.resizePolicy, LeaderClient: sm.leaderClientID, Reason: reason,
		SourceClient: sm.lastResizeSource, Connected: len(sm.clients), ResizeEnabled: sm.resizePolicy != "fixed", SizeControl: sm.resizePolicy == "leader"}
	data, _ := json.Marshal(state)
	return append([]byte{'8'}, data...)
}

func (sm *SessionManager) GetClientCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.clients)
}

// NotifyClients sends a notification message to all connected Web clients.
// Used for API execution indicators (show/hide).
func (sm *SessionManager) NotifyClients(msgType byte, payload []byte) {
	msg := append([]byte{msgType}, payload...)
	sm.mu.RLock()
	for client := range sm.clients {
		select {
		case client.send <- msg:
		default:
		}
	}
	sm.mu.RUnlock()
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// UploadFileMessage represents a file upload message from the client
type UploadFileMessage struct {
	Name        string `json:"name"`
	Size        int    `json:"size"`
	Chunk       int    `json:"chunk"`
	TotalChunks int    `json:"totalChunks"`
	Data        string `json:"data"`
}

// HandleFileUpload handles file upload from the client
func (sm *SessionManager) HandleFileUpload(payload []byte) {
	var msg UploadFileMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		log.Printf("Upload: Failed to parse message: %v", err)
		return
	}

	// Validate filename
	if err := validateUploadFileName(msg.Name); err != nil {
		log.Printf("Upload: Invalid filename: %v", err)
		return
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Handle first chunk
	if msg.Chunk == 0 {
		if sm.uploadFile != nil {
			if f, ok := sm.uploadFile.(*os.File); ok {
				f.Close()
			}
		}

		workDir, err := sm.slave.GetWorkingDir()
		if err != nil {
			log.Printf("Upload: Failed to get working directory: %v", err)
			return
		}

		filePath := filepath.Join(workDir, msg.Name)
		log.Printf("Upload: Creating file at %s", filePath)

		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			log.Printf("Upload: Failed to create file: %v", err)
			return
		}

		sm.uploadFile = file
		sm.uploadFileName = msg.Name
		sm.uploadChunks = 0
		sm.uploadWorkDir = workDir
	}

	// Decode and write chunk
	fileData, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		log.Printf("Upload: Failed to decode data: %v", err)
		return
	}

	if f, ok := sm.uploadFile.(*os.File); ok {
		_, err = f.Write(fileData)
		if err != nil {
			log.Printf("Upload: Failed to write data: %v", err)
			return
		}
	}

	sm.uploadChunks++

	// Handle last chunk
	if msg.Chunk == msg.TotalChunks-1 {
		if f, ok := sm.uploadFile.(*os.File); ok {
			f.Close()
			log.Printf("Upload: File upload completed: %s", sm.uploadFileName)
		}
		sm.uploadFile = nil
		sm.uploadFileName = ""
		sm.uploadChunks = 0
		sm.uploadWorkDir = ""
	}
}

func validateUploadFileName(name string) error {
	if name == "" {
		return fmt.Errorf("empty filename")
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return fmt.Errorf("absolute paths not allowed")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("path traversal not allowed")
	}
	return nil
}
