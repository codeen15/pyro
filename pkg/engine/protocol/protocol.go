// Package protocol holds types shared by all protocol drivers.
package protocol

import (
	"encoding/json"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/model"
)

// Response is the result of a request/response exchange.
type Response struct {
	Status     int              `json:"status"`
	StatusText string           `json:"statusText"`
	Proto      string           `json:"proto"`
	Headers    []model.KeyValue `json:"headers"`
	Cookies    []Cookie         `json:"cookies,omitempty"`
	// Body holds at most the driver's MaxBodyBytes; Size is the full size.
	Body          []byte  `json:"body"`
	BodyTruncated bool    `json:"bodyTruncated,omitempty"`
	Size          int64   `json:"size"`
	Timings       Timings `json:"timings"`
	// RemoteAddr is the IP:port actually connected to.
	RemoteAddr string `json:"remoteAddr,omitempty"`
}

type Cookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain,omitempty"`
	Path     string    `json:"path,omitempty"`
	Expires  time.Time `json:"expires,omitzero"`
	HttpOnly bool      `json:"httpOnly,omitempty"`
	Secure   bool      `json:"secure,omitempty"`
}

// Timings breaks a request into phases. Phases that didn't happen (e.g. DNS
// on a reused connection) are zero.
type Timings struct {
	DNS      time.Duration `json:"dns"`
	Connect  time.Duration `json:"connect"`
	TLS      time.Duration `json:"tls"`
	Send     time.Duration `json:"send"`
	Wait     time.Duration `json:"wait"` // time to first byte after the request was written
	Download time.Duration `json:"download"`
	Total    time.Duration `json:"total"`
	Reused   bool          `json:"reused"`
}

// MarshalJSON reports durations in fractional milliseconds, which is what
// the UI displays.
func (t Timings) MarshalJSON() ([]byte, error) {
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	return json.Marshal(map[string]any{
		"dns": ms(t.DNS), "connect": ms(t.Connect), "tls": ms(t.TLS), "send": ms(t.Send),
		"wait": ms(t.Wait), "download": ms(t.Download), "total": ms(t.Total), "reused": t.Reused,
	})
}
