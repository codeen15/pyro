package protocol

import (
	"context"
	"time"

	"github.com/codeen15/pyro/pkg/engine/model"
)

// Message is one entry in a streaming session's timeline.
type Message struct {
	Seq       int       `json:"seq"`
	At        time.Time `json:"at"`
	Direction string    `json:"direction"` // sent | received | system
	// Kind is text | binary for WebSocket; event | ack for Socket.IO;
	// open | close | error for system entries.
	Kind string `json:"kind"`
	// Data is the payload: text, base64 for binary, or a JSON array of
	// Socket.IO event arguments.
	Data  string `json:"data,omitempty"`
	Event string `json:"event,omitempty"`
	AckID *int   `json:"ackId,omitempty"`
	// RTT is set on Socket.IO acks: time from emit to ack.
	RTT  time.Duration `json:"-"`
	Size int           `json:"size"`
}

// Outgoing is a message to send. For Socket.IO, Data is a JSON array of
// arguments (a bare value is sent as a single argument).
type Outgoing struct {
	Data   string `json:"data"`
	Binary bool   `json:"binary,omitempty"`
	Event  string `json:"event,omitempty"`
	Ack    bool   `json:"ack,omitempty"`
}

// SessionInfo describes an open connection.
type SessionInfo struct {
	URL         string           `json:"url"`
	Status      int              `json:"status,omitempty"`
	Headers     []model.KeyValue `json:"headers,omitempty"`
	Subprotocol string           `json:"subprotocol,omitempty"`
	SID         string           `json:"sid,omitempty"`
	// Transport is the Socket.IO transport in use: websocket or polling.
	Transport   string        `json:"transport,omitempty"`
	ConnectTime time.Duration `json:"-"`
}

// Session is an open WebSocket or Socket.IO connection.
type Session interface {
	// Messages delivers received messages (and system events) until Done.
	Messages() <-chan Message
	Send(ctx context.Context, m Outgoing) (Message, error)
	Close() error
	Done() <-chan struct{}
	// Err is why the session ended; nil after a clean Close.
	Err() error
	Info() SessionInfo
}
