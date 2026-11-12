package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type uploadSlave struct {
	recordingSlave
	dir string
}

func (s *uploadSlave) GetWorkingDir() (string, error) { return s.dir, nil }

func uploadManager(t *testing.T) (*SessionManager, *Client, string) {
	t.Helper()
	dir := t.TempDir()
	sm := NewSessionManager(context.Background(), &uploadSlave{dir: dir}, &Options{})
	t.Cleanup(func() { sm.cleanupUploadLocked(); sm.stream.Close() })
	c := addResizeClient(sm, "upload-client", "", false)
	return sm, c, dir
}

func uploadChunk(t *testing.T, sm *SessionManager, c *Client, msg UploadFileMessage) fileUploadResult {
	t.Helper()
	data, _ := json.Marshal(msg)
	sm.handleClientFileUpload(c, data)
	select {
	case data := <-c.send:
		var result fileUploadResult
		if len(data) < 2 || data[0] != 'd' || json.Unmarshal(data[1:], &result) != nil {
			t.Fatalf("invalid upload result: %s", data)
		}
		return result
	default:
		t.Fatal("missing upload result")
		return fileUploadResult{}
	}
}

func TestAcknowledgedUploadCommitsOnlyCompleteFiles(t *testing.T) {
	sm, c, dir := uploadManager(t)
	name := "中文 文件.bin"
	target := filepath.Join(dir, name)
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	msg := UploadFileMessage{UploadID: "transfer-one", Name: name, Size: 6, TotalChunks: 2, Data: base64.StdEncoding.EncodeToString([]byte{0, 1, 2})}
	result := uploadChunk(t, sm, c, msg)
	if result.Error != "" || result.Done || result.Received != 3 {
		t.Fatal(result)
	}
	if data, _ := os.ReadFile(target); string(data) != "original" {
		t.Fatal("partial upload replaced destination")
	}
	msg.Chunk = 1
	msg.Data = base64.StdEncoding.EncodeToString([]byte{3, 4, 255})
	result = uploadChunk(t, sm, c, msg)
	if result.Error != "" || !result.Done || result.Received != 6 || result.Name != name {
		t.Fatal(result)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != string([]byte{0, 1, 2, 3, 4, 255}) {
		t.Fatal(data, err)
	}
	if stat, _ := os.Stat(target); stat.Mode().Perm() != 0600 {
		t.Fatal("existing permissions changed")
	}
	if temporary, _ := filepath.Glob(filepath.Join(dir, ".gotty-upload-*")); len(temporary) != 0 {
		t.Fatal(temporary)
	}
}

func TestUploadFailureCancelAndDisconnectPreserveDestination(t *testing.T) {
	for _, failure := range []string{"cancel", "disconnect", "restart", "sequence", "size", "base64", "rename"} {
		t.Run(failure, func(t *testing.T) {
			sm, c, dir := uploadManager(t)
			target := filepath.Join(dir, "test.bin")
			if err := os.WriteFile(target, []byte("original"), 0644); err != nil {
				t.Fatal(err)
			}
			msg := UploadFileMessage{UploadID: "transfer", Name: "test.bin", Size: 6, TotalChunks: 2, Data: "YWJj"}
			if result := uploadChunk(t, sm, c, msg); result.Error != "" {
				t.Fatal(result)
			}
			switch failure {
			case "cancel":
				uploadChunk(t, sm, c, UploadFileMessage{UploadID: msg.UploadID, Cancel: true})
			case "disconnect":
				sm.removeClientLocked(c, time.Now())
			case "restart":
				sm.cleanupUploadLocked()
			default:
				msg.Chunk = 1
				if failure == "sequence" {
					msg.Chunk = 0
				}
				if failure == "size" {
					msg.Data = "YWJjZA=="
				}
				if failure == "base64" {
					msg.Data = "!!!!"
				}
				if failure == "rename" {
					os.Remove(target)
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if result := uploadChunk(t, sm, c, msg); result.Error == "" || result.Done {
					t.Fatal(result)
				}
			}
			if c.upload != nil {
				t.Fatal("transfer state leaked")
			}
			if failure != "rename" {
				if data, _ := os.ReadFile(target); string(data) != "original" {
					t.Fatal("original file changed")
				}
			}
			if temporary, _ := filepath.Glob(filepath.Join(dir, ".gotty-upload-*")); len(temporary) != 0 {
				t.Fatal(temporary)
			}
		})
	}
}

func TestUploadIsolationValidationAndLegacyCompatibility(t *testing.T) {
	sm, first, dir := uploadManager(t)
	second := addResizeClient(sm, "other", "", false)
	msg := UploadFileMessage{UploadID: "first", Name: "same.bin", Size: 6, TotalChunks: 2, Data: "YWJj"}
	if result := uploadChunk(t, sm, first, msg); result.Error != "" {
		t.Fatal(result)
	}
	other := msg
	other.UploadID = "second"
	if result := uploadChunk(t, sm, second, other); !strings.Contains(result.Error, "another client") {
		t.Fatal(result)
	}
	other.Name = "different.bin"
	if result := uploadChunk(t, sm, second, other); result.Error != "" {
		t.Fatal(result)
	}
	msg.Chunk = 1
	other.Chunk = 1
	if result := uploadChunk(t, sm, first, msg); !result.Done {
		t.Fatal(result)
	}
	if result := uploadChunk(t, sm, second, other); !result.Done {
		t.Fatal(result)
	}
	for _, name := range []string{"../bad", "sub/file", "sub\\file", ".", ""} {
		bad := UploadFileMessage{UploadID: "invalid", Name: name, TotalChunks: 1}
		if result := uploadChunk(t, sm, first, bad); result.Error == "" {
			t.Fatal(name, result)
		}
	}
	if result := uploadChunk(t, sm, first, UploadFileMessage{UploadID: "large", Name: "large", Size: maxClientUploadSize + 1, TotalChunks: 1}); result.Error == "" {
		t.Fatal(result)
	}
	if result := uploadChunk(t, sm, first, UploadFileMessage{UploadID: "empty", Name: "empty", TotalChunks: 1}); !result.Done || result.Received != 0 {
		t.Fatal(result)
	}
	if err := os.Mkdir(filepath.Join(dir, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if result := uploadChunk(t, sm, first, UploadFileMessage{UploadID: "directory", Name: "directory", TotalChunks: 1}); result.Error == "" {
		t.Fatal(result)
	}
	legacy, _ := json.Marshal(UploadFileMessage{Name: "legacy.bin", Size: 3, TotalChunks: 1, Data: "YWJj"})
	sm.handleClientFileUpload(first, legacy)
	if data, _ := os.ReadFile(filepath.Join(dir, "legacy.bin")); string(data) != "abc" {
		t.Fatal("legacy upload broke")
	}
	if len(first.send) != 0 || len(second.send) != 0 {
		t.Fatal("upload replies escaped to another client or legacy sender")
	}
	server := &Server{sessionManager: sm, options: &Options{PermitWrite: true}}
	observer := addResizeClient(sm, "observer", "", true)
	denied, _ := json.Marshal(UploadFileMessage{UploadID: "denied", Name: "denied.bin", Size: 3, TotalChunks: 1, Data: "YWJj"})
	server.handleClientInput(observer, append([]byte{'7'}, denied...))
	server.options.PermitWrite = false
	server.handleClientInput(first, append([]byte{'7'}, denied...))
	if len(observer.send) != 0 || len(first.send) != 0 {
		t.Fatal("read-only upload was acknowledged")
	}
	if _, err := os.Stat(filepath.Join(dir, "denied.bin")); !os.IsNotExist(err) {
		t.Fatal("read-only upload created a file")
	}
}
