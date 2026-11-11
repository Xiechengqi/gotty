package server

type InitMessage struct {
	Arguments     string `json:"Arguments,omitempty"`
	AuthToken     string `json:"AuthToken,omitempty"`
	LastOffset    int64  `json:"LastOffset,omitempty"`
	Epoch         string `json:"Epoch,omitempty"`
	ClientKey     string `json:"ClientKey,omitempty"`
	ReplayVersion int    `json:"ReplayVersion,omitempty"`
	LastSequence  int64  `json:"LastSequence,omitempty"`
}
