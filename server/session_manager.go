package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
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
	id    string
	conn  *websocket.Conn
	send  chan []byte
	ready chan struct{}
}

type resizeDimensions struct {
	Columns        int
	Rows           int
	DeviceClass    string
	PixelRatio     float64
	ViewportWidth  int
	ViewportHeight int
	UpdatedAt      time.Time
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
}

type SessionManager struct {
	slave      Slave
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	history    *HistoryBuffer
	mu         sync.RWMutex
	ctx        context.Context

	resizePolicy     string
	leaderSelect     string
	leaderSwitch     string
	leaderIdle       time.Duration
	minCols          int
	maxCols          int
	minRows          int
	maxRows          int
	resizeDebounce   time.Duration
	configuredCols   int
	configuredRows   int
	leaderClientID   string
	lastResizeAt     time.Time
	lastResizeSource string
	activeCols       int
	activeRows       int
	nextClientSeq    int64
	clientOrder      map[string]int64
	clientSizes      map[string]resizeDimensions

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
		slave:          slave,
		clients:        make(map[*Client]bool),
		broadcast:      make(chan []byte, 256),
		register:       make(chan *Client),
		unregister:     make(chan *Client),
		history:        NewHistoryBuffer(10 * 1024 * 1024), // 10MB
		ctx:            ctx,
		resizePolicy:   options.ResizePolicy,
		leaderSelect:   options.LeaderSelect,
		leaderSwitch:   options.LeaderSwitch,
		leaderIdle:     time.Duration(options.LeaderIdleMs) * time.Millisecond,
		minCols:        options.MinCols,
		maxCols:        options.MaxCols,
		minRows:        options.MinRows,
		maxRows:        options.MaxRows,
		resizeDebounce: time.Duration(options.ResizeDebounceMs) * time.Millisecond,
		configuredCols: configuredCols,
		configuredRows: configuredRows,
		clientOrder:    make(map[string]int64),
		clientSizes:    make(map[string]resizeDimensions),
	}
}

func (sm *SessionManager) InitializeTerminal() error {
	sm.mu.Lock()
	cols := sm.configuredCols
	rows := sm.configuredRows
	reason := "initial-size"
	sm.lastResizeSource = "server-init"
	if cols > 0 && rows > 0 {
		sm.activeCols = cols
		sm.activeRows = rows
	} else {
		// Dynamic policies should not force a startup PTY size; wait for client-reported fit size.
		sm.activeCols = 0
		sm.activeRows = 0
		reason = "await-first-client-resize"
	}
	sm.mu.Unlock()

	if cols > 0 && rows > 0 {
		if err := sm.slave.ResizeTerminal(cols, rows); err != nil {
			return err
		}
	}
	sm.broadcastTerminalState(reason)
	return nil
}

func (sm *SessionManager) Run() {
	for {
		select {
		case <-sm.ctx.Done():
			return
		case client := <-sm.register:
			sm.mu.Lock()
			sm.nextClientSeq++
			client.id = fmt.Sprintf("c-%d", sm.nextClientSeq)
			sm.clients[client] = true
			sm.clientOrder[client.id] = sm.nextClientSeq
			if sm.resizePolicy == "leader" {
				if sm.leaderSelect == "latest" {
					sm.leaderClientID = client.id
				} else if sm.leaderClientID == "" {
					sm.leaderClientID = client.id
				}
			}
			if client.ready != nil {
				close(client.ready)
				client.ready = nil
			}
			sm.mu.Unlock()

			sm.sendConnectionCount()
			sm.broadcastTerminalState("client-connect")
			sm.reconcileResize("client-connect")
		case client := <-sm.unregister:
			removed := false
			sm.mu.Lock()
			if _, ok := sm.clients[client]; ok {
				removed = true
				delete(sm.clients, client)
				delete(sm.clientOrder, client.id)
				delete(sm.clientSizes, client.id)
				if client.id == sm.leaderClientID {
					switch sm.leaderSwitch {
					case "never":
						sm.leaderClientID = ""
					case "on_idle":
						sm.leaderClientID = sm.pickLeaderFallbackLocked()
					default:
						sm.leaderClientID = sm.pickLeaderFallbackLocked()
					}
				}
				close(client.send)
			}
			sm.mu.Unlock()

			if removed {
				sm.sendConnectionCount()
				sm.broadcastTerminalState("client-disconnect")
				sm.reconcileResize("client-disconnect")
			}
		case message := <-sm.broadcast:
			sm.history.Append(message)
			sm.mu.Lock()
			for client := range sm.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(sm.clients, client)
					delete(sm.clientOrder, client.id)
					delete(sm.clientSizes, client.id)
				}
			}
			sm.mu.Unlock()
		}
	}
}

