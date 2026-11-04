package team

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/codeen15/pyro/internal/db"
)

// Tables. Times are Unix seconds. "user" is a reserved word in PostgreSQL,
// so hand-written SQL quotes it ("user"); Bun quotes model columns itself.

type userRow struct {
	bun.BaseModel `bun:"table:users"`
	ID            string `bun:"id,pk"`
	Email         string `bun:"email,notnull,unique"` // stored lower-case
	Name          string `bun:"name,notnull"`
	PwHash        string `bun:"pw_hash,notnull,default:''"`
	Provider      string `bun:"provider,notnull,default:'local'"`
	Subject       string `bun:"subject,notnull,default:''"`
	Admin         bool   `bun:"admin,notnull,default:false"`
	Disabled      bool   `bun:"disabled,notnull,default:false"`
	Created       int64  `bun:"created,notnull"`
}

type sessionRow struct {
	bun.BaseModel `bun:"table:sessions"`
	Hash          string `bun:"hash,pk"`
	UserID        string `bun:"user,notnull"`
	Expires       int64  `bun:"expires,notnull"`
	Created       int64  `bun:"created,notnull"`
	IP            string `bun:"ip,notnull,default:''"`
	UA            string `bun:"ua,notnull,default:''"`
}

type workspaceRow struct {
	bun.BaseModel `bun:"table:workspaces"`
	ID            string `bun:"id,pk"`
	Name          string `bun:"name,notnull"`
	Kind          string `bun:"kind,notnull"`
	CreatedBy     string `bun:"created_by,notnull"`
	Created       int64  `bun:"created,notnull"`
}

type memberRow struct {
	bun.BaseModel `bun:"table:members"`
	WS            string `bun:"ws,pk"`
	UserID        string `bun:"user,pk"`
	Role          string `bun:"role,notnull"`
	Added         int64  `bun:"added,notnull"`
}

type projectRow struct {
	bun.BaseModel `bun:"table:projects"`
	ID            string `bun:"id,pk"`
	WS            string `bun:"ws,notnull"`
	Name          string `bun:"name,notnull"`
	CreatedBy     string `bun:"created_by,notnull"`
	Created       int64  `bun:"created,notnull"`
}

type inviteRow struct {
	bun.BaseModel `bun:"table:invites"`
	ID            string `bun:"id,pk"`
	Hash          string `bun:"hash,notnull,unique"`
	WS            string `bun:"ws,notnull"`
	Email         string `bun:"email,notnull,default:''"`
	Role          string `bun:"role,notnull"`
	CreatedBy     string `bun:"created_by,notnull"`
	Created       int64  `bun:"created,notnull"`
	Expires       int64  `bun:"expires,notnull"`
	MaxUses       int    `bun:"max_uses,notnull,default:0"`
	Uses          int    `bun:"uses,notnull,default:0"`
	Revoked       bool   `bun:"revoked,notnull,default:false"`
}

type auditRow struct {
	bun.BaseModel `bun:"table:audit"`
	ID            int64  `bun:"id,pk,autoincrement"`
	At            int64  `bun:"at,notnull"`
	UserID        string `bun:"user,notnull"`
	WS            string `bun:"ws,notnull"`
	Project       string `bun:"project,notnull,default:''"`
	Action        string `bun:"action,notnull"`
	Target        string `bun:"target,notnull,default:''"`
}

// migrations build the schema; add new steps at the end, never edit old ones.
var migrations = []db.Migration{
	{Name: "1-tables", Up: func(ctx context.Context, tx bun.Tx) error {
		create := func(model any, fks ...string) error {
			q := tx.NewCreateTable().Model(model).IfNotExists()
			for _, fk := range fks {
				q = q.ForeignKey(fk)
			}
			_, err := q.Exec(ctx)
			return err
		}
		steps := []func() error{
			func() error { return create((*userRow)(nil)) },
			func() error {
				return create((*sessionRow)(nil), `("user") REFERENCES "users" ("id") ON DELETE CASCADE`)
			},
			func() error { return create((*workspaceRow)(nil)) },
			func() error {
				return create((*memberRow)(nil),
					`("ws") REFERENCES "workspaces" ("id") ON DELETE CASCADE`,
					`("user") REFERENCES "users" ("id") ON DELETE CASCADE`)
			},
			func() error { return create((*projectRow)(nil), `("ws") REFERENCES "workspaces" ("id")`) },
			func() error {
				return create((*inviteRow)(nil), `("ws") REFERENCES "workspaces" ("id") ON DELETE CASCADE`)
			},
			func() error { return create((*auditRow)(nil)) },
			func() error { return db.CreateIndex(ctx, tx, (*sessionRow)(nil), "sessions_user", `"user"`) },
			func() error { return db.CreateIndex(ctx, tx, (*memberRow)(nil), "members_user", `"user"`) },
			func() error { return db.CreateIndex(ctx, tx, (*projectRow)(nil), "projects_ws", "ws") },
			func() error { return db.CreateIndex(ctx, tx, (*inviteRow)(nil), "invites_ws", "ws") },
			func() error { return db.CreateIndex(ctx, tx, (*auditRow)(nil), "audit_ws", "ws", "id DESC") },
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return err
			}
		}
		return nil
	}},
}
