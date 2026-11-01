// Package auth applies request authentication and resolves inheritance from
// folders and collections.
package auth

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/model"
)

// Effective returns the auth that applies to a request: its own, otherwise
// the nearest ancestor's. chain is ordered outermost (collection) first.
// A nil result or type "none" means no auth.
func Effective(own *model.Auth, chain []*model.Collection) *model.Auth {
	if own != nil {
		return own
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i] != nil && chain[i].Auth != nil {
			return chain[i].Auth
		}
	}
	return nil
}

// Apply adds credentials to req (which must have variables resolved).
// Explicit headers or query params set by the user are not overwritten.
func Apply(req *model.Request, a *model.Auth) error {
	if a == nil {
		return nil
	}
	p := a.Params
	switch a.Type {
	case "", "none", "noauth":
		return nil
	case "basic":
		cred := base64.StdEncoding.EncodeToString([]byte(p["username"] + ":" + p["password"]))
		setHeader(req, "Authorization", "Basic "+cred)
	case "bearer":
		setHeader(req, "Authorization", "Bearer "+p["token"])
	case "apikey":
		if p["key"] == "" {
			return fmt.Errorf("API key auth: key name is empty")
		}
		if strings.EqualFold(p["in"], "query") {
			if !hasKey(req.Query, p["key"]) {
				req.Query = append(req.Query, model.KeyValue{Key: p["key"], Value: p["value"]})
			}
		} else {
			setHeader(req, p["key"], p["value"])
		}
	case "oauth2":
		// Token fetching happens in the engine (it needs the network); here
		// only a token that was pasted, imported or already fetched is used.
		if p["accessToken"] == "" {
			return fmt.Errorf("OAuth 2.0: no access token (set one, or configure a grant to fetch it)")
		}
		ApplyOAuth2Token(req, p, p["accessToken"])
	default:
		return fmt.Errorf("auth type %q is not supported yet", a.Type)
	}
	return nil
}

func setHeader(req *model.Request, key, value string) {
	if !hasKey(req.Headers, key) {
		req.Headers = append(req.Headers, model.KeyValue{Key: key, Value: value})
	}
}

func hasKey(kvs []model.KeyValue, key string) bool {
	for _, kv := range kvs {
		if !kv.Disabled && strings.EqualFold(kv.Key, key) {
			return true
		}
	}
	return false
}
