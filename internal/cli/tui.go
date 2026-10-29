package cli

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/restore"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

type tuiStep struct {
	seq    int64
	agent  string
	prompt string
	tag    string
}

type tuiModel struct {
	root       string
	steps      []tuiStep
	cursor     int
	view       int // 0=list, 1=detail, 2=diff, 3=why input, 4=why result
	detail     string
	diff       string
	whyInput   string
	whyText    string
	returnView int
	err        error
}

func tuiInit(root string) tea.Cmd {
	return func() tea.Msg {
		e, err := engine.Open(root)
		if err != nil {
			return tuiErrMsg{err}
		}
		defer e.Close()
		rows, err := e.DB.Query(`SELECT k.seq,COALESCE(s.agent,''),COALESCE(t.prompt,''),COALESCE(k.tag,'') FROM keyframes k LEFT JOIN turns t ON t.id=k.turn_id LEFT JOIN sessions s ON s.id=t.session_id ORDER BY k.seq`)
		if err != nil {
			return tuiErrMsg{err}
		}
		defer rows.Close()
		var steps []tuiStep
		for rows.Next() {
			var st tuiStep
			if err := rows.Scan(&st.seq, &st.agent, &st.prompt, &st.tag); err != nil {
				return tuiErrMsg{err}
			}
			steps = append(steps, st)
		}
		return tuiLoadedMsg{steps: steps}
	}
}

type tuiLoadedMsg struct{ steps []tuiStep }
type tuiErrMsg struct{ err error }
type tuiDetailMsg struct{ text string }
type tuiDiffMsg struct{ text string }
type tuiWhyMsg struct{ text string }

func (m tuiModel) Init() tea.Cmd { return tuiInit(m.root) }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tuiErrMsg:
		m.err = msg.err
		return m, tea.Quit
	case tuiLoadedMsg:
		m.steps = msg.steps
		return m, nil
	case tuiDetailMsg:
		m.detail = msg.text
		m.view = 1
		return m, nil
	case tuiDiffMsg:
		m.diff = msg.text
		m.view = 2
		return m, nil
	case tuiWhyMsg:
		m.whyText = msg.text
		m.view = 4
		return m, nil
	case tea.KeyMsg:
		if m.view == 3 {
			switch msg.Type {
			case tea.KeyCtrlC:
				return m, tea.Quit
			case tea.KeyEsc:
				m.view = m.returnView
			case tea.KeyBackspace:
				runes := []rune(m.whyInput)
				if len(runes) > 0 {
					m.whyInput = string(runes[:len(runes)-1])
				}
			case tea.KeyEnter:
				if strings.TrimSpace(m.whyInput) != "" {
					return m, tuiWhy(m.root, m.whyInput)
				}
			default:
				m.whyInput += string(msg.Runes)
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.view == 0 && m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.view == 0 && m.cursor < len(m.steps)-1 {
				m.cursor++
			}
		case "enter":
			if m.view == 0 && len(m.steps) > 0 {
				seq := m.steps[m.cursor].seq
				return m, func() tea.Msg {
					e, err := engine.Open(m.root)
					if err != nil {
						return tuiErrMsg{err}
					}
					defer e.Close()
					var prompt, reasoning, agent, tag string
					err = e.DB.QueryRow(`SELECT COALESCE(t.prompt,''),COALESCE(t.reasoning,''),COALESCE(s.agent,''),COALESCE(k.tag,'') FROM keyframes k LEFT JOIN turns t ON t.id=k.turn_id LEFT JOIN sessions s ON s.id=t.session_id WHERE k.seq=?`, seq).Scan(&prompt, &reasoning, &agent, &tag)
					if err != nil && err != sql.ErrNoRows {
						return tuiErrMsg{err}
					}
					text := fmt.Sprintf("Step %d (%s, %s)\nAgent: %s\n\nPrompt:\n%s\n\nReasoning:\n%s\n", seq, tag, agent, agent, prompt, reasoning)
					return tuiDetailMsg{text: text}
				}
			}
		case "d":
			if m.view == 1 && len(m.steps) > 0 {
				seq := m.steps[m.cursor].seq
				return m, func() tea.Msg {
					e, err := engine.Open(m.root)
					if err != nil {
						return tuiErrMsg{err}
					}
					defer e.Close()
					current, err := e.ManifestAt(seq)
					if err != nil {
						return tuiErrMsg{err}
					}
					previous := manifest.Manifest{}
					var prevSeq int64
					if err := e.DB.QueryRow("SELECT seq FROM keyframes WHERE seq<? ORDER BY seq DESC LIMIT 1", seq).Scan(&prevSeq); err == nil {
						previous, _ = e.ManifestAt(prevSeq)
					}
					files := restore.Plan(previous, current)
					var diff strings.Builder
					for _, p := range files {
						oldText, _, _ := blobTextLimited(e.Blobs, previous[p].Hash)
						newText, _, _ := blobTextLimited(e.Blobs, current[p].Hash)
						diff.WriteString(unifiedDiff(p, oldText, newText))
					}
					return tuiDiffMsg{text: diff.String()}
				}
			}
		case "w":
			if m.view == 0 || m.view == 1 {
				m.returnView = m.view
				m.view = 3
				m.whyInput = ""
			}
		case "esc", "backspace":
			if m.view == 4 {
				m.view = m.returnView
				m.whyText = ""
			} else if m.view == 2 {
				m.view = 1
				m.diff = ""
			} else if m.view == 1 {
				m.view = 0
				m.detail = ""
			}
		}
	}
	return m, nil
}

func (m tuiModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("error: %v\n", m.err)
	}
	if m.view == 2 {
		return m.diff + "\n[esc] back  [q] quit\n"
	}
	if m.view == 3 {
		return "Find the conversation for a line\nfile:line> " + m.whyInput + "\n[enter] search  [esc] back\n"
	}
	if m.view == 4 {
		return m.whyText + "\n[esc] back  [q] quit\n"
	}
	if m.view == 1 {
		return m.detail + "\n[d] diff  [w] why line  [esc] back  [q] quit\n"
	}
	if len(m.steps) == 0 {
		return "No steps recorded yet.\n[q] quit\n"
	}
	title := lipgloss.NewStyle().Bold(true).Render("Trace timeline")
	var rows string
	for i, s := range m.steps {
		line := fmt.Sprintf("%d  %-12s  %s", s.seq, s.agent, truncate(s.prompt, 60))
		if i == m.cursor {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		rows += line + "\n"
	}
	return title + "\n" + rows + "\n[↑/↓] navigate  [enter] view  [w] why line  [q] quit\n"
}

func tuiWhy(root, position string) tea.Cmd {
	return func() tea.Msg {
		jsonOutput := false
		cmd := whyCommand(&root, &jsonOutput)
		cmd.SetArgs([]string{"--messages", position})
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := cmd.Execute(); err != nil {
			return tuiWhyMsg{text: fmt.Sprintf("Could not find line: %v", err)}
		}
		return tuiWhyMsg{text: out.String()}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func tuiCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{Use: "tui", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		m := tuiModel{root: root}
		p := tea.NewProgram(m)
		_, err = p.Run()
		return err
	}}
}
