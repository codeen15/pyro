package team

import (
	"fmt"
	"testing"

	"github.com/jimlambrt/gldap"
	"github.com/jimlambrt/gldap/testdirectory"
)

// A real LDAP server (gldap's in-memory test directory) speaking the
// protocol to Pyro's LDAP client.
func startDirectory(t *testing.T) (*testdirectory.Directory, LDAPConfig) {
	t.Helper()
	d := testdirectory.Start(t, testdirectory.WithNoTLS(t))
	admins := testdirectory.NewMemberOf(t, []string{"pyro-admins"})
	users := testdirectory.NewUsers(t, []string{"bob", "svc"})
	users = append(users, testdirectory.NewUsers(t, []string{"alice"}, testdirectory.WithMembersOf(t, admins...))...)
	d.SetUsers(users...)
	cfg := LDAPConfig{
		URL:            fmt.Sprintf("ldap://%s:%d", d.Host(), d.Port()),
		BindDN:         "cn=svc," + testdirectory.DefaultUserDN,
		BindPassword:   "password",
		BaseDN:         testdirectory.DefaultUserDN,
		UserFilter:     "(cn={username})", // the test directory only matches simple filters
		EmailAttribute: "email",
		NameAttribute:  "name",
		AdminGroup:     admins[0],
	}
	return d, cfg
}

func TestLDAPSignIn(t *testing.T) {
	d, cfg := startDirectory(t)
	dir, err := NewLDAP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := newTeam(t, Config{Directory: dir})
	signup(t, newClient(t, ts), "first@example.com") // the server's first account (admin)

	bob := newClient(t, ts)
	code, me := bob.do("POST", "/api/v1/auth/login", `{"email":"bob","password":"password"}`)
	if code != 200 {
		t.Fatalf("ldap sign-in: %d %v", code, me)
	}
	u := me["user"].(map[string]any)
	if u["provider"] != "ldap" || u["email"] != "bob@example.com" || u["admin"] != false {
		t.Fatalf("ldap user: %v", u)
	}
	// Signing in again reuses the account (by DN), and works by email too.
	again := newClient(t, ts)
	_, me2 := again.do("POST", "/api/v1/auth/login", `{"email":"bob","password":"password"}`)
	if me2["user"].(map[string]any)["id"] != u["id"] {
		t.Fatal("second sign-in made a new account")
	}
	// The personal workspace exists for directory accounts too.
	if len(me["workspaces"].([]any)) != 1 {
		t.Fatalf("workspaces: %v", me["workspaces"])
	}

	for name, body := range map[string]string{
		"wrong password": `{"email":"bob","password":"nope-nope-nope"}`,
		"empty password": `{"email":"bob","password":""}`,
		"unknown user":   `{"email":"carol","password":"password"}`,
		"filter attack":  `{"email":"*","password":"password"}`,
	} {
		if code, out := newClient(t, ts).do("POST", "/api/v1/auth/login", body); code != 400 {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}

	// Group membership makes alice an administrator.
	_, am := newClient(t, ts).do("POST", "/api/v1/auth/login", `{"email":"alice","password":"password"}`)
	if am["user"].(map[string]any)["admin"] != true {
		t.Fatalf("alice should be admin: %v", am["user"])
	}

	// API tokens work for directory accounts.
	if code, tok := bearerDo(t, ts.URL, "POST", "/api/v1/auth/token", "", `{"email":"bob","password":"password"}`); code != 200 || tok["accessToken"] == "" {
		t.Fatalf("token for ldap user: %d %v", code, tok)
	}
	_ = d
}

// A directory that's down is reported, not mistaken for a wrong password.
func TestLDAPDown(t *testing.T) {
	dir, _ := NewLDAP(LDAPConfig{URL: "ldap://127.0.0.1:1", BaseDN: "dc=example,dc=org"})
	ts, _ := newTeam(t, Config{Directory: dir})
	if code, _ := newClient(t, ts).do("POST", "/api/v1/auth/login", `{"email":"bob","password":"password"}`); code != 502 {
		t.Fatalf("directory down: %d", code)
	}
}

func TestLDAPEscapesTheUsername(t *testing.T) {
	_, cfg := startDirectory(t)
	cfg.UserFilter = "(&(objectClass=person)(cn={username}))"
	dir, _ := NewLDAP(cfg)
	_, err := dir.Authenticate(t.Context(), "bob)(cn=*", "password")
	if err == nil {
		t.Fatal("filter injection signed in")
	}
}

func TestLDAPConfigChecks(t *testing.T) {
	if d, err := NewLDAP(LDAPConfig{}); d != nil || err != nil {
		t.Fatal("empty config should mean no LDAP")
	}
	if _, err := NewLDAP(LDAPConfig{URL: "ldap://x"}); err == nil {
		t.Fatal("missing baseDN accepted")
	}
	if _, err := NewLDAP(LDAPConfig{URL: "ldap://x", BaseDN: "dc=x", UserFilter: "(uid=bob)"}); err == nil {
		t.Fatal("filter without {username} accepted")
	}
}

var _ = gldap.NewEntry
