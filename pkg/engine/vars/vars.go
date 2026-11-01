// Package vars resolves {{name}} placeholders against layered variable scopes
// and Postman-compatible dynamic variables such as {{$guid}}.
package vars

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/model"
)

// maxDepth bounds nested resolution ({{a}} -> "{{b}}" -> ...) so that
// self-referencing variables can't loop forever.
const maxDepth = 10

var placeholder = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)

// Scope is one named layer of variables, e.g. "environment" or "collection".
type Scope struct {
	Name string
	Vars map[string]string
}

// Resolver looks names up from the narrowest scope to the broadest.
type Resolver struct {
	scopes []Scope
	// Now is overridable for deterministic tests.
	Now func() time.Time
}

// New builds a resolver; pass scopes narrowest first
// (runtime, data row, environment, folder, collection, globals).
func New(scopes ...Scope) *Resolver {
	return &Resolver{scopes: scopes, Now: time.Now}
}

// FromVariables converts model variables into a scope, skipping disabled ones.
func FromVariables(name string, vs []model.Variable) Scope {
	m := make(map[string]string, len(vs))
	for _, v := range vs {
		if !v.Disabled {
			m[v.Key] = v.Value
		}
	}
	return Scope{Name: name, Vars: m}
}

// Lookup returns the value of name and the scope it came from.
func (r *Resolver) Lookup(name string) (value, scope string, ok bool) {
	for _, s := range r.scopes {
		if v, ok := s.Vars[name]; ok {
			return v, s.Name, true
		}
	}
	return "", "", false
}

// Resolve substitutes every placeholder in s. Unknown names are left as-is
// and returned (deduplicated, in order of first appearance) in unresolved.
func (r *Resolver) Resolve(s string) (out string, unresolved []string) {
	seen := map[string]bool{}
	out = r.resolve(s, 0, func(name string) {
		if !seen[name] {
			seen[name] = true
			unresolved = append(unresolved, name)
		}
	})
	return out, unresolved
}

func (r *Resolver) resolve(s string, depth int, miss func(string)) string {
	if depth >= maxDepth || !strings.Contains(s, "{{") {
		return s
	}
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		name := placeholder.FindStringSubmatch(m)[1]
		if strings.HasPrefix(name, "$") {
			if v, ok := r.dynamic(name); ok {
				return v
			}
		}
		v, _, ok := r.Lookup(name)
		if !ok {
			miss(name)
			return m
		}
		return r.resolve(v, depth+1, miss)
	})
}

// dynamic implements the most common Postman dynamic variables.
func (r *Resolver) dynamic(name string) (string, bool) {
	switch name {
	case "$guid", "$randomUUID":
		return uuid4(), true
	case "$timestamp":
		return strconv.FormatInt(r.Now().Unix(), 10), true
	case "$isoTimestamp":
		return r.Now().UTC().Format("2006-01-02T15:04:05.000Z"), true
	case "$randomInt":
		return strconv.FormatInt(randInt(1001), 10), true
	case "$randomBoolean":
		return strconv.FormatBool(randInt(2) == 1), true
	case "$randomAlphaNumeric":
		return randString(1, alnum), true
	case "$randomHexadecimalColor":
		b := make([]byte, 3)
		_, _ = rand.Read(b)
		return "#" + hex.EncodeToString(b), true
	case "$randomEmail":
		return randString(8, lower) + "@example.com", true
	case "$randomUserName":
		return randString(8, lower), true
	case "$randomPassword":
		return randString(15, alnum), true
	case "$randomIP":
		return fmt.Sprintf("%d.%d.%d.%d", randInt(256), randInt(256), randInt(256), randInt(256)), true
	}
	return "", false
}

const (
	lower = "abcdefghijklmnopqrstuvwxyz"
	alnum = lower + "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

func randInt(n int64) int64 {
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		panic(err)
	}
	return v.Int64()
}

func randString(n int, alphabet string) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[randInt(int64(len(alphabet)))]
	}
	return string(b)
}

func uuid4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
