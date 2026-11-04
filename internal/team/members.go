package team

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/codeen15/pyro/internal/server"
)

// Member is a workspace member.
type Member struct {
	UserID string    `json:"userId"`
	Email  string    `json:"email"`
	Name   string    `json:"name"`
	Role   string    `json:"role"`
	Added  time.Time `json:"added"`
}

// Members lists a workspace's members, owners first.
func (s *Store) Members(ctx context.Context, wsID string) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.email, u.name, m.role, m.added FROM members m JOIN users u ON u.id = m.user
		WHERE m.ws = ? ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'editor' THEN 1 ELSE 2 END, u.name COLLATE NOCASE`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		var added int64
		if err := rows.Scan(&m.UserID, &m.Email, &m.Name, &m.Role, &added); err != nil {
			return nil, err
		}
		m.Added = time.Unix(added, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) workspaceKind(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, wsID string) (string, error) {
	var kind string
	err := q.QueryRowContext(ctx, `SELECT kind FROM workspaces WHERE id = ?`, wsID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return kind, err
}

// WorkspaceName returns a workspace's name and kind.
func (s *Store) WorkspaceName(ctx context.Context, wsID string) (string, string, error) {
	var name, kind string
	err := s.db.QueryRowContext(ctx, `SELECT name, kind FROM workspaces WHERE id = ?`, wsID).Scan(&name, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return name, kind, err
}

// ownersAfter counts owners left if userID's role became newRole.
func ownersAfter(ctx context.Context, tx *sql.Tx, wsID, userID, newRole string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM members WHERE ws = ? AND role = 'owner' AND user != ?`, wsID, userID).Scan(&n)
	if newRole == "owner" {
		n++
	}
	return n, err
}

