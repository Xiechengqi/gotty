package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const shareAccessTokenPrefix = "gotty-share-v1:"

func (server *Server) shareAccessToken(record ShareRecord) string {
	mac := hmac.New(sha256.New, []byte(server.shareAccessTokenSecret()))
	for _, part := range []string{
		"gotty-share-access-v1",
		record.ID,
		record.OwnerID,
		record.Subdomain,
		record.PublicURL,
	} {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return shareAccessTokenPrefix + hex.EncodeToString(mac.Sum(nil))
}

func (server *Server) shareRecordForAccessToken(token string) (ShareRecord, bool) {
	token = strings.TrimSpace(token)
	if token == "" || !strings.HasPrefix(token, shareAccessTokenPrefix) || server.shareManager == nil {
		return ShareRecord{}, false
	}
	for _, record := range server.shareManager.List() {
		if record.Status != ShareStatusActive || (!record.Passwordless && !record.CustomPassword) {
			continue
		}
		expected := server.shareAccessToken(record)
		if hmac.Equal([]byte(token), []byte(expected)) {
			return record, true
		}
	}
	return ShareRecord{}, false
}

func (server *Server) shareAccessTokenSecret() string {
	if server.options != nil && server.options.Credential != "" {
		return server.options.Credential
	}
	if server.shareManager != nil {
		if ownerID := server.shareManager.OwnerID(); ownerID != "" {
			return ownerID
		}
	}
	return "gotty-share-access-token"
}
