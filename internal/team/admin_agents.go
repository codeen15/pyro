package team

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/codeen15/pyro/internal/server"
	"github.com/codeen15/pyro/internal/version"
)

// The admin API for load agents. Agents given with --agent (ids "flag:...")
// are listed and can be tested, but not changed.

// adminAgent is an agent as the admin page sees it.
type adminAgent struct {
	server.AgentStatus
	InsecureTLS bool   `json:"insecureTLS,omitempty"`
	CreatedBy   string `json:"createdBy,omitempty"` // the administrator's name
	Created     int64  `json:"created,omitempty"`   // Unix seconds
}

// agentView joins a registry status with the stored agent's details (a nil
// agent is a flag agent).
func agentView(st server.AgentStatus, a *Agent, names map[string]string) adminAgent {
	v := adminAgent{AgentStatus: st}
	if a != nil {
		v.InsecureTLS, v.Created, v.CreatedBy = a.InsecureTLS, a.Created.Unix(), names[a.CreatedBy]
	}
	return v
}

// userNames maps account ids to display names.
func (s *Server) userNames(ctx context.Context) map[string]string {
	names := map[string]string{}
	users, err := s.st.Users(ctx)
	if err != nil {
		s.log.Warn("listing users failed", "err", err)
		return names
	}
	for _, u := range users {
		names[u.ID] = u.Name
		if u.Name == "" {
			names[u.ID] = u.Email
		}
	}
	return names
}

func (s *Server) adminAgents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stored, err := s.st.Agents(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	byID := make(map[string]*Agent, len(stored))
	for i := range stored {
		byID[stored[i].ID] = &stored[i]
	}
	names := s.userNames(ctx)
	out := []adminAgent{}
	for _, st := range s.agents.List(ctx) {
		out = append(out, agentView(st, byID[st.ID], names))
	}
	writeJSON(w, http.StatusOK, out)
}

// safeBase matches the server addresses a command may carry: http(s), a
// plain host name, IPv4 or bracketed IPv6 address, an optional port and a
// simple path. Anything else (the Host header is client-controlled) is data
// for the page to show, never text for a shell.
var safeBase = regexp.MustCompile(`^https?://(?:\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]+)(?::[0-9]{1,5})?(?:/[A-Za-z0-9._~/-]*)?$`)

// shellQuote wraps s in single quotes for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// agentCommand is how to start a connecting agent: the Docker command, and
// the base address it dials (the public URL, else the one the admin used).
// When that address isn't a plain one, the command carries a placeholder
// and warning says to set PYRO_PUBLIC_URL.
func (s *Server) agentCommand(r *http.Request, token string) (base, command, warning string) {
	base = strings.TrimRight(s.baseURL(r), "/")
	arg := base
	if !safeBase.MatchString(base) {
		arg = "<server-url>"
		warning = "The server's address couldn't be used in the command. Set PYRO_PUBLIC_URL to the address agents should use, or replace <server-url> yourself."
	}
	return base, fmt.Sprintf("docker run -d --name pyro-agent --restart unless-stopped ghcr.io/codeen15/pyro:%s agent --connect %s --token %s",
		agentImageTag(version.Version), shellQuote(arg), shellQuote(token)), warning
}

// setCommand adds the generated command (and its warning) to a response.
func setCommand(out map[string]any, base, command, warning string) {
	out["base"], out["command"] = base, command
	if warning != "" {
		out["warning"] = warning
	}
}

var releaseVersion = regexp.MustCompile(`^v?(\d+\.\d+\.\d+(?:-rc\.\d+)?)$`)

// agentImageTag is the Docker tag matching this server: a release gives its
// version without the leading v; builds past a tag, dirty trees and dev
// builds have no image of their own, so they get latest.
func agentImageTag(v string) string {
	if m := releaseVersion.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	return "latest"
}

func (s *Server) adminAddAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Mode        string `json:"mode"`
		Address     string `json:"address"`
		Token       string `json:"token"`
		InsecureTLS bool   `json:"insecureTLS"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx, by := r.Context(), current(r).user.ID
	out := map[string]any{}
	var a Agent
	var err error
	switch body.Mode {
	case AgentDirect:
		a, err = s.st.AddDirectAgent(ctx, body.Name, body.Address, body.Token, body.InsecureTLS, by)
	case AgentConnect:
		if body.Address != "" || body.Token != "" || body.InsecureTLS {
			writeError(w, http.StatusBadRequest, "a connecting agent takes only a name; its token is generated")
			return
		}
		var token string
		if a, token, err = s.st.AddConnectAgent(ctx, body.Name, by); err == nil {
			out["token"] = token
			base, command, warning := s.agentCommand(r, token)
			setCommand(out, base, command, warning)
			w.Header().Set("Cache-Control", "no-store")
		}
	default:
		writeError(w, http.StatusBadRequest, `mode must be "direct" or "connect"`)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.record(r, "", "", "agent.add", a.Name)
	// A direct agent is checked now, so the reply shows whether it works.
	out["agent"] = agentView(s.agents.Probe(ctx, a.ID), &a, s.userNames(ctx))
	writeJSON(w, http.StatusCreated, out)
}

// storedAgent finds a registered agent, refusing flag agents, which can't
// be changed from here.
func (s *Server) storedAgent(w http.ResponseWriter, r *http.Request) (Agent, bool) {
	id := chi.URLParam(r, "id")
	if strings.HasPrefix(id, flagAgentPrefix) {
		writeError(w, http.StatusBadRequest, "this agent comes from the server's --agent flags; change it there")
		return Agent{}, false
	}
	row, err := s.st.loadAgent(r.Context(), s.st.db, id)
	if err != nil {
		s.fail(w, err)
		return Agent{}, false
	}
	return row.agent(), true
}

func (s *Server) adminUpdateAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        *string `json:"name"`
		Address     *string `json:"address"`
		Token       *string `json:"token"`
		InsecureTLS *bool   `json:"insecureTLS"`
	}
	if !decode(w, r, &body) {
		return
	}
	old, ok := s.storedAgent(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	a, err := s.st.UpdateAgent(ctx, old.ID, body.Name, body.Address, body.Token, body.InsecureTLS)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.record(r, "", "", "agent.update", a.Name)
	// A new address or token shows its effect right away.
	writeJSON(w, http.StatusOK, agentView(s.agents.Probe(ctx, a.ID), &a, s.userNames(ctx)))
}

func (s *Server) adminDeleteAgent(w http.ResponseWriter, r *http.Request) {
	a, ok := s.storedAgent(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteAgent(r.Context(), a.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.agents.Forget(a.ID)
	s.record(r, "", "", "agent.remove", a.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminTestAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, isFlag := s.agents.isFlag(id); isFlag {
		writeJSON(w, http.StatusOK, s.agents.Probe(r.Context(), id))
		return
	}
	if strings.HasPrefix(id, flagAgentPrefix) {
		writeError(w, http.StatusNotFound, "no such agent")
		return
	}
	if _, err := s.st.loadAgent(r.Context(), s.st.db, id); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.agents.Probe(r.Context(), id))
}

func (s *Server) adminRotateAgent(w http.ResponseWriter, r *http.Request) {
	a, ok := s.storedAgent(w, r)
	if !ok {
		return
	}
	token, err := s.st.RotateAgentToken(r.Context(), a.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.agents.Forget(a.ID)
	s.record(r, "", "", "agent.rotate", a.Name)
	base, command, warning := s.agentCommand(r, token)
	out := map[string]any{"token": token}
	setCommand(out, base, command, warning)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
