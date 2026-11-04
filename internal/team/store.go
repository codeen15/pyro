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

	_ "modernc.org/sqlite"

	"github.com/codeen15/pyro/internal/server"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	// ErrInvalid wraps validation failures (bad input, rule violations).
	ErrInvalid = errors.New("invalid")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id       TEXT PRIMARY KEY,
	email    TEXT NOT NULL UNIQUE COLLATE NOCASE,
	name     TEXT NOT NULL,
	pw_hash  TEXT NOT NULL DEFAULT '',
	provider TEXT NOT NULL DEFAULT 'local',
	subject  TEXT NOT NULL DEFAULT '',
	admin    INTEGER NOT NULL DEFAULT 0,
	disabled INTEGER NOT NULL DEFAULT 0,
	created  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	hash    TEXT PRIMARY KEY,
	user    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires INTEGER NOT NULL,
	created INTEGER NOT NULL,
	ip      TEXT NOT NULL DEFAULT '',
	ua      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user);
CREATE TABLE IF NOT EXISTS workspaces (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	kind       TEXT NOT NULL,
	created_by TEXT NOT NULL,
	created    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS members (
	ws    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	user  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role  TEXT NOT NULL,
	added INTEGER NOT NULL,
	PRIMARY KEY (ws, user)
);
CREATE INDEX IF NOT EXISTS members_user ON members(user);
CREATE TABLE IF NOT EXISTS projects (
	id         TEXT PRIMARY KEY,
	ws         TEXT NOT NULL REFERENCES workspaces(id),
	name       TEXT NOT NULL,
	created_by TEXT NOT NULL,
	created    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS projects_ws ON projects(ws);`

// Store keeps accounts, workspaces and projects in SQLite.
type Store struct {
	db *sql.DB
}

func Open(file string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+file+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init team db: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func newID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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

	pwHash string
}

const userCols = `id, email, name, pw_hash, provider, admin, disabled, created`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var created int64
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.pwHash, &u.Provider, &u.Admin, &u.Disabled, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Created = time.Unix(created, 0)
	return &u, nil
}

// CountUsers reports how many accounts exist (the first becomes admin).
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
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
	email := strings.TrimSpace(nu.Email)
	if !validEmail(email) {
		return nil, invalid("enter a valid email address")
	}
	name := strings.TrimSpace(nu.Name)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return nil, err
	}
	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email = ?`, email).Scan(&taken); err != nil {
		return nil, err
	}
	if taken > 0 {
		return nil, fmt.Errorf("%w: an account with this email already exists", ErrExists)
	}
	now := time.Now()
	u := &User{ID: newID(), Email: email, Name: name, Provider: nu.Provider, Admin: nu.Admin || n == 0, Created: now.Truncate(time.Second)}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (`+userCols+`, subject) VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		u.ID, u.Email, u.Name, hash, u.Provider, u.Admin, now.Unix(), nu.Subject); err != nil {
		return nil, err
	}
	wsID := newID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id, name, kind, created_by, created) VALUES (?, 'Personal', 'personal', ?, ?)`, wsID, u.ID, now.Unix()); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO members (ws, user, role, added) VALUES (?, ?, 'owner', ?)`, wsID, u.ID, now.Unix()); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO projects (id, ws, name, created_by, created) VALUES (?, ?, 'My project', ?, ?)`, newID(), wsID, u.ID, now.Unix()); err != nil {
		return nil, err
	}
	return u, tx.Commit()
}

func validEmail(e string) bool {
	at := strings.IndexByte(e, '@')
	return at > 0 && at < len(e)-1 && !strings.ContainsAny(e, " \t\r\n<>") && len(e) <= 254
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE email = ?`, strings.TrimSpace(email)))
}

func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// Users lists every account (admin page, CLI).
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
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
// except keepSession (a session hash; "" signs out every session).
func (s *Store) SetPassword(ctx context.Context, userID, password, keepSession string) error {
	if err := checkPassword(password); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET pw_hash = ?, provider = 'local' WHERE id = ?`, hash, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user = ? AND hash != ?`, userID, keepSession)
	return err
}

