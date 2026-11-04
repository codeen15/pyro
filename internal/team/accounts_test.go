package team

import (
	"strings"
	"testing"
)

func TestPersonalAccessTokens(t *testing.T) {
	ts, _ := newTeam(t, Config{})
	c := newClient(t, ts)
	me := signup(t, c, "dev@example.com")
	pid := firstProject(t, me)

	code, out := c.do("POST", "/api/v1/me/tokens", `{"name":"CI pipeline","days":30}`)
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	tok := out["token"].(string)
	if !strings.HasPrefix(tok, PATPrefix) {
		t.Fatalf("token %q", tok)
	}
	if code, _ := c.do("POST", "/api/v1/me/tokens", `{"name":""}`); code != 400 {
		t.Fatalf("unnamed token: %d", code)
	}

	// The token acts as the user, writes included.
	if code, _ := bearerDo(t, ts.URL, "GET", "/api/v1/me", tok, ""); code != 200 {
		t.Fatalf("me with PAT: %d", code)
	}
	if code, _ := bearerDo(t, ts.URL, "POST", "/api/v1/projects/"+pid+"/collections", tok, `{"slug":"ci","name":"CI"}`); code != 201 {
		t.Fatalf("write with PAT: %d", code)
	}
	code, list := c.doList("GET", "/api/v1/me/tokens")
	if code != 200 || len(list) != 1 || list[0]["name"] != "CI pipeline" || list[0]["lastUsed"] == nil || list[0]["expires"] == nil {
		t.Fatalf("list: %d %v", code, list)
	}
	if strings.Contains(toJSON(list), tok) {
		t.Fatal("list shows the token")
	}

	// Revoked tokens stop working; made-up ones never do.
	if code, _ := c.do("DELETE", "/api/v1/me/tokens/"+list[0]["id"].(string), ""); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	for _, bad := range []string{tok, PATPrefix + "made-up"} {
		if code, _ := bearerDo(t, ts.URL, "GET", "/api/v1/me", bad, ""); code != 401 {
			t.Fatalf("token %q after revoke: %d", bad, code)
		}
	}
}

func TestAdminAndPasswordResets(t *testing.T) {
	mail := &fakeMailer{}
	ts, st := newTeam(t, Config{Mailer: mail})
	admin := newClient(t, ts)
	signup(t, admin, "admin@example.com")
	u, err := st.CreateUser(t.Context(), NewUser{Email: "user@example.com", Password: "correct horse battery"})
	if err != nil {
		t.Fatal(err)
	}
	user := newClient(t, ts)
	user.do("POST", "/api/v1/auth/login", `{"email":"user@example.com","password":"correct horse battery"}`)

	// Only administrators reach the admin API.
	if code, _ := user.do("GET", "/api/v1/admin/users", ""); code != 403 {
		t.Fatalf("non-admin: %d", code)
	}
	code, users := admin.doList("GET", "/api/v1/admin/users")
	if code != 200 || len(users) != 2 {
		t.Fatalf("users: %d %v", code, users)
	}
	if code, set := admin.do("GET", "/api/v1/admin/settings", ""); code != 200 || set["email"] != true {
		t.Fatalf("settings: %d %v", code, set)
	}

	// The only admin can't demote or disable themself.
	_, me := admin.do("GET", "/api/v1/me", "")
	aid := me["user"].(map[string]any)["id"].(string)
	if code, _ := admin.do("PATCH", "/api/v1/admin/users/"+aid, `{"admin":false}`); code != 400 {
		t.Fatalf("last admin demoted: %d", code)
	}
	if code, _ := admin.do("PATCH", "/api/v1/admin/users/"+aid, `{"disabled":true}`); code != 400 {
		t.Fatalf("last admin disabled: %d", code)
	}

	// Disabling signs the user out at once; enabling lets them back in.
	if code, _ := admin.do("PATCH", "/api/v1/admin/users/"+u.ID, `{"disabled":true}`); code != 200 {
		t.Fatalf("disable: %d", code)
	}
	if code, _ := user.do("GET", "/api/v1/me", ""); code != 401 {
		t.Fatalf("disabled user still in: %d", code)
	}
	if code, _ := newClient(t, ts).do("POST", "/api/v1/auth/login", `{"email":"user@example.com","password":"correct horse battery"}`); code != 400 {
		t.Fatalf("disabled user signed in: %d", code)
	}
	admin.do("PATCH", "/api/v1/admin/users/"+u.ID, `{"disabled":false}`)

	// An admin reset link: emailed, shown once, sets the password, one use.
	code, link := admin.do("POST", "/api/v1/admin/users/"+u.ID+"/reset-link", "")
	if code != 201 || link["emailed"] != true || len(mail.sent) != 1 {
		t.Fatalf("reset link: %d %v %d", code, link, len(mail.sent))
	}
	tok := link["url"].(string)[strings.LastIndex(link["url"].(string), "/")+1:]
	anon := newClient(t, ts)
	if code, p := anon.do("GET", "/api/v1/auth/reset/"+tok, ""); code != 200 || p["email"] != "user@example.com" {
		t.Fatalf("preview: %d %v", code, p)
	}
	if code, _ := anon.do("POST", "/api/v1/auth/reset", `{"token":"`+tok+`","password":"short"}`); code != 400 {
		t.Fatalf("short password: %d", code)
	}
	if code, _ := anon.do("POST", "/api/v1/auth/reset", `{"token":"`+tok+`","password":"a whole new password"}`); code != 200 {
		t.Fatalf("reset: %d", code)
	}
	if code, _ := anon.do("GET", "/api/v1/me", ""); code != 200 {
		t.Fatalf("signed in after reset: %d", code)
	}
	if code, _ := newClient(t, ts).do("POST", "/api/v1/auth/reset", `{"token":"`+tok+`","password":"another new password"}`); code != 410 {
		t.Fatalf("reused link: %d", code)
	}
	if code, _ := newClient(t, ts).do("POST", "/api/v1/auth/login", `{"email":"user@example.com","password":"a whole new password"}`); code != 200 {
		t.Fatalf("new password: %d", code)
	}

	// Forgot password: always 204, emails only real local accounts.
	before := len(mail.sent)
	for _, e := range []string{"user@example.com", "nobody@example.com"} {
		if code, _ := newClient(t, ts).do("POST", "/api/v1/auth/forgot", `{"email":"`+e+`"}`); code != 204 {
			t.Fatalf("forgot %s: %d", e, code)
		}
	}
	if len(mail.sent) != before+1 || !strings.Contains(mail.sent[len(mail.sent)-1], "/reset/") {
		t.Fatalf("forgot emails: %v", mail.sent[before:])
	}
}

func TestForgotNeedsEmail(t *testing.T) {
	ts, _ := newTeam(t, Config{})
	if code, _ := newClient(t, ts).do("POST", "/api/v1/auth/forgot", `{"email":"a@example.com"}`); code != 404 {
		t.Fatalf("forgot without SMTP: %d", code)
	}
}
