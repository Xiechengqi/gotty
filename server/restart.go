package server

import (
	"encoding/json"
	"fmt"
	"log"
	"time"
)

func (server *Server) restartTerminal() error {
	server.restartMu.Lock()
	defer server.restartMu.Unlock()

	newSlave, err := server.factory.New(nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create replacement terminal: %w", err)
	}

	oldSlave, generation, cols, rows := server.sessionManager.ReplaceSlave(newSlave)
	if cols > 0 && rows > 0 {
		if err := newSlave.ResizeTerminal(cols, rows); err != nil {
			log.Printf("failed to resize restarted terminal to %dx%d: %v", cols, rows, err)
		}
	}

	server.rebuildExecManager(newSlave, generation)
	go server.readSlaveOutput(server.sessionManager.ctx, newSlave, generation)

	if oldSlave != nil {
		go func() {
			if err := oldSlave.Close(); err != nil {
				log.Printf("failed to close old terminal during restart: %v", err)
			}
		}()
	}

	server.sessionManager.broadcastTerminalState("terminal-restart")
	return nil
}

func (server *Server) rebuildExecManager(slave Slave, generation int64) {
	if !server.options.EnableAPI || server.terminalStatus == nil || server.broadcastCtrl == nil {
		return
	}

	probeTimeout := time.Duration(server.options.ProbeTimeoutMs) * time.Millisecond
	probeManager := NewProbeManager(slave, server.broadcastCtrl, probeTimeout)

	notifyFn := func(execID, status string) {
		payload, _ := json.Marshal(map[string]string{
			"type":    status,
			"exec_id": execID,
		})
		clientCount := server.sessionManager.GetClientCount()
		log.Printf("[API Notify] Sending %s to %d clients (exec_id=%s)", status, clientCount, execID)
		server.sessionManager.NotifyClients('9', payload)
	}

	replayFn := func(raw []byte) {
		select {
		case server.sessionManager.broadcast <- newOutputBroadcastWithGeneration(raw, generation):
		case <-time.After(5 * time.Second):
			log.Printf("[API Replay] WARNING: broadcast channel full, replay dropped (%d bytes)", len(raw))
		}
	}

	server.execManager = NewExecManager(slave, server.terminalStatus, probeManager, server.broadcastCtrl, notifyFn, replayFn)
}
