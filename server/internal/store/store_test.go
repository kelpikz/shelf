package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// oldDB makes a database from before migrations: the first tables, one Site,
// and user_version 0.
func oldDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "shelf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sites (path, kind, file, folder, visibility, archived, created_at, updated_at)
		VALUES ('notes', 'folder', '', 'notes-1', 'public', 0, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	return dir
}

func count(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigrationBacksUpFirst(t *testing.T) {
	dir := oldDB(t)
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v := count(t, db, `PRAGMA user_version`); v != len(migrations) {
		t.Errorf("user_version %d, want %d", v, len(migrations))
	}
	count(t, db, `SELECT COUNT(*) FROM cli_keys`)

	backups, _ := filepath.Glob(filepath.Join(dir, "backups", "*.db"))
	if len(backups) != 1 {
		t.Fatalf("backups: %v", backups)
	}
	b, err := sql.Open("sqlite", "file:"+backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if n := count(t, b, `SELECT COUNT(*) FROM sites`); n != 1 {
		t.Errorf("backup has %d Sites, want 1", n)
	}
	if n := count(t, b, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'cli_keys'`); n != 0 {
		t.Error("backup was made after the migration")
	}
}

func TestMigrationStopsWhenBackupFails(t *testing.T) {
	dir := oldDB(t)
	// A file where the backups folder must go makes the backup fail.
	if err := os.WriteFile(filepath.Join(dir, "backups"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if db, err := Open(dir); err == nil {
		db.Close()
		t.Fatal("Open migrated without a backup")
	}

	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "shelf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if n := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'cli_keys'`); n != 0 {
		t.Error("tables changed after the backup failed")
	}
}
