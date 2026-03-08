package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"

	"github.com/gorilla/websocket"
)

type clientResizeRequest struct {
	Columns        int     `json:"columns"`
	Rows           int     `json:"rows"`
	DeviceClass    string  `json:"deviceClass"`
	PixelRatio     float64 `json:"pixelRatio"`
	ViewportWidth  int     `json:"viewportWidth"`
	ViewportHeight int     `json:"viewportHeight"`
}

func (server *Server) sendInitMessages(conn *websocket.Conn, client *Client) error {
	titleVars := server.titleVariables(
		[]string{"server", "slave"},
		map[string]map[string]interface{}{
			"server": server.options.TitleVariables,
			"slave":  server.sessionManager.slave.WindowTitleVariables(),
		},
	)

	titleBuf := new(bytes.Buffer)
	if err := server.titleTemplate.Execute(titleBuf, titleVars); err != nil {
		return err
	}

	// Send window title
	titleMsg := append([]byte{'3'}, titleBuf.Bytes()...)
	conn.WriteMessage(websocket.TextMessage, titleMsg)

	// Send preferences
	prefs := map[string]interface{}{}
	if server.options.EnableReconnect {
		prefs["reconnect"] = server.options.ReconnectTime
	}
	if client != nil {
		prefs["client-id"] = client.id
	}
	prefsData, _ := json.Marshal(prefs)
	prefsMsg := append([]byte{'4'}, prefsData...)
	conn.WriteMessage(websocket.TextMessage, prefsMsg)

	return nil
}

func (server *Server) handleClientInput(client *Client, message []byte) {
	if len(message) == 0 {
		return
	}

	switch message[0] {
	case '1': // Input data
		if len(message) > 1 {
			decoded := make([]byte, base64.StdEncoding.DecodedLen(len(message)-1))
			n, err := base64.StdEncoding.Decode(decoded, message[1:])
			if err == nil && n > 0 {
				server.sessionManager.slave.Write(decoded[:n])
			}
		}
	case '3': // Resize
		if len(message) <= 1 {
			return
		}
		var resizeReq clientResizeRequest
		if err := json.Unmarshal(message[1:], &resizeReq); err != nil {
			return
		}
		server.sessionManager.HandleClientResize(client, resizeReq)
	}
}
