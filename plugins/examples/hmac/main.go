// Command hmac is a Pyro auth plugin: it adds X-Timestamp and an
// HMAC-SHA256 signature over "METHOD\nURL\nTIMESTAMP\nBODY".
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	pyro "github.com/pyrohq/pyro/plugins/sdk/go"
)

func init() {
	pyro.RegisterAuth("hmac", pyro.AuthFunc(sign))
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
