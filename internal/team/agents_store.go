package team

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// Agent modes: Pyro dials a direct agent; a connecting agent dials Pyro.
const (
	AgentDirect  = "direct"
	AgentConnect = "connect"
)

const maxAgentName = 64

// Agent is a registered load agent. Tokens are never part of it.
type Agent struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Mode        string    `json:"mode"`
	Address     string    `json:"address,omitempty"`
	InsecureTLS bool      `json:"insecureTLS,omitempty"`
	CreatedBy   string    `json:"createdBy,omitempty"`
	Created     time.Time `json:"created"`
	LastSeen    time.Time `json:"lastSeen,omitzero"`
}

func (r *agentRow) agent() Agent {
	a := Agent{ID: r.ID, Name: r.Name, Mode: r.Mode, Address: r.Address, InsecureTLS: r.InsecureTLS,
		CreatedBy: r.CreatedBy, Created: time.Unix(r.CreatedAt, 0)}
	if r.LastSeenAt > 0 {
		a.LastSeen = time.Unix(r.LastSeenAt, 0)
	}
	return a
}

// SetAgentKey derives the key that seals direct agents' tokens from the
// server's JWT secret. The team server calls it when it starts.
func (s *Store) SetAgentKey(jwtSecret []byte) error {
	box, err := newTokenBox(jwtSecret)
	if err != nil {
		return err
	}
	s.box = box
	return nil
}

func (s *Store) sealer() (*tokenBox, error) {
	if s.box == nil {
		return nil, errors.New("agent token key is not set")
	}
	return s.box, nil
}

func checkAgentName(name string) (string, error) {
	name = tidy(name)
	if name == "" || len([]rune(name)) > maxAgentName {
		return "", invalid("agent name must be 1 to %d characters", maxAgentName)
	}
	return name, nil
}

// checkAgentAddress accepts host:port, or an http(s):// URL with a host.
func checkAgentAddress(addr string) (string, error) {
	addr = tidy(addr)
	if addr == "" {
		return "", invalid("agent address is required")
	}
	if strings.Contains(addr, "://") {
		u, err := url.Parse(addr)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return "", invalid("agent address must be host:port or an http(s):// URL")
		}
		return addr, nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return "", invalid("agent address must be host:port or an http(s):// URL")
	}
	return addr, nil
}

// nameTaken reports whether another agent already uses name (case-insensitive).
func nameTaken(ctx context.Context, tx bun.IDB, name, exceptID string) error {
	q := tx.NewSelect().Model((*agentRow)(nil)).Where("lower(name) = ?", strings.ToLower(name))
	if exceptID != "" {
		q = q.Where("id <> ?", exceptID)
	}
	n, err := q.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: an agent with this name already exists", ErrExists)
	}
	return nil
}

// AddDirectAgent registers an agent Pyro connects to; its token is stored encrypted.
func (s *Store) AddDirectAgent(ctx context.Context, name, address, token string, insecure bool, by string) (Agent, error) {
	box, err := s.sealer()
	if err != nil {
		return Agent{}, err
	}
	if name, err = checkAgentName(name); err != nil {
		return Agent{}, err
	}
	if address, err = checkAgentAddress(address); err != nil {
		return Agent{}, err
	}
	if token == "" {
		return Agent{}, invalid("agent token is required")
	}
	enc, err := box.seal(token)
	if err != nil {
		return Agent{}, err
	}
	row := &agentRow{ID: newID(), Name: name, Mode: AgentDirect, Address: address, TokenEnc: enc,
		InsecureTLS: insecure, CreatedBy: by, CreatedAt: time.Now().Unix()}
	return s.insertAgent(ctx, row)
}

// AddConnectAgent registers an agent that connects to Pyro and returns the
// token it must present. The token is shown once; only its hash is stored.
func (s *Store) AddConnectAgent(ctx context.Context, name, by string) (Agent, string, error) {
	name, err := checkAgentName(name)
	if err != nil {
		return Agent{}, "", err
	}
	token, hash, err := newConnectToken()
	if err != nil {
		return Agent{}, "", err
	}
	row := &agentRow{ID: newID(), Name: name, Mode: AgentConnect, TokenHash: hash, CreatedBy: by, CreatedAt: time.Now().Unix()}
	a, err := s.insertAgent(ctx, row)
	if err != nil {
		return Agent{}, "", err
	}
	return a, token, nil
}

