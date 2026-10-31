package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

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
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, dashboardHTML)
		})
		mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			var sessions, turns, keyframes, ops int64
			e.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions)
			e.DB.QueryRow("SELECT COUNT(*) FROM turns").Scan(&turns)
			e.DB.QueryRow("SELECT COUNT(*) FROM keyframes").Scan(&keyframes)
			e.DB.QueryRow("SELECT COUNT(*) FROM ops").Scan(&ops)
			json.NewEncoder(w).Encode(map[string]any{"sessions": sessions, "turns": turns, "keyframes": keyframes, "ops": ops})
		})
		mux.HandleFunc("/api/log", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit <= 0 || limit > 100 {
				limit = 20
			}
			rows, err := e.DB.Query("SELECT k.seq,COALESCE(s.agent,''),COALESCE(t.prompt,'') FROM keyframes k LEFT JOIN turns t ON t.id=k.turn_id LEFT JOIN sessions s ON s.id=t.session_id ORDER BY k.seq DESC LIMIT ?", limit)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			defer rows.Close()
			type step struct {
				Step   int64  `json:"step"`
				Agent  string `json:"agent"`
				Prompt string `json:"prompt"`
			}
			var steps []step
			for rows.Next() {
				var st step
				rows.Scan(&st.Step, &st.Agent, &st.Prompt)
				steps = append(steps, st)
			}
			json.NewEncoder(w).Encode(steps)
		})
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		fmt.Fprintf(c.OutOrStdout(), "dashboard listening on http://%s (Ctrl-C to stop)\n", addr)
		return http.ListenAndServe(addr, mux)
	}}
	cmd.Flags().IntVar(&port, "port", 8787, "port to listen on")
	return cmd
}

const dashboardHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>Trace dashboard</title>
<style>body{font-family:system-ui,sans-serif;margin:2rem;background:#111;color:#eee} h1{color:#7cf} table{border-collapse:collapse;width:100%} td,th{border:1px solid #333;padding:.5rem;text-align:left} .stat{display:inline-block;margin:1rem;padding:1rem;background:#222;border-radius:.5rem}</style>
</head><body>
<h1>Trace dashboard</h1>
<div id="stats"></div>
<h2>Recent steps</h2>
<table id="log"><thead><tr><th>Step</th><th>Agent</th><th>Prompt</th></tr></thead><tbody></tbody></table>
<script>
fetch('/api/stats').then(r=>r.json()).then(d=>{
  const s=document.getElementById('stats');
  for(const [k,v] of Object.entries(d)){const el=document.createElement('div');el.className='stat';el.textContent=k+': '+v;s.appendChild(el);}
});
fetch('/api/log?limit=50').then(r=>r.json()).then(d=>{
  const tb=document.querySelector('#log tbody');
  for(const row of d){const tr=document.createElement('tr');tr.innerHTML='<td>'+row.step+'</td><td>'+row.agent+'</td><td>'+ (row.prompt||'').slice(0,80)+'</td>';tb.appendChild(tr);}
});
</script></body></html>`