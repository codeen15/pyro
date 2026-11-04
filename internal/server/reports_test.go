package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// runAndWait starts a collection run and returns its final view.
func runAndWait(t *testing.T, ts *httptest.Server, body string) map[string]any {
	t.Helper()
	_, out := do(t, ts, "POST", "/api/v1/runs", body, true)
	var run map[string]any
	_ = json.Unmarshal([]byte(out), &run)
	for i := 0; i < 100; i++ {
		_, out = do(t, ts, "GET", "/api/v1/runs/"+run["id"].(string), "", true)
		_ = json.Unmarshal([]byte(out), &run)
		if run["status"] != "running" {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run didn't finish: %s", out)
	return nil
}

func TestRunsAreSavedAsReports(t *testing.T) {
	healthy := true
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy && r.URL.Path == "/b" {
			w.WriteHeader(500)
		}
		w.Write([]byte(`{"ok":true,"token":"super-secret-value"}`))
	}))
	defer target.Close()
	ts := newTestServer(t)
	do(t, ts, "PUT", "/api/v1/environments/staging", `{"name":"staging","variables":[{"key":"base","value":"`+target.URL+`"},{"key":"apiKey","value":"super-secret-value","secret":true}]}`, true)
	do(t, ts, "PUT", "/api/v1/requests/demo/a", `{"name":"A","url":"{{base}}/a","headers":[{"key":"X-Key","value":"{{apiKey}}"}],"assertions":[{"type":"status","value":"200"}]}`, true)
	do(t, ts, "PUT", "/api/v1/requests/demo/b", `{"name":"B","url":"{{base}}/b","assertions":[{"type":"status","value":"200"}]}`, true)

	first := runAndWait(t, ts, `{"path":"demo","environment":"staging","dataFile":"users.csv"}`)
	id1, _ := first["reportId"].(string)
	if id1 == "" {
		t.Fatalf("no report id: %v", first)
	}
	healthy = false
	id2 := runAndWait(t, ts, `{"path":"demo","environment":"staging"}`)["reportId"].(string)

	// The list has both, newest first, with their outcome.
	_, out := do(t, ts, "GET", "/api/v1/reports", "", true)
	var list []map[string]any
	_ = json.Unmarshal([]byte(out), &list)
	if len(list) != 2 || list[0]["id"] != id2 || list[0]["status"] != "failed" || list[1]["status"] != "passed" || list[0]["title"] != "Demo" || list[0]["environment"] != "staging" {
		t.Fatalf("list: %s", out)
	}

	// The saved report masks the secret wherever it appeared.
	res, out := do(t, ts, "GET", "/api/v1/reports/"+id1, "", true)
	if res.StatusCode != 200 || strings.Contains(out, "super-secret-value") || !strings.Contains(out, `"dataFile":"users.csv"`) {
		t.Fatalf("report: %d %s", res.StatusCode, out)
	}

	// Compared with the previous run, B now fails.
	_, out = do(t, ts, "GET", "/api/v1/reports/"+id2+"/compare", "", true)
	if !strings.Contains(out, `"name":"B"`) || !strings.Contains(out, `"newlyFailing":[{`) {
		t.Fatalf("compare: %s", out)
	}
	if res, _ := do(t, ts, "GET", "/api/v1/reports/"+id1+"/compare", "", true); res.StatusCode != 204 {
		t.Fatalf("first run has nothing to compare with: %d", res.StatusCode)
	}

	// Every export format.
	for format, want := range map[string]string{"html": "Now failing", "csv": "Iteration,Request", "junit": "<testsuites", "json": `"totals"`, "xlsx": "PK"} {
		res, out := do(t, ts, "GET", "/api/v1/reports/"+id2+"/export?format="+format, "", true)
		if res.StatusCode != 200 || !strings.Contains(out, want) || strings.Contains(out, "super-secret-value") {
			t.Errorf("export %s: %d %.120q", format, res.StatusCode, out)
		}
		if format == "xlsx" && !strings.Contains(res.Header.Get("Content-Disposition"), `.xlsx"`) {
			t.Errorf("xlsx filename: %q", res.Header.Get("Content-Disposition"))
		}
	}
	if res, _ := do(t, ts, "GET", "/api/v1/reports/"+id2+"/export?format=pdf", "", true); res.StatusCode != 400 {
		t.Fatalf("unknown format: %d", res.StatusCode)
	}

	// Uploads (from the CLI) and deletes.
	upload := `{"kind":"run","report":{"title":"CI run","target":"demo","summary":{"iterations":1,"results":[{"path":"demo/a","name":"A","result":{"sent":{"method":"GET","url":"http://x","headers":[{"key":"Authorization","value":"Bearer leaked"}]},"response":{"status":200}}}]}}}`
	res, out = do(t, ts, "POST", "/api/v1/reports", upload, true)
	if res.StatusCode != 201 {
		t.Fatalf("upload: %d %s", res.StatusCode, out)
	}
	var up map[string]string
	_ = json.Unmarshal([]byte(out), &up)
	_, out = do(t, ts, "GET", "/api/v1/reports/"+up["id"], "", true)
	if strings.Contains(out, "leaked") || !strings.Contains(out, `"status":"passed"`) {
		t.Fatalf("uploaded report: %s", out)
	}
	if res, _ := do(t, ts, "POST", "/api/v1/reports", `{"kind":"run"}`, true); res.StatusCode != 400 {
		t.Fatalf("empty upload: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "DELETE", "/api/v1/reports/"+id1, "", true); res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "GET", "/api/v1/reports/"+id1, "", true); res.StatusCode != 404 {
		t.Fatalf("after delete: %d", res.StatusCode)
	}
}
