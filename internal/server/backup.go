package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/backup"
	"github.com/chrthal/accountant/internal/notify"
	"github.com/chrthal/accountant/internal/store"
)

// backupStaleAfter is when the overview starts warning about the last backup.
const backupStaleAfter = 7 * 24 * time.Hour

// WithBackupSchedule tells the settings page that daily backups run, and
// where to (ADR 0019). The scheduler itself runs in the binary.
func WithBackupSchedule(dir string, keep int) Option {
	return func(s *Server) {
		s.backupDir, s.backupKeep = dir, keep
	}
}

// backupStatus is what the overview and the settings page show.
type backupStatus struct {
	Last      time.Time
	Stale     bool
	Dir       string
	Keep      int
	NewestDir time.Time
}

func (s *Server) backupStatus(r *http.Request) (backupStatus, error) {
	last, err := s.store.LastBackup(r.Context())
	if err != nil {
		return backupStatus{}, err
	}
	st := backupStatus{Last: last, Dir: s.backupDir, Keep: s.backupKeep,
		Stale: last.IsZero() || time.Since(last) > backupStaleAfter}
	if s.backupDir != "" {
		st.NewestDir, _ = backup.Newest(s.backupDir)
	}
	return st, nil
}

// handleBackupDownload streams a complete backup to the browser. Once the
// response is under way, an error can no longer change the status code. The
// file then lacks its trailer, and `bookkeeper verify` and every restore
// reject it as incomplete rather than restoring part of the books.
func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", backup.FileName(now)))
	w.Header().Set("Cache-Control", "no-store")
	sum, err := s.store.Backup(r.Context(), w, s.version)
	if err != nil {
		log.Printf("backup download: %v", err)
		return
	}
	if err := s.store.MarkBackup(r.Context(), sum.CreatedAt); err != nil {
		log.Printf("record backup: %v", err)
	}
}

// ago renders how long ago t was, the way the overview speaks: "heute",
// "gestern", "vor 5 Tagen". The zero time is "noch nie".
func (s *Server) ago(t time.Time) string {
	if t.IsZero() {
		return s.T("noch nie")
	}
	days := int(time.Since(t).Hours() / 24)
	switch {
	case days <= 0:
		return s.Tf("heute, %s", t.Local().Format("15:04"))
	case days == 1:
		return s.T("gestern")
	default:
		return s.Tf("vor %d Tagen", days)
	}
}

// maxRestoreBytes bounds the upload. Backups hold every Beleg, so they can be
// large, but not without limit.
const maxRestoreBytes = 4 << 30

// handleRestore takes a backup upload and loads it into this installation,
// which must be fresh (ADR 0030). The file goes to a temporary file first:
// it is read twice, once to check it and once to load it.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if fresh, err := s.store.Fresh(ctx); err != nil {
		s.fail(w, "check fresh", err)
		return
	} else if !fresh {
		s.settingsError(w, r, s.T("Wiederherstellen geht nur in eine neue, leere Installation. Diese enthält bereits Daten."))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRestoreBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	tmp, err := os.CreateTemp("", "bookkeeper-restore-*.backup.gz")
	if err != nil {
		s.fail(w, "temp file", err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	got := false
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.settingsError(w, r, s.T("Die Datei konnte nicht vollständig hochgeladen werden."))
			return
		}
		if part.FormName() == "backup" {
			if _, err := io.Copy(tmp, part); err != nil {
				s.settingsError(w, r, s.T("Die Datei konnte nicht vollständig hochgeladen werden."))
				return
			}
			got = true
		}
		part.Close()
	}
	if !got {
		s.settingsError(w, r, s.T("Bitte eine Sicherungsdatei auswählen."))
		return
	}

	sum, err := s.store.RestoreRunning(ctx, func() (io.ReadCloser, error) { return os.Open(tmp.Name()) })
	switch {
	case errors.Is(err, store.ErrNotFresh):
		s.settingsError(w, r, s.T("Wiederherstellen geht nur in eine neue, leere Installation. Diese enthält bereits Daten."))
		return
	case errors.Is(err, store.ErrNeedsCLI):
		s.settingsError(w, r, s.T("Diese Sicherung stammt von einer sehr alten Version und muss über die Kommandozeile wiederhergestellt werden (siehe unten)."))
		return
	case err != nil:
		log.Printf("restore in the browser: %v", err)
		s.settingsError(w, r, s.Tf("Die Sicherung wurde nicht übernommen, es hat sich nichts geändert: %s", s.errText(err)))
		return
	}
	log.Printf("restored a backup of %s in the browser: %d rows", sum.CreatedAt.Format(time.RFC3339), sum.TotalRows())
	s.loadPrefs()
	http.Redirect(w, r, "/settings?restored="+strconv.Itoa(sum.TotalRows())+"#backup", http.StatusSeeOther)
}

// handleNotifyTest mails a test message to the notification address, so a
// wrong server setting shows now and not with the first reminder.
func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	// What is typed in the form, saved or not, so it can be tried out before
	// it is kept.
	to := settings.NotifyEmail
	if v := strings.TrimSpace(r.FormValue("notify_email")); v != "" {
		to = v
	}
	if to == "" {
		s.settingsError(w, r, s.T("Bitte zuerst die Adresse für Benachrichtigungen eintragen."))
		return
	}
	mailer := s.envMailer
	if mailer == nil {
		m, typed, err := s.mailServerFromForm(r)
		if err != nil {
			s.settingsError(w, r, s.errText(err))
			return
		}
		if !typed {
			stored, ok, err := s.store.GetMailServer(ctx)
			if err != nil {
				s.fail(w, "mail server", err)
				return
			}
			if !ok {
				s.settingsError(w, r, s.T("Es ist kein Mailserver eingerichtet."))
				return
			}
			m = stored
		}
		account := notify.FromStore(m)
		if s.sendWith != nil {
			mailer = s.sendWith(account)
		} else {
			mailer = account
		}
	}
	if err := mailer.Send(ctx, to, s.T("kmu-buchhaltung: Test-Mail"),
		s.T("Diese Test-Mail zeigt, dass Benachrichtigungen ankommen.")+"\n"); err != nil {
		log.Printf("notify test mail: %v", err)
		s.settingsError(w, r, s.Tf("Die Test-Mail konnte nicht verschickt werden: %s", err.Error()))
		return
	}
	http.Redirect(w, r, "/settings?mailtest=1#notify", http.StatusSeeOther)
}
