package engine

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

// DefaultHandoffBytes bounds a handoff context so it fits in one command-line
// argument for every launcher with room to spare.
const DefaultHandoffBytes = 12 << 10

// MinHandoffBytes is the smallest accepted handoff budget.
const MinHandoffBytes = 1 << 10

// MaxHandoffBytes is the largest accepted handoff budget. The handoff is one
// command-line argument, and Linux limits one argument to 128 KiB
// (MAX_ARG_STRLEN).
const MaxHandoffBytes = 100 << 10

const (
	handoffMaxTurns      = 20
	handoffPromptRunes   = 500
	handoffMaxFiles      = 60
	handoffMaxTodos      = 20
	handoffTodoRunes     = 300
	handoffStatBlobBytes = 256 << 10
)

// Handoff is a redacted, bounded summary of a session up to a step, written
// so another agent can continue the work.
type Handoff struct {
	Step      int64    `json:"step"`
	SessionID string   `json:"session_id,omitempty"`
	Agent     string   `json:"source_agent,omitempty"`
	GitHead   string   `json:"git_head,omitempty"`
	Turns     int      `json:"turns"`
	Files     []string `json:"files"`
	Todos     []string `json:"todos"`
	Text      string   `json:"-"`
	Truncated bool     `json:"truncated"`
}

// ResumeTargetKind says how ResolveResumeTarget reads its argument.
type ResumeTargetKind int

const (
	// ResumeAuto reads a number as a step and anything else as a session,
	// and refuses a number that also names a session.
	ResumeAuto ResumeTargetKind = iota
	// ResumeStep reads the argument as a step number.
	ResumeStep
	// ResumeSession reads the argument as a session.
	ResumeSession
)

// ResolveResumeTarget turns a step number or a session (Trace session ID,
// unique prefix of one, or the agent's own session ID) into a step. For a
// session the step is its latest keyframe.
func (e *Engine) ResolveResumeTarget(arg string, kind ResumeTargetKind) (int64, error) {
	if arg == "" {
		return 0, fmt.Errorf("empty step or session")
	}
	step, numErr := strconv.ParseInt(arg, 10, 64)
	if kind == ResumeStep {
		if numErr != nil {
			return 0, fmt.Errorf("invalid step %q", arg)
		}
		return step, nil
	}
	if kind == ResumeAuto && numErr == nil {
		named, err := e.numberNamesSession(arg)
		if err != nil {
			return 0, err
		}
		if named {
			return 0, fmt.Errorf("ambiguous: %q is step %d or a session ID (or prefix of one); use --step or --session", arg, step)
		}
		return step, nil
	}
	session, err := e.findSession(arg)
	if err != nil {
		return 0, err
	}
	var latest sql.NullInt64
	if err := e.DB.QueryRow(`SELECT MAX(k.seq) FROM keyframes k JOIN turns t ON t.id=k.turn_id WHERE t.session_id=?`, session).Scan(&latest); err != nil {
		return 0, fmt.Errorf("finding session step: %w", err)
	}
	if !latest.Valid {
		return 0, fmt.Errorf("session %s has no recorded steps", session)
	}
	return latest.Int64, nil
}

var errNoSession = errors.New("no session matches")

// minNumericSessionPrefix is the shortest number also tried as a session ID
// prefix without --session.
const minNumericSessionPrefix = 4

// numberNamesSession reports whether a numeric argument is also a Trace or
// agent session ID, or a prefix of a Trace session ID at least
// minNumericSessionPrefix long. Every ULID starts with "0", so shorter
// numbers are never read as prefixes.
func (e *Engine) numberNamesSession(arg string) (bool, error) {
	ids, err := e.sessionIDs(`SELECT id FROM sessions WHERE id=? OR agent_sid=? LIMIT 1`, arg, arg)
	if err != nil || len(ids) > 0 || len(arg) < minNumericSessionPrefix {
		return len(ids) > 0, err
	}
	ids, err = e.sessionIDs(`SELECT id FROM sessions WHERE id LIKE ? LIMIT 1`, arg+"%") // digits need no escaping
	return len(ids) > 0, err
}

