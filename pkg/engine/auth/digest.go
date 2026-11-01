package auth

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"net/url"
	"strings"
	"sync"
)

// DigestChallenge is a parsed WWW-Authenticate: Digest header (RFC 7616).
type DigestChallenge struct {
	Realm, Nonce, Opaque, Algorithm, Qop string
	userhash                             bool
	nc                                   uint32
}

// ParseDigest parses a WWW-Authenticate value; ok is false if it isn't a
// Digest challenge Pyro can answer (qop must be absent or offer "auth").
func ParseDigest(header string) (*DigestChallenge, bool) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(header), " ")
	if !strings.EqualFold(scheme, "Digest") {
		return nil, false
	}
	c := &DigestChallenge{}
	for _, kv := range splitParams(rest) {
		k, v, _ := strings.Cut(kv, "=")
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "realm":
			c.Realm = v
		case "nonce":
			c.Nonce = v
		case "opaque":
			c.Opaque = v
		case "algorithm":
			c.Algorithm = v
		case "qop":
			for _, q := range strings.Split(v, ",") {
				if strings.TrimSpace(q) == "auth" {
					c.Qop = "auth"
				}
			}
			if c.Qop == "" {
				return nil, false // only auth-int offered
			}
		case "userhash":
			c.userhash = strings.EqualFold(v, "true")
		}
	}
	if c.Nonce == "" || c.newHash() == nil {
		return nil, false
	}
	return c, true
}

// splitParams splits on commas outside quotes.
func splitParams(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case r == ',' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func (c *DigestChallenge) newHash() func() hash.Hash {
	switch strings.ToUpper(strings.TrimSuffix(strings.ToUpper(c.Algorithm), "-SESS")) {
	case "", "MD5":
		return md5.New
	case "SHA-256":
		return sha256.New
	}
	return nil
}

func (c *DigestChallenge) h(s string) string {
	h := c.newHash()()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// Authorization computes the header for one request; each call uses the
// next nonce count. uri is the request target (path and query).
func (c *DigestChallenge) Authorization(method, uri, user, pass, cnonce string) string {
	c.nc++
	nc := fmt.Sprintf("%08x", c.nc)
	ha1 := c.h(user + ":" + c.Realm + ":" + pass)
	if strings.HasSuffix(strings.ToUpper(c.Algorithm), "-SESS") {
		ha1 = c.h(ha1 + ":" + c.Nonce + ":" + cnonce)
	}
	ha2 := c.h(method + ":" + uri)
	var resp string
	if c.Qop != "" {
		resp = c.h(strings.Join([]string{ha1, c.Nonce, nc, cnonce, c.Qop, ha2}, ":"))
	} else {
		resp = c.h(ha1 + ":" + c.Nonce + ":" + ha2)
	}
	username := user
	if c.userhash {
		username = c.h(user + ":" + c.Realm)
	}
	parts := []string{
		fmt.Sprintf(`username=%q`, username), fmt.Sprintf(`realm=%q`, c.Realm), fmt.Sprintf(`nonce=%q`, c.Nonce),
		fmt.Sprintf(`uri=%q`, uri), fmt.Sprintf(`response=%q`, resp),
	}
	if c.Algorithm != "" {
		parts = append(parts, "algorithm="+c.Algorithm)
	}
	if c.Opaque != "" {
		parts = append(parts, fmt.Sprintf(`opaque=%q`, c.Opaque))
	}
	if c.Qop != "" {
		parts = append(parts, "qop="+c.Qop, "nc="+nc, fmt.Sprintf(`cnonce=%q`, cnonce))
	}
	if c.userhash {
		parts = append(parts, "userhash=true")
	}
	return "Digest " + strings.Join(parts, ", ")
}

// DigestCache remembers the last challenge per host, so later requests
// authenticate up front instead of taking a 401 round trip each time.
type DigestCache struct {
	mu sync.Mutex
	m  map[string]*DigestChallenge
}

// Authorize returns a header for rawURL from a cached challenge, or "".
func (d *DigestCache) Authorize(method, rawURL, user, pass string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.m[u.Host]
	if c == nil {
		return ""
	}
	return c.Authorization(method, u.RequestURI(), user, pass, randomString(16))
}

// Answer stores a fresh challenge for rawURL's host and returns the header
// that answers it.
func (d *DigestCache) Answer(c *DigestChallenge, method, rawURL, user, pass string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = map[string]*DigestChallenge{}
	}
	d.m[u.Host] = c
	return c.Authorization(method, u.RequestURI(), user, pass, randomString(16))
}
