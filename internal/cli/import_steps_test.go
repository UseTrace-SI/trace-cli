package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/store"
)

func execHistory(t *testing.T, e *engine.Engine, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		if _, err := e.DB.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

// stepSource builds a checkout whose history starts at step 0, as every
// exported history does: a baseline, two turns with their keyframes, a trail
// over all three keyframes and a companion snapshot taken at step 1. It
// returns the checkout and its keyframe IDs in step order.
func stepSource(t *testing.T) (string, []string) {
	t.Helper()
	root := competitiveRepo(t)
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	var baseline, snapshot string
	if err := e.DB.QueryRow("SELECT id,manifest_hash FROM keyframes WHERE seq=0").Scan(&baseline, &snapshot); err != nil {
		t.Fatal(err)
	}
	execHistory(t, e,
		"INSERT INTO sessions(id,agent,worktree,started_at) VALUES('src-session','codex','repo',1)",
		"INSERT INTO turns(id,session_id,seq,prompt,started_at) VALUES('src-turn-1','src-session',1,'first',2),('src-turn-2','src-session',2,'second',3)",
		"INSERT INTO keyframes(id,turn_id,seq,manifest_hash,tag,created_at) VALUES('src-key-1','src-turn-1',1,'"+snapshot+"','turn',2),('src-key-2','src-turn-2',2,'"+snapshot+"','turn',3)",
		"INSERT INTO trails(id,title,kind,base_step,head_step,created_at) VALUES('src-trail','whole history','feature',0,2,4)",
		`INSERT INTO context_snapshots(id,source_step,created_at,payload,fingerprint) VALUES('src-snapshot',1,5,'{"schema":1,"source_step":1}','src')`,
	)
	return root, []string{baseline, "src-key-1", "src-key-2"}
}

// recordLocalSteps gives a destination keyframes and turns at the given steps.
// Keyframe step 0 is the baseline `trace init` takes; later keyframes reuse
// its snapshot.
func recordLocalSteps(t *testing.T, root string, keyframes, turns []int64) {
	t.Helper()
	if len(keyframes) == 0 {
		return
	}
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	var snapshot string
	if err := e.DB.QueryRow("SELECT manifest_hash FROM keyframes WHERE seq=0").Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	execHistory(t, e, "INSERT INTO sessions(id,agent,worktree,started_at) VALUES('local-session','codex','repo',1)")
	for _, step := range keyframes[1:] {
		execHistory(t, e, fmt.Sprintf("INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) VALUES('local-key-%d',%d,'%s','turn',1)", step, step, snapshot))
	}
	for _, step := range turns {
		execHistory(t, e, fmt.Sprintf("INSERT INTO turns(id,session_id,seq,started_at) VALUES('local-turn-%d','local-session',%d,1)", step, step))
	}
}

// allSteps returns the step of every row of table, by ID.
func allSteps(t *testing.T, root, table string) map[string]int64 {
	t.Helper()
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	rows, err := e.DB.Query("SELECT id,seq FROM " + table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	steps := map[string]int64{}
	for rows.Next() {
		var id string
		var step int64
		if err := rows.Scan(&id, &step); err != nil {
			t.Fatal(err)
		}
		steps[id] = step
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return steps
}

// importedSteps is where a stepSource history landed: the steps of its
// keyframes and turns in source order, its trail range and snapshot step.
type importedSteps struct {
	Keyframes, Turns []int64
	Trail            [2]int64
	Snapshot         int64
}

func readImportedSteps(t *testing.T, root string, keyframeIDs []string) importedSteps {
	t.Helper()
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	steps := func(table string, ids []string) []int64 {
		var out []int64
		for _, id := range ids {
			var step int64
			if err := e.DB.QueryRow("SELECT seq FROM "+table+" WHERE id=?", id).Scan(&step); err != nil {
				t.Fatalf("%s %s: %v", table, id, err)
			}
			out = append(out, step)
		}
		return out
	}
	got := importedSteps{Keyframes: steps("keyframes", keyframeIDs), Turns: steps("turns", []string{"src-turn-1", "src-turn-2"})}
	if err := e.DB.QueryRow("SELECT base_step,head_step FROM trails WHERE id='src-trail'").Scan(&got.Trail[0], &got.Trail[1]); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT source_step FROM context_snapshots WHERE id='src-snapshot'").Scan(&got.Snapshot); err != nil {
		t.Fatal(err)
	}
	return got
}

// `trace init` and the daemon take a baseline at step 0, and every exported
// history starts at step 0 too. An import appends steps that are already taken
// in source order, carries trail and snapshot steps along, leaves local steps
// alone, and importing the same history again changes nothing.
func TestImportRenumbersTakenSteps(t *testing.T) {
	source, keyframeIDs := stepSource(t)
	imports := []struct {
		name string
		run  func(t *testing.T, target string) (importReport, error)
	}{
		{"bundle", func(t *testing.T, target string) (importReport, error) {
			archive := filepath.Join(t.TempDir(), "history.tar.gz")
			if err := writeBundle(source, archive); err != nil {
				t.Fatal(err)
			}
			return readBundleReport(context.Background(), target, archive)
		}},
		{"database", func(t *testing.T, target string) (importReport, error) {
			return importDatabase(context.Background(), target, filepath.Join(source, ".trace", "trace.db"))
		}},
	}
	for _, tc := range []struct {
		name             string
		keyframes, turns []int64 // local steps before the import
		want             importedSteps
	}{
		{"empty history", nil, nil, importedSteps{[]int64{0, 1, 2}, []int64{1, 2}, [2]int64{0, 2}, 1}},
		{"initialized checkout", []int64{0}, nil, importedSteps{[]int64{1, 2, 3}, []int64{1, 2}, [2]int64{1, 3}, 2}},
		{"local history", []int64{0, 1, 2}, []int64{1, 2}, importedSteps{[]int64{3, 4, 5}, []int64{3, 4}, [2]int64{3, 5}, 4}},
		// Free steps after a renumbered one are appended too, keeping the order.
		{"sparse local history", []int64{0, 5}, []int64{1, 5}, importedSteps{[]int64{6, 7, 8}, []int64{6, 7}, [2]int64{6, 8}, 7}},
	} {
		for _, imp := range imports {
			t.Run(tc.name+"/"+imp.name, func(t *testing.T) {
				target := competitiveRepo(t)
				recordLocalSteps(t, target, tc.keyframes, tc.turns)
				local := map[string]map[string]int64{"keyframes": allSteps(t, target, "keyframes"), "turns": allSteps(t, target, "turns")}
				for attempt, want := range []importReport{{Imported: 8}, {Skipped: 8}} {
					report, err := imp.run(t, target)
					if err != nil {
						t.Fatalf("import %d: %v", attempt+1, err)
					}
					if report.Imported != want.Imported || report.Skipped != want.Skipped {
						t.Fatalf("import %d reported %+v, want %d imported and %d skipped", attempt+1, report, want.Imported, want.Skipped)
					}
					if got := readImportedSteps(t, target, keyframeIDs); !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("import %d steps = %+v, want %+v", attempt+1, got, tc.want)
					}
					for table, before := range local {
						after := allSteps(t, target, table)
						for id, step := range before {
							if got, ok := after[id]; !ok || got != step {
								t.Fatalf("import %d moved local %s %s from step %d to %d (present %v)", attempt+1, table, id, step, got, ok)
							}
						}
					}
				}
			})
		}
	}
}

// A source row that no longer matches the row it was imported as is still a
// conflict after renumbering, including a changed step reference, and the
// failed import commits nothing.
func TestImportRejectsChangedRowsAfterRenumbering(t *testing.T) {
	for _, tc := range []struct{ name, change string }{
		{"keyframe", "UPDATE keyframes SET tag='edited' WHERE id='src-key-2'"},
		{"turn", "UPDATE turns SET prompt='edited' WHERE id='src-turn-1'"},
		{"trail step", "UPDATE trails SET head_step=1 WHERE id='src-trail'"},
		{"snapshot step", "UPDATE context_snapshots SET source_step=2 WHERE id='src-snapshot'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, _ := stepSource(t)
			target := competitiveRepo(t)
			recordLocalSteps(t, target, []int64{0}, nil)
			history := filepath.Join(source, ".trace", "trace.db")
			if _, err := importDatabase(context.Background(), target, history); err != nil {
				t.Fatalf("first import: %v", err)
			}
			e, err := engine.Open(source)
			if err != nil {
				t.Fatal(err)
			}
			execHistory(t, e, tc.change, "INSERT INTO sessions(id,agent,worktree,started_at) VALUES('src-later','codex','repo',9)")
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := importDatabase(context.Background(), target, history); err == nil || !strings.Contains(err.Error(), "conflicting") {
				t.Fatalf("changed row was not rejected: %v", err)
			}
			got, err := engine.Open(target)
			if err != nil {
				t.Fatal(err)
			}
			defer got.Close()
			var n int
			if err := got.DB.QueryRow("SELECT COUNT(*) FROM sessions WHERE id='src-later'").Scan(&n); err != nil || n != 0 {
				t.Fatalf("failed import committed rows: %d %v", n, err)
			}
		})
	}
}

// A history column holding a step must be described in stepColumns, and the
// table numbering a step must be imported before the rows referring to it.
func TestStepColumnsCoverHistory(t *testing.T) {
	position := map[string]int{}
	for i, table := range store.HistoryTables {
		position[table] = i
	}
	for _, table := range store.HistoryTables {
		for _, col := range store.HistoryColumns[table] {
			if _, ok := stepColumns[table][col]; !ok && (col == "seq" || strings.HasSuffix(col, "_step")) {
				t.Errorf("%s.%s holds a step but is missing from stepColumns", table, col)
			}
		}
	}
	for table, cols := range stepColumns {
		for col, owner := range cols {
			switch {
			case !slices.Contains(store.HistoryColumns[table], col):
				t.Errorf("%s.%s is not a history column", table, col)
			case col == "seq" && owner != table:
				t.Errorf("%s.seq numbers the steps of %s instead of its own", table, owner)
			case col != "seq" && (stepColumns[owner]["seq"] != owner || position[owner] >= position[table]):
				t.Errorf("%s.%s refers to steps of %s, which must be numbered by seq and imported first", table, col, owner)
			}
		}
	}
}
