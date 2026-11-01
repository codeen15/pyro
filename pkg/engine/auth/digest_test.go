package auth

import (
	"strings"
	"testing"
)

// RFC 7616 §3.9.1.
func TestDigestRFC7616Example(t *testing.T) {
	for algo, want := range map[string]string{
		"MD5":     "8ca523f5e9506fed4657c9700eebdbec",
		"SHA-256": "753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1",
	} {
		c, ok := ParseDigest(`Digest realm="http-auth@example.org", qop="auth, auth-int", algorithm=` + algo +
			`, nonce="7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v", opaque="FQhe/qaU925kfnzjCev0ciny7QMkPqMAFRtzCUYo5tdS"`)
		if !ok {
			t.Fatalf("%s: challenge not parsed", algo)
		}
		h := c.Authorization("GET", "/dir/index.html", "Mufasa", "Circle of Life", "f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ")
		if !strings.Contains(h, `response="`+want+`"`) || !strings.Contains(h, "nc=00000001") || !strings.Contains(h, `opaque="FQhe`) {
			t.Errorf("%s: %s", algo, h)
		}
		if h2 := c.Authorization("GET", "/dir/index.html", "Mufasa", "Circle of Life", "x"); !strings.Contains(h2, "nc=00000002") {
			t.Errorf("nonce count not incremented: %s", h2)
		}
	}
	if _, ok := ParseDigest(`Basic realm="x"`); ok {
		t.Error("Basic parsed as Digest")
	}
	if _, ok := ParseDigest(`Digest realm="x", nonce="n", qop="auth-int"`); ok {
		t.Error("auth-int-only challenge accepted")
	}
	if _, ok := ParseDigest(`Digest realm="x", nonce="n", algorithm=SHA-512-256`); ok {
		t.Error("unsupported algorithm accepted")
	}
}
