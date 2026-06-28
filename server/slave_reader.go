package server

import (
	"context"
	"encoding/base64"
)

func (server *Server) readSlaveOutput(ctx context.Context, slave Slave, generation int64) {
	buffer := make([]byte, 1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			n, err := slave.Read(buffer)
			if err != nil {
				return
			}
			if !server.sessionManager.IsGeneration(generation) {
				return
			}

			raw := buffer[:n]

			if server.sessionManager.lineHistory != nil {
				server.sessionManager.lineHistory.Append(raw)
			}

			// Feed raw output to exec manager (for marker detection)
			if server.execManager != nil {
				server.execManager.FeedOutput(raw)
			}

			// Encode for WebSocket clients
			encoded := make([]byte, base64.StdEncoding.EncodedLen(n)+1)
			encoded[0] = '1'
			base64.StdEncoding.Encode(encoded[1:], buffer[:n])

			// Check broadcast controller — during probe, output is redirected internally
			if server.broadcastCtrl != nil && !server.broadcastCtrl.HandleOutput(raw) {
				continue
			}

			if !server.sessionManager.IsGeneration(generation) {
				return
			}

			select {
			case server.sessionManager.broadcast <- encoded:
			case <-ctx.Done():
				return
			}
		}
	}
}
