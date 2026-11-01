package plugin

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestIndexNeedsHTTPS(t *testing.T) {
	for raw, ok := range map[string]bool{"https://plugins.example/i.json": true, "http://127.0.0.1:8080/i.json": true, "http://localhost/i.json": true, "http://plugins.example/i.json": false, "file:///etc/passwd": false} {
		u, _ := url.Parse(raw)
		if secureURL(u) != ok {
			t.Errorf("%s: secure = %v", raw, !ok)
		}
	}
	if _, err := FetchIndex(context.Background(), "http://plugins.example/index.json"); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("err = %v", err)
	}
}
