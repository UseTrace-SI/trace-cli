package historysearch

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode"
)

type Options struct {
	Query      string
	Scope      string
	EditedPath string
	Limit      int
	Before     int64
}

type Hit struct {
	Step   int64  `json:"step"`
	Agent  string `json:"agent"`
	Prompt string `json:"prompt"`
}

// Search finds completed turns by text or edited path. Text terms use AND,
// with | for alternatives within one term.
func Search(db *sql.DB, opt Options) ([]Hit, error) {
	if opt.Limit < 1 {
		return nil, fmt.Errorf("limit must be positive")
	}
	if opt.Scope == "" {
		opt.Scope = "all"
	}
	if opt.Scope != "all" && opt.Scope != "turns" && opt.Scope != "messages" {
		return nil, fmt.Errorf("scope must be all, turns, or messages")
	}
	query, err := terms(opt.Query)
	if err != nil {
		return nil, err
	}
	if query == "" && opt.EditedPath == "" {
		return nil, fmt.Errorf("provide search terms or an edited path")
	}
	statement := `SELECT k.seq,COALESCE(s.agent,''),COALESCE(t.prompt,'')
		FROM keyframes k JOIN turns t ON t.id=k.turn_id JOIN sessions s ON s.id=t.session_id WHERE 1=1`
	var params []any
	if query != "" {
		turnMatch := `t.rowid IN (SELECT rowid FROM turn_fts WHERE turn_fts MATCH ?)`
		messageMatch := `t.id IN (SELECT m.turn_id FROM message_fts
			JOIN messages m ON m.rowid=message_fts.rowid WHERE message_fts MATCH ?)`
		switch opt.Scope {
		case "turns":
			statement += " AND " + turnMatch
			params = append(params, query)
		case "messages":
			statement += " AND " + messageMatch
			params = append(params, query)
		default:
			statement += " AND (" + turnMatch + " OR " + messageMatch + ")"
			params = append(params, query, query)
		}
	}
	if opt.EditedPath != "" {
		statement += ` AND EXISTS (SELECT 1 FROM ops o WHERE o.turn_id=t.id AND o.path GLOB ?)`
		params = append(params, opt.EditedPath)
	}
	if opt.Before > 0 {
		statement += " AND k.seq<?"
		params = append(params, opt.Before)
	}
	statement += " ORDER BY k.seq DESC LIMIT ?"
	params = append(params, opt.Limit)
	rows, err := db.Query(statement, params...)
	if err != nil {
		return nil, fmt.Errorf("searching history: %w", err)
	}
	defer rows.Close()
	hits := make([]Hit, 0)
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Step, &h.Agent, &h.Prompt); err != nil {
			return nil, fmt.Errorf("reading search result: %w", err)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading search results: %w", err)
	}
	return hits, nil
}

func terms(query string) (string, error) {
	var result []string
	for _, word := range strings.Fields(query) {
		var alternatives []string
		for _, value := range strings.Split(word, "|") {
			if strings.IndexFunc(value, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) < 0 {
				continue
			}
			alternatives = append(alternatives, `"`+strings.ReplaceAll(value, `"`, `""`)+`"`)
		}
		if len(alternatives) == 0 {
			return "", fmt.Errorf("search term %q has no letters or digits", word)
		}
		result = append(result, "("+strings.Join(alternatives, " OR ")+")")
	}
	return strings.Join(result, " AND "), nil
}
