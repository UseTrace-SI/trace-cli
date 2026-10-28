package export

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/store"
)

func TestAgentTrace(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, sql := range []string{
		"INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s','codex','/repo',1)",
		"INSERT INTO turns(id,session_id,seq,prompt,started_at,ended_at) VALUES('t','s',1,'prompt',1,2)",
		"INSERT INTO keyframes(id,turn_id,seq,manifest_hash,tag,created_at) VALUES('k','t',1,'hash','turn',2)",
		"INSERT INTO ops(id,turn_id,path,kind,start_line,end_line,before_hash,after_hash,source,at) VALUES('o','t','src/app.go','modify',3,5,'before','after','tool',2)",
	} {
		if _, err := db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	var b bytes.Buffer
	n, err := AgentTrace(db, &b)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("records: %d", n)
	}
	var r Record
	if err := json.Unmarshal(bytes.TrimSpace(b.Bytes()), &r); err != nil {
		t.Fatal(err)
	}
	if r.Version != "0.1.0" || r.Files[0].Path != "src/app.go" || r.Files[0].Conversations[0].Contributor.Type != "ai" || r.Files[0].Conversations[0].URL != "trace://turn/t" || r.Tool.Name != "codex" || !strings.Contains(r.ID, "-") {
		t.Fatalf("record: %+v", r)
	}
}
