package output

import (
	"encoding/binary"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/load"
	"github.com/pyrohq/pyro/pkg/engine/metrics"
)

func sampleTick() (load.Tick, []metrics.RequestStat) {
	t := load.Tick{Elapsed: 3, VUs: 10, VUsMax: 12, RPS: 250, Requests: 250, ErrorRate: 0.02,
		Latency:    load.Latency{Avg: 21, P50: 20, P90: 30, P95: 40, P99: 80, Max: 120},
		Thresholds: []load.ThresholdResult{{Metric: "http_req_failed", Condition: "rate<0.01", Passed: false}}}
	reqs := []metrics.RequestStat{{Scenario: "s", Request: "shop/list items", Method: "GET", Count: 700, Failed: 3, Duration: metrics.Stat{P95: 42, P99: 90}}}
	return t, reqs
}

func TestLineProtocol(t *testing.T) {
	at := time.Unix(1700000000, 5)
	got := string(lineProtocol([]Point{
		{Name: "pyro_vus", Labels: map[string]string{"test": "My test", "run": "r1"}, Value: 10, Time: at},
		{Name: "pyro_request_reqs_total", Labels: map[string]string{"request": "a,b=c", "empty": ""}, Value: 1.5, Time: at},
	}))
	want := "pyro_vus,run=r1,test=My\\ test value=10 1700000000000000005\n" +
		"pyro_request_reqs_total,request=a\\,b\\=c value=1.5 1700000000000000005\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

type recorder struct {
	mu     sync.Mutex
	bodies [][]byte
	hdr    http.Header
	query  string
}

func (r *recorder) server(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, b)
		r.hdr = req.Header.Clone()
		r.query = req.URL.RawQuery
		r.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestInfluxOutput(t *testing.T) {
	var rec recorder
	srv := rec.server(t)
	t.Setenv("PYRO_INFLUXDB_TOKEN", "tok")
	o, err := Open("influxdb="+srv.URL+"/api/v2/write?org=o&bucket=b", "Checkout", "run1")
	if err != nil {
		t.Fatal(err)
	}
	tk, reqs := sampleTick()
	o.Tick(tk, reqs)
	o.Tick(tk, reqs)
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if len(rec.bodies) != 2 || rec.hdr.Get("Authorization") != "Token tok" || !strings.Contains(rec.query, "precision=ns") {
		t.Fatalf("%d writes, auth %q, query %q", len(rec.bodies), rec.hdr.Get("Authorization"), rec.query)
	}
	body := string(rec.bodies[1])
	for _, want := range []string{
		"pyro_http_reqs_total,run=run1,test=Checkout value=500 ",
		"pyro_http_req_duration_seconds,quantile=0.95,run=run1,test=Checkout value=0.04 ",
		`pyro_request_reqs_total,method=GET,request=shop/list\ items,run=run1,scenario=s,test=Checkout value=700 `,
		`pyro_threshold_passing,run=run1,test=Checkout,threshold=http_req_failed\ rate<0.01 value=0 `,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
}

func TestInfluxBareHostDefaults(t *testing.T) {
	in, err := newInflux("http://db:8086")
	if err != nil || in.url != "http://db:8086/write?db=pyro" {
		t.Errorf("%v %v", in, err)
	}
}

func TestOutputReportsFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "database not found", 404)
	}))
	defer srv.Close()
	o, _ := Open("influxdb="+srv.URL+"/write?db=x", "t", "r")
	tk, _ := sampleTick()
	o.Tick(tk, nil)
	if err := o.Close(); err == nil || !strings.Contains(err.Error(), "database not found") {
		t.Errorf("err = %v", err)
	}
	if _, err := Open("graphite=x", "t", "r"); err == nil {
		t.Error("unknown kind accepted")
	}
}

// unsnappy decodes the literal-only snappy blocks snappyLiteral makes.
func unsnappy(t *testing.T, b []byte) []byte {
	n, k := binary.Uvarint(b)
	b = b[k:]
	var out []byte
	for len(b) > 0 {
		tag := b[0]
		if tag&3 != 0 {
			t.Fatal("not a literal")
		}
		l := int(tag >> 2)
		b = b[1:]
		switch l {
		case 60:
			l, b = int(b[0]), b[1:]
		case 61:
			l, b = int(b[0])|int(b[1])<<8, b[2:]
		}
		l++
		out = append(out, b[:l]...)
		b = b[l:]
	}
	if uint64(len(out)) != n {
		t.Fatalf("length %d, header says %d", len(out), n)
	}
	return out
}