// SetName renames an account.
func (s *Store) SetName(ctx context.Context, userID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return invalid("name is required")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET name = ? WHERE id = ?`, name, userID)
	return err
}

// SetAdmin grants or removes server administration.
func (s *Store) SetAdmin(ctx context.Context, userID string, admin bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET admin = ? WHERE id = ?`, admin, userID)
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.id, w.name, w.kind, m.role FROM workspaces w JOIN members m ON m.ws = w.id
		WHERE m.user = ? ORDER BY w.kind = 'team', w.name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	out := []Workspace{}
	idx := map[string]int{}
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.Name, &w.Kind, &w.Role); err != nil {
			rows.Close()
			return nil, err
		}
		w.Projects = []Project{}
		idx[w.ID] = len(out)
		out = append(out, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	prows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.name, p.ws, p.created FROM projects p JOIN members m ON m.ws = p.ws
		WHERE m.user = ? ORDER BY p.name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var p Project
		var created int64
		if err := prows.Scan(&p.ID, &p.Name, &p.Workspace, &created); err != nil {
			return nil, err
		}
		p.Created = time.Unix(created, 0)
		if i, ok := idx[p.Workspace]; ok {
			out[i].Projects = append(out[i].Projects, p)
		}
	}
	return out, prows.Err()
}

// CreateWorkspace adds a team workspace owned by userID.
func (s *Store) CreateWorkspace(ctx context.Context, userID, name string) (*Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, invalid("name is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	w := &Workspace{ID: newID(), Name: name, Kind: "team", Role: "owner", Projects: []Project{}}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id, name, kind, created_by, created) VALUES (?, ?, 'team', ?, ?)`, w.ID, name, userID, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO members (ws, user, role, added) VALUES (?, ?, 'owner', ?)`, w.ID, userID, now); err != nil {
		return nil, err
	}
	return w, tx.Commit()
}

// RenameWorkspace renames a workspace.
func (s *Store) RenameWorkspace(ctx context.Context, wsID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return invalid("name is required")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE workspaces SET name = ? WHERE id = ?`, name, wsID)
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
	var kind string
	var projects int
	if err := s.db.QueryRowContext(ctx, `SELECT kind, (SELECT COUNT(*) FROM projects WHERE ws = ?) FROM workspaces WHERE id = ?`, wsID, wsID).Scan(&kind, &projects); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if kind == "personal" {
		return invalid("a personal workspace can't be deleted")
	}
	if projects > 0 {
		return invalid("delete or move this workspace's projects first")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = ?`, wsID)
	return err
}

// Role returns the user's role in a workspace (RoleNone if not a member).
func (s *Store) Role(ctx context.Context, wsID, userID string) (server.Role, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM members WHERE ws = ? AND user = ?`, wsID, userID).Scan(&role)
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
	var p ProjectInfo
	var created int64
	var role sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT p.id, p.name, p.ws, p.created, w.name, w.kind, m.role
		FROM projects p JOIN workspaces w ON w.id = p.ws
		LEFT JOIN members m ON m.ws = p.ws AND m.user = ?
		WHERE p.id = ?`, userID, projectID).Scan(&p.ID, &p.Name, &p.Workspace, &created, &p.WorkspaceName, &p.WorkspaceKind, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, server.RoleNone, ErrNotFound
	}
	if err != nil {
		return nil, server.RoleNone, err
	}
	p.Created = time.Unix(created, 0)
	p.Role = role.String
	return &p, server.ParseRole(role.String), nil
}

// CreateProject adds a project to a workspace.
func (s *Store) CreateProject(ctx context.Context, wsID, userID, name string) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, invalid("name is required")
	}
	p := &Project{ID: newID(), Name: name, Workspace: wsID, Created: time.Now().Truncate(time.Second)}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO projects (id, ws, name, created_by, created) VALUES (?, ?, ?, ?, ?)`, p.ID, wsID, name, userID, p.Created.Unix()); err != nil {
		return nil, err
	}
	return p, nil
}

// RenameProject renames a project.
func (s *Store) RenameProject(ctx context.Context, projectID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return invalid("name is required")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET name = ? WHERE id = ?`, name, projectID)
	return err
}

// DeleteProject removes a project row (the caller removes its folder).
func (s *Store) DeleteProject(ctx context.Context, projectID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, projectID)
	return err
}