// SetRole changes a member's role. A workspace always keeps one owner.
func (s *Store) SetRole(ctx context.Context, wsID, userID string, role server.Role) error {
	if role < server.RoleViewer || role > server.RoleOwner {
		return invalid("role must be owner, editor or viewer")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n, err := ownersAfter(ctx, tx, wsID, userID, role.String())
	if err != nil {
		return err
	}
	if n == 0 {
		return invalid("a workspace needs at least one owner; make someone else an owner first")
	}
	res, err := tx.ExecContext(ctx, `UPDATE members SET role = ? WHERE ws = ? AND user = ?`, role.String(), wsID, userID)
	if err != nil {
		return err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// RemoveMember takes someone out of a workspace (or lets them leave).
func (s *Store) RemoveMember(ctx context.Context, wsID, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	kind, err := s.workspaceKind(ctx, tx, wsID)
	if err != nil {
		return err
	}
	if kind == "personal" {
		return invalid("nobody can leave a personal workspace")
	}
	n, err := ownersAfter(ctx, tx, wsID, userID, "")
	if err != nil {
		return err
	}
	if n == 0 {
		return invalid("a workspace needs at least one owner; make someone else an owner first")
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM members WHERE ws = ? AND user = ?`, wsID, userID)
	if err != nil {
		return err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// Invite is an invitation to a workspace (the token itself is never stored).
type Invite struct {
	ID        string    `json:"id"`
	Email     string    `json:"email,omitempty"`
	Role      string    `json:"role"`
	CreatedBy string    `json:"createdBy"`
	Created   time.Time `json:"created"`
	Expires   time.Time `json:"expires"`
	MaxUses   int       `json:"maxUses"`
	Uses      int       `json:"uses"`
}

// InviteOptions shapes a new invite.
type InviteOptions struct {
	Email   string // only this address may accept (one use)
	Role    server.Role
	TTL     time.Duration
	MaxUses int // 0 = unlimited (link invites)
}

const maxInviteTTL = 30 * 24 * time.Hour

// CreateInvite makes an invite and returns its token (shown once).
func (s *Store) CreateInvite(ctx context.Context, wsID, by string, o InviteOptions) (string, *Invite, error) {
	kind, err := s.workspaceKind(ctx, s.db, wsID)
	if err != nil {
		return "", nil, err
	}
	if kind == "personal" {
		return "", nil, invalid("personal workspaces can't be shared; create a team workspace")
	}
	if o.Role < server.RoleViewer || o.Role > server.RoleOwner {
		return "", nil, invalid("role must be owner, editor or viewer")
	}
	o.Email = strings.TrimSpace(o.Email)
	if o.Email != "" {
		if !validEmail(o.Email) {
			return "", nil, invalid("enter a valid email address")
		}
		o.MaxUses = 1
	}
	if o.TTL <= 0 {
		o.TTL = 7 * 24 * time.Hour
	}
	if o.TTL > maxInviteTTL {
		o.TTL = maxInviteTTL
	}
	if o.MaxUses < 0 {
		o.MaxUses = 0
	}
	tok := randomToken()
	now := time.Now().Truncate(time.Second)
	inv := &Invite{ID: newID(), Email: o.Email, Role: o.Role.String(), CreatedBy: by, Created: now, Expires: now.Add(o.TTL), MaxUses: o.MaxUses}
	_, err = s.db.ExecContext(ctx, `INSERT INTO invites (id, hash, ws, email, role, created_by, created, expires, max_uses) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inv.ID, hashToken(tok), wsID, inv.Email, inv.Role, by, now.Unix(), inv.Expires.Unix(), inv.MaxUses)
	if err != nil {
		return "", nil, err
	}
	return tok, inv, nil
}

// Invites lists a workspace's open invites.
func (s *Store) Invites(ctx context.Context, wsID string) ([]Invite, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.email, i.role, COALESCE(u.name, ''), i.created, i.expires, i.max_uses, i.uses
		FROM invites i LEFT JOIN users u ON u.id = i.created_by
		WHERE i.ws = ? AND i.revoked = 0 AND i.expires > ? AND (i.max_uses = 0 OR i.uses < i.max_uses)
		ORDER BY i.created DESC`, wsID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		var inv Invite
		var created, expires int64
		if err := rows.Scan(&inv.ID, &inv.Email, &inv.Role, &inv.CreatedBy, &created, &expires, &inv.MaxUses, &inv.Uses); err != nil {
			return nil, err
		}
		inv.Created, inv.Expires = time.Unix(created, 0), time.Unix(expires, 0)
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RevokeInvite cancels an invite.
func (s *Store) RevokeInvite(ctx context.Context, wsID, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE invites SET revoked = 1 WHERE ws = ? AND id = ?`, wsID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// InvitePreview is what an invite link shows before it's accepted.
type InvitePreview struct {
	Workspace     string    `json:"workspace"`
	WorkspaceID   string    `json:"workspaceId"`
	Role          string    `json:"role"`
	Email         string    `json:"email,omitempty"`
	InvitedBy     string    `json:"invitedBy"`
	Expires       time.Time `json:"expires"`
	inviteID      string
	maxUses, uses int
}

var errInviteGone = errors.New("this invite has expired or been used; ask for a new one")

// InviteByToken looks up a usable invite.
func (s *Store) InviteByToken(ctx context.Context, tok string) (*InvitePreview, error) {
	return inviteByToken(ctx, s.db, tok)
}

func inviteByToken(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, tok string) (*InvitePreview, error) {
	var p InvitePreview
	var expires int64
	var revoked bool
	err := q.QueryRowContext(ctx, `
		SELECT i.id, w.id, w.name, i.role, i.email, COALESCE(u.name, ''), i.expires, i.max_uses, i.uses, i.revoked
		FROM invites i JOIN workspaces w ON w.id = i.ws LEFT JOIN users u ON u.id = i.created_by
		WHERE i.hash = ?`, hashToken(tok)).Scan(&p.inviteID, &p.WorkspaceID, &p.Workspace, &p.Role, &p.Email, &p.InvitedBy, &expires, &p.maxUses, &p.uses, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInviteGone
	}
	if err != nil {
		return nil, err
	}
	p.Expires = time.Unix(expires, 0)
	if revoked || time.Now().After(p.Expires) || (p.maxUses > 0 && p.uses >= p.maxUses) {
		return nil, errInviteGone
	}
	return &p, nil
}

// AcceptInvite adds the user to the invite's workspace. Someone already in
// it keeps the higher of their current and the invited role.
func (s *Store) AcceptInvite(ctx context.Context, tok string, u *User) (*InvitePreview, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	p, err := inviteByToken(ctx, tx, tok)
	if err != nil {
		return nil, err
	}
	if p.Email != "" && !strings.EqualFold(p.Email, u.Email) {
		return nil, invalid("this invite is for %s; sign in with that address", p.Email)
	}
	var cur string
	err = tx.QueryRowContext(ctx, `SELECT role FROM members WHERE ws = ? AND user = ?`, p.WorkspaceID, u.ID).Scan(&cur)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `INSERT INTO members (ws, user, role, added) VALUES (?, ?, ?, ?)`, p.WorkspaceID, u.ID, p.Role, time.Now().Unix()); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case server.ParseRole(p.Role) > server.ParseRole(cur):
		if _, err := tx.ExecContext(ctx, `UPDATE members SET role = ? WHERE ws = ? AND user = ?`, p.Role, p.WorkspaceID, u.ID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invites SET uses = uses + 1 WHERE id = ?`, p.inviteID); err != nil {
		return nil, err
	}
	return p, tx.Commit()
}

// MoveProject puts a project in another workspace.
func (s *Store) MoveProject(ctx context.Context, projectID, wsID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET ws = ? WHERE id = ?`, wsID, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Activity is an audit log entry.
type Activity struct {
	At      time.Time `json:"at"`
	User    string    `json:"user"`
	Project string    `json:"project,omitempty"`
	Action  string    `json:"action"`
	Target  string    `json:"target,omitempty"`
}

// Record adds an audit entry (errors are the caller's to log).
func (s *Store) Record(ctx context.Context, userID, wsID, projectID, action, target string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit (at, user, ws, project, action, target) VALUES (?, ?, ?, ?, ?, ?)`,
		time.Now().Unix(), userID, wsID, projectID, action, target)
	return err
}

// ActivityLog lists a workspace's recent changes, newest first.
func (s *Store) ActivityLog(ctx context.Context, wsID string, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.at, COALESCE(u.name, 'someone'), COALESCE(p.name, ''), a.action, a.target
		FROM audit a LEFT JOIN users u ON u.id = a.user LEFT JOIN projects p ON p.id = a.project
		WHERE a.ws = ? ORDER BY a.id DESC LIMIT ?`, wsID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var at int64
		if err := rows.Scan(&at, &a.User, &a.Project, &a.Action, &a.Target); err != nil {
			return nil, err
		}
		a.At = time.Unix(at, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

// RecordProject adds an audit entry for a project, in its current workspace.
func (s *Store) RecordProject(ctx context.Context, userID, projectID, action, target string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit (at, user, ws, project, action, target)
		SELECT ?, ?, ws, id, ?, ? FROM projects WHERE id = ?`, time.Now().Unix(), userID, action, target, projectID)
	return err
}
