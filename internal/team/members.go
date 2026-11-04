package team

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/uptrace/bun"

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
	var rows []struct {
		UserID string `bun:"user_id"`
		Email  string `bun:"email"`
		Name   string `bun:"name"`
		Role   string `bun:"role"`
		Added  int64  `bun:"added"`
	}
	err := s.db.NewSelect().TableExpr("members AS m").Join(`JOIN users AS u ON u.id = m."user"`).
		ColumnExpr("u.id AS user_id, u.email, u.name, m.role, m.added").Where("m.ws = ?", wsID).
		OrderExpr("CASE m.role WHEN 'owner' THEN 0 WHEN 'editor' THEN 1 ELSE 2 END, lower(u.name)").Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, Member{UserID: r.UserID, Email: r.Email, Name: r.Name, Role: r.Role, Added: time.Unix(r.Added, 0)})
	}
	return out, nil
}

func workspaceKind(ctx context.Context, q bun.IDB, wsID string) (string, error) {
	var kind string
	err := q.NewSelect().Model((*workspaceRow)(nil)).Column("kind").Where("id = ?", wsID).Scan(ctx, &kind)
	return kind, notFound(err)
}

// WorkspaceName returns a workspace's name and kind.
func (s *Store) WorkspaceName(ctx context.Context, wsID string) (string, string, error) {
	var w workspaceRow
	if err := s.db.NewSelect().Model(&w).Where("id = ?", wsID).Scan(ctx); err != nil {
		return "", "", notFound(err)
	}
	return w.Name, w.Kind, nil
}

// ownersAfter counts owners left if userID's role became newRole.
func ownersAfter(ctx context.Context, tx bun.IDB, wsID, userID, newRole string) (int, error) {
	n, err := tx.NewSelect().Model((*memberRow)(nil)).Where(`ws = ? AND role = 'owner' AND "user" != ?`, wsID, userID).Count(ctx)
	if newRole == "owner" {
		n++
	}
	return int(n), err
}

var errNeedOwner = errors.New("a workspace needs at least one owner; make someone else an owner first")

// SetRole changes a member's role. A workspace always keeps one owner.
func (s *Store) SetRole(ctx context.Context, wsID, userID string, role server.Role) error {
	if role < server.RoleViewer || role > server.RoleOwner {
		return invalid("role must be owner, editor or viewer")
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		n, err := ownersAfter(ctx, tx, wsID, userID, role.String())
		if err != nil {
			return err
		}
		if n == 0 {
			return invalid("%s", errNeedOwner)
		}
		res, err := tx.NewUpdate().Model((*memberRow)(nil)).Set("role = ?", role.String()).Where(`ws = ? AND "user" = ?`, wsID, userID).Exec(ctx)
		if err != nil {
			return err
		}
		if c, _ := res.RowsAffected(); c == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// RemoveMember takes someone out of a workspace (or lets them leave).
func (s *Store) RemoveMember(ctx context.Context, wsID, userID string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		kind, err := workspaceKind(ctx, tx, wsID)
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
			return invalid("%s", errNeedOwner)
		}
		res, err := tx.NewDelete().Model((*memberRow)(nil)).Where(`ws = ? AND "user" = ?`, wsID, userID).Exec(ctx)
		if err != nil {
			return err
		}
		if c, _ := res.RowsAffected(); c == 0 {
			return ErrNotFound
		}
		return nil
	})
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
	kind, err := workspaceKind(ctx, s.db, wsID)
	if err != nil {
		return "", nil, err
	}
	if kind == "personal" {
		return "", nil, invalid("personal workspaces can't be shared; create a team workspace")
	}
	if o.Role < server.RoleViewer || o.Role > server.RoleOwner {
		return "", nil, invalid("role must be owner, editor or viewer")
	}
	o.Email = strings.ToLower(strings.TrimSpace(o.Email))
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
	row := &inviteRow{ID: inv.ID, Hash: hashToken(tok), WS: wsID, Email: inv.Email, Role: inv.Role, CreatedBy: by, Created: now.Unix(), Expires: inv.Expires.Unix(), MaxUses: inv.MaxUses}
	if _, err := s.db.NewInsert().Model(row).Exec(ctx); err != nil {
		return "", nil, err
	}
	return tok, inv, nil
}

// Invites lists a workspace's open invites.
func (s *Store) Invites(ctx context.Context, wsID string) ([]Invite, error) {
	var rows []struct {
		inviteRow `bun:",extend"`
		ByName    sql.NullString `bun:"by_name"`
	}
	err := s.db.NewSelect().TableExpr("invites AS i").Join("LEFT JOIN users AS u ON u.id = i.created_by").
		ColumnExpr("i.*, u.name AS by_name").
		Where("i.ws = ? AND i.revoked = ? AND i.expires > ?", wsID, false, time.Now().Unix()).
		Where("(i.max_uses = 0 OR i.uses < i.max_uses)").OrderExpr("i.created DESC").Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]Invite, 0, len(rows))
	for _, r := range rows {
		out = append(out, Invite{ID: r.ID, Email: r.Email, Role: r.Role, CreatedBy: r.ByName.String, Created: time.Unix(r.Created, 0),
			Expires: time.Unix(r.Expires, 0), MaxUses: r.MaxUses, Uses: r.Uses})
	}
	return out, nil
}

