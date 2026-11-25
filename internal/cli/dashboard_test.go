package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/store"
)

const testDashboardToken = "test-token-0123456789"

func dashboardTestHandler(t *testing.T) http.Handler {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	const prompt = `<img src=x onerror="alert(1)"><script>alert(2)</script>`
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s1',?,'/w',1)", []any{`<b>agent</b>`}},
		{"INSERT INTO turns(id,session_id,seq,prompt,started_at) VALUES('t1','s1',1,?,1)", []any{prompt}},
		{"INSERT INTO keyframes(id,seq,manifest_hash,turn_id,created_at) VALUES('k1',1,'h','t1',1)", nil},
	} {
		if _, err := db.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.query, err)
		}
	}
	return newDashboardHandler(db.DB, 8787, testDashboardToken)
}

func dashboardRequest(h http.Handler, method, target, host string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func validDashboardCookie() *http.Cookie {
	return &http.Cookie{Name: "trace_dashboard_8787", Value: testDashboardToken}
}

// A DNS-rebound page reaches 127.0.0.1 under its own host name; every route,
// with or without a valid cookie, must refuse it.
func TestDashboardRejectsForeignHosts(t *testing.T) {
	h := dashboardTestHandler(t)
	for _, host := range []string{"attacker.example:8787", "attacker.example", "127.0.0.1", "127.0.0.1:9999", "localhost.attacker.example:8787", ""} {
		for _, path := range []string{"/", "/api/log", "/api/stats", "/manifest.json", "/?token=" + testDashboardToken} {
			if rec := dashboardRequest(h, http.MethodGet, path, host, validDashboardCookie()); rec.Code != http.StatusForbidden {
				t.Errorf("host %q path %s: status %d; want 403", host, path, rec.Code)
			}
		}
	}
	for _, host := range []string{"127.0.0.1:8787", "localhost:8787", "LOCALHOST:8787", "[::1]:8787"} {
		if rec := dashboardRequest(h, http.MethodGet, "/api/stats", host, validDashboardCookie()); rec.Code != http.StatusOK {
			t.Errorf("host %q: status %d; want 200", host, rec.Code)
		}
	}
}

func TestDashboardRequiresLaunchToken(t *testing.T) {
	h := dashboardTestHandler(t)
	const host = "127.0.0.1:8787"
	for _, tc := range []struct {
		name, path string
		cookie     *http.Cookie
		want       int
	}{
		{"page without cookie", "/", nil, http.StatusForbidden},
		{"API without cookie", "/api/log", nil, http.StatusForbidden},
		{"comments without cookie", "/api/comments", nil, http.StatusForbidden},
		{"API with a wrong cookie", "/api/log", &http.Cookie{Name: "trace_dashboard_8787", Value: "wrong"}, http.StatusForbidden},
		{"API with another port's cookie", "/api/log", &http.Cookie{Name: "trace_dashboard_9999", Value: testDashboardToken}, http.StatusForbidden},
		{"API token in the query is not enough", "/api/log?token=" + testDashboardToken, nil, http.StatusForbidden},
		{"wrong URL token", "/?token=wrong", nil, http.StatusForbidden},
		{"page with cookie", "/", validDashboardCookie(), http.StatusOK},
		{"API with cookie", "/api/log", validDashboardCookie(), http.StatusOK},
		{"timeline with cookie", "/api/timeline", validDashboardCookie(), http.StatusOK},
		{"comments with cookie", "/api/comments", validDashboardCookie(), http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := dashboardRequest(h, http.MethodGet, tc.path, host, tc.cookie); rec.Code != tc.want {
				t.Fatalf("status %d; want %d: %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
	if rec := dashboardRequest(h, http.MethodPost, "/api/log", host, validDashboardCookie()); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d; want 405", rec.Code)
	}
}

func TestDashboardURLTokenSetsLaxHttpOnlyCookie(t *testing.T) {
	h := dashboardTestHandler(t)
	rec := dashboardRequest(h, http.MethodGet, "/?token="+testDashboardToken, "localhost:8787", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("status %d location %q; want a redirect to /", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies %v", cookies)
	}
	c := cookies[0]
	if c.Name != "trace_dashboard_8787" || c.Value != testDashboardToken || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Fatalf("cookie %+v is not a Lax HttpOnly session cookie", c)
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("the token URL may leak through the Referer header")
	}
	if next := dashboardRequest(h, http.MethodGet, "/api/log", "localhost:8787", c); next.Code != http.StatusOK {
		t.Fatalf("issued cookie rejected: %d", next.Code)
	}
}

// Prompts and agent names are untrusted. The API must carry them as JSON
// strings, and the page must insert them as text, never as markup.
func TestDashboardRendersHistoryAsText(t *testing.T) {
	h := dashboardTestHandler(t)
	rec := dashboardRequest(h, http.MethodGet, "/api/log", "127.0.0.1:8787", validDashboardCookie())
	if strings.Contains(rec.Body.String(), "<img") || strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("API response carries raw markup: %s", rec.Body)
	}
	var steps []struct{ Agent, Prompt string }
	if err := json.Unmarshal(rec.Body.Bytes(), &steps); err != nil || len(steps) != 1 || !strings.HasPrefix(steps[0].Prompt, "<img") || steps[0].Agent != "<b>agent</b>" {
		t.Fatalf("steps %+v, %v", steps, err)
	}
	if strings.Contains(dashboardHTML, "innerHTML") || strings.Contains(dashboardHTML, "outerHTML") || strings.Contains(dashboardHTML, "insertAdjacentHTML") || strings.Contains(dashboardHTML, "document.write") {
		t.Fatal("dashboard page parses data as HTML")
	}
	if !strings.Contains(dashboardScript, "td.textContent=text") {
		t.Fatal("dashboard page does not render cells as text")
	}
	if strings.Contains(dashboardHTML, "serviceWorker") {
		t.Fatal("dashboard registers a service worker on a shared localhost port")
	}
	page := dashboardRequest(h, http.MethodGet, "/", "127.0.0.1:8787", validDashboardCookie())
	csp := page.Header().Get("Content-Security-Policy")
	sum := sha256.Sum256([]byte(dashboardScript))
	if !strings.Contains(csp, "script-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") || strings.Contains(csp, "script-src 'unsafe-inline'") {
		t.Fatalf("CSP %q does not pin the inline script", csp)
	}
	if !strings.Contains(page.Body.String(), "<script>"+dashboardScript+"</script>") {
		t.Fatal("served script differs from the hashed script")
	}
}

func TestDashboardEmptyListsEncodeAsArrays(t *testing.T) {
	h := dashboardTestHandler(t)
	for _, path := range []string{"/api/comments", "/api/timeline"} {
		rec := dashboardRequest(h, http.MethodGet, path, "127.0.0.1:8787", validDashboardCookie())
		if strings.TrimSpace(rec.Body.String()) != "[]" {
			t.Errorf("%s: %q; want []", path, rec.Body)
		}
	}
}
