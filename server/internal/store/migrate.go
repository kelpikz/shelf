package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Versions 1 and 2 were used before migrations were removed. The first MCP
// implementation also used 1. Keep those databases and their rows intact.
const schemaVersion = 3

const oauthSchema = `
CREATE TABLE oauth_requests (
 request_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL,
 resource TEXT NOT NULL, scope TEXT NOT NULL, state TEXT NOT NULL,
 challenge TEXT NOT NULL, expires_at INTEGER NOT NULL
);
CREATE TABLE oauth_codes (
 code_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL,
 resource TEXT NOT NULL, scope TEXT NOT NULL, challenge TEXT NOT NULL, expires_at INTEGER NOT NULL
);
CREATE TABLE oauth_grants (
 id TEXT PRIMARY KEY, client_id TEXT NOT NULL, resource TEXT NOT NULL,
 scope TEXT NOT NULL, expires_at INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE oauth_tokens (
 token_hash TEXT PRIMARY KEY, grant_id TEXT NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('access','refresh')), expires_at INTEGER NOT NULL,
 used INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX oauth_tokens_grant ON oauth_tokens(grant_id);
`

func migrate(db *sql.DB, backup func() error) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema %d is newer than this server", version)
	}
	if version == schemaVersion {
		return nil
	}
	if err := backup(); err != nil {
		return fmt.Errorf("migration stopped: backup: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("baseline schema: %w", err)
	}
	if err := migrateOAuth(tx); err != nil {
		return fmt.Errorf("OAuth migration: %w", err)
	}
	if _, err := tx.Exec("PRAGMA user_version = 3"); err != nil {
		return err
	}
	return tx.Commit()
}

func migrateOAuth(tx *sql.Tx) error {
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table'
		AND name IN ('oauth_requests','oauth_codes','oauth_grants','oauth_tokens')`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		_, err := tx.Exec(oauthSchema)
		return err
	}
	if count != 4 {
		return fmt.Errorf("incomplete OAuth schema; no tables were replaced")
	}
	// Recognize databases made by the first MCP implementation using metadata
	// only. LIMIT 0 does not read credentials or other stored rows.
	for _, table := range []struct{ name, columns string }{
		{"oauth_requests", "request_hash client_id redirect_uri resource scope state challenge expires_at"},
		{"oauth_codes", "code_hash client_id redirect_uri resource scope challenge expires_at"},
		{"oauth_grants", "id client_id resource scope expires_at revoked"},
		{"oauth_tokens", "token_hash grant_id kind expires_at used"},
	} {
		rows, err := tx.Query("SELECT * FROM " + table.name + " LIMIT 0")
		if err != nil {
			return err
		}
		columns, err := rows.Columns()
		rows.Close()
		if err != nil {
			return err
		}
		if !slices.Equal(columns, strings.Fields(table.columns)) {
			return fmt.Errorf("unrecognized %s schema; no tables were replaced", table.name)
		}
	}
	_, err := tx.Exec("CREATE INDEX IF NOT EXISTS oauth_tokens_grant ON oauth_tokens(grant_id)")
	return err
}

// backupDatabase uses SQLite to copy a consistent snapshot, including the WAL.
// A backup is retained on disk before any migration changes tables or rows.
func backupDatabase(db *sql.DB, dir string) error {
	f, err := os.CreateTemp(dir, "shelf-before-migration-*.db")
	if err != nil {
		return err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		return err
	}
	if _, err := db.Exec("VACUUM INTO ?", path); err != nil {
		os.Remove(path)
		return err
	}
	copyDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return err
	}
	defer copyDB.Close()
	var result string
	if err := copyDB.QueryRow("PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("backup check failed")
	}
	return nil
}