func (sm *SessionManager) HandleClientResize(client *Client, req clientResizeRequest) {
	if client == nil || req.Columns <= 0 || req.Rows <= 0 {
		return
	}

	now := time.Now()
	sm.mu.Lock()
	if _, ok := sm.clients[client]; !ok {
		sm.mu.Unlock()
		return
	}

	sm.clientSizes[client.id] = resizeDimensions{
		Columns:        clamp(req.Columns, sm.minCols, sm.maxCols),
		Rows:           clamp(req.Rows, sm.minRows, sm.maxRows),
		DeviceClass:    req.DeviceClass,
		PixelRatio:     req.PixelRatio,
		ViewportWidth:  req.ViewportWidth,
		ViewportHeight: req.ViewportHeight,
		UpdatedAt:      now,
	}

	cols, rows, sourceClientID, shouldResize := sm.computeResizeTargetLocked(client.id, now, false)
	if !shouldResize {
		sm.mu.Unlock()
		return
	}

	sm.activeCols = cols
	sm.activeRows = rows
	sm.lastResizeAt = now
	sm.lastResizeSource = sourceClientID
	sm.mu.Unlock()

	if err := sm.slave.ResizeTerminal(cols, rows); err != nil {
		log.Printf("failed to resize terminal to %dx%d: %v", cols, rows, err)
		return
	}
	sm.broadcastTerminalState("client-resize")
}

func (sm *SessionManager) reconcileResize(reason string) {
	now := time.Now()
	sm.mu.Lock()
	cols, rows, sourceClientID, shouldResize := sm.computeResizeTargetLocked("", now, true)
	if !shouldResize {
		sm.mu.Unlock()
		return
	}
	sm.activeCols = cols
	sm.activeRows = rows
	sm.lastResizeAt = now
	sm.lastResizeSource = sourceClientID
	sm.mu.Unlock()

	if err := sm.slave.ResizeTerminal(cols, rows); err != nil {
		log.Printf("failed to reconcile terminal size to %dx%d: %v", cols, rows, err)
		return
	}
	sm.broadcastTerminalState(reason)
}

func (sm *SessionManager) computeResizeTargetLocked(requestClientID string, now time.Time, allowNonRequester bool) (int, int, string, bool) {
	if sm.resizeDebounce > 0 && !sm.lastResizeAt.IsZero() && now.Sub(sm.lastResizeAt) < sm.resizeDebounce {
		return 0, 0, "", false
	}

	var cols int
	var rows int
	source := "policy"

	switch sm.resizePolicy {
	case "fixed":
		cols = sm.configuredCols
		rows = sm.configuredRows
		source = "fixed"
	case "leader":
		if sm.leaderClientID == "" {
			sm.leaderClientID = sm.pickLeaderFallbackLocked()
		}
		if sm.leaderClientID == "" {
			return 0, 0, "", false
		}
		if !allowNonRequester && requestClientID != sm.leaderClientID {
			if sm.leaderSwitch == "on_idle" && requestClientID != "" {
				sm.maybeSwitchLeaderOnIdleLocked(requestClientID, now)
			}
		}
		if !allowNonRequester && requestClientID != sm.leaderClientID {
			return 0, 0, "", false
		}
		size, ok := sm.clientSizes[sm.leaderClientID]
		if !ok {
			return 0, 0, "", false
		}
		cols = size.Columns
		rows = size.Rows
		source = sm.leaderClientID
	case "median":
		colsVals := make([]int, 0, len(sm.clients))
		rowsVals := make([]int, 0, len(sm.clients))
		for client := range sm.clients {
			size, ok := sm.clientSizes[client.id]
			if !ok {
				continue
			}
			colsVals = append(colsVals, size.Columns)
			rowsVals = append(rowsVals, size.Rows)
		}
		if len(colsVals) == 0 || len(rowsVals) == 0 {
			return 0, 0, "", false
		}
		cols = medianInt(colsVals)
		rows = medianInt(rowsVals)
		source = "median"
	default:
		return 0, 0, "", false
	}

	cols = clamp(cols, sm.minCols, sm.maxCols)
	rows = clamp(rows, sm.minRows, sm.maxRows)

	if cols == sm.activeCols && rows == sm.activeRows {
		return 0, 0, "", false
	}

	return cols, rows, source, true
}

