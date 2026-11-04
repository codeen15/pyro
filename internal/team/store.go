// Package team is the team server: accounts, sign-in, workspaces and the
// projects inside them. Each project is an ordinary Pyro workspace folder,
// served by its own internal/server.Server.
package team

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/codeen15/pyro/internal/db"
	"github.com/codeen15/pyro/internal/server"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	// ErrInvalid wraps validation failures (bad input, rule violations).
	ErrInvalid = errors.New("invalid")
)

// tidy trims a name and drops NUL characters (PostgreSQL text can't hold them).
func tidy(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "\x00", "")) }

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Store keeps accounts, workspaces and projects in the database (SQLite or
// PostgreSQL, through Bun).
type Store struct {
	db  *bun.DB
	own bool
}

// Open opens (creating if needed) a SQLite database file.
func Open(file string) (*Store, error) {
	d, err := db.Open(file)
	if err != nil {
		return nil, err
	}
	st, err := NewStore(context.Background(), d)
	if err != nil {
		d.Close()
		return nil, err
	}
	st.own = true
	return st, nil
}

// NewStore uses an open database, creating or upgrading the tables.
func NewStore(ctx context.Context, d *bun.DB) (*Store, error) {
	if err := db.Migrate(ctx, d, "team", migrations); err != nil {
		return nil, fmt.Errorf("init team db: %w", err)
	}
	return &Store{db: d}, nil
}

// Close closes the database if the store opened it.
func (s *Store) Close() error {
	if s.own {
		return s.db.Close()
	}
	return nil
}

func newID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// User is an account.
type User struct {
	ID       string    `json:"id"`
	Email    string    `json:"email"`
	Name     string    `json:"name"`
	Provider string    `json:"provider"`
	Admin    bool      `json:"admin"`
	Disabled bool      `json:"disabled,omitempty"`
	Created  time.Time `json:"created"`

	pwHash      string
	tokensAfter int64
}

func (r *userRow) user() *User {
	return &User{ID: r.ID, Email: r.Email, Name: r.Name, Provider: r.Provider, Admin: r.Admin, Disabled: r.Disabled, Created: time.Unix(r.Created, 0),
		pwHash: r.PwHash, tokensAfter: r.TokensAfter}
}

// CountUsers reports how many accounts exist (the first becomes admin).
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	n, err := s.db.NewSelect().Model((*userRow)(nil)).Count(ctx)
	return int(n), err
}

// NewUser describes an account to create.
type NewUser struct {
	Email, Name, Password string
	Provider              string // local (default) | ldap | oidc
	Subject               string
	Admin                 bool
}

// CreateUser adds an account with its personal workspace and a first project.
// The very first account is always an admin.
func (s *Store) CreateUser(ctx context.Context, nu NewUser) (*User, error) {
	email := strings.ToLower(strings.TrimSpace(nu.Email))
	if !validEmail(email) {
		return nil, invalid("enter a valid email address")
	}
	name := tidy(nu.Name)
	if name == "" {
		name = strings.SplitN(email, "@", 2)[0]
	}
	if nu.Provider == "" {
		nu.Provider = "local"
	}
	hash := ""
	if nu.Provider == "local" {
		if err := checkPassword(nu.Password); err != nil {
			return nil, err
		}
		var err error
		if hash, err = hashPassword(nu.Password); err != nil {
			return nil, err
		}
	}
	now := time.Now()
	var u *User
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		n, err := tx.NewSelect().Model((*userRow)(nil)).Count(ctx)
		if err != nil {
			return err
		}
		taken, err := tx.NewSelect().Model((*userRow)(nil)).Where("lower(email) = ?", email).Count(ctx)
		if err != nil {
			return err
		}
		if taken > 0 {
			return fmt.Errorf("%w: an account with this email already exists", ErrExists)
		}
		row := &userRow{ID: newID(), Email: email, Name: name, PwHash: hash, Provider: nu.Provider, Subject: nu.Subject, Admin: nu.Admin || n == 0, Created: now.Unix()}
		if _, err := tx.NewInsert().Model(row).Exec(ctx); err != nil {
			return err
		}
		ws := &workspaceRow{ID: newID(), Name: "Personal", Kind: "personal", CreatedBy: row.ID, Created: now.Unix()}
		if _, err := tx.NewInsert().Model(ws).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&memberRow{WS: ws.ID, UserID: row.ID, Role: "owner", Added: now.Unix()}).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&projectRow{ID: newID(), WS: ws.ID, Name: "My project", CreatedBy: row.ID, Created: now.Unix()}).Exec(ctx); err != nil {
			return err
		}
		u = row.user()
		return nil
	})
	return u, err
}

func validEmail(e string) bool {
	at := strings.IndexByte(e, '@')
	return at > 0 && at < len(e)-1 && !strings.ContainsAny(e, " \t\r\n<>") && len(e) <= 254
}

