//go:build treesitter

package structure

import "testing"

func TestFunctionChanges(t *testing.T) {
	cases := []struct{ path, before, after string }{
		{"file.go", "package p\nfunc Old() int { return 1 }\nfunc Changed() int { return 1 }\nfunc Gone() { println(9) }\n", "package p\nfunc New() int { return 1 }\nfunc Changed() int { return 2 }\nfunc Added() { println(3) }\n"},
		{"file.ts", "function Old() { return 1; }\nfunction Changed() { return 1; }\nfunction Gone() { return 9; }\n", "function New() { return 1; }\nfunction Changed() { return 2; }\nfunction Added() { return 3; }\n"},
		{"file.py", "def Old():\n    return 1\ndef Changed():\n    return 1\ndef Gone():\n    return 9\n", "def New():\n    return 1\ndef Changed():\n    return 2\ndef Added():\n    return 3\n"},
		{"file.rs", "fn Old() -> i32 { 1 }\nfn Changed() -> i32 { 1 }\nfn Gone() -> i32 { 9 }\n", "fn New() -> i32 { 1 }\nfn Changed() -> i32 { 2 }\nfn Added() -> i32 { 3 }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			changes := Classify(tc.path, tc.before, tc.after)
			found := map[string]bool{}
			for _, ch := range changes {
				found[ch.Kind] = true
				if ch.StartLine < 1 || ch.EndLine < ch.StartLine {
					t.Fatalf("invalid line range: %+v", ch)
				}
			}
			for _, kind := range []string{"function-added", "function-removed", "function-renamed", "function-changed"} {
				if !found[kind] {
					t.Fatalf("missing %s: %+v", kind, changes)
				}
			}
		})
	}
}

func TestUnsupportedFileHasNoChanges(t *testing.T) {
	if got := Classify("notes.md", "old", "new"); len(got) != 0 {
		t.Fatalf("unexpected changes: %v", got)
	}
}

func TestTSXArrowFunctionRename(t *testing.T) {
	changes := Classify("view.tsx", "const Old = () => <div>ok</div>;\n", "const New = () => <div>ok</div>;\n")
	if len(changes) != 1 || changes[0].Kind != "function-renamed" {
		t.Fatalf("TSX change: %+v", changes)
	}
}

func TestQualifiedPythonMethod(t *testing.T) {
	changes := Classify("view.py", "class Widget:\n    def render(self):\n        return 1\n", "class Widget:\n    def render(self):\n        return 2\n")
	if len(changes) != 1 || changes[0].Name != "Widget.render" {
		t.Fatalf("method change: %+v", changes)
	}
}
