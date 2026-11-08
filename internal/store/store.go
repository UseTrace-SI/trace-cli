package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct{ *sql.DB }

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}
	// Reserve the writer before a transaction reads, so another process cannot
	// invalidate its WAL snapshot before a later write. Apply connection-local
	// pragmas through the DSN as well, including on replacement connections.
	parameters := url.Values{
		"_txlock": {"immediate"},
		"_pragma": {"busy_timeout(30000)", "foreign_keys(1)", "journal_mode(WAL)"},
	}
	dsn := path + "?" + parameters.Encode()
	if path != "" && path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolving database path: %w", err)
		}
		u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute), RawQuery: parameters.Encode()}
		dsn = u.String()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db}, nil
}