func (s *Store) userWhere(ctx context.Context, where string, arg any) (*User, error) {
	var row userRow
	if err := s.db.NewSelect().Model(&row).Where(where, arg).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return row.user(), nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	return s.userWhere(ctx, "lower(email) = ?", strings.ToLower(strings.TrimSpace(email)))
}

func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	return s.userWhere(ctx, "id = ?", id)
}

// Users lists every account (admin page, CLI).
func (s *Store) Users(ctx context.Context) ([]User, error) {
	var rows []userRow
	if err := s.db.NewSelect().Model(&rows).Order("created").Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]User, 0, len(rows))
	for i := range rows {
		out = append(out, *rows[i].user())
	}
	return out, nil
}

// Authenticate checks an email and password.
func (s *Store) Authenticate(ctx context.Context, email, password string) (*User, error) {
	u, err := s.UserByEmail(ctx, email)
	if err != nil {
		verifyPassword(dummyHash, password) // same work whether or not the account exists
		return nil, errBadLogin
	}
	if u.Provider != "local" || !verifyPassword(u.pwHash, password) || u.Disabled {
		return nil, errBadLogin
	}
	return u, nil
}

var errBadLogin = errors.New("wrong email or password")

// SetPassword replaces a local account's password and signs it out everywhere
// except keepSession (a session hash; "" signs out every session): refresh
// tokens are deleted and earlier access tokens revoked.
func (s *Store) SetPassword(ctx context.Context, userID, password, keepSession string) error {
	if err := checkPassword(password); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	res, err := s.db.NewUpdate().Model((*userRow)(nil)).Set("pw_hash = ?", hash).Set("provider = 'local'").
		Set("tokens_after = ?", time.Now().UnixMilli()).Where("id = ?", userID).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.db.NewDelete().Model((*sessionRow)(nil)).Where(`"user" = ? AND hash != ?`, userID, keepSession).Exec(ctx)
	return err
}

// SignOutEverywhere deletes every refresh token and revokes access tokens
// issued until now.
func (s *Store) SignOutEverywhere(ctx context.Context, userID string) error {
	if _, err := s.db.NewUpdate().Model((*userRow)(nil)).Set("tokens_after = ?", time.Now().UnixMilli()).Where("id = ?", userID).Exec(ctx); err != nil {
		return err
	}
	_, err := s.db.NewDelete().Model((*sessionRow)(nil)).Where(`"user" = ?`, userID).Exec(ctx)
	return err
}

// SetName renames an account.
func (s *Store) SetName(ctx context.Context, userID, name string) error {
	name = tidy(name)
	if name == "" {
		return invalid("name is required")
	}
	_, err := s.db.NewUpdate().Model((*userRow)(nil)).Set("name = ?", name).Where("id = ?", userID).Exec(ctx)
	return err
}

// SetAdmin grants or removes server administration.
func (s *Store) SetAdmin(ctx context.Context, userID string, admin bool) error {
	res, err := s.db.NewUpdate().Model((*userRow)(nil)).Set("admin = ?", admin).Where("id = ?", userID).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Workspace is a workspace as one member sees it.
type Workspace struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind"` // personal | team
	Role     string    `json:"role"`
	Projects []Project `json:"projects"`
}

// Project is a project row.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Workspace string    `json:"workspace"`
	Created   time.Time `json:"created"`
}

