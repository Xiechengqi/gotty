package server

type InitMessage struct {
	Arguments  string `json:"Arguments,omitempty"`
	AuthToken  string `json:"AuthToken,omitempty"`
	LastOffset int64  `json:"LastOffset,omitempty"`
	Epoch      string `json:"Epoch,omitempty"`
}
