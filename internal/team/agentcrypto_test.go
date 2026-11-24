package team

import (
	"errors"
	"strings"
	"testing"
)

func TestTokenBoxRoundTrip(t *testing.T) {
	box, err := newTokenBox([]byte("a-jwt-secret"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := box.seal("s3cret-token")
	if err != nil {
		t.Fatal(err)
	}
	b, err := box.seal("s3cret-token")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two seals of the same token must differ (random nonce)")
	}
	if strings.Contains(a, "s3cret-token") {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := box.open(a)
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cret-token" {
		t.Fatalf("open = %q", got)
	}
}

func TestTokenBoxWrongSecret(t *testing.T) {
	one, err := newTokenBox([]byte("secret-one"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := newTokenBox([]byte("secret-two"))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := one.seal("tok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := two.open(enc); !errors.Is(err, errTokenUnreadable) {
		t.Fatalf("err = %v, want errTokenUnreadable", err)
	}
	if _, err := one.open("not base64 !!"); !errors.Is(err, errTokenUnreadable) {
		t.Fatalf("garbage err = %v, want errTokenUnreadable", err)
	}
	if _, err := one.open(""); !errors.Is(err, errTokenUnreadable) {
		t.Fatalf("empty err = %v, want errTokenUnreadable", err)
	}
}

func TestConnectToken(t *testing.T) {
	tok, hash, err := newConnectToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "pyro_agent_") {
		t.Fatalf("token = %q", tok)
	}
	if len(tok) != 11+43 {
		t.Fatalf("len = %d", len(tok))
	}
	if hashAgentToken(tok) != hash {
		t.Fatal("hash does not match token")
	}
	tok2, hash2, err := newConnectToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok == tok2 || hash == hash2 {
		t.Fatal("tokens must differ")
	}
}
