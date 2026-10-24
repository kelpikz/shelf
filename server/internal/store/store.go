// Package store opens the SQLite database and migrates it.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// migrations run in order. PRAGMA user_version holds how many have run.
// Never edit one that has shipped; add a new one.
var migrations = []string{
	// 1: sessions and Sites. IF NOT EXISTS: databases from before migrations
	// have these tables at user_version 0.
	`
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
`,
	// 2: CLI logins and keys. Dev databases from the unmerged CLI branches
	// may have these tables without AUTOINCREMENT, so they are made again.
	// The rows are only open logins and CLI keys.
	`
DROP TABLE IF EXISTS cli_logins;
DROP TABLE IF EXISTS cli_keys;

CREATE TABLE cli_logins (
	device_hash TEXT PRIMARY KEY,          -- hash of the secret device code
	user_code   TEXT NOT NULL UNIQUE,      -- the short code, without the dash
	device_name TEXT NOT NULL,
	ip          TEXT NOT NULL,
	status      TEXT NOT NULL,             -- "pending", "approved", or "denied"
	created_at  INTEGER NOT NULL,
	expires_at  INTEGER NOT NULL,
	polled_at   INTEGER NOT NULL           -- 0 before the first poll
);

CREATE TABLE cli_keys (
	id           INTEGER PRIMARY KEY AUTOINCREMENT, -- never reused: a stale Cancel form must not hit a newer key
	key_hash     TEXT NOT NULL UNIQUE,
	device_name  TEXT NOT NULL,
	created_at   INTEGER NOT NULL,
	expires_at   INTEGER NOT NULL,
	last_used_at INTEGER NOT NULL
);
`,
}

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
	if err := migrate(db, dataDir); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// migrate runs the migrations that have not run. If the database has tables,
// it first copies it to DATA_DIR/backups; if the copy fails, nothing changes.
func migrate(db *sql.DB, dataDir string) error {
	var version, tables int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= len(migrations) {
		return nil
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil {
		return err
	}
	if tables > 0 {
		dir := filepath.Join(dataDir, "backups")
		backup := filepath.Join(dir, fmt.Sprintf("shelf-v%d-%d.db", version, time.Now().Unix()))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("backup before migration failed, nothing changed: %w", err)
		}
		if _, err := db.Exec(`VACUUM INTO ?`, backup); err != nil {
			return fmt.Errorf("backup before migration failed, nothing changed: %w", err)
		}
	}

	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	return nil
}
