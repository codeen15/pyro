package team

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Sign-in uses two tokens:
//   - an access token: a JWT (HS256) valid for AccessTTL, checked on every
//     request without a database lookup of the session;
//   - a refresh token: a random value whose hash is a row in sessions,
//     valid for 14 days from last use. It mints new access tokens and is what
//     signing out deletes.
//
// Browsers get both as HttpOnly cookies and never see them; API clients and
// the CLI get them from POST /auth/token and send "Authorization: Bearer".
// A user's tokens_after time revokes every access token issued before it
// (password change, sign out everywhere).

const (
	// AccessCookie holds the access JWT; RefreshCookie the refresh token.
	AccessCookie  = "pyro_access"
	RefreshCookie = "pyro_session"

	defaultAccessTTL = 15 * time.Minute
	jwtIssuer        = "pyro"
	minSecret        = 32
)

// Claims are the access token's contents.
type Claims struct {
	Name  string `json:"name,omitempty"`
	Admin bool   `json:"admin,omitempty"`
	// IssuedMS is iat in milliseconds, so a revocation in the same second
	// as a sign-in tells them apart.
	IssuedMS int64 `json:"iat_ms"`
	jwt.RegisteredClaims
}

type issuer struct {
	secret []byte
	ttl    time.Duration
}

func (i *issuer) mint(u *User) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(i.ttl)
	c := Claims{
		Name: u.Name, Admin: u.Admin, IssuedMS: now.UnixMilli(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: jwtIssuer, Subject: u.ID, IssuedAt: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp), ID: newID(),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(i.secret)
	return tok, exp, err
}

func (i *issuer) parse(tok string) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(tok, &c, func(*jwt.Token) (any, error) { return i.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(jwtIssuer),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(30*time.Second))
	if err != nil {
		return nil, err
	}
	if c.Subject == "" {
		return nil, errors.New("token has no subject")
	}
	return &c, nil
}

// LoadSecret returns the JWT signing key: $PYRO_JWT_SECRET if set (needed
// when several servers share a database), else <dataDir>/jwt.secret,
// created with a random key on first start.
func LoadSecret(dataDir string) ([]byte, error) {
	if s := os.Getenv("PYRO_JWT_SECRET"); s != "" {
		if len(s) < minSecret {
			return nil, fmt.Errorf("PYRO_JWT_SECRET must be at least %d characters", minSecret)
		}
		return []byte(s), nil
	}
	file := filepath.Join(dataDir, "jwt.secret")
	if b, err := os.ReadFile(file); err == nil {
		key := []byte(strings.TrimSpace(string(b)))
		if len(key) < minSecret {
			return nil, fmt.Errorf("%s is shorter than %d characters", file, minSecret)
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := []byte(randomToken()) // 43 characters of base64url
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(file, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// bearer returns the token from an "Authorization: Bearer" header.
func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// userFromAccess checks an access JWT and loads its user, refusing tokens
// issued before the user's tokens_after (revoked) or for disabled accounts.
func (s *Server) userFromAccess(ctx context.Context, tok string) (*User, error) {
	c, err := s.tokens.parse(tok)
	if err != nil {
		return nil, err
	}
	u, err := s.st.UserByID(ctx, c.Subject)
	if err != nil {
		return nil, err
	}
	if u.Disabled || (u.tokensAfter > 0 && c.IssuedMS < u.tokensAfter) {
		return nil, errors.New("token revoked")
	}
	return u, nil
}

// setAccessCookie gives the browser a fresh access JWT.
func (s *Server) setAccessCookie(w http.ResponseWriter, r *http.Request, u *User) error {
	tok, _, err := s.tokens.mint(u)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: AccessCookie, Value: tok, Path: "/", HttpOnly: true, Secure: s.secureCookies(r),
		SameSite: http.SameSiteLaxMode, MaxAge: int(s.tokens.ttl.Seconds()),
	})
	return nil
}

// authenticate finds the signed-in user: a Bearer JWT, else the access
// cookie, else the refresh cookie (minting a new access cookie). The second
// result is the refresh session's hash, when one was used or is present.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (*User, string, error) {
	ctx := r.Context()
	if tok := bearer(r); tok != "" {
		if strings.HasPrefix(tok, PATPrefix) {
			u, err := s.st.UserByAPIToken(ctx, tok)
			return u, "", err
		}
		u, err := s.userFromAccess(ctx, tok)
		return u, "", err
	}
	refresh := ""
	if c, err := r.Cookie(RefreshCookie); err == nil {
		refresh = c.Value
	}
	if c, err := r.Cookie(AccessCookie); err == nil {
		if u, err := s.userFromAccess(ctx, c.Value); err == nil {
			if refresh != "" {
				return u, hashToken(refresh), nil
			}
			return u, "", nil
		}
	}
	if refresh == "" {
		return nil, "", ErrNotFound
	}
	u, h, err := s.st.SessionUser(ctx, refresh)
	if err != nil {
		return nil, "", err
	}
	if err := s.setAccessCookie(w, r, u); err != nil {
		return nil, "", err
	}
	return u, h, nil
}