func (s *Store) insertAgent(ctx context.Context, row *agentRow) (Agent, error) {
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := nameTaken(ctx, tx, row.Name, ""); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(row).Exec(ctx)
		return err
	})
	if err != nil {
		return Agent{}, err
	}
	return row.agent(), nil
}

// Agents lists every registered agent by name.
func (s *Store) Agents(ctx context.Context) ([]Agent, error) {
	var rows []agentRow
	if err := s.db.NewSelect().Model(&rows).OrderExpr("lower(name), name").Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]Agent, len(rows))
	for i := range rows {
		out[i] = rows[i].agent()
	}
	return out, nil
}

func (s *Store) loadAgent(ctx context.Context, q bun.IDB, id string) (*agentRow, error) {
	var row agentRow
	if err := q.NewSelect().Model(&row).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// AgentToken returns the decrypted token of a direct agent. It returns
// errTokenUnreadable when the server's secret changed since the token was saved.
func (s *Store) AgentToken(ctx context.Context, id string) (string, error) {
	box, err := s.sealer()
	if err != nil {
		return "", err
	}
	row, err := s.loadAgent(ctx, s.db, id)
	if err != nil {
		return "", err
	}
	if row.Mode != AgentDirect {
		return "", invalid("only direct agents have a stored token")
	}
	return box.open(row.TokenEnc)
}

// AgentByToken finds the connecting agent that owns token.
func (s *Store) AgentByToken(ctx context.Context, token string) (Agent, bool, error) {
	if token == "" {
		return Agent{}, false, nil
	}
	var row agentRow
	err := s.db.NewSelect().Model(&row).Where("token_hash = ?", hashAgentToken(token)).Where("mode = ?", AgentConnect).Limit(1).Scan(ctx)
	if errors.Is(notFound(err), ErrNotFound) {
		return Agent{}, false, nil
	}
	if err != nil {
		return Agent{}, false, err
	}
	return row.agent(), true, nil
}

// UpdateAgent changes the fields that are not nil. Address and token apply
// to direct agents only.
func (s *Store) UpdateAgent(ctx context.Context, id string, name, address, token *string, insecure *bool) (Agent, error) {
	var box *tokenBox
	if token != nil {
		var err error
		if box, err = s.sealer(); err != nil {
			return Agent{}, err
		}
	}
	var out Agent
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		row, err := s.loadAgent(ctx, tx, id)
		if err != nil {
			return err
		}
		if (address != nil || token != nil) && row.Mode != AgentDirect {
			return invalid("only direct agents have an address and a stored token")
		}
		if name != nil {
			n, err := checkAgentName(*name)
			if err != nil {
				return err
			}
			if err := nameTaken(ctx, tx, n, id); err != nil {
				return err
			}
			row.Name = n
		}
		if address != nil {
			if row.Address, err = checkAgentAddress(*address); err != nil {
				return err
			}
		}
		if token != nil {
			if *token == "" {
				return invalid("agent token is required")
			}
			if row.TokenEnc, err = box.seal(*token); err != nil {
				return err
			}
		}
		if insecure != nil {
			row.InsecureTLS = *insecure
		}
		if _, err := tx.NewUpdate().Model(row).WherePK().Exec(ctx); err != nil {
			return err
		}
		out = row.agent()
		return nil
	})
	return out, err
}

// RotateAgentToken replaces a connecting agent's token and returns the new one.
func (s *Store) RotateAgentToken(ctx context.Context, id string) (string, error) {
	row, err := s.loadAgent(ctx, s.db, id)
	if err != nil {
		return "", err
	}
	if row.Mode != AgentConnect {
		return "", invalid("only connecting agents have a generated token")
	}
	token, hash, err := newConnectToken()
	if err != nil {
		return "", err
	}
	res, err := s.db.NewUpdate().Model((*agentRow)(nil)).Set("token_hash = ?", hash).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return token, nil
}

// DeleteAgent removes an agent.
func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	res, err := s.db.NewDelete().Model((*agentRow)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchAgent records when an agent was last reachable.
func (s *Store) TouchAgent(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.NewUpdate().Model((*agentRow)(nil)).Set("last_seen_at = ?", at.Unix()).Where("id = ?", id).Exec(ctx)
	return err
}
