package companions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func learnedFixture() MemoryContext {
	score := 1.0
	return MemoryContext{Protocol: 1, Status: "ready", Retrieval: "learned_local_ollama", Learned: &LearnedInfo{Model: "test:1", ModelDigest: strings.Repeat("a", 64), ModelStatus: "queried", IndexedMemories: 1}, Memories: []Memory{{ID: "memory", Scope: "project", Content: "a car", Source: "manual", Score: &score}}}
}
func learnedHelper(t *testing.T, reply MemoryContext) string {
	t.Helper()
	bin := t.TempDir()
	data, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(string(data), '\'') {
		t.Fatal("fixture is not shell-literal safe")
	}
	body := "#!/bin/sh\nif [ -n \"$TRACE_TEST_LEARNED_ARGS\" ]; then printf '%s\\n' \"$@\" > \"$TRACE_TEST_LEARNED_ARGS\"; fi\nprintf '%s' '" + string(data) + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "trace-brain"), []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	return bin
}
func TestCompanionLearnedContextForwardsOnlyExplicitPinnedConfiguration(t *testing.T) {
	root := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("PATH", learnedHelper(t, learnedFixture()))
	t.Setenv("TRACE_TEST_LEARNED_ARGS", argsFile)
	options := LearnedOptions{Model: "test:1", Scope: "project", MinScore: 0.5}
	brain, code := LookupWithLearned(context.Background(), root, "", "query $(literal)", 20, options)
	if brain.Status != "ready" || len(brain.Memories) != 1 || brain.Learned == nil || code.Status != "not_installed" {
		t.Fatalf("wrong explicit lookup: %+v %+v", brain, code)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"--learned-model\ntest:1", "--learned-endpoint\nhttp://127.0.0.1:11434", "--learned-scope\nproject", "--learned-min-score\n0.5", "query $(literal)"} {
		if !strings.Contains(string(data), value) {
			t.Fatalf("missing explicit argument %q: %s", value, data)
		}
	}
	if strings.Contains(string(data), "--learned-include-documents") {
		t.Fatal("document consent was fabricated")
	}
	brain, _ = Lookup(context.Background(), root, "", "query", 20)
	if brain.Status != "invalid_response" || len(brain.Memories) != 0 || brain.Learned != nil {
		t.Fatal("ordinary context accepted unrequested learned evidence")
	}
	data, err = os.ReadFile(argsFile)
	if err != nil || strings.Contains(string(data), "--learned-") {
		t.Fatal("ordinary lookup enabled model flags", err)
	}
}
func TestCompanionLearnedContextRejectsFallbackAndMismatchedEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*MemoryContext)
	}{
		{"keyword fallback", func(r *MemoryContext) { r.Retrieval = ""; r.Learned = nil }},
		{"model mismatch", func(r *MemoryContext) { r.Learned.Model = "other:1" }},
		{"bad digest", func(r *MemoryContext) { r.Learned.ModelDigest = "unknown" }},
		{"wrong scope", func(r *MemoryContext) { r.Memories[0].Scope = "other" }},
		{"unconsented document", func(r *MemoryContext) { r.Memories[0].Source = "document" }},
		{"missing score", func(r *MemoryContext) { r.Memories[0].Score = nil }},
		{"oversized content", func(r *MemoryContext) { r.Memories[0].Content = strings.Repeat("x", 2049) }},
		{"invalid counts", func(r *MemoryContext) { r.Learned.IndexedMemories = -1 }},
		{"private provenance", func(r *MemoryContext) { r.Memories[0].ID = "ghp_abcdefghijklmnopqrstuvwxyz" }},
		{"false no-embeddings", func(r *MemoryContext) { r.Status = "needs_embeddings" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			reply := learnedFixture()
			test.mutate(&reply)
			t.Setenv("PATH", learnedHelper(t, reply))
			brain, _ := LookupWithLearned(context.Background(), t.TempDir(), "", "car", 20, LearnedOptions{Model: "test:1", Scope: "project"})
			if brain.Status != "invalid_response" || len(brain.Memories) != 0 || brain.Learned != nil {
				t.Fatalf("unverified learned result accepted: %+v", brain)
			}
		})
	}
}
func TestCompanionLearnedContextAcceptsEmptyStatesWithoutOfflineFallback(t *testing.T) {
	for _, status := range []string{"missing_index", "needs_refresh", "needs_embeddings"} {
		reply := learnedFixture()
		reply.Status = status
		reply.Memories = []Memory{}
		reply.Learned.IndexedMemories = 0
		if status != "needs_embeddings" {
			reply.Learned.ModelStatus = "not_probed"
			reply.Learned.ModelDigest = ""
		} else {
			reply.Learned.MissingVectors = 1
		}
		t.Setenv("PATH", learnedHelper(t, reply))
		brain, _ := LookupWithLearned(context.Background(), t.TempDir(), "", "car", 20, LearnedOptions{Model: "test:1"})
		if brain.Status != status || brain.Learned == nil || len(brain.Memories) != 0 {
			t.Fatalf("empty learned status changed: %+v", brain)
		}
	}
}
func TestCompanionLearnedContextConfigurationIsLoopbackAndExplicit(t *testing.T) {
	for _, options := range []LearnedOptions{{Model: "test:1", Endpoint: "https://example.com"}, {Model: "test:1", Endpoint: "http://user:pass@localhost"}, {Model: "test:1", Endpoint: "http://localhost/path"}, {Model: "test:1", Endpoint: "http://localhost?"}, {Model: "ghp_abcdefghijklmnopqrstuvwxyz"}} {
		if err := options.Validate("car"); err == nil {
			t.Fatal("invalid local model config accepted")
		}
	}
	if err := (LearnedOptions{Model: "test:1"}).Validate(""); err == nil {
		t.Fatal("model lookup accepted an empty query")
	}
	if err := (LearnedOptions{Model: "test:1", Endpoint: "http://[::1]:11434/"}).Validate("car"); err != nil {
		t.Fatal("IPv6 loopback rejected", err)
	}
}
