package server_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

func TestBackupDownloadIsCompleteAndRecorded(t *testing.T) {
	h, st := newTestServer(t)
	createContact(t, h, st)
	book(t, h, "2026-03-15", "outgoing", "Papier", "10,00")

	if body := get(t, h, "/").Body.String(); !strings.Contains(body, "noch nie") {
		t.Error("the overview does not warn that no backup exists")
	}

	rec := get(t, h, "/backup")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/gzip" ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), ".backup.gz") {
		t.Fatalf("download = %d %v", rec.Code, rec.Header())
	}
	sum, err := store.VerifyBackup(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("the downloaded backup does not verify: %v", err)
	}
	if sum.Rows["contacts"] != 1 || sum.Rows["entries"] != 1 {
		t.Errorf("rows = %v", sum.Rows)
	}

	if last, _ := st.LastBackup(t.Context()); last.IsZero() {
		t.Error("the download was not recorded as a backup")
	}
	body := get(t, h, "/").Body.String()
	if strings.Contains(body, "noch nie") || !strings.Contains(body, "zuletzt heute") {
		t.Error("the overview still warns after a backup")
	}
	if !strings.Contains(get(t, h, "/settings").Body.String(), `href="/backup"`) {
		t.Error("the settings page offers no backup download")
	}
}

func TestBackupNeedsALogin(t *testing.T) {
	h := newProtectedServer(t, "geheim")
	if rec := get(t, h, "/backup"); rec.Code != http.StatusSeeOther {
		t.Errorf("GET /backup without session = %d, want a redirect to the login", rec.Code)
	}
}
