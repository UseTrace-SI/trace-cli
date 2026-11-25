package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func doctorChecks(t *testing.T, root string) map[string]struct {
	OK     bool
	Detail string
} {
	t.Helper()
	jsonOutput := true
	cmd := doctorCommand(&root, &jsonOutput)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Checks []struct {
			Name   string
			OK     bool
			Detail string
		}
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	checks := map[string]struct {
		OK     bool
		Detail string
	}{}
	for _, c := range report.Checks {
		checks[c.Name] = struct {
			OK     bool
			Detail string
		}{c.OK, c.Detail}
	}
	return checks
}

func TestDoctorWarnsAboutStoreSizeAndDroppedEvents(t *testing.T) {
	t.Setenv("TRACE_HOME", t.TempDir())
	t.Setenv("TRACE_MAX_STORE_BYTES", "")
	root := competitiveRepo(t)
	checks := doctorChecks(t, root)
	if c := checks[".trace size"]; !c.OK {
		t.Fatalf("small store flagged: %+v", c)
	}
	if c := checks["event spool"]; !c.OK {
		t.Fatalf("empty spool flagged: %+v", c)
	}
	t.Setenv("TRACE_MAX_STORE_BYTES", "1")
	if c := doctorChecks(t, root)[".trace size"]; c.OK || !strings.Contains(c.Detail, "trace gc") {
		t.Fatalf("oversized store not flagged: %+v", c)
	}
	t.Setenv("TRACE_MAX_STORE_BYTES", "")
	if err := os.WriteFile(filepath.Join(root, ".trace", "spool-dropped"), []byte("xxx"), 0600); err != nil {
		t.Fatal(err)
	}
	if c := doctorChecks(t, root)["event spool"]; c.OK || !strings.Contains(c.Detail, "3 hook events dropped") {
		t.Fatalf("dropped events not reported: %+v", c)
	}
	old := time.Now().Add(-2 * spoolDropWarnWindow)
	if err := os.Chtimes(filepath.Join(root, ".trace", "spool-dropped"), old, old); err != nil {
		t.Fatal(err)
	}
	if c := doctorChecks(t, root)["event spool"]; !c.OK {
		t.Fatalf("drops from long ago still flagged: %+v", c)
	}
}

func TestDoctorChecksRedactionKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TRACE_HOME", home)
	root := competitiveRepo(t)
	if c := doctorChecks(t, root)["redaction key"]; c.OK || !strings.Contains(c.Detail, "missing") {
		t.Fatalf("missing key not flagged: %+v", c)
	}
	if err := os.WriteFile(filepath.Join(home, "redaction.key"), []byte(strings.Repeat("ab", 32)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if c := doctorChecks(t, root)["redaction key"]; !c.OK {
		t.Fatalf("usable key flagged: %+v", c)
	}
	if err := os.Chmod(filepath.Join(home, "redaction.key"), 0644); err != nil {
		t.Fatal(err)
	}
	if c := doctorChecks(t, root)["redaction key"]; c.OK || !strings.Contains(c.Detail, "0600") {
		t.Fatalf("readable key not flagged: %+v", c)
	}
}
