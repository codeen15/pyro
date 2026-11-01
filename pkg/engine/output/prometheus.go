package output

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// prometheus pushes with the remote-write 1.0 protocol: a snappy-compressed
// protobuf WriteRequest. Both encodings are small enough to write by hand,
// which keeps Pyro free of protobuf code generation.
type prometheus struct{ url string }

func newPrometheus(target string) (*prometheus, error) {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid URL %q", target)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/api/v1/write"
	}
	return &prometheus{url: u.String()}, nil
}

func (p *prometheus) write(ctx context.Context, b Batch) error {
	body := snappyLiteral(writeRequest(b.Points))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

func (p *prometheus) close() error { return nil }

// writeRequest encodes prometheus.WriteRequest:
//
//	WriteRequest { repeated TimeSeries timeseries = 1; }
//	TimeSeries   { repeated Label labels = 1; repeated Sample samples = 2; }
//	Label        { string name = 1; string value = 2; }
//	Sample       { double value = 1; int64 timestamp = 2; } // ms
func writeRequest(points []Point) []byte {
	var out []byte
	for _, p := range points {
		labels := map[string]string{"__name__": p.Name}
		for k, v := range p.Labels {
			if v != "" {
				labels[k] = v
			}
		}
		names := make([]string, 0, len(labels))
		for k := range labels {
			names = append(names, k)
		}
		sort.Strings(names) // remote write requires sorted labels
		var ts []byte
		for _, n := range names {
			var l []byte
			l = appendBytesField(l, 1, []byte(n))
			l = appendBytesField(l, 2, []byte(labels[n]))
			ts = appendBytesField(ts, 1, l)
		}
		var s []byte
		s = append(s, 1<<3|1) // field 1, fixed64
		s = binary.LittleEndian.AppendUint64(s, math.Float64bits(p.Value))
		s = append(s, 2<<3|0) // field 2, varint
		s = binary.AppendUvarint(s, uint64(p.Time.UnixMilli()))
		ts = appendBytesField(ts, 2, s)
		out = appendBytesField(out, 1, ts)
	}
	return out
}

func appendBytesField(b []byte, field int, v []byte) []byte {
	b = binary.AppendUvarint(b, uint64(field)<<3|2)
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}

// snappyLiteral wraps data in a valid snappy block made only of literals.
// It doesn't compress, but every snappy decoder accepts it, and remote-write
// payloads from one tick are a few kilobytes.
func snappyLiteral(data []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(len(data)))
	const chunk = 1 << 16
	for len(data) > 0 {
		n := min(len(data), chunk)
		switch m := n - 1; {
		case m < 60:
			out = append(out, byte(m)<<2)
		case m < 1<<8:
			out = append(out, 60<<2, byte(m))
		default:
			out = append(out, 61<<2, byte(m), byte(m>>8))
		}
		out = append(out, data[:n]...)
		data = data[n:]
	}
	return out
}