// Workspaces lists the user's workspaces (personal first) with their projects.
func (s *Store) Workspaces(ctx context.Context, userID string) ([]Workspace, error) {
	var wsRows []struct {
		ID   string `bun:"id"`
		Name string `bun:"name"`
		Kind string `bun:"kind"`
		Role string `bun:"role"`
	}
	err := s.db.NewSelect().TableExpr("workspaces AS w").Join(`JOIN members AS m ON m.ws = w.id`).
		ColumnExpr("w.id, w.name, w.kind, m.role").Where(`m."user" = ?`, userID).
		OrderExpr("CASE WHEN w.kind = 'personal' THEN 0 ELSE 1 END, lower(w.name)").Scan(ctx, &wsRows)
	if err != nil {
		return nil, err
	}
	out := make([]Workspace, 0, len(wsRows))
	idx := map[string]int{}
	for _, w := range wsRows {
		idx[w.ID] = len(out)
		out = append(out, Workspace{ID: w.ID, Name: w.Name, Kind: w.Kind, Role: w.Role, Projects: []Project{}})
	}
	var pRows []projectRow
	err = s.db.NewSelect().Model(&pRows).ModelTableExpr("projects AS p").ColumnExpr("p.*").
		Join(`JOIN members AS m ON m.ws = p.ws`).Where(`m."user" = ?`, userID).OrderExpr("lower(p.name)").Scan(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range pRows {
		if i, ok := idx[p.WS]; ok {
			out[i].Projects = append(out[i].Projects, Project{ID: p.ID, Name: p.Name, Workspace: p.WS, Created: time.Unix(p.Created, 0)})
		}
	}
	return out, nil
}

// CreateWorkspace adds a team workspace owned by userID.
func (s *Store) CreateWorkspace(ctx context.Context, userID, name string) (*Workspace, error) {
	name = tidy(name)
	if name == "" {
		return nil, invalid("name is required")
	}
	now := time.Now().Unix()
	w := &Workspace{ID: newID(), Name: name, Kind: "team", Role: "owner", Projects: []Project{}}
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(&workspaceRow{ID: w.ID, Name: name, Kind: "team", CreatedBy: userID, Created: now}).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&memberRow{WS: w.ID, UserID: userID, Role: "owner", Added: now}).Exec(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return w, nil
}

// RenameWorkspace renames a workspace.
func (s *Store) RenameWorkspace(ctx context.Context, wsID, name string) error {
	name = tidy(name)
	if name == "" {
		return invalid("name is required")
	}
	res, err := s.db.NewUpdate().Model((*workspaceRow)(nil)).Set("name = ?", name).Where("id = ?", wsID).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteWorkspace removes an empty team workspace.
func (s *Store) DeleteWorkspace(ctx context.Context, wsID string) error {
	var w workspaceRow
	if err := s.db.NewSelect().Model(&w).Where("id = ?", wsID).Scan(ctx); err != nil {
		return notFound(err)
	}
	if w.Kind == "personal" {
		return invalid("a personal workspace can't be deleted")
	}
	projects, err := s.db.NewSelect().Model((*projectRow)(nil)).Where("ws = ?", wsID).Count(ctx)
	if err != nil {
		return err
	}
	if projects > 0 {
		return invalid("delete or move this workspace's projects first")
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*auditRow)(nil)).Where("ws = ?", wsID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewDelete().Model((*workspaceRow)(nil)).Where("id = ?", wsID).Exec(ctx)
		return err
	})
}

// Role returns the user's role in a workspace (RoleNone if not a member).
func (s *Store) Role(ctx context.Context, wsID, userID string) (server.Role, error) {
	var role string
	err := s.db.NewSelect().Model((*memberRow)(nil)).Column("role").Where(`ws = ? AND "user" = ?`, wsID, userID).Scan(ctx, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return server.RoleNone, nil
	}
	return server.ParseRole(role), err
}

// ProjectInfo is a project with its workspace, as one member sees it.
type ProjectInfo struct {
	Project
	WorkspaceName string `json:"workspaceName"`
	WorkspaceKind string `json:"workspaceKind"`
	Role          string `json:"role"`
}

// ProjectFor returns a project and the user's role in its workspace.
func (s *Store) ProjectFor(ctx context.Context, projectID, userID string) (*ProjectInfo, server.Role, error) {
	var row struct {
		ID      string         `bun:"id"`
		Name    string         `bun:"name"`
		WS      string         `bun:"ws"`
		Created int64          `bun:"created"`
		WSName  string         `bun:"ws_name"`
		WSKind  string         `bun:"ws_kind"`
		Role    sql.NullString `bun:"role"`
	}
	err := s.db.NewSelect().TableExpr("projects AS p").
		ColumnExpr("p.id, p.name, p.ws, p.created, w.name AS ws_name, w.kind AS ws_kind, m.role").
		Join("JOIN workspaces AS w ON w.id = p.ws").
		Join(`LEFT JOIN members AS m ON m.ws = p.ws AND m."user" = ?`, userID).
		Where("p.id = ?", projectID).Scan(ctx, &row)
	if err != nil {
		return nil, server.RoleNone, notFound(err)
	}
	p := &ProjectInfo{
		Project:       Project{ID: row.ID, Name: row.Name, Workspace: row.WS, Created: time.Unix(row.Created, 0)},
		WorkspaceName: row.WSName, WorkspaceKind: row.WSKind, Role: row.Role.String,
	}
	return p, server.ParseRole(row.Role.String), nil
}

// CreateProject adds a project to a workspace.
func (s *Store) CreateProject(ctx context.Context, wsID, userID, name string) (*Project, error) {
	name = tidy(name)
	if name == "" {
		return nil, invalid("name is required")
	}
	p := &Project{ID: newID(), Name: name, Workspace: wsID, Created: time.Now().Truncate(time.Second)}
	_, err := s.db.NewInsert().Model(&projectRow{ID: p.ID, WS: wsID, Name: name, CreatedBy: userID, Created: p.Created.Unix()}).Exec(ctx)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// RenameProject renames a project.
func (s *Store) RenameProject(ctx context.Context, projectID, name string) error {
	name = tidy(name)
	if name == "" {
		return invalid("name is required")
	}
	_, err := s.db.NewUpdate().Model((*projectRow)(nil)).Set("name = ?", name).Where("id = ?", projectID).Exec(ctx)
	return err
}

// DeleteProject removes a project row (the caller removes its folder).
func (s *Store) DeleteProject(ctx context.Context, projectID string) error {
	_, err := s.db.NewDelete().Model((*projectRow)(nil)).Where("id = ?", projectID).Exec(ctx)
	return err
}
