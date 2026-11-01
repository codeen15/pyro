package output

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
)

type influx struct {
	url   string
	token string
}

func newInflux(target string) (*influx, error) {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid URL %q", target)
	}
	in := &influx{token: os.Getenv("PYRO_INFLUXDB_TOKEN")}
	if u.Path == "" || u.Path == "/" {
		// Bare host: assume 1.x with database "pyro".
		u.Path = "/write"
		if u.Query().Get("db") == "" {
			q := u.Query()
			q.Set("db", "pyro")
			u.RawQuery = q.Encode()
		}
	}
	if strings.HasSuffix(u.Path, "/api/v2/write") && u.Query().Get("precision") == "" {
		q := u.Query()
		q.Set("precision", "ns")
		u.RawQuery = q.Encode()
	}
	in.url = u.String()
	return in, nil
}

func (in *influx) write(ctx context.Context, b Batch) error {
	body := lineProtocol(b.Points)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, in.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if in.token != "" {
		req.Header.Set("Authorization", "Token "+in.token)
	}
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

func (in *influx) close() error { return nil }

var (
	measurementEscaper = strings.NewReplacer(",", `\,`, " ", `\ `)
	tagEscaper         = strings.NewReplacer(",", `\,`, " ", `\ `, "=", `\=`)
)

// lineProtocol renders points as InfluxDB line protocol:
// measurement,tag=v,... value=1.5 1700000000000000000
func lineProtocol(points []Point) []byte {
	var buf bytes.Buffer
	for _, p := range points {
		buf.WriteString(measurementEscaper.Replace(p.Name))
		keys := make([]string, 0, len(p.Labels))
		for k, v := range p.Labels {
			if v != "" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			buf.WriteByte(',')
			buf.WriteString(tagEscaper.Replace(k))
			buf.WriteByte('=')
			buf.WriteString(tagEscaper.Replace(p.Labels[k]))
		}
		buf.WriteString(" value=")
		buf.WriteString(strconv.FormatFloat(p.Value, 'g', -1, 64))
		buf.WriteByte(' ')
		buf.WriteString(strconv.FormatInt(p.Time.UnixNano(), 10))
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}
