// Package httpdriver executes HTTP requests with per-phase timings.
package httpdriver

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/protocol"
)

type Options struct {
	Timeout         time.Duration // whole request, default 30s
	FollowRedirects bool
	MaxRedirects    int   // default 10
	MaxBodyBytes    int64 // response bytes kept in memory, default 10 MiB
	InsecureTLS     bool
	// FileRoot resolves relative paths in multipart/file bodies.
	FileRoot string
	// ReadFile, when set, replaces reading body files from disk (agents
	// serve the files a controller sent with the job).
	ReadFile func(path string) ([]byte, error)
	// MaxIdleConnsPerHost keeps connections open for reuse; default 100
	// (net/http's default of 2 throttles load tests).
	MaxIdleConnsPerHost int
}

// Driver sends HTTP requests. It is safe for concurrent use; the cookie jar
// is shared by all requests sent through the same Driver.
type Driver struct {
	opts   Options
	client *http.Client
	Jar    http.CookieJar
}

func New(opts Options) *Driver {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.MaxRedirects == 0 {
		opts.MaxRedirects = 10
	}
	if opts.MaxBodyBytes == 0 {
		opts.MaxBodyBytes = 10 << 20
	}
	if opts.MaxIdleConnsPerHost == 0 {
		opts.MaxIdleConnsPerHost = 100
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: opts.InsecureTLS} //nolint:gosec // user opt-in
	// Pyro reports what the server sent; don't transparently decompress.
	transport.DisableCompression = true
	transport.MaxIdleConns = 0 // no global cap; per-host cap below
	transport.MaxIdleConnsPerHost = opts.MaxIdleConnsPerHost
	jar, _ := cookiejar.New(nil)

	d := &Driver{opts: opts, Jar: jar}
	d.client = &http.Client{
		Transport: transport,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !opts.FollowRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) >= opts.MaxRedirects {
				return fmt.Errorf("stopped after %d redirects", opts.MaxRedirects)
			}
			return nil
		},
	}
	return d
}

func (d *Driver) Kind() model.Kind { return model.KindHTTP }

// ForVU returns a driver that shares this one's connection pool but has its
// own cookie jar, so each virtual user behaves like a separate client.
func (d *Driver) ForVU() *Driver {
	jar, _ := cookiejar.New(nil)
	c := *d.client
	c.Jar = jar
	return &Driver{opts: d.opts, client: &c, Jar: jar}
}

// Execute sends req, which must already have variables resolved and auth applied.
func (d *Driver) Execute(ctx context.Context, req *model.Request) (*protocol.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, d.opts.Timeout)
	defer cancel()

	hreq, err := d.build(ctx, req)
	if err != nil {
		return nil, err
	}

	// Dial callbacks can run on the transport's dial goroutine, even after
	// this request was handed a different (pooled) connection, so the
	// trace state is mutex-guarded and dial timings are frozen at GotConn.
	var (
		mu                                   sync.Mutex
		t                                    protocol.Timings
		start, dnsStart, connStart, tlsStart time.Time
		wroteAt, firstByte                   time.Time
		remote                               string
		gotConn                              bool
	)
	locked := func(f func()) {
		mu.Lock()
		defer mu.Unlock()
		f()
	}
	dial := func(f func()) {
		locked(func() {
			if !gotConn {
				f()
			}
		})
	}
	trace := &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { dial(func() { dnsStart = time.Now() }) },
		DNSDone:           func(httptrace.DNSDoneInfo) { dial(func() { t.DNS = time.Since(dnsStart) }) },
		ConnectStart:      func(string, string) { dial(func() { connStart = time.Now() }) },
		ConnectDone:       func(string, string, error) { dial(func() { t.Connect = time.Since(connStart) }) },
		TLSHandshakeStart: func() { dial(func() { tlsStart = time.Now() }) },
		TLSHandshakeDone:  func(tls.ConnectionState, error) { dial(func() { t.TLS = time.Since(tlsStart) }) },
		GotConn: func(info httptrace.GotConnInfo) {
			locked(func() {
				gotConn = true
				t.Reused = info.Reused
				if info.Reused {
					t.DNS, t.Connect, t.TLS = 0, 0, 0
				}
				if info.Conn != nil {
					remote = info.Conn.RemoteAddr().String()
				}
			})
		},
		WroteRequest:         func(httptrace.WroteRequestInfo) { locked(func() { wroteAt = time.Now() }) },
		GotFirstResponseByte: func() { locked(func() { firstByte = time.Now() }) },
	}
	hreq = hreq.WithContext(httptrace.WithClientTrace(ctx, trace))

	start = time.Now()
	res, err := d.client.Do(hreq)
	if err != nil {
		return nil, describe(err)
	}
	defer res.Body.Close()

	body, size, truncated, err := readLimited(res.Body, d.opts.MaxBodyBytes)
	end := time.Now()
	mu.Lock()
	defer mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", describe(err))
	}

	t.Total = end.Sub(start)
	if !wroteAt.IsZero() {
		t.Send = wroteAt.Sub(start) - t.DNS - t.Connect - t.TLS
		if t.Send < 0 {
			t.Send = 0
		}
		if !firstByte.IsZero() {
			t.Wait = firstByte.Sub(wroteAt)
		}
	}
	if !firstByte.IsZero() {
		t.Download = end.Sub(firstByte)
	}

	out := &protocol.Response{
		Status:        res.StatusCode,
		StatusText:    strings.TrimSpace(strings.TrimPrefix(res.Status, fmt.Sprint(res.StatusCode))),
		Proto:         res.Proto,
		Body:          body,
		BodyTruncated: truncated,
		Size:          size,
		Timings:       t,
		RemoteAddr:    remote,
	}
	for k, vs := range res.Header {
		for _, v := range vs {
			out.Headers = append(out.Headers, model.KeyValue{Key: k, Value: v})
		}
	}
	for _, c := range res.Cookies() {
		out.Cookies = append(out.Cookies, protocol.Cookie{
			Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path,
			Expires: c.Expires, HttpOnly: c.HttpOnly, Secure: c.Secure,
		})
	}
	return out, nil
}

