package team

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type fakeMailer struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeMailer) Send(to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, to+"|"+subject+"|"+body)
	return nil
}

// teamWithWorkspace signs up an owner and creates a team workspace with one
// project; it returns the owner's client, the workspace and the project.
func teamWithWorkspace(t *testing.T, cfg Config) (*client, string, string, func(email string) *client) {
	t.Helper()
	ts, _ := newTeam(t, cfg)
	owner := newClient(t, ts)
	signup(t, owner, "owner@example.com")
	_, ws := owner.do("POST", "/api/v1/workspaces", `{"name":"QA"}`)
	wsID := ws["id"].(string)
	_, p := owner.do("POST", "/api/v1/workspaces/"+wsID+"/projects", `{"name":"Payments"}`)
	newUser := func(email string) *client { return newClient(t, ts) }
	return owner, wsID, p["id"].(string), newUser
}

func TestInviteLinkJoinRolesAndLeave(t *testing.T) {
	mail := &fakeMailer{}
	owner, wsID, pid, newUser := teamWithWorkspace(t, Config{Mailer: mail})

	// A link invite as viewer; invite-only sign-up accepts it.
	code, out := owner.do("POST", "/api/v1/workspaces/"+wsID+"/invites", `{"role":"viewer","days":3}`)
	if code != 201 {
		t.Fatalf("create invite: %d %v", code, out)
	}
	link := out["url"].(string)
	tok := link[strings.LastIndex(link, "/")+1:]
	if !strings.Contains(link, "/invite/") || out["emailed"] != false {
		t.Fatalf("invite: %v", out)
	}

	v := newUser("v@example.com")
	if code, out := v.do("GET", "/api/v1/invites/"+tok, ""); code != 200 || out["workspace"] != "QA" || out["role"] != "viewer" {
		t.Fatalf("preview: %d %v", code, out)
	}
	if code, out := v.do("POST", "/api/v1/auth/signup", `{"email":"v@example.com","password":"correct horse battery","invite":"`+tok+`"}`); code != 201 {
		t.Fatalf("signup with invite: %d %v", code, out)
	}
	if code, _ := v.do("GET", "/api/v1/projects/"+pid+"/workspace", ""); code != 200 {
		t.Fatalf("viewer reads project: %d", code)
	}
	if code, _ := v.do("POST", "/api/v1/projects/"+pid+"/collections", `{"slug":"x"}`); code != 403 {
		t.Fatalf("viewer writes: %d", code)
	}

	// Owner promotes the viewer; then the editor can write.
	code, list := owner.doList("GET", "/api/v1/workspaces/"+wsID+"/members")
	if code != 200 || len(list) != 2 {
		t.Fatalf("members: %d %v", code, list)
	}
	var vid string
	for _, m := range list {
		if m["email"] == "v@example.com" {
			vid = m["userId"].(string)
		}
	}
	if code, _ := v.do("PATCH", "/api/v1/workspaces/"+wsID+"/members/"+vid, `{"role":"owner"}`); code != 403 {
		t.Fatalf("viewer promoting self: %d", code)
	}
	if code, _ := owner.do("PATCH", "/api/v1/workspaces/"+wsID+"/members/"+vid, `{"role":"editor"}`); code != 204 {
		t.Fatalf("promote: %d", code)
	}
	if code, out := v.do("POST", "/api/v1/projects/"+pid+"/collections", `{"slug":"x","name":"X"}`); code != 201 {
		t.Fatalf("editor writes: %d %v", code, out)
	}

	// The last owner can't demote themself or leave.
	_, me := owner.do("GET", "/api/v1/me", "")
	oid := me["user"].(map[string]any)["id"].(string)
	if code, _ := owner.do("PATCH", "/api/v1/workspaces/"+wsID+"/members/"+oid, `{"role":"editor"}`); code != 400 {
		t.Fatalf("last owner demoting self: %d", code)
	}
	if code, _ := owner.do("DELETE", "/api/v1/workspaces/"+wsID+"/members/"+oid, ""); code != 400 {
		t.Fatalf("last owner leaving: %d", code)
	}
	// A member can leave.
	if code, _ := v.do("DELETE", "/api/v1/workspaces/"+wsID+"/members/"+vid, ""); code != 204 {
		t.Fatalf("leave: %d", code)
	}
	if code, _ := v.do("GET", "/api/v1/projects/"+pid+"/workspace", ""); code != 404 {
		t.Fatalf("after leaving: %d", code)
	}

	// Activity shows the story, newest first.
	code, acts := owner.doList("GET", "/api/v1/workspaces/"+wsID+"/activity")
	if code != 200 || len(acts) < 4 {
		t.Fatalf("activity: %d %v", code, acts)
	}
	all := toJSON(acts)
	for _, want := range []string{"left the workspace", "created collection", "changed a role to editor", "joined as viewer", "invited as viewer", "created project"} {
		if !strings.Contains(all, want) {
			t.Errorf("activity lacks %q: %s", want, all)
		}
	}
}

