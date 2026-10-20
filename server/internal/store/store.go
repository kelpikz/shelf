// Package store opens the SQLite database and creates the tables.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id_hash         TEXT PRIMARY KEY,
	csrf            TEXT NOT NULL,
	github_id       INTEGER NOT NULL,
	login           TEXT NOT NULL,
	avatar_url      TEXT NOT NULL,
	created_at      INTEGER NOT NULL,
	renewed_at      INTEGER NOT NULL,
	idle_expires_at INTEGER NOT NULL,
	max_expires_at  INTEGER NOT NULL
);
`

// Open opens (or creates) shelf.db in dataDir.
func Open(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dataDir, "shelf.db") +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create tables: %w", err)
	}
	return db, nil
}
