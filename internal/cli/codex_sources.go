package cli

import (
	"sync"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// hookedTTL bounds how long an observed hook keeps suppressing transcript
// fallback for a session that never sends SessionEnd.
const hookedTTL = 24 * time.Hour

// codexSources gives observed hooks precedence over matching event kinds from
// the legacy transcript poller. Different kinds remain eligible for fallback,
// since Codex allows users to trust hooks individually.
type codexSources struct {
	mu       sync.Mutex
	hooked   map[string]map[string]bool
	hookedAt map[string]time.Time
	process  func(event.Event) error
}

func (s *codexSources) pruneExpired(now time.Time) {
	for session, at := range s.hookedAt {
		if now.Sub(at) > hookedTTL {
			delete(s.hooked, session)
			delete(s.hookedAt, session)
		}
	}
}

func (s *codexSources) hook(ev event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.process(ev); err != nil {
		return err
	}
	if ev.Agent == "codex" && ev.AgentSessionID != "" {
		now := time.Now()
		s.pruneExpired(now)
		if s.hooked == nil {
			s.hooked = make(map[string]map[string]bool)
		}
		if s.hookedAt == nil {
			s.hookedAt = make(map[string]time.Time)
		}
		if ev.Kind == event.SessionEnd {
			// The session is over; its dedup state is no longer useful.
			delete(s.hooked, ev.AgentSessionID)
			delete(s.hookedAt, ev.AgentSessionID)
			return nil
		}
		if s.hooked[ev.AgentSessionID] == nil {
			s.hooked[ev.AgentSessionID] = make(map[string]bool)
		}
		s.hooked[ev.AgentSessionID][codexSourceKey(ev)] = true
		s.hookedAt[ev.AgentSessionID] = now
	}
	return nil
}

func (s *codexSources) transcript(ev event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.Agent == "codex" && ev.AgentSessionID != "" && ev.Kind != event.TurnContext {
		if at, ok := s.hookedAt[ev.AgentSessionID]; ok && time.Since(at) <= hookedTTL && s.hooked[ev.AgentSessionID][codexSourceKey(ev)] {
			return nil
		}
	}
	return s.process(ev)
}

func codexSourceKey(ev event.Event) string {
	if ev.Kind == event.ToolEdit || ev.Kind == event.FileRead {
		return string(ev.Kind) + ":" + ev.ToolName
	}
	return string(ev.Kind)
}
