// Package backup writes backup files to disk and keeps a directory of them
// current (ADR 0019). The format and the restore live in the store package.
// This package adds the parts that touch the file system: atomic writes,
// verification from disk, retention, and the daily schedule.
package backup

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/store"
)

const (
	filePrefix = "bookkeeper-"
	fileSuffix = ".backup.gz"
)

// FileName is the name a backup taken at t gets. It sorts by time, which
// retention relies on.
func FileName(t time.Time) string {
	return filePrefix + t.UTC().Format("2006-01-02-150405") + fileSuffix
}

// ToFile writes a backup to path. The data goes to a temporary file next to
// it, which is read back and verified before it is renamed into place, so path
// either holds a complete, checked backup or does not exist. Only then is the
// backup recorded as the last one.
func ToFile(ctx context.Context, st *store.Store, path, appVersion string) (store.BackupSummary, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".bookkeeper-backup-*.tmp")
	if err != nil {
		return store.BackupSummary{}, err
	}
	defer os.Remove(tmp.Name()) // a no-op after the rename

	sum, err := st.Backup(ctx, tmp, appVersion)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return sum, err
	}
	if err := VerifyFile(tmp.Name()); err != nil {
		return sum, fmt.Errorf("the written backup does not verify: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil { // it holds the whole books
		return sum, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return sum, err
	}
	return sum, st.MarkBackup(ctx, sum.CreatedAt)
}

// VerifyFile checks a backup file on disk.
func VerifyFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = store.VerifyBackup(f)
	return err
}

// Prune deletes all but the newest keep backups in dir. It only touches files
// named like backups, so a directory shared with other files is safe.
func Prune(dir string, keep int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), filePrefix) && strings.HasSuffix(e.Name(), fileSuffix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) <= keep {
		return nil, nil
	}
	var removed []string
	for _, n := range names[:len(names)-keep] {
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			return removed, err
		}
		removed = append(removed, n)
	}
	return removed, nil
}

// Newest returns the time of the newest backup file in dir, or the zero time
// when there is none (or no dir yet).
func Newest(dir string) (time.Time, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var newest time.Time
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, filePrefix) || !strings.HasSuffix(n, fileSuffix) {
			continue
		}
		t, err := time.Parse("2006-01-02-150405", strings.TrimSuffix(strings.TrimPrefix(n, filePrefix), fileSuffix))
		if err == nil && t.After(newest) {
			newest = t
		}
	}
	return newest, nil
}

// Scheduler takes a backup into Dir whenever the newest backup file there is
// older than Every, and keeps the newest Keep files. It looks at its own
// directory, not at "last backup by any means": a download to a laptop must
// not stand in for the copy the operator expects in Dir, and an emptied Dir
// fills up again on the next check.
type Scheduler struct {
	Store      *store.Store
	Dir        string
	Every      time.Duration
	Keep       int
	AppVersion string
	Log        *log.Logger
	// OnError, when set, hears about a failed backup, e.g. to mail it
	// (ADR 0039).
	OnError func(error)

	now func() time.Time
}

// Run checks once at start and then hourly, until ctx ends. Checking often and
// acting only when a backup is due means a restart never skips a day and
// never takes two.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if _, err := s.RunOnce(ctx); err != nil {
			s.logf("backup: %v", err)
			if s.OnError != nil {
				s.OnError(err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce takes a backup if one is due and reports whether it did.
func (s *Scheduler) RunOnce(ctx context.Context) (bool, error) {
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	last, err := Newest(s.Dir)
	if err != nil {
		return false, err
	}
	if !last.IsZero() && now().Sub(last) < s.Every {
		return false, nil
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return false, err
	}
	path := filepath.Join(s.Dir, FileName(now()))
	sum, err := ToFile(ctx, s.Store, path, s.AppVersion)
	if err != nil {
		return false, err
	}
	s.logf("backup: wrote %s (%d rows)", path, sum.TotalRows())
	if removed, err := Prune(s.Dir, s.Keep); err != nil {
		return true, fmt.Errorf("prune old backups: %w", err)
	} else if len(removed) > 0 {
		s.logf("backup: removed %d old backup(s)", len(removed))
	}
	return true, nil
}

func (s *Scheduler) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log.Printf(format, args...)
	}
}
