package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationBacksUpExistingDataAndRunsOnce(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "shelf.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sites(path,kind,file,folder,visibility,archived,created_at,updated_at) VALUES('keep','file','a','b','private',0,1,1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	backups, _ := filepath.Glob(filepath.Join(dir, "shelf-before-migration-*.db"))
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	backup, err := sql.Open("sqlite", backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var path string
	if err := backup.QueryRow("SELECT path FROM sites").Scan(&path); err != nil || path != "keep" {
		t.Fatalf("backup: %q, %v", path, err)
	}
	var tables int
	backup.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='oauth_grants'").Scan(&tables)
	if tables != 0 {
		t.Fatal("backup includes the new schema")
	}
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backups, _ = filepath.Glob(filepath.Join(dir, "shelf-before-migration-*.db"))
	if len(backups) != 1 {
		t.Fatalf("migration repeated: %v", backups)
	}
}

func TestMigrationStopsWhenBackupFails(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := errors.New("backup unavailable")
	err = migrate(db, func() error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	var count int
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='oauth_grants'").Scan(&count)
	if count != 0 {
		t.Fatal("schema changed after backup failure")
	}
}

func TestMigrationAcceptsHistoricalVersionTwoAndPreservesData(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "shelf.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`
INSERT INTO sites(path,kind,file,folder,visibility,archived,created_at,updated_at)
 VALUES('keep','file','index.html','keep-folder','private',0,1,1);
INSERT INTO cli_keys(key_hash,device_name,created_at,expires_at,last_used_at)
 VALUES('test-key-hash','keep-device',1,9999999999,1);
PRAGMA user_version=2;`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, sites, keys, tables int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM sites WHERE path='keep'").Scan(&sites); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM cli_keys WHERE device_name='keep-device'").Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='oauth_grants'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if version != 3 || sites != 1 || keys != 1 || tables != 1 {
		t.Fatalf("version=%d sites=%d keys=%d OAuth tables=%d", version, sites, keys, tables)
	}
	backups, err := filepath.Glob(filepath.Join(dir, "shelf-before-migration-*.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups=%v error=%v", backups, err)
	}
	before, err := sql.Open("sqlite", backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer before.Close()
	if err = before.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("backup version=%d error=%v", version, err)
	}
}

func TestMigrationAcceptsBothVersionOneLayouts(t *testing.T) {
	for _, withOAuth := range []bool{false, true} {
		name := "historical baseline"
		if withOAuth {
			name = "first MCP schema"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			db, err := sql.Open("sqlite", filepath.Join(dir, "shelf.db"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(schema); err != nil {
				t.Fatal(err)
			}
			if withOAuth {
				if _, err = db.Exec(oauthSchema); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`INSERT INTO oauth_grants(id,client_id,resource,scope,expires_at)
				 VALUES('keep-grant','client','resource','shelf:read',9999999999);
				 INSERT INTO oauth_tokens(token_hash,grant_id,kind,expires_at)
				 VALUES('test-token-hash','keep-grant','access',9999999999);`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = db.Exec("PRAGMA user_version=1"); err != nil {
				t.Fatal(err)
			}
			db.Close()
			db, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var version, count int
			if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
				t.Fatalf("version=%d error=%v", version, err)
			}
			if withOAuth {
				if err = db.QueryRow("SELECT count(*) FROM oauth_tokens WHERE token_hash='test-token-hash'").Scan(&count); err != nil || count != 1 {
					t.Fatalf("existing credentials were lost: count=%d error=%v", count, err)
				}
			}
			if err = migrate(db, func() error { t.Fatal("migration ran twice"); return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationRefusesUnknownFutureVersionWithoutChanges(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("PRAGMA user_version=4"); err != nil {
		t.Fatal(err)
	}
	err = migrate(db, func() error { t.Fatal("unexpected backup for an unsupported schema"); return nil })
	if err == nil || !strings.Contains(err.Error(), "newer than this server") {
		t.Fatalf("error=%v", err)
	}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 4 {
		t.Fatalf("version=%d error=%v", version, err)
	}
}

func TestMigrationRefusesPartialOAuthSchemaAndRollsBack(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE oauth_grants(id TEXT); PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	backedUp := false
	err = migrate(db, func() error { backedUp = true; return nil })
	if !backedUp || err == nil || !strings.Contains(err.Error(), "incomplete OAuth schema") {
		t.Fatalf("backedUp=%v error=%v", backedUp, err)
	}
	var version, tables int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if version != 1 || tables != 1 {
		t.Fatalf("partial schema changed: version=%d tables=%d", version, tables)
	}
}
