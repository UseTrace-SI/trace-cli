package cli

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

func dashboardCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var port int
	cmd := &cobra.Command{Use: "dashboard", Short: "Serve a local read-only web dashboard", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return fmt.Errorf("listening for the dashboard: %w", err)
		}
		defer ln.Close()
		token, err := newDashboardToken()
		if err != nil {
			return err
		}
		actual := ln.Addr().(*net.TCPAddr).Port
		server := &http.Server{
			Handler:           newDashboardHandler(e.DB.DB, actual, token),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    64 << 10,
		}
		// The token authorizes this launch only. Opening the URL sets it as a
		// cookie and redirects, so it does not linger in the address bar.
		fmt.Fprintf(c.OutOrStdout(), "dashboard listening on http://127.0.0.1:%d/?token=%s (Ctrl-C to stop)\n", actual, url.QueryEscape(token))
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serving the dashboard: %w", err)
		}
		return nil
	}}
	cmd.Flags().IntVar(&port, "port", 8787, "port to listen on")
	return cmd
}

func newDashboardToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("creating dashboard token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// newDashboardHandler serves the dashboard on 127.0.0.1:port. Every request
// must name this server in its Host header, which defeats DNS rebinding: a
// page on attacker.example that rebinds its name to 127.0.0.1 still sends
// Host: attacker.example. History is only served to a browser holding this
// launch's token cookie, so other local pages and processes cannot read it.
func newDashboardHandler(db *sql.DB, port int, token string) http.Handler {
	cookieName := "trace_dashboard_" + strconv.Itoa(port)
	allowedHosts := map[string]bool{}
	for _, host := range []string{"127.0.0.1", "localhost", "[::1]"} {
		allowedHosts[host+":"+strconv.Itoa(port)] = true
	}
	authorized := func(r *http.Request) bool {
		cookie, err := r.Cookie(cookieName)
		return err == nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) == 1
	}
	api := http.NewServeMux()
	api.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		counts := map[string]int64{}
		for _, table := range []string{"sessions", "turns", "keyframes", "ops"} {
			var n int64
			if err := db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
				dashboardError(w, fmt.Errorf("counting %s: %w", table, err))
				return
			}
			counts[table] = n
		}
		writeDashboardJSON(w, counts)
	})
	api.HandleFunc("/api/comments", func(w http.ResponseWriter, r *http.Request) {
		type comment struct {
			ID   string `json:"id"`
			File string `json:"file"`
			Line int    `json:"line"`
			Body string `json:"body"`
			At   int64  `json:"at"`
		}
		out, err := dashboardRows(r, db, "SELECT id,file,line,body,at FROM comments ORDER BY at DESC LIMIT 100", nil, func(rows *sql.Rows) (comment, error) {
			var c comment
			return c, rows.Scan(&c.ID, &c.File, &c.Line, &c.Body, &c.At)
		})
		if err != nil {
			dashboardError(w, fmt.Errorf("listing comments: %w", err))
			return
		}
		writeDashboardJSON(w, out)
	})
	api.HandleFunc("/api/timeline", func(w http.ResponseWriter, r *http.Request) {
		type op struct {
			At     int64  `json:"at"`
			Path   string `json:"path"`
			Kind   string `json:"kind"`
			Source string `json:"source"`
		}
		out, err := dashboardRows(r, db, "SELECT at,path,kind,source FROM ops ORDER BY at ASC LIMIT 200", nil, func(rows *sql.Rows) (op, error) {
			var o op
			return o, rows.Scan(&o.At, &o.Path, &o.Kind, &o.Source)
		})
		if err != nil {
			dashboardError(w, fmt.Errorf("listing operations: %w", err))
			return
		}
		writeDashboardJSON(w, out)
	})
	api.HandleFunc("/api/log", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 100 {
			limit = 20
		}
		type step struct {
			Step   int64  `json:"step"`
			Agent  string `json:"agent"`
			Prompt string `json:"prompt"`
		}
		out, err := dashboardRows(r, db, "SELECT k.seq,COALESCE(s.agent,''),COALESCE(t.prompt,'') FROM keyframes k LEFT JOIN turns t ON t.id=k.turn_id LEFT JOIN sessions s ON s.id=t.session_id ORDER BY k.seq DESC LIMIT ?", []any{limit}, func(rows *sql.Rows) (step, error) {
			var st step
			return st, rows.Scan(&st.Step, &st.Agent, &st.Prompt)
		})
		if err != nil {
			dashboardError(w, fmt.Errorf("listing steps: %w", err))
			return
		}
		writeDashboardJSON(w, out)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "no-store")
		if !allowedHosts[strings.ToLower(r.Host)] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/manifest.json":
			// Browsers fetch manifests without cookies; it holds no history.
			h.Set("Content-Type", "application/manifest+json")
			fmt.Fprint(w, dashboardManifest)
			return
		case "/sw.js":
			// Earlier versions registered a service worker on this shared
			// localhost port. Serve one that removes itself.
			h.Set("Content-Type", "application/javascript")
			fmt.Fprint(w, dashboardSW)
			return
		case "/":
			if supplied := r.URL.Query().Get("token"); supplied != "" {
				if subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) != 1 {
					http.Error(w, "invalid dashboard token; open the URL printed by trace dashboard", http.StatusForbidden)
					return
				}
				http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			if !authorized(r) {
				http.Error(w, "open the URL printed by trace dashboard", http.StatusForbidden)
				return
			}
			h.Set("Content-Type", "text/html; charset=utf-8")
			h.Set("Content-Security-Policy", "default-src 'none'; script-src 'sha256-"+dashboardScriptHash+"'; style-src 'unsafe-inline'; connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
			fmt.Fprint(w, dashboardHTML)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if !authorized(r) {
			http.Error(w, "dashboard token required", http.StatusForbidden)
			return
		}
		api.ServeHTTP(w, r)
	})
}