func TestEmailInvites(t *testing.T) {
	mail := &fakeMailer{}
	owner, wsID, _, newUser := teamWithWorkspace(t, Config{Mailer: mail})
	code, out := owner.do("POST", "/api/v1/workspaces/"+wsID+"/invites", `{"role":"editor","email":"e@example.com"}`)
	if code != 201 || out["emailed"] != true || len(mail.sent) != 1 || !strings.Contains(mail.sent[0], out["url"].(string)) {
		t.Fatalf("email invite: %d %v %v", code, out, mail.sent)
	}
	tok := out["url"].(string)[strings.LastIndex(out["url"].(string), "/")+1:]
	// Only that address may use it.
	other := newUser("x")
	if code, _ := other.do("POST", "/api/v1/auth/signup", `{"email":"x@example.com","password":"correct horse battery","invite":"`+tok+`"}`); code != 403 {
		t.Fatalf("wrong address: %d", code)
	}
	e := newUser("e")
	if code, _ := e.do("POST", "/api/v1/auth/signup", `{"email":"e@example.com","password":"correct horse battery","invite":"`+tok+`"}`); code != 201 {
		t.Fatalf("right address: %d", code)
	}
	// One use only.
	if code, _ := other.do("GET", "/api/v1/invites/"+tok, ""); code != 410 {
		t.Fatalf("used invite: %d", code)
	}
	// Revoked invites stop working; personal workspaces can't invite.
	_, out = owner.do("POST", "/api/v1/workspaces/"+wsID+"/invites", `{"role":"viewer"}`)
	id := out["invite"].(map[string]any)["id"].(string)
	tok2 := out["url"].(string)[strings.LastIndex(out["url"].(string), "/")+1:]
	if code, _ := owner.do("DELETE", "/api/v1/workspaces/"+wsID+"/invites/"+id, ""); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := other.do("GET", "/api/v1/invites/"+tok2, ""); code != 410 {
		t.Fatalf("revoked: %d", code)
	}
	_, me := owner.do("GET", "/api/v1/me", "")
	personal := me["workspaces"].([]any)[0].(map[string]any)["id"].(string)
	if code, _ := owner.do("POST", "/api/v1/workspaces/"+personal+"/invites", `{"role":"viewer"}`); code != 400 {
		t.Fatalf("personal invite: %d", code)
	}
}

