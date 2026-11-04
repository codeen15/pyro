package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTimingsRoundTrip(t *testing.T) {
	in := Timings{DNS: 1500 * time.Microsecond, Total: 120 * time.Millisecond, Reused: true}
	b, _ := json.Marshal(in)
	var out Timings
	if err := json.Unmarshal(b, &out); err != nil || out != in {
		t.Fatalf("%s -> %+v %v", b, out, err)
	}
}