func TestSnappyLiteralSizes(t *testing.T) {
	for _, n := range []int{0, 1, 60, 61, 256, 257, 70000, 200000} {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(i)
		}
		if got := unsnappy(t, snappyLiteral(data)); string(got) != string(data) {
			t.Errorf("size %d round trip failed", n)
		}
	}
}

// field reads one protobuf field: number, wire type, payload (for length-
// delimited and fixed64) or value (varint).
func field(t *testing.T, b []byte) (num int, wt byte, payload []byte, val uint64, rest []byte) {
	key, k := binary.Uvarint(b)
	b = b[k:]
	num, wt = int(key>>3), byte(key&7)
	switch wt {
	case 0:
		val, k = binary.Uvarint(b)
		return num, wt, nil, val, b[k:]
	case 1:
		return num, wt, b[:8], binary.LittleEndian.Uint64(b[:8]), b[8:]
	case 2:
		l, k := binary.Uvarint(b)
		b = b[k:]
		return num, wt, b[:l], 0, b[l:]
	}
	t.Fatalf("wire type %d", wt)
	return
}

func TestPrometheusRemoteWrite(t *testing.T) {
	var rec recorder
	srv := rec.server(t)
	o, err := Open("prometheus="+srv.URL, "Checkout", "run1")
	if err != nil {
		t.Fatal(err)
	}
	tk, reqs := sampleTick()
	o.Tick(tk, reqs)
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if rec.hdr.Get("Content-Encoding") != "snappy" || rec.hdr.Get("Content-Type") != "application/x-protobuf" {
		t.Fatalf("headers %v", rec.hdr)
	}
	b := unsnappy(t, rec.bodies[0])
	found := map[string]float64{}
	for len(b) > 0 {
		num, _, ts, _, rest := field(t, b)
		b = rest
		if num != 1 {
			t.Fatalf("WriteRequest field %d", num)
		}
		labels, prev := map[string]string{}, ""
		var value float64
		var stamp uint64
		for len(ts) > 0 {
			n, _, payload, _, r := field(t, ts)
			ts = r
			switch n {
			case 1:
				_, _, name, _, r2 := field(t, payload)
				_, _, v, _, _ := field(t, r2)
				if string(name) < prev {
					t.Fatalf("labels not sorted: %q after %q", name, prev)
				}
				prev = string(name)
				labels[string(name)] = string(v)
			case 2:
				_, _, _, bits, r2 := field(t, payload)
				_, _, _, stamp, _ = field(t, r2)
				value = math.Float64frombits(bits)
			}
		}
		if labels["test"] != "Checkout" || labels["run"] != "run1" || stamp == 0 {
			t.Fatalf("series %v at %d", labels, stamp)
		}
		key := labels["__name__"]
		if q := labels["quantile"]; q != "" {
			key += "{" + q + "}"
		}
		if r := labels["request"]; r != "" {
			key += "[" + r + "]"
		}
		found[key] = value
	}
	for k, want := range map[string]float64{
		"pyro_vus": 10, "pyro_http_reqs_total": 250, "pyro_http_req_duration_seconds{0.99}": 0.08,
		"pyro_request_failed_total[shop/list items]": 3,
	} {
		if found[k] != want {
			t.Errorf("%s = %v, want %v", k, found[k], want)
		}
	}
}

func TestFileOutputs(t *testing.T) {
	dir := t.TempDir()
	set, err := OpenAll([]string{"json=" + filepath.Join(dir, "t.ndjson"), "csv=" + filepath.Join(dir, "t.csv")}, "t", "r")
	if err != nil {
		t.Fatal(err)
	}
	tk, reqs := sampleTick()
	set.Tick(tk, reqs)
	set.Tick(tk, reqs)
	if err := set.Close(); err != nil {
		t.Fatal(err)
	}
	j, _ := os.ReadFile(filepath.Join(dir, "t.ndjson"))
	if lines := strings.Split(strings.TrimSpace(string(j)), "\n"); len(lines) != 2 || !strings.Contains(lines[0], `"rps":250`) || !strings.Contains(lines[0], `"shop/list items"`) {
		t.Errorf("ndjson:\n%s", j)
	}
	c, _ := os.ReadFile(filepath.Join(dir, "t.csv"))
	rows := strings.Split(strings.TrimSpace(string(c)), "\n")
	if len(rows) != 3 || !strings.HasPrefix(rows[0], "time,elapsed_s,vus") || !strings.Contains(rows[1], ",3,10,12,250,250,0.02,") || !strings.HasSuffix(rows[1], ",1") {
		t.Errorf("csv:\n%s", c)
	}
}