func (sm *SessionManager) maybeSwitchLeaderOnIdleLocked(candidateClientID string, now time.Time) {
	if sm.leaderClientID == "" || sm.leaderClientID == candidateClientID {
		return
	}
	if sm.leaderIdle <= 0 {
		sm.leaderClientID = candidateClientID
		return
	}

	leaderSize, ok := sm.clientSizes[sm.leaderClientID]
	if !ok {
		sm.leaderClientID = candidateClientID
		return
	}

	if leaderSize.UpdatedAt.IsZero() || now.Sub(leaderSize.UpdatedAt) >= sm.leaderIdle {
		sm.leaderClientID = candidateClientID
	}
}

func (sm *SessionManager) pickFirstClientIDLocked() string {
	var firstID string
	var firstSeq int64
	for clientID, seq := range sm.clientOrder {
		if firstID == "" || seq < firstSeq {
			firstID = clientID
			firstSeq = seq
		}
	}
	return firstID
}

func (sm *SessionManager) pickLatestClientIDLocked() string {
	var latestID string
	var latestSeq int64
	for clientID, seq := range sm.clientOrder {
		if latestID == "" || seq > latestSeq {
			latestID = clientID
			latestSeq = seq
		}
	}
	return latestID
}

func (sm *SessionManager) pickLeaderFallbackLocked() string {
	if sm.leaderSelect == "latest" {
		return sm.pickLatestClientIDLocked()
	}
	return sm.pickFirstClientIDLocked()
}

func (sm *SessionManager) sendConnectionCount() {
	sm.mu.RLock()
	count := len(sm.clients)
	sm.mu.RUnlock()

	msg := []byte{'7'}
	msg = append(msg, []byte(fmt.Sprintf("%d", count))...)

	sm.mu.RLock()
	for client := range sm.clients {
		select {
		case client.send <- msg:
		default:
		}
	}
	sm.mu.RUnlock()
}

func (sm *SessionManager) broadcastTerminalState(reason string) {
	msg := sm.terminalStateMessage(reason)
	if msg == nil {
		return
	}
	sm.mu.RLock()
	for client := range sm.clients {
		select {
		case client.send <- msg:
		default:
		}
	}
	sm.mu.RUnlock()
}

func (sm *SessionManager) terminalStateMessage(reason string) []byte {
	sm.mu.RLock()
	state := terminalState{
		ActiveCols:    sm.activeCols,
		ActiveRows:    sm.activeRows,
		Policy:        sm.resizePolicy,
		LeaderClient:  sm.leaderClientID,
		Reason:        reason,
		SourceClient:  sm.lastResizeSource,
		Connected:     len(sm.clients),
		ResizeEnabled: sm.resizePolicy != "fixed",
	}
	sm.mu.RUnlock()

	data, err := json.Marshal(state)
	if err != nil {
		return nil
	}
	return append([]byte{'8'}, data...)
}

func (sm *SessionManager) GetClientCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.clients)
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

func medianInt(values []int) int {
	sort.Ints(values)
	n := len(values)
	mid := n / 2
	if n%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
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
