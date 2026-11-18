package companions

import (
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/UseTrace-SI/trace-cli/internal/redact"
)

type LearnedOptions struct {
	Model            string
	Endpoint         string
	Scope            string
	MinScore         float64
	IncludeDocuments bool
}
type LearnedInfo struct {
	Model             string `json:"model"`
	ModelDigest       string `json:"model_digest,omitempty"`
	ModelStatus       string `json:"model_status"`
	StaleVectors      int    `json:"stale_vectors"`
	MissingVectors    int    `json:"missing_vectors"`
	IndexedMemories   int    `json:"indexed_memories"`
	ResultTruncated   bool   `json:"result_truncated"`
	DocumentsIncluded bool   `json:"documents_included"`
}

func (options LearnedOptions) origin() string {
	if options.Endpoint == "" {
		return "http://127.0.0.1:11434"
	}
	return strings.TrimSuffix(options.Endpoint, "/")
}
func (options LearnedOptions) Validate(query string) error {
	if options.Model == "" {
		return nil
	}
	if strings.TrimSpace(options.Model) != options.Model || len(options.Model) > 256 || !utf8.ValidString(options.Model) || strings.ContainsFunc(options.Model, unicode.IsControl) || (redact.Redactor{}).Text(options.Model) != options.Model {
		return fmt.Errorf("learned model must be an exact installed name of at most 256 bytes without protected text")
	}
	if strings.TrimSpace(query) == "" || len(query) > 4096 || !utf8.ValidString(query) {
		return fmt.Errorf("learned context requires a UTF-8 task query of 1–4096 bytes")
	}
	if len(options.Scope) > 256 || !utf8.ValidString(options.Scope) {
		return fmt.Errorf("learned scope exceeds 256 UTF-8 bytes")
	}
	if math.IsNaN(options.MinScore) || math.IsInf(options.MinScore, 0) || options.MinScore < -1 || options.MinScore > 1 {
		return fmt.Errorf("learned minimum score must be between -1 and 1")
	}
	u, err := url.Parse(options.origin())
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" {
		return fmt.Errorf("learned endpoint must be a loopback HTTP(S) origin without credentials, path or query")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("learned endpoint must use localhost or a loopback IP")
	}
	return nil
}

func (options LearnedOptions) validReply(brain MemoryContext) bool {
	info := brain.Learned
	if brain.Retrieval != "learned_local_ollama" || info == nil || info.Model != options.Model || info.DocumentsIncluded != options.IncludeDocuments {
		return false
	}
	if info.StaleVectors < 0 || info.MissingVectors < 0 || info.IndexedMemories < 0 || info.StaleVectors > 100000 || info.MissingVectors > 100000 || info.IndexedMemories > 100000 || info.StaleVectors+info.MissingVectors+info.IndexedMemories > 100000 {
		return false
	}
	if brain.Status == "missing_index" || brain.Status == "needs_refresh" {
		return info.ModelStatus == "not_probed" && info.ModelDigest == "" && len(brain.Memories) == 0 && info.StaleVectors+info.MissingVectors+info.IndexedMemories == 0
	}
	if brain.Status != "ready" && brain.Status != "needs_embeddings" {
		return false
	}
	if info.ModelStatus != "queried" || len(info.ModelDigest) != 64 || strings.ToLower(info.ModelDigest) != info.ModelDigest {
		return false
	}
	if _, err := hex.DecodeString(info.ModelDigest); err != nil {
		return false
	}
	if brain.Status == "needs_embeddings" && (info.IndexedMemories != 0 || len(brain.Memories) != 0) {
		return false
	}
	if brain.Status == "ready" && info.IndexedMemories == 0 {
		return false
	}
	if len(brain.Memories) > info.IndexedMemories {
		return false
	}
	ids := map[string]bool{}
	for _, memory := range brain.Memories {
		if memory.ID == "" || ids[memory.ID] {
			return false
		}
		ids[memory.ID] = true
		for _, value := range []string{memory.ID, memory.Scope, memory.Source, memory.SessionID, memory.TurnID} {
			if len(value) > 256 || !utf8.ValidString(value) || (redact.Redactor{}).Text(value) != value {
				return false
			}
		}
		if memory.Score == nil || math.IsNaN(*memory.Score) || math.IsInf(*memory.Score, 0) || *memory.Score < -1 || *memory.Score > 1 || *memory.Score < options.MinScore || len(memory.Content) > 2048 || !utf8.ValidString(memory.Content) {
			return false
		}
		if options.Scope != "" && memory.Scope != options.Scope {
			return false
		}
		if !options.IncludeDocuments && memory.Source == "document" {
			return false
		}
	}
	return true
}