// RevokeInvite cancels an invite.
func (s *Store) RevokeInvite(ctx context.Context, wsID, id string) error {
	res, err := s.db.NewUpdate().Model((*inviteRow)(nil)).Set("revoked = ?", true).Where("ws = ? AND id = ?", wsID, id).Exec(ctx)
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

func inviteByToken(ctx context.Context, q bun.IDB, tok string) (*InvitePreview, error) {
	var row struct {
		inviteRow `bun:",extend"`
		WSName    string         `bun:"ws_name"`
		ByName    sql.NullString `bun:"by_name"`
	}
	err := q.NewSelect().TableExpr("invites AS i").Join("JOIN workspaces AS w ON w.id = i.ws").
		Join("LEFT JOIN users AS u ON u.id = i.created_by").
		ColumnExpr("i.*, w.name AS ws_name, u.name AS by_name").Where("i.hash = ?", hashToken(tok)).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInviteGone
	}
	if err != nil {
		return nil, err
	}
	p := &InvitePreview{Workspace: row.WSName, WorkspaceID: row.WS, Role: row.Role, Email: row.Email, InvitedBy: row.ByName.String,
		Expires: time.Unix(row.Expires, 0), inviteID: row.ID, maxUses: row.MaxUses, uses: row.Uses}
	if row.Revoked || time.Now().After(p.Expires) || (p.maxUses > 0 && p.uses >= p.maxUses) {
		return nil, errInviteGone
	}
	return p, nil
}

// AcceptInvite adds the user to the invite's workspace. Someone already in
// it keeps the higher of their current and the invited role.
func (s *Store) AcceptInvite(ctx context.Context, tok string, u *User) (*InvitePreview, error) {
	var p *InvitePreview
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		if p, err = inviteByToken(ctx, tx, tok); err != nil {
			return err
		}
		if p.Email != "" && !strings.EqualFold(p.Email, u.Email) {
			return invalid("this invite is for %s; sign in with that address", p.Email)
		}
		var cur string
		err = tx.NewSelect().Model((*memberRow)(nil)).Column("role").Where(`ws = ? AND "user" = ?`, p.WorkspaceID, u.ID).Scan(ctx, &cur)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.NewInsert().Model(&memberRow{WS: p.WorkspaceID, UserID: u.ID, Role: p.Role, Added: time.Now().Unix()}).Exec(ctx); err != nil {
				return err
			}
		case err != nil:
			return err
		case server.ParseRole(p.Role) > server.ParseRole(cur):
			if _, err := tx.NewUpdate().Model((*memberRow)(nil)).Set("role = ?", p.Role).Where(`ws = ? AND "user" = ?`, p.WorkspaceID, u.ID).Exec(ctx); err != nil {
				return err
			}
		}
		_, err = tx.NewUpdate().Model((*inviteRow)(nil)).Set("uses = uses + 1").Where("id = ?", p.inviteID).Exec(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// MoveProject puts a project in another workspace.
func (s *Store) MoveProject(ctx context.Context, projectID, wsID string) error {
	res, err := s.db.NewUpdate().Model((*projectRow)(nil)).Set("ws = ?", wsID).Where("id = ?", projectID).Exec(ctx)
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
	_, err := s.db.NewInsert().Model(&auditRow{At: time.Now().Unix(), UserID: userID, WS: wsID, Project: projectID, Action: tidy(action), Target: tidy(target)}).Exec(ctx)
	return err
}

// RecordProject adds an audit entry for a project, in its current workspace.
func (s *Store) RecordProject(ctx context.Context, userID, projectID, action, target string) error {
	var ws string
	if err := s.db.NewSelect().Model((*projectRow)(nil)).Column("ws").Where("id = ?", projectID).Scan(ctx, &ws); err != nil {
		return notFound(err)
	}
	return s.Record(ctx, userID, ws, projectID, action, target)
}

// ActivityLog lists a workspace's recent changes, newest first.
func (s *Store) ActivityLog(ctx context.Context, wsID string, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows []struct {
		At      int64          `bun:"at"`
		User    sql.NullString `bun:"user_name"`
		Project sql.NullString `bun:"project_name"`
		Action  string         `bun:"action"`
		Target  string         `bun:"target"`
	}
	err := s.db.NewSelect().TableExpr("audit AS a").
		Join(`LEFT JOIN users AS u ON u.id = a."user"`).Join("LEFT JOIN projects AS p ON p.id = a.project").
		ColumnExpr("a.at, u.name AS user_name, p.name AS project_name, a.action, a.target").
		Where("a.ws = ?", wsID).OrderExpr("a.id DESC").Limit(int64(limit)).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]Activity, 0, len(rows))
	for _, r := range rows {
		user := r.User.String
		if user == "" {
			user = "someone"
		}
		out = append(out, Activity{At: time.Unix(r.At, 0), User: user, Project: r.Project.String, Action: r.Action, Target: r.Target})
	}
	return out, nil
}
