package cli

import (
	"sync"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// codexSources gives observed hooks precedence over matching event kinds from
// the legacy transcript poller. Different kinds remain eligible for fallback,
// since Codex allows users to trust hooks individually.
type codexSources struct {
	mu      sync.Mutex
	hooked  map[string]map[string]bool
	process func(event.Event) error
}

func (s *codexSources) hook(ev event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.process(ev); err != nil {
		return err
	}
	if ev.Agent == "codex" && ev.AgentSessionID != "" {
		if s.hooked == nil {
			s.hooked = make(map[string]map[string]bool)
		}
		if s.hooked[ev.AgentSessionID] == nil {
			s.hooked[ev.AgentSessionID] = make(map[string]bool)
		}
		s.hooked[ev.AgentSessionID][codexSourceKey(ev)] = true
	}
	return nil
}

func (s *codexSources) transcript(ev event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.Agent == "codex" && ev.AgentSessionID != "" && ev.Kind != event.TurnContext && s.hooked[ev.AgentSessionID][codexSourceKey(ev)] {
		return nil
	}
	return s.process(ev)
}

func codexSourceKey(ev event.Event) string {
	if ev.Kind == event.ToolEdit || ev.Kind == event.FileRead {
		return string(ev.Kind) + ":" + ev.ToolName
	}
	return string(ev.Kind)
}
