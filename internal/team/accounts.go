package team

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// Accounts beyond passwords: personal access tokens, password resets,
// accounts from a directory or identity provider, and administration.

// PATPrefix starts every personal access token, so a Bearer value is
// recognisable (and scanners can spot leaked ones).
const PATPrefix = "pyro_pat_"

// APIToken is a personal access token as its owner sees it.
type APIToken struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"lastUsed,omitempty"`
	Expires  *time.Time `json:"expires,omitempty"`
}

func (r *apiTokenRow) view() APIToken {
	t := APIToken{ID: r.ID, Name: r.Name, Created: time.Unix(r.Created, 0)}
	if r.LastUsed > 0 {
		lu := time.Unix(r.LastUsed, 0)
		t.LastUsed = &lu
	}
	if r.Expires > 0 {
		ex := time.Unix(r.Expires, 0)
		t.Expires = &ex
	}
	return t
}

// CreateAPIToken makes a personal access token; ttl 0 never expires. The
// token is returned once and only its hash is kept.
func (s *Store) CreateAPIToken(ctx context.Context, userID, name string, ttl time.Duration) (string, *APIToken, error) {
	name = tidy(name)
	if name == "" {
		return "", nil, invalid("give the token a name, e.g. where it's used")
	}
	tok := PATPrefix + randomToken()
	now := time.Now()
	row := &apiTokenRow{ID: newID(), Hash: hashToken(tok), UserID: userID, Name: name, Created: now.Unix()}
	if ttl > 0 {
		row.Expires = now.Add(ttl).Unix()
	}
	if _, err := s.db.NewInsert().Model(row).Exec(ctx); err != nil {
		return "", nil, err
	}
	v := row.view()
	return tok, &v, nil
}

// APITokens lists a user's tokens, newest first.
func (s *Store) APITokens(ctx context.Context, userID string) ([]APIToken, error) {
	var rows []apiTokenRow
	if err := s.db.NewSelect().Model(&rows).Where(`"user" = ?`, userID).Order("created DESC").Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]APIToken, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].view())
	}
	return out, nil
}

// RevokeAPIToken deletes one of the user's tokens.
func (s *Store) RevokeAPIToken(ctx context.Context, userID, id string) error {
	res, err := s.db.NewDelete().Model((*apiTokenRow)(nil)).Where(`"user" = ? AND id = ?`, userID, id).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UserByAPIToken returns the user a personal access token belongs to.
func (s *Store) UserByAPIToken(ctx context.Context, tok string) (*User, error) {
	var row apiTokenRow
	if err := s.db.NewSelect().Model(&row).Where("hash = ?", hashToken(tok)).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	now := time.Now()
	if row.Expires > 0 && now.Unix() > row.Expires {
		return nil, ErrNotFound
	}
	u, err := s.UserByID(ctx, row.UserID)
	if err != nil || u.Disabled {
		return nil, ErrNotFound
	}
	if now.Unix()-row.LastUsed > 60 { // record use at most once a minute
		_, _ = s.db.NewUpdate().Model((*apiTokenRow)(nil)).Set("last_used = ?", now.Unix()).Where("id = ?", row.ID).Exec(ctx)
	}
	return u, nil
}

// CreateReset makes a one-time password reset token for a local account.
func (s *Store) CreateReset(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if u.Provider != "local" {
		return "", invalid("%s signs in through %s; reset the password there", u.Email, strings.ToUpper(u.Provider))
	}
	tok := randomToken()
	now := time.Now()
	_, err = s.db.NewInsert().Model(&resetRow{Hash: hashToken(tok), UserID: userID, Created: now.Unix(), Expires: now.Add(ttl).Unix()}).Exec(ctx)
	return tok, err
}

var errResetGone = errors.New("this reset link has expired or was already used; ask for a new one")

func (s *Store) resetFor(ctx context.Context, q bun.IDB, tok string) (*resetRow, error) {
	var row resetRow
	err := q.NewSelect().Model(&row).Where("hash = ?", hashToken(tok)).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (row.Used || time.Now().Unix() > row.Expires) {
		return nil, errResetGone
	}
	return &row, err
}

// ResetEmail returns the address a reset link is for (to show on the page).
func (s *Store) ResetEmail(ctx context.Context, tok string) (string, error) {
	row, err := s.resetFor(ctx, s.db, tok)
	if err != nil {
		return "", err
	}
	u, err := s.UserByID(ctx, row.UserID)
	if err != nil {
		return "", err
	}
	return u.Email, nil
}

// UseReset sets a new password with a reset token, signing the account out
// everywhere.
func (s *Store) UseReset(ctx context.Context, tok, password string) (*User, error) {
	if err := checkPassword(password); err != nil {
		return nil, err
	}
	var userID string
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		row, err := s.resetFor(ctx, tx, tok)
		if err != nil {
			return err
		}
		userID = row.UserID
		res, err := tx.NewUpdate().Model((*resetRow)(nil)).Set("used = ?", true).Where("hash = ? AND used = ?", row.Hash, false).Exec(ctx)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errResetGone
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.SetPassword(ctx, userID, password, ""); err != nil {
		return nil, err
	}
	return s.UserByID(ctx, userID)
}

// External describes an account from a directory or identity provider.
type External struct {
	Provider string // ldap | oidc
	Subject  string // LDAP DN, or "<issuer>|<sub>"
	Email    string
	Name     string
	// Admin, when set, syncs server administration from a group.
	Admin *bool
	// LinkByEmail lets a sign-in take over an existing account with the same
	// email (only when the provider vouches for the address).
	LinkByEmail bool
}

// UpsertExternal finds or creates the account for an external sign-in and
// refreshes its name, email and (when given) admin flag.
func (s *Store) UpsertExternal(ctx context.Context, e External) (*User, error) {
	e.Email = strings.ToLower(tidy(e.Email))
	if !validEmail(e.Email) {
		return nil, invalid("your %s account has no usable email address", strings.ToUpper(e.Provider))
	}
	var row userRow
	err := s.db.NewSelect().Model(&row).Where("provider = ? AND subject = ?", e.Provider, e.Subject).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.db.NewSelect().Model(&row).Where("lower(email) = ?", e.Email).Scan(ctx)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			u, err := s.CreateUser(ctx, NewUser{Email: e.Email, Name: e.Name, Provider: e.Provider, Subject: e.Subject, Admin: e.Admin != nil && *e.Admin})
			return u, err
		case err != nil:
			return nil, err
		case !e.LinkByEmail:
			return nil, invalid("an account for %s already exists; sign in the way you did before, or ask an administrator", e.Email)
		}
	} else if err != nil {
		return nil, err
	}
	if row.Disabled {
		return nil, invalid("this account is disabled")
	}
	q := s.db.NewUpdate().Model((*userRow)(nil)).Set("provider = ?", e.Provider).Set("subject = ?", e.Subject).Where("id = ?", row.ID)
	if n := tidy(e.Name); n != "" {
		q = q.Set("name = ?", n)
	}
	if e.Email != row.Email {
		taken, err := s.db.NewSelect().Model((*userRow)(nil)).Where("lower(email) = ? AND id != ?", e.Email, row.ID).Count(ctx)
		if err != nil {
			return nil, err
		}
		if taken == 0 {
			q = q.Set("email = ?", e.Email)
		}
	}
	if e.Admin != nil {
		q = q.Set("admin = ?", *e.Admin)
	}
	if _, err := q.Exec(ctx); err != nil {
		return nil, err
	}
	return s.UserByID(ctx, row.ID)
}

// SetDisabled turns an account off (signing it out everywhere) or on.
func (s *Store) SetDisabled(ctx context.Context, userID string, disabled bool) error {
	res, err := s.db.NewUpdate().Model((*userRow)(nil)).Set("disabled = ?", disabled).Where("id = ?", userID).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if disabled {
		return s.SignOutEverywhere(ctx, userID)
	}
	return nil
}

// otherActiveAdmins counts enabled admins besides userID.
func (s *Store) otherActiveAdmins(ctx context.Context, userID string) (int, error) {
	n, err := s.db.NewSelect().Model((*userRow)(nil)).Where("admin = ? AND disabled = ? AND id != ?", true, false, userID).Count(ctx)
	return int(n), err
}

// errLastAdmin protects the server from losing its only administrator.
var errLastAdmin = errors.New("this is the only active administrator; make someone else an administrator first")
