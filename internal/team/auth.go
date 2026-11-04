package team

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Password hashing: argon2id with OWASP's recommended minimums.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	minPassword  = 10
)

func checkPassword(pw string) error {
	if utf8.RuneCountInString(pw) < minPassword {
		return invalid("use a password of at least %d characters", minPassword)
	}
	if len(pw) > 1024 {
		return invalid("that password is too long")
	}
	return nil
}

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// dummyHash keeps a failed lookup as slow as a wrong password.
var dummyHash, _ = hashPassword("pyro-not-a-real-password")

func verifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Sessions: the cookie holds a random token; the database keeps its hash.
const (
	sessionTTL     = 14 * 24 * time.Hour
	sessionRefresh = 24 * time.Hour // extend at most once a day
)

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// CreateSession signs a user in and returns the cookie token.
func (s *Store) CreateSession(ctx context.Context, userID, ip, ua string) (string, error) {
	tok := randomToken()
	now := time.Now()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	row := &sessionRow{Hash: hashToken(tok), UserID: userID, Expires: now.Add(sessionTTL).Unix(), Created: now.Unix(), IP: ip, UA: ua}
	if _, err := s.db.NewInsert().Model(row).Exec(ctx); err != nil {
		return "", err
	}
	// Opportunistic cleanup of expired sessions.
	_, _ = s.db.NewDelete().Model((*sessionRow)(nil)).Where("expires < ?", now.Unix()).Exec(ctx)
	return tok, nil
}

// SessionUser returns the signed-in user for a cookie token, sliding the
// session's expiry forward.
func (s *Store) SessionUser(ctx context.Context, tok string) (*User, string, error) {
	if tok == "" {
		return nil, "", ErrNotFound
	}
	h := hashToken(tok)
	var sess sessionRow
	if err := s.db.NewSelect().Model(&sess).Where("hash = ?", h).Scan(ctx); err != nil {
		return nil, "", notFound(err)
	}
	u, err := s.UserByID(ctx, sess.UserID)
	if err != nil {
		return nil, "", err
	}
	now := time.Now()
	if now.Unix() > sess.Expires || u.Disabled {
		_, _ = s.db.NewDelete().Model((*sessionRow)(nil)).Where("hash = ?", h).Exec(ctx)
		return nil, "", ErrNotFound
	}
	if time.Unix(sess.Expires, 0).Sub(now) < sessionTTL-sessionRefresh {
		_, _ = s.db.NewUpdate().Model((*sessionRow)(nil)).Set("expires = ?", now.Add(sessionTTL).Unix()).Where("hash = ?", h).Exec(ctx)
	}
	return u, h, nil
}

// DeleteSession signs one session out.
func (s *Store) DeleteSession(ctx context.Context, tok string) error {
	_, err := s.db.NewDelete().Model((*sessionRow)(nil)).Where("hash = ?", hashToken(tok)).Exec(ctx)
	return err
}

// limiter counts failed sign-ins per key in a sliding window.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) recent(key string, now time.Time) []time.Time {
	h := l.hits[key]
	i := 0
	for i < len(h) && now.Sub(h[i]) > l.window {
		i++
	}
	h = h[i:]
	if len(h) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = h
	}
	return h
}

// blocked reports whether key has used up its attempts.
func (l *limiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key, time.Now())) >= l.max
}

func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.hits[key] = append(l.recent(key, now), now)
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}
