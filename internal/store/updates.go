package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/UseTrace-SI/trace-cli/internal/id"
)

// RecordUpdate stores an immutable snapshot of a row after a local mutation.
// Sync can then append the correction without changing an earlier upload.
func RecordUpdate(tx *sql.Tx, table, rowID string) error {
	if table != "sessions" && table != "turns" && table != "ops" {
		return errors.New("unsupported sync update table")
	}
	rows, err := tx.Query("SELECT * FROM "+table+" WHERE id=?", rowID)
	if err != nil {
		return fmt.Errorf("reading sync update: %w", err)
	}
	cols, err := rows.Columns()
	if err != nil {
		rows.Close()
		return fmt.Errorf("reading sync update columns: %w", err)
	}
	values := make([]any, len(cols))
	pointers := make([]any, len(cols))
	for i := range values {
		pointers[i] = &values[i]
	}
	if !rows.Next() {
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fmt.Errorf("reading sync update row: %w", err)
		}
		return sql.ErrNoRows
	}
	if err := rows.Scan(pointers...); err != nil {
		rows.Close()
		return fmt.Errorf("scanning sync update row: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("closing sync update row: %w", err)
	}
	snapshot := make(map[string]any, len(cols))
	for i, col := range cols {
		if bytes, ok := values[i].([]byte); ok {
			snapshot[col] = string(bytes)
		} else {
			snapshot[col] = values[i]
		}
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encoding sync update: %w", err)
	}
	updateID, err := id.New()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO sync_updates(id,table_name,row_id,data) VALUES(?,?,?,?)", updateID, table, rowID, string(data)); err != nil {
		return fmt.Errorf("saving sync update: %w", err)
	}
	return nil
}