func TestTransferCollectionAndMoveProject(t *testing.T) {
	owner, wsID, pid, _ := teamWithWorkspace(t, Config{})
	_, me := owner.do("GET", "/api/v1/me", "")
	personal := me["workspaces"].([]any)[0].(map[string]any)
	myProject := personal["projects"].([]any)[0].(map[string]any)["id"].(string)

	owner.do("POST", "/api/v1/projects/"+pid+"/collections", `{"slug":"shop","name":"Shop"}`)
	owner.do("PUT", "/api/v1/projects/"+pid+"/requests/shop/list", `{"kind":"http","name":"List","method":"GET","url":"http://x"}`)
	owner.do("PUT", "/api/v1/projects/"+pid+"/environments/staging", `{"name":"staging","variables":[]}`)

	// Copy into the personal project, with environments.
	code, out := owner.do("POST", "/api/v1/projects/"+pid+"/transfer", `{"collection":"shop","project":"`+myProject+`","mode":"copy","environments":true}`)
	if code != 200 || out["slug"] != "shop" {
		t.Fatalf("copy: %d %v", code, out)
	}
	if code, _ := owner.do("GET", "/api/v1/projects/"+myProject+"/requests/shop/list", ""); code != 200 {
		t.Fatalf("copied request: %d", code)
	}
	if code, _ := owner.do("GET", "/api/v1/projects/"+myProject+"/environments/staging", ""); code != 200 {
		t.Fatalf("copied environment: %d", code)
	}
	// Move it back: the target already has "shop", so it becomes shop-2.
	code, out = owner.do("POST", "/api/v1/projects/"+myProject+"/transfer", `{"collection":"shop","project":"`+pid+`","mode":"move"}`)
	if code != 200 || out["slug"] != "shop-2" {
		t.Fatalf("move: %d %v", code, out)
	}
	if code, _ := owner.do("GET", "/api/v1/projects/"+myProject+"/collections/shop", ""); code != 404 {
		t.Fatalf("source still has it: %d", code)
	}
	if code, _ := owner.do("POST", "/api/v1/projects/"+pid+"/transfer", `{"collection":"shop","project":"nope","mode":"copy"}`); code != 404 {
		t.Fatalf("unknown target: %d", code)
	}

	// Download the project, upload it as a new one.
	res, err := owner.c.Get(owner.ts.URL + "/api/v1/projects/" + pid + "/archive")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("archive: %v %v", err, res)
	}
	zipData, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if _, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData))); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", owner.ts.URL+"/api/v1/workspaces/"+wsID+"/projects/import?name=Copy", bytes.NewReader(zipData))
	req.Header.Set(CSRFHeader, "1")
	res, err = owner.c.Do(req)
	if err != nil || res.StatusCode != 201 {
		t.Fatalf("upload: %v %v", err, res.StatusCode)
	}
	res.Body.Close()

	// Move the project into the personal workspace.
	if code, _ := owner.do("POST", "/api/v1/projects/"+pid+"/move", `{"workspace":"`+personal["id"].(string)+`"}`); code != 204 {
		t.Fatalf("move project: %d", code)
	}
	if code, out := owner.do("GET", "/api/v1/projects/"+pid, ""); code != 200 || out["workspaceKind"] != "personal" {
		t.Fatalf("after move: %d %v", code, out)
	}
}

func TestVersionedSaveThroughProxy(t *testing.T) {
	owner, _, pid, _ := teamWithWorkspace(t, Config{})
	owner.do("POST", "/api/v1/projects/"+pid+"/collections", `{"slug":"shop","name":"Shop"}`)
	body := `{"kind":"http","name":"A","method":"GET","url":"http://x"}`
	if code, _ := owner.do("PUT", "/api/v1/projects/"+pid+"/requests/shop/a", body); code != 200 {
		t.Fatalf("create: %d", code)
	}
	res, _ := owner.c.Get(owner.ts.URL + "/api/v1/projects/" + pid + "/requests/shop/a")
	tag := res.Header.Get("ETag")
	res.Body.Close()
	put := func(match string) int {
		req, _ := http.NewRequest("PUT", owner.ts.URL+"/api/v1/projects/"+pid+"/requests/shop/a", strings.NewReader(body))
		req.Header.Set(CSRFHeader, "1")
		req.Header.Set("If-Match", match)
		res, err := owner.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if tag == "" || put(tag) != 200 {
		t.Fatalf("save with current version failed (tag %q)", tag)
	}
	if put(`"stale"`) != 409 {
		t.Fatal("stale version should conflict")
	}
}
