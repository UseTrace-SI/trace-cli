package codebuff

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSDKWrapper(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, relative)
	original, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(root, "fake-trace")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s ' \"$5\" >> \"$TRACE_TEST_EVENTS\"\ncat >> \"$TRACE_TEST_EVENTS\"\nprintf '\\n' >> \"$TRACE_TEST_EVENTS\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(fake)
	lines := strings.Split(string(original), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "const TRACE = ") {
			lines[i] = "const TRACE = " + string(encoded) + ";"
		}
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	script := `import { pathToFileURL } from 'node:url';
import { readFileSync } from 'node:fs';
const {traceCodebuff} = await import(pathToFileURL(process.argv[1]).href);
let finish;
const client = {run(opts) { if (opts.prompt === 'first') return new Promise(r => { finish = r; }); return Promise.resolve('second result'); }};
const traced = traceCodebuff(client);
const first = traced.run({prompt:'first'});
if (await traced.run({prompt:'second'}) !== 'second result') throw new Error('concurrent result changed');
finish('first result');
if (await first !== 'first result') throw new Error('result changed');
traced.finish();
await traced.flush();
const lines = readFileSync(process.env.TRACE_TEST_EVENTS, 'utf8').trim().split('\n');
if (lines.length !== 4 || !lines[0].startsWith('turn_start ') || !lines[1].startsWith('tool_edit ') || !lines[2].startsWith('turn_end ') || !lines[3].startsWith('session_end ')) throw new Error(lines.join('\n'));
if (lines.some(line => line.includes('second'))) throw new Error('concurrent run captured');
`
	cmd := exec.Command(node, "--input-type=module", "-e", script, p)
	cmd.Env = append(os.Environ(), "TRACE_TEST_EVENTS="+filepath.Join(root, "events"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("SDK wrapper: %v: %s", err, out)
	}
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
}

func TestPreservesModifiedBridge(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, relative)
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("user content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err == nil {
		t.Fatal("overwrote modified bridge")
	}
	if err := Uninstall(root); err == nil {
		t.Fatal("removed modified bridge")
	}
}