// dashboardRows collects every row of a query, reporting scan and iteration
// errors instead of serving a silently truncated list. An empty result
// encodes as [] rather than null.
func dashboardRows[T any](r *http.Request, db *sql.DB, query string, args []any, scan func(*sql.Rows) (T, error)) ([]T, error) {
	rows, err := db.QueryContext(r.Context(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func dashboardError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func writeDashboardJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// dashboardScript renders history with textContent only: prompts and agent
// names are untrusted text and must never be parsed as HTML.
const dashboardScript = `
fetch('/api/stats').then(r=>r.json()).then(d=>{
  const s=document.getElementById('stats');
  for(const [k,v] of Object.entries(d)){const el=document.createElement('div');el.className='stat';el.textContent=k+': '+v;s.appendChild(el);}
});
fetch('/api/log?limit=50').then(r=>r.json()).then(d=>{
  const tb=document.querySelector('#log tbody');
  for(const row of d||[]){
    const tr=document.createElement('tr');
    for(const text of [String(row.step),String(row.agent||''),String(row.prompt||'').slice(0,80)]){const td=document.createElement('td');td.textContent=text;tr.appendChild(td);}
    tb.appendChild(tr);
  }
});
`

var dashboardScriptHash = func() string {
	sum := sha256.Sum256([]byte(dashboardScript))
	return base64.StdEncoding.EncodeToString(sum[:])
}()

const dashboardHTML = `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="manifest" href="/manifest.json"><title>Trace dashboard</title>
<style>body{font-family:system-ui,sans-serif;margin:1rem;background:#111;color:#eee} h1{color:#7cf} table{border-collapse:collapse;width:100%} td,th{border:1px solid #333;padding:.5rem;text-align:left} .stat{display:inline-block;margin:.5rem;padding:.75rem;background:#222;border-radius:.5rem} @media(max-width:600px){body{margin:.5rem} table{font-size:.85rem} .stat{display:block;margin:.25rem 0}}</style>
</head><body>
<h1>Trace dashboard</h1>
<div id="stats"></div>
<h2>Recent steps</h2>
<table id="log"><thead><tr><th>Step</th><th>Agent</th><th>Prompt</th></tr></thead><tbody></tbody></table>
<script>` + dashboardScript + `</script></body></html>`

const dashboardManifest = `{"name":"Trace dashboard","short_name":"Trace","start_url":"/","display":"standalone","background_color":"#111","theme_color":"#7cf"}`

const dashboardSW = `self.addEventListener('install',()=>self.skipWaiting());self.addEventListener('activate',e=>e.waitUntil(self.registration.unregister()));`
