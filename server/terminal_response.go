package server

import (
	"regexp"
	"time"
)

// xterm emits protocol replies through onData just like keystrokes. A shared
// PTY must receive one answer, not one per watching browser. Keep cursor keys,
// Alt shortcuts, ordinary input and bracketed paste outside this allowlist.
var terminalResponse = regexp.MustCompile("^(?:\\x1b\\[[?>]?[0-9;:]*[cn]|\\x1b\\[\\??[0-9;]*\\$y|\\x1b\\](?:4;[0-9]+|10|11|12);[^\\x1b\\x07]*(?:\\x07|\\x1b\\\\)|\\x1bP[01][+$]r[^\\x1b]*\\x1b\\\\)$")
var terminalCursorResponse = regexp.MustCompile("^\\x1b\\[\\??[0-9]+;[0-9]+(?:;[0-9]+)?R$")

// A CPR and a modified F3 key can have identical bytes. Only classify a CPR
// after the PTY has actually requested it; ordinary function keys stay input.
func (sm *SessionManager) handleTerminalResponse(client *Client, data []byte) bool {
	cursor := terminalCursorResponse.Match(data)
	if !cursor && !terminalResponse.Match(data) {
		return false
	}
	sm.mu.Lock()
	if cursor {
		if client == nil || sm.stream == nil {
			sm.mu.Unlock()
			return false
		}
		index := 0
		if data[2] == '?' {
			index = 1
		}
		query := sm.stream.cursorQueries[index]
		if query.sequence <= client.cursorQuerySeen[index] || time.Since(query.at) > 5*time.Second {
			sm.mu.Unlock()
			return false
		}
		client.cursorQuerySeen[index] = query.sequence
	}
	accepted := sm.acceptsTerminalResponseLocked(client)
	if accepted && sm.paste != nil {
		select {
		case sm.paste.replies <- append([]byte(nil), data...):
		default:
			sm.paste.cancel()
		}
		sm.mu.Unlock()
		return true
	}
	slave := sm.slave
	sm.mu.Unlock()
	if accepted {
		_, _ = slave.Write(data)
	}
	return true
}

func (sm *SessionManager) acceptsTerminalResponseLocked(client *Client) bool {
	if sm.resizePolicy == "leader" {
		for c := range sm.clients {
			if c.id == sm.leaderClientID && sm.eligibleLocked(c) {
				return c == client
			}
		}
	}
	// Fixed and median geometry still need a single responder. Choose the
	// oldest visible writable client, also while a leader is reconnecting or
	// hidden. This does not change the PTY size or steal its ownership lease.
	var selected *Client
	for c := range sm.clients {
		if !sm.eligibleLocked(c) {
			continue
		}
		if selected == nil || sm.clientOrder[c.id] < sm.clientOrder[selected.id] {
			selected = c
		}
	}
	return selected != nil && selected == client
}
