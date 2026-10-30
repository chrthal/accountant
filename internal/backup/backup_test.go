package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chrthal/accountant/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open("sqlite://" + filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestToFileWritesAVerifiedPrivateFileAndRecordsIt(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	st.CreateContact(ctx, store.Contact{Kind: "customer", Name: "Muster GmbH"})

	path := filepath.Join(t.TempDir(), FileName(time.Now()))
	sum, err := ToFile(ctx, st, path, "test")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Rows["contacts"] != 1 {
		t.Errorf("rows = %v", sum.Rows)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("backup file mode = %v (%v), want 0600 — it holds the whole books", fi.Mode().Perm(), err)
	}
	if err := VerifyFile(path); err != nil {
		t.Error(err)
	}
	if last, _ := st.LastBackup(ctx); last.IsZero() {
		t.Error("the backup was not recorded")
	}
	tmp, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".bookkeeper-backup-*"))
	if len(tmp) != 0 {
		t.Errorf("temporary files left behind: %v", tmp)
	}
}

func TestSchedulerIsDailyAndKeepsTheNewest(t *testing.T) {
	st := openStore(t)
	dir := filepath.Join(t.TempDir(), "backups")
	clock := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	s := &Scheduler{Store: st, Dir: dir, Every: 24 * time.Hour, Keep: 3, AppVersion: "test",
		now: func() time.Time { return clock }}
	ctx := context.Background()

	if did, err := s.RunOnce(ctx); err != nil || !did {
		t.Fatalf("first run: did=%v err=%v — no backup yet, so one is due", did, err)
	}
	clock = clock.Add(time.Hour)
	if did, _ := s.RunOnce(ctx); did {
		t.Error("a second backup an hour later — it should be daily")
	}
	for range 5 {
		clock = clock.Add(25 * time.Hour)
		if did, err := s.RunOnce(ctx); err != nil || !did {
			t.Fatalf("daily run: did=%v err=%v", did, err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "bookkeeper-*.backup.gz"))
	if len(files) != 3 {
		t.Errorf("%d backups kept, want 3", len(files))
	}
	if newest, _ := Newest(dir); !newest.Equal(clock.Truncate(time.Second)) {
		t.Errorf("newest = %v, want %v — pruning removed the wrong end", newest, clock)
	}

	// Foreign files in the directory are never touched.
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600)
	Prune(dir, 1)
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Error("prune removed a file that is not a backup")
	}

	// An emptied directory fills up again, whatever LastBackup says.
	os.RemoveAll(dir)
	clock = clock.Add(time.Minute)
	if did, _ := s.RunOnce(ctx); !did {
		t.Error("an empty backup directory was not refilled")
	}
}
