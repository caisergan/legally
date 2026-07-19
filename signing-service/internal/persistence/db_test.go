package persistence

import (
	"os"
	"path/filepath"
	"testing"
)

func tempJournal(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "signerd.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesSchemaFromEmpty(t *testing.T) {
	db := tempJournal(t)
	for _, table := range []string{
		"credentials", "jobs", "job_events", "commands", "pin_challenges",
		"plan_authorizations", "validation_evidence", "administrative_events", "schema_migrations",
	} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("expected table %q to exist: %v", table, err)
		}
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	db := tempJournal(t)
	if err := Migrate(db.DB); err != nil {
		t.Fatalf("second Migrate rejected: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != len(migrations) {
		t.Fatalf("applied %d migrations, want %d", count, len(migrations))
	}
}

func TestMigrationChecksumDriftFailsClosed(t *testing.T) {
	db := tempJournal(t)
	if _, err := db.Exec(`UPDATE schema_migrations SET checksum='tampered' WHERE version=1`); err != nil {
		t.Fatalf("tamper checksum: %v", err)
	}
	if err := Migrate(db.DB); err == nil {
		t.Fatal("checksum drift was accepted")
	}
}

func TestJournalPermissionsArePrivate(t *testing.T) {
	db := tempJournal(t)
	dirInfo, err := os.Stat(filepath.Dir(db.Path()))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("journal dir perms = %04o, want 0700", dirInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(db.Path())
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if fileInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("journal file perms = %04o, want group/other unreadable", fileInfo.Mode().Perm())
	}
}

func TestAuditTablesAreAppendOnly(t *testing.T) {
	db := tempJournal(t)
	if _, err := db.Exec(`INSERT INTO administrative_events (id, event_type, created_at) VALUES ('a1','ENROLL','2026-07-19T00:00:00Z')`); err != nil {
		t.Fatalf("insert admin event: %v", err)
	}
	if _, err := db.Exec(`UPDATE administrative_events SET detail='x' WHERE id='a1'`); err == nil {
		t.Fatal("administrative_events accepted an update")
	}
	if _, err := db.Exec(`DELETE FROM administrative_events WHERE id='a1'`); err == nil {
		t.Fatal("administrative_events accepted a delete")
	}
}

func TestOpenRejectsRelativePath(t *testing.T) {
	if _, err := Open("relative/signerd.db"); err == nil {
		t.Fatal("relative journal path was accepted")
	}
}