// findSession matches arg exactly against Trace and agent session IDs,
// then as a prefix of Trace session IDs.
func (e *Engine) findSession(arg string) (string, error) {
	ids, err := e.sessionIDs(`SELECT DISTINCT id FROM sessions WHERE id=? OR agent_sid=? ORDER BY id LIMIT 3`, arg, arg)
	if err != nil {
		return "", err
	}
	if len(ids) > 1 {
		return "", fmt.Errorf("%q names several sessions (%s) as a Trace or agent session ID; pass the full Trace session ID", arg, strings.Join(ids, ", "))
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	escaped := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(arg)
	ids, err = e.sessionIDs(`SELECT id FROM sessions WHERE id LIKE ? ESCAPE '\' ORDER BY id LIMIT 3`, escaped+"%")
	if err != nil {
		return "", err
	}
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("%w %q", errNoSession, arg)
	case 1:
		return ids[0], nil
	}
	return "", fmt.Errorf("session prefix %q matches several sessions (%s, ...); use more characters", arg, strings.Join(ids[:2], ", "))
}

func (e *Engine) sessionIDs(query string, args ...any) ([]string, error) {
	rows, err := e.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("finding session: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type handoffTurn struct {
	step              int64
	prompt, reasoning string
	turnID            string
}

// Handoff builds the handoff context for step. maxBytes caps the text
// (DefaultHandoffBytes when zero). All text is redacted again before it is
// returned, and paths matching the secret excludes are left out.
func (e *Engine) Handoff(step int64, maxBytes int) (Handoff, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultHandoffBytes
	}
	if maxBytes < MinHandoffBytes {
		maxBytes = MinHandoffBytes
	}
	h := Handoff{Step: step, Files: []string{}, Todos: []string{}}
	if err := e.DB.QueryRow(`SELECT COALESCE(k.git_head,''),COALESCE(t.session_id,''),COALESCE(s.agent,'')
		FROM keyframes k LEFT JOIN turns t ON t.id=k.turn_id LEFT JOIN sessions s ON s.id=t.session_id WHERE k.seq=?`, step).
		Scan(&h.GitHead, &h.SessionID, &h.Agent); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return h, fmt.Errorf("step %d not found", step)
		}
		return h, fmt.Errorf("finding step: %w", err)
	}
	turns, total, err := e.handoffTurns(step, h.SessionID)
	if err != nil {
		return h, err
	}
	h.Turns = total
	// Redact every field before it is clipped, so a cut can never leave
	// part of a secret that only the whole value would have matched.
	for i := range turns {
		turns[i].prompt = e.Redactor.Text(turns[i].prompt)
		turns[i].reasoning = e.Redactor.Text(turns[i].reasoning)
	}
	h.Agent = sanitizeHandoff(e.Redactor.Text(h.Agent))
	target, err := e.ManifestAt(step)
	if err != nil {
		return h, err
	}
	base, err := e.handoffBase(step, h.SessionID)
	if err != nil {
		return h, err
	}
	target, base = target.WithoutExcluded(), base.WithoutExcluded()
	added, modified, deleted := manifest.Diff(base, target)
	marks := map[string]string{}
	var paths []string
	for _, group := range []struct {
		mark  string
		paths []string
	}{{"A", added}, {"M", modified}, {"D", deleted}} {
		for _, p := range group.paths {
			if manifest.Excluded(p) {
				continue
			}
			marks[p] = group.mark
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	var stat []string
	for i, p := range paths {
		shown := sanitizeHandoff(e.Redactor.Text(p))
		h.Files = append(h.Files, shown)
		if i < handoffMaxFiles { // line counts only for listed files
			plus, minus := e.lineStat(base[p].Hash, target[p].Hash)
			stat = append(stat, fmt.Sprintf("%s %s (+%d -%d)", marks[p], shown, plus, minus))
		}
	}
	if len(turns) > 0 {
		response := e.Redactor.Text(e.lastResponse(turns[len(turns)-1]))
		for _, todo := range extractTodos(response) {
			h.Todos = append(h.Todos, sanitizeHandoff(e.Redactor.Text(todo)))
		}
	}

	var body strings.Builder
	fmt.Fprintf(&body, "# Trace handoff\n\nYou are continuing work recorded by Trace")
	if h.Agent != "" {
		fmt.Fprintf(&body, " in a %s session", h.Agent)
	}
	fmt.Fprintf(&body, ". The working tree is at Trace step %d", step)
	if h.GitHead != "" {
		fmt.Fprintf(&body, " (git HEAD %s)", shortHead(h.GitHead))
	}
	fmt.Fprintf(&body, ". Review the summary below, then continue the task.\n")
	if h.SessionID != "" {
		fmt.Fprintf(&body, "\nSource session: %s\n", h.SessionID)
	}
	if len(turns) > 0 {
		fmt.Fprintf(&body, "\n## Original goal\n\n%s\n", clip(firstNonEmpty(turns), handoffPromptRunes*2))
		fmt.Fprintf(&body, "\n## Turns (%d)\n\n", total)
		if total > len(turns) {
			fmt.Fprintf(&body, "(%d earlier turns omitted)\n", total-len(turns))
		}
		for _, t := range turns {
			prompt := clip(t.prompt, handoffPromptRunes)
			if prompt == "" {
				prompt = "(no prompt recorded)"
			}
			fmt.Fprintf(&body, "- step %d: %s\n", t.step, prompt)
		}
	}
	fmt.Fprintf(&body, "\n## Files changed (%d)\n\n", len(h.Files))
	if len(stat) == 0 {
		fmt.Fprintln(&body, "(none)")
	}
	for _, line := range stat {
		fmt.Fprintf(&body, "- %s\n", line)
	}
	if len(paths) > len(stat) {
		fmt.Fprintf(&body, "(%d more)\n", len(paths)-len(stat))
	}
	if len(h.Todos) > 0 {
		fmt.Fprintf(&body, "\n## Open items from the last response\n\n")
		for _, todo := range h.Todos {
			fmt.Fprintf(&body, "- %s\n", todo)
		}
	}
	footer := fmt.Sprintf("\n## More history\n\nRun `trace show %d` for this step's turn and diff, `trace log` for all steps, and `trace diff STEP STEP` to compare steps. With the Trace MCP server, use get_turn, get_messages and search_history.\n", step)

	text := sanitizeHandoff(e.Redactor.Text(body.String()))
	footer = sanitizeHandoff(e.Redactor.Text(footer))
	const marker = "\n[handoff truncated to fit the size limit]\n"
	if len(text)+len(footer) > maxBytes {
		h.Truncated = true
		text = truncateKeepingMarkers(text, maxBytes-len(footer)-len(marker)) + marker
	}
	h.Text = text + footer
	return h, nil
}

// handoffTurns returns the latest turns of the session up to step, oldest
// first, and the session's turn count up to step. Without a session it uses
// the latest turns of any session.
func (e *Engine) handoffTurns(step int64, session string) ([]handoffTurn, int, error) {
	where := "k.seq<=?"
	args := []any{step}
	if session != "" {
		where += " AND t.session_id=?"
		args = append(args, session)
	}
	var total int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM keyframes k JOIN turns t ON t.id=k.turn_id WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting turns: %w", err)
	}
	limit := handoffMaxTurns
	if session == "" {
		limit = 5
		if total > limit {
			total = limit
		}
	}
	rows, err := e.DB.Query(`SELECT k.seq,COALESCE(t.prompt,''),COALESCE(t.reasoning,''),t.id FROM keyframes k
		JOIN turns t ON t.id=k.turn_id WHERE `+where+` ORDER BY k.seq DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, 0, fmt.Errorf("reading turns: %w", err)
	}
	defer rows.Close()
	var turns []handoffTurn
	for rows.Next() {
		var t handoffTurn
		if err := rows.Scan(&t.step, &t.prompt, &t.reasoning, &t.turnID); err != nil {
			return nil, 0, err
		}
		turns = append([]handoffTurn{t}, turns...)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if session != "" && total > len(turns) {
		// Keep the session's first prompt as the original goal.
		var first handoffTurn
		err := e.DB.QueryRow(`SELECT k.seq,COALESCE(t.prompt,''),'',t.id FROM keyframes k JOIN turns t ON t.id=k.turn_id
			WHERE t.session_id=? ORDER BY k.seq LIMIT 1`, session).Scan(&first.step, &first.prompt, &first.reasoning, &first.turnID)
		if err != nil {
			return nil, 0, fmt.Errorf("reading first turn: %w", err)
		}
		turns = append([]handoffTurn{first}, turns...)
	}
	return turns, total, nil
}

// handoffBase is the manifest before the session's first step, or before
// step without a session.
func (e *Engine) handoffBase(step int64, session string) (manifest.Manifest, error) {
	first := step
	if session != "" {
		if err := e.DB.QueryRow(`SELECT MIN(k.seq) FROM keyframes k JOIN turns t ON t.id=k.turn_id WHERE t.session_id=?`, session).Scan(&first); err != nil {
			return nil, fmt.Errorf("finding session start: %w", err)
		}
	}
	var prev int64
	err := e.DB.QueryRow("SELECT seq FROM keyframes WHERE seq<? ORDER BY seq DESC LIMIT 1", first).Scan(&prev)
	if errors.Is(err, sql.ErrNoRows) {
		return manifest.Manifest{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding base step: %w", err)
	}
	return e.ManifestAt(prev)
}

// lastResponse is the turn's last assistant message, or its reasoning,
// without the "Assistant response: " label recorded before each response.
func (e *Engine) lastResponse(t handoffTurn) string {
	content := t.reasoning
	var message string
	if err := e.DB.QueryRow(`SELECT COALESCE(content,'') FROM messages WHERE turn_id=? AND role='assistant' ORDER BY at DESC LIMIT 1`, t.turnID).Scan(&message); err == nil && message != "" {
		content = message
	}
	return strings.ReplaceAll(content, "Assistant response: ", "\n")
}

var (
	todoLine    = regexp.MustCompile(`(?i)^\s*(?:[-*+]|\d+[.)])?\s*(?:\[ \]\s*|(?:TODO|FIXME)\b[:\s-]*)(.+)$`)
	todoHeading = regexp.MustCompile(`(?i)^\s*(?:#+\s*|\*\*)?(?:next steps?|todo|to do|remaining( work| items)?|follow[- ]ups?|open (items|questions))\b`)
	bulletLine  = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+(.+)$`)
)

// extractTodos finds unchecked task items, TODO lines and bullets under a
// "Next steps"-style heading.
func extractTodos(text string) []string {
	todos := []string{}
	seen := map[string]bool{}
	add := func(s string) {
		s = clip(strings.TrimSpace(s), handoffTodoRunes)
		if s != "" && !seen[s] && len(todos) < handoffMaxTodos {
			seen[s] = true
			todos = append(todos, s)
		}
	}
	under := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case todoHeading.MatchString(trimmed):
			under = true
			if m := todoLine.FindStringSubmatch(line); m != nil && !strings.HasSuffix(trimmed, ":") {
				add(m[1])
			}
		case todoLine.MatchString(line):
			add(todoLine.FindStringSubmatch(line)[1])
		case under && bulletLine.MatchString(line):
			add(bulletLine.FindStringSubmatch(line)[1])
		case under && trimmed == "":
		default:
			under = false
		}
	}
	return todos
}

// lineStat counts added and removed lines between two blobs. Only the first
// 256 KiB of each blob is read, so counts for larger files cover that part;
// when both line lists are large, every line is counted as changed.
// Unreadable blobs count as empty.
func (e *Engine) lineStat(before, after blob.Hash) (int, int) {
	a, b := e.statLines(before), e.statLines(after)
	if len(a)*len(b) > 4_000_000 {
		return len(b), len(a)
	}
	common := lcsLen(a, b)
	return len(b) - common, len(a) - common
}

func (e *Engine) statLines(h blob.Hash) []string {
	if h == "" {
		return nil
	}
	r, err := e.Blobs.Get(h)
	if err != nil {
		return nil
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, handoffStatBlobBytes))
	if err != nil || len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func lcsLen(a, b []string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				cur[j] = prev[j+1] + 1
			} else {
				cur[j] = max(prev[j], cur[j+1])
			}
		}
		prev, cur = cur, prev
	}
	return prev[0]
}

func firstNonEmpty(turns []handoffTurn) string {
	for _, t := range turns {
		if strings.TrimSpace(t.prompt) != "" {
			return t.prompt
		}
	}
	return "(no prompt recorded)"
}

func clip(s string, runes int) string {
	s = strings.Join(strings.Fields(s), " ")
	if cut := textutil.Truncate(s, runes); cut != s {
		return strings.TrimSpace(cut) + "…"
	}
	return s
}

func shortHead(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

var terminalEscape = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)?|[@-_])`)

// sanitizeHandoff removes terminal escape sequences, NUL and other control
// characters except newline and tab, so the handoff is a valid argument and
// cannot drive the terminal it is printed to.
func sanitizeHandoff(s string) string {
	s = terminalEscape.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}

// truncateKeepingMarkers cuts s to at most n bytes without splitting a
// UTF-8 sequence or a redaction marker.
func truncateKeepingMarkers(s string, n int) string {
	const open = "⟨redacted:"
	cut := textutil.TruncateBytes(s, n)
	if i := strings.LastIndex(cut, open); i >= 0 && !strings.Contains(cut[i:], "⟩") {
		return cut[:i]
	}
	// A cut inside the marker's opening text is still a partial marker.
	for i := len(open) - 1; i > 0; i-- {
		if strings.HasSuffix(cut, open[:i]) && strings.HasPrefix(s[len(cut)-i:], open) {
			return cut[:len(cut)-i]
		}
	}
	return cut
}
