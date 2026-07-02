package server

import "encoding/base64"

type broadcastMessage struct {
	wire          []byte
	raw           []byte
	generation    int64
	hasGeneration bool
}

func newOutputBroadcast(raw []byte) broadcastMessage {
	rawCopy := append([]byte(nil), raw...)
	return broadcastMessage{
		wire: encodeOutputMessage(rawCopy),
		raw:  rawCopy,
	}
}

func newOutputBroadcastWithGeneration(raw []byte, generation int64) broadcastMessage {
	message := newOutputBroadcast(raw)
	message.generation = generation
	message.hasGeneration = true
	return message
}

func encodeOutputMessage(raw []byte) []byte {
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(raw))+1)
	encoded[0] = '1'
	base64.StdEncoding.Encode(encoded[1:], raw)
	return encoded
}