func (d *Driver) build(ctx context.Context, req *model.Request) (*http.Request, error) {
	u, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", req.URL, err)
	}
	if u.Scheme == "" {
		// Like Postman, assume http:// when the scheme is omitted.
		if u, err = url.Parse("http://" + strings.TrimSpace(req.URL)); err != nil {
			return nil, fmt.Errorf("invalid URL %q: %w", req.URL, err)
		}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("URL %q has no host", req.URL)
	}
	if len(req.Query) > 0 {
		q := u.Query()
		for _, kv := range req.Query {
			if !kv.Disabled {
				q.Add(kv.Key, kv.Value)
			}
		}
		u.RawQuery = q.Encode()
	}

	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}
	body, contentType, err := d.body(req.Body)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("User-Agent", "Pyro")
	hreq.Header.Set("Accept", "*/*")
	for _, kv := range req.Headers {
		if kv.Disabled || kv.Key == "" {
			continue
		}
		if strings.EqualFold(kv.Key, "Host") {
			hreq.Host = kv.Value
			continue
		}
		hreq.Header.Add(kv.Key, kv.Value)
	}
	if contentType != "" && hreq.Header.Get("Content-Type") == "" {
		hreq.Header.Set("Content-Type", contentType)
	}
	return hreq, nil
}

// body encodes the request body and returns the default Content-Type,
// which explicit headers override.
func (d *Driver) body(b *model.Body) (io.Reader, string, error) {
	if b == nil {
		return nil, "", nil
	}
	switch b.Type {
	case "", model.BodyNone:
		return nil, "", nil
	case model.BodyJSON:
		return strings.NewReader(b.Content), "application/json", nil
	case model.BodyText:
		return strings.NewReader(b.Content), "text/plain; charset=utf-8", nil
	case model.BodyXML:
		return strings.NewReader(b.Content), "application/xml", nil
	case model.BodyGraphQL:
		payload, err := graphQLPayload(b.Content, b.Variables)
		if err != nil {
			return nil, "", err
		}
		return bytes.NewReader(payload), "application/json", nil
	case model.BodyForm:
		v := url.Values{}
		for _, f := range b.Fields {
			if !f.Disabled {
				v.Add(f.Key, f.Value)
			}
		}
		return strings.NewReader(v.Encode()), "application/x-www-form-urlencoded", nil
	case model.BodyMultipart:
		return d.multipart(b.Fields)
	case model.BodyBinaryFile:
		data, err := d.readFile(b.File)
		if err != nil {
			return nil, "", fmt.Errorf("body file: %w", err)
		}
		return bytes.NewReader(data), "application/octet-stream", nil
	}
	return nil, "", fmt.Errorf("unsupported body type %q", b.Type)
}

func (d *Driver) multipart(fields []model.KeyValue) (io.Reader, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range fields {
		if f.Disabled {
			continue
		}
		if !f.File {
			if err := w.WriteField(f.Key, f.Value); err != nil {
				return nil, "", err
			}
			continue
		}
		data, err := d.readFile(f.Value)
		if err != nil {
			return nil, "", fmt.Errorf("form file %q: %w", f.Key, err)
		}
		part, err := w.CreateFormFile(f.Key, filepath.Base(f.Value))
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(data); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

func (d *Driver) readFile(p string) ([]byte, error) {
	if d.opts.ReadFile != nil {
		return d.opts.ReadFile(p)
	}
	return os.ReadFile(d.path(p))
}

func (d *Driver) path(p string) string {
	if filepath.IsAbs(p) || d.opts.FileRoot == "" {
		return p
	}
	return filepath.Join(d.opts.FileRoot, p)
}

func graphQLPayload(query, variables string) ([]byte, error) {
	vars := json.RawMessage("null")
	if strings.TrimSpace(variables) != "" {
		if !json.Valid([]byte(variables)) {
			return nil, errors.New("GraphQL variables must be valid JSON")
		}
		vars = json.RawMessage(variables)
	}
	return json.Marshal(struct {
		Query     string          `json:"query"`
		Variables json.RawMessage `json:"variables"`
	}{query, vars})
}

// readLimited keeps up to limit bytes but drains the rest so that Size and
// the download timing reflect the full body.
func readLimited(r io.Reader, limit int64) (body []byte, size int64, truncated bool, err error) {
	var buf bytes.Buffer
	n, err := io.CopyN(&buf, r, limit)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, n, false, err
	}
	if errors.Is(err, io.EOF) {
		return buf.Bytes(), n, false, nil
	}
	rest, err := io.Copy(io.Discard, r)
	if err != nil {
		return nil, n + rest, true, err
	}
	return buf.Bytes(), n + rest, rest > 0, nil
}

// describe turns low-level network errors into messages a user can act on.
func describe(err error) error {
	var dnsErr *net.DNSError
	var opErr *net.OpError
	var certErr *tls.CertificateVerificationError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("request timed out")
	case errors.Is(err, context.Canceled):
		return errors.New("request cancelled")
	case errors.As(err, &dnsErr):
		return fmt.Errorf("could not resolve host %q", dnsErr.Name)
	case errors.As(err, &certErr):
		return fmt.Errorf("TLS certificate not trusted: %v (enable insecure TLS to skip verification)", certErr.Err)
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return fmt.Errorf("could not connect to %s: %v", opErr.Addr, opErr.Err)
	}
	return err
}
