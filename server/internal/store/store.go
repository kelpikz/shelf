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
	display_name    TEXT NOT NULL,
	avatar_url      TEXT NOT NULL,
	created_at      INTEGER NOT NULL,
	renewed_at      INTEGER NOT NULL,
	idle_expires_at INTEGER NOT NULL,
	max_expires_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sites (
	id          INTEGER PRIMARY KEY,
	path        TEXT NOT NULL UNIQUE,
	kind        TEXT NOT NULL,             -- "file" or "folder"
	file        TEXT NOT NULL,             -- the file name of a one-file Site
	folder      TEXT NOT NULL,             -- the folder in DATA_DIR/sites
	visibility  TEXT NOT NULL,             -- "public" or "private"
	archived    INTEGER NOT NULL,
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL           -- the last upload
);

CREATE TABLE IF NOT EXISTS cli_logins (
	device_hash TEXT PRIMARY KEY,          -- hash of the secret device code
	user_code   TEXT NOT NULL UNIQUE,      -- the short code, without the dash
	device_name TEXT NOT NULL,
	ip          TEXT NOT NULL,
	status      TEXT NOT NULL,             -- "pending", "approved", or "denied"
	created_at  INTEGER NOT NULL,
	expires_at  INTEGER NOT NULL,
	polled_at   INTEGER NOT NULL           -- 0 before the first poll
);

CREATE TABLE IF NOT EXISTS cli_keys (
	id           INTEGER PRIMARY KEY AUTOINCREMENT, -- never reused: a stale Cancel form must not hit a newer key
	key_hash     TEXT NOT NULL UNIQUE,
	device_name  TEXT NOT NULL,
	created_at   INTEGER NOT NULL,
	expires_at   INTEGER NOT NULL,
	last_used_at INTEGER NOT NULL
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
