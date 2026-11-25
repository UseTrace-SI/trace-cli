package cli

import (
	"sync"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// hookedTTL bounds how long an observed hook keeps suppressing transcript
// fallback for a session that never sends SessionEnd.
const hookedTTL = 24 * time.Hour

// sessionKey identifies one agent session.
type sessionKey struct{ agent, session string }

// sessionSources gives observed hooks precedence over matching event kinds
// from a session reader, for agents whose adapter reports
// HooksSupersedeSessions (Codex). Different kinds remain eligible for
// fallback, since an agent may let users trust hooks individually.
type sessionSources struct {
	mu        sync.Mutex
	supersede map[string]bool
	hooked    map[sessionKey]map[string]bool
	hookedAt  map[sessionKey]time.Time
	process   func(event.Event) error
}

func newSessionSources(process func(event.Event) error) *sessionSources {
	s := &sessionSources{supersede: map[string]bool{}, process: process}
	for _, reader := range adapters.SessionReaders() {
		if reader.HooksSupersedeSessions() {
			s.supersede[reader.Name()] = true
		}
	}
	return s
}

func (s *sessionSources) pruneExpired(now time.Time) {
	for session, at := range s.hookedAt {
		if now.Sub(at) > hookedTTL {
			delete(s.hooked, session)
			delete(s.hookedAt, session)
		}
	}
}

func (s *sessionSources) hook(ev event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.process(ev); err != nil {
		return err
	}
	if s.supersede[ev.Agent] && ev.AgentSessionID != "" {
		key := sessionKey{ev.Agent, ev.AgentSessionID}
		now := time.Now()
		s.pruneExpired(now)
		if s.hooked == nil {
			s.hooked = make(map[sessionKey]map[string]bool)
		}
		if s.hookedAt == nil {
			s.hookedAt = make(map[sessionKey]time.Time)
		}
		if ev.Kind == event.SessionEnd {
			// The session is over; its dedup state is no longer useful.
			delete(s.hooked, key)
			delete(s.hookedAt, key)
			return nil
		}
		if s.hooked[key] == nil {
			s.hooked[key] = make(map[string]bool)
		}
		s.hooked[key][sessionSourceKey(ev)] = true
		s.hookedAt[key] = now
	}
	return nil
}

// transcript receives the events of a session reader whose agent's hooks
// supersede it.
func (s *sessionSources) transcript(ev event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.supersede[ev.Agent] && ev.AgentSessionID != "" && ev.Kind != event.TurnContext {
		key := sessionKey{ev.Agent, ev.AgentSessionID}
		if at, ok := s.hookedAt[key]; ok && time.Since(at) <= hookedTTL && s.hooked[key][sessionSourceKey(ev)] {
			return nil
		}
	}
	return s.process(ev)
}

func sessionSourceKey(ev event.Event) string {
	if ev.Kind == event.ToolEdit || ev.Kind == event.FileRead {
		return string(ev.Kind) + ":" + ev.ToolName
	}
	return string(ev.Kind)
}
