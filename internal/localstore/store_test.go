package localstore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreferencesPersistAndImportWithoutReplacingSQLiteValues(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(t.TempDir(), "local # data"))
	values := map[string]string{
		"steam-releaser:last-config":                               "release.yaml",
		"steam-releaser:notifications":                             "false",
		"steam-releaser:project-root:release.yaml":                 "/games/demo",
		`steam-releaser:selections:["release.yaml","/games/demo"]`: `{"profile":"demo","profiles":{}}`,
	}
	if err := SavePreferences(values); err != nil {
		t.Fatal(err)
	}
	got, err := Preferences(map[string]string{
		"steam-releaser:notifications":  "true",
		"steam-releaser:recent-configs": `[{"path":"release.yaml","project":"Demo"}]`,
		"steam_password":                "must not be imported",
	})
	values["steam-releaser:recent-configs"] = `[{"path":"release.yaml","project":"Demo"}]`
	if err != nil || !reflect.DeepEqual(got, values) {
		t.Fatalf("preferences = %#v, %v", got, err)
	}
	got, err = Preferences(nil)
	if err != nil || !reflect.DeepEqual(got, values) {
		t.Fatalf("preferences did not survive reopen: %#v, %v", got, err)
	}
	if err := SavePreferences(map[string]string{"steam-releaser:last-config": "changed", "steam_password": "secret"}); err == nil {
		t.Fatal("unsupported preference was accepted")
	}
	got, err = Preferences(nil)
	if err != nil || !reflect.DeepEqual(got, values) {
		t.Fatalf("rejected save changed preferences: %#v, %v", got, err)
	}
}

func TestMigrationRollsBackAndRunsOnce(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	calls := 0
	importData := func(tx *sql.Tx) error {
		calls++
		if _, err := tx.Exec("INSERT INTO settings(key, value) VALUES ('test', 'imported')"); err != nil {
			return err
		}
		if calls == 1 {
			return os.ErrInvalid
		}
		return nil
	}
	if err := Migrate(db, "test", importData); err == nil {
		t.Fatal("failed import was committed")
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM settings").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration did not roll back: %d, %v", count, err)
	}
	for range 2 {
		if err := Migrate(db, "test", importData); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("migration ran %d times", calls)
	}
}

func TestCompletedMigrationDoesNotTakeAWriteLock(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	writer, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := Migrate(writer, "test", func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	reader, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Exec("PRAGMA busy_timeout = 0"); err != nil {
		t.Fatal(err)
	}
	tx, err := writer.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	called := false
	err = Migrate(reader, "test", func(*sql.Tx) error {
		called = true
		return nil
	})
	if err != nil || called {
		t.Fatalf("completed migration needed a write lock or ran again: called=%t, %v", called, err)
	}
}

func TestConcurrentMigrationImportsOnlyOnce(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	const connections = 8
	var databases []*sql.DB
	for range connections {
		db, err := Open()
		if err != nil {
			t.Fatal(err)
		}
		databases = append(databases, db)
		t.Cleanup(func() { db.Close() })
	}
	var calls atomic.Int32
	start := make(chan struct{})
	done := make(chan error, connections)
	for _, db := range databases {
		go func() {
			<-start
			done <- Migrate(db, "once", func(tx *sql.Tx) error {
				calls.Add(1)
				_, err := tx.Exec("INSERT INTO settings(key, value) VALUES ('migrated', 'once')")
				return err
			})
		}()
	}
	close(start)
	for range connections {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("migration import ran %d times", got)
	}
	var value string
	if err := databases[0].QueryRow("SELECT value FROM settings WHERE key = 'migrated'").Scan(&value); err != nil || value != "once" {
		t.Fatalf("migration value = %q, %v", value, err)
	}
}

func TestWriteWaitsForContendedDatabase(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	first, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	held, err := first.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	started := make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go func() {
		close(started)
		tx, err := second.BeginTx(ctx, nil)
		if err == nil {
			defer tx.Rollback()
			_, err = tx.ExecContext(ctx, "INSERT INTO settings(key, value) VALUES ('waited', 'saved')")
			if err == nil {
				err = tx.Commit()
			}
		}
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("write returned while another transaction held the lock: %v", err)
	case <-time.After(6 * time.Second):
	}
	if err := held.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write failed after lock release: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("write did not resume after lock release: %v", ctx.Err())
	}
	var value string
	if err := first.QueryRow("SELECT value FROM settings WHERE key = 'waited'").Scan(&value); err != nil || value != "saved" {
		t.Fatalf("resumed write was lost: %q, %v", value, err)
	}
}

func TestOpenRejectsNewerDatabaseSchema(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), "PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, err := Open(); err == nil {
		db.Close()
		t.Fatal("newer schema was accepted")
	}
}

func TestOpenMigratesHistoryVisibilityWithoutLosingExistingData(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", directory)
	db, err := sql.Open("sqlite", filepath.Join(directory, Filename))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE migrations (name TEXT PRIMARY KEY);
		CREATE TABLE release_history (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
			config_path TEXT NOT NULL, project_root TEXT NOT NULL, payload TEXT NOT NULL
		);
		INSERT INTO settings VALUES ('steam-releaser:last-config', 'release.yaml');
		INSERT INTO release_history(id, config_path, project_root, payload)
			VALUES ('old', 'release.yaml', 'game', '{"logs":"existing logs"}');
		PRAGMA user_version = 1;
	`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	for range 2 {
		db, err := Open()
		if err != nil {
			t.Fatal(err)
		}
		var payload, preference string
		var hidden int
		if err := db.QueryRow("SELECT payload, hidden FROM release_history WHERE id = 'old'").Scan(&payload, &hidden); err != nil {
			db.Close()
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT value FROM settings WHERE key = 'steam-releaser:last-config'").Scan(&preference); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
		if payload != `{"logs":"existing logs"}` || hidden != 0 || preference != "release.yaml" {
			t.Fatalf("migration changed existing data: payload=%q hidden=%d preference=%q", payload, hidden, preference)
		}
	}
}
