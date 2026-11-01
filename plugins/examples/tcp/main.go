// Command tcp is a Pyro protocol plugin for raw TCP: it sends the request
// body and reads the reply until a delimiter, a byte count, an idle gap or
// the server closing the connection.
package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	pyro "github.com/pyrohq/pyro/plugins/sdk/go"
)

func init() {
	pyro.RegisterProtocol("tcp", pyro.ProtocolFunc(execute))
}

func main() {}

const maxReply = 1 << 20

func execute(_ *pyro.Context, req *pyro.Request) (*pyro.Response, error) {
	addr := strings.TrimSpace(req.URL)
	for _, p := range []string{"tcp://", "tls://"} {
		addr = strings.TrimPrefix(addr, p)
	}
	addr = strings.TrimSuffix(addr, "/")
	if !strings.Contains(addr, ":") {
		return nil, fmt.Errorf("address %q needs a port, e.g. tcp://localhost:7000", req.URL)
	}
	payload, err := encodePayload(req.Body, req.Option("encoding", "text"))
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(atoi(req.Option("timeoutMs", "5000"), 5000)) * time.Millisecond
	useTLS := req.Option("tls", "false") == "true" || strings.HasPrefix(req.URL, "tls://")

	conn, err := pyro.Dial(addr, useTLS, timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if len(payload) > 0 {
		if _, err := conn.Write(payload); err != nil {
			return nil, err
		}
	}

	mode := req.Option("readUntil", "delimiter")
	delim := unescape(req.Option("delimiter", `\n`))
	want := atoi(req.Option("bytes", "1024"), 1024)
	conn.SetReadTimeout(timeout)
	var got bytes.Buffer
	buf := make([]byte, 32<<10)
	status := "OK"
	for got.Len() < maxReply {
		if mode == "idle" {
			conn.SetReadTimeout(300 * time.Millisecond)
		}
		n, err := conn.Read(buf)
		got.Write(buf[:n])
		if mode == "delimiter" && delim != "" && bytes.Contains(got.Bytes(), []byte(delim)) {
			break
		}
		if mode == "bytes" && got.Len() >= want {
			break
		}
		if errors.Is(err, io.EOF) {
			if mode != "close" && got.Len() == 0 {
				status = "Closed without a reply"
			}
			break
		}
		if errors.Is(err, pyro.ErrTimeout) {
			if mode == "idle" && got.Len() > 0 {
				break
			}
			return nil, fmt.Errorf("no complete reply within %v (read %d bytes)", timeout, got.Len())
		}
		if err != nil {
			return nil, err
		}
	}
	return &pyro.Response{
		StatusText: status,
		Body:       got.Bytes(),
		RemoteAddr: addr,
		Headers: []pyro.KeyValue{
			{Key: "Bytes-Sent", Value: strconv.Itoa(len(payload))},
			{Key: "Bytes-Received", Value: strconv.Itoa(got.Len())},
		},
	}, nil
}

func encodePayload(body, encoding string) ([]byte, error) {
	if encoding != "hex" {
		return []byte(body), nil
	}
	clean := strings.NewReplacer(" ", "", "\n", "", "\t", "", "0x", "").Replace(body)
	b, err := hex.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("payload is not valid hex: %v", err)
	}
	return b, nil
}

func unescape(s string) string {
	return strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\0`, "\x00").Replace(s)
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
		return n
	}
	return def
}
