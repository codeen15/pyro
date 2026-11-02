// Command hmac is a Pyro plugin with two features:
//
//   - auth type hmac: adds X-Timestamp and an HMAC-SHA256 signature over
//     "METHOD\nURL\nTIMESTAMP\nBODY";
//   - processor hmac-var: before a request it stores the HMAC of a message
//     in a variable; after it, it checks a response header holds the HMAC
//     of the response body.
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	pyro "github.com/codeen15/pyro/plugins/sdk/go"
)

func init() {
	pyro.RegisterAuth("hmac", pyro.AuthFunc(sign))
	pyro.RegisterProcessor("hmac-var", pyro.ProcessorFunc(process))
}

func hexMAC(secret, msg string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func process(ctx *pyro.Context, p *pyro.Process) error {
	secret := ctx.Params["secret"]
	if secret == "" {
		return errors.New("HMAC secret is empty")
	}
	if p.Phase == "pre" {
		name := ctx.Params["var"]
		if name == "" {
			name = "signature"
		}
		p.SetVar(name, hexMAC(secret, ctx.Params["message"]))
		return nil
	}
	header := ctx.Params["header"]
	if header == "" {
		header = "X-Signature"
	}
	got := ""
	for _, h := range p.Response.Headers {
		if strings.EqualFold(h.Key, header) {
			got = h.Value
		}
	}
	want := hexMAC(secret, string(p.Response.Body))
	msg := ""
	if !hmac.Equal([]byte(got), []byte(want)) {
		msg = header + " doesn't match the body's HMAC"
	}
	p.Test("response signature ("+header+")", msg == "", msg)
	return nil
}

func main() {}

func sign(ctx *pyro.Context, req *pyro.Request) error {
	secret := ctx.Params["secret"]
	if secret == "" {
		return errors.New("HMAC secret is empty")
	}
	header := ctx.Params["header"]
	if header == "" {
		header = "X-Signature"
	}
	ts := req.Header("X-Timestamp")
	if ts == "" {
		ts = strconv.FormatInt(time.Now().Unix(), 10)
		req.SetHeader("X-Timestamp", ts)
	}
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = "GET"
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(method + "\n" + req.URL + "\n" + ts + "\n" + req.Body))
	sig := hex.EncodeToString(mac.Sum(nil))
	if id := ctx.Params["keyId"]; id != "" {
		sig = "keyId=" + id + ",sig=" + sig
	}
	req.SetHeader(header, sig)
	return nil
}
