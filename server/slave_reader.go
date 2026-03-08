package server

import (
	"context"
	"encoding/base64"
)

func (server *Server) readSlaveOutput(ctx context.Context) {
	buffer := make([]byte, 1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			n, err := server.sessionManager.slave.Read(buffer)
			if err != nil {
				return
			}

			encoded := make([]byte, base64.StdEncoding.EncodedLen(n)+1)
			encoded[0] = '1'
			base64.StdEncoding.Encode(encoded[1:], buffer[:n])

			server.sessionManager.broadcast <- encoded
		}
	}
}
