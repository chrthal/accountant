package server_test

import (
	"bytes"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

const bankExport = `Datum;Betrag;Empfänger;Zweck
15.03.2026;-1.234,56;Büro Müller GmbH;Rechnung 42
16.03.2026;2.000,00;Muster GmbH;Honorar März
`

func uploadCSV(t *testing.T, h http.Handler, content string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "umsaetze.csv")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	part.Write([]byte(content))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/bank/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The mapping step carries the file in a hidden field, so the import form can
// be submitted without re-uploading (ADR 0009).
func importThroughUI(t *testing.T, h http.Handler, content string) *httptest.ResponseRecorder {
	t.Helper()
	upload := uploadCSV(t, h, content)
	if upload.Code != http.StatusOK {
		t.Fatalf("upload = %d, want 200: %s", upload.Code, upload.Body.String())
	}
	payload := base64.StdEncoding.EncodeToString([]byte(content))
	if !strings.Contains(upload.Body.String(), payload) {
		t.Fatal("mapping page does not carry the uploaded file")
	}

	return post(t, h, "/bank/import", url.Values{
		"payload":          {payload},
		"source_name":      {"umsaetze.csv"},
		"has_header":       {"1"},
		"col_date":         {"0"},
		"col_amount":       {"1"},
		"col_counterparty": {"2"},
		"col_purpose":      {"3"},
	})
}

func TestBankImportThroughTheUI(t *testing.T) {
	h, st := newTestServer(t)

	rec := importThroughUI(t, h, bankExport)
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "2 Umsätze importiert") {
		t.Error("import result not reported to the user")
	}

	list, err := st.ListBankTransactions(t.Context(), false, 100)
	if err != nil || len(list) != 2 {
		t.Fatalf("transactions = %d (err %v), want 2", len(list), err)
	}

	// Re-importing the same file reports duplicates instead of doubling.
	again := importThroughUI(t, h, bankExport)
	if !strings.Contains(again.Body.String(), "0 Umsätze importiert") ||
		!strings.Contains(again.Body.String(), "2 bereits vorhanden") {
		t.Errorf("second import not reported as duplicates: %s", again.Body.String())
	}
	list, _ = st.ListBankTransactions(t.Context(), false, 100)
	if len(list) != 2 {
		t.Errorf("got %d transactions after re-import, want 2", len(list))
	}
}

func TestBankMatchAndUnmatchThroughTheUI(t *testing.T) {
	h, st := newTestServer(t)
	importThroughUI(t, h, bankExport)

	// A booking that corresponds to the outgoing transaction.
	if rec := post(t, h, "/entries", url.Values{
		"booked_on": {"2026-03-15"}, "direction": {"outgoing"},
		"description": {"Büromaterial"}, "amount": {"1.234,56"}, "vat_rate": {"20"},
		"konto": {"7600"}, "gegenkonto": {"2800"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("create entry = %d", rec.Code)
	}

	entries, _ := st.ListEntries(t.Context(), 10)
	txs, _ := st.ListBankTransactions(t.Context(), true, 10)
	var txID int64
	for _, b := range txs {
		if b.AmountCents == -123456 {
			txID = b.ID
		}
	}

	path := "/bank/" + strconv.FormatInt(txID, 10)
	rec := post(t, h, path+"/match", url.Values{"entry_id": {strconv.FormatInt(entries[0].ID, 10)}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("match = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	body := get(t, h, "/bank?all=1").Body.String()
	if !strings.Contains(body, "zugeordnet") {
		t.Error("matched transaction is not shown as matched")
	}

	if rec := post(t, h, path+"/unmatch", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("unmatch = %d, want 303", rec.Code)
	}
	tx, _ := st.GetBankTransaction(t.Context(), txID)
	if tx.IsMatched() {
		t.Error("transaction still matched after unmatching")
	}
}

// A mismatch is a user-facing message, not a 500 and not a silent link.
func TestBankMatchRefusesMismatch(t *testing.T) {
	h, st := newTestServer(t)
	importThroughUI(t, h, bankExport)

	post(t, h, "/entries", url.Values{
		"booked_on": {"2026-03-15"}, "direction": {"outgoing"},
		"description": {"Anderer Betrag"}, "amount": {"10,00"}, "vat_rate": {"20"},
		"konto": {"7600"}, "gegenkonto": {"2800"},
	})
	entries, _ := st.ListEntries(t.Context(), 10)
	txs, _ := st.ListBankTransactions(t.Context(), true, 10)
	var txID int64
	for _, b := range txs {
		if b.AmountCents == -123456 {
			txID = b.ID
		}
	}

	rec := post(t, h, "/bank/"+strconv.FormatInt(txID, 10)+"/match",
		url.Values{"entry_id": {strconv.FormatInt(entries[0].ID, 10)}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mismatched match = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Betr") && !strings.Contains(rec.Body.String(), "amounts do not match") {
		t.Errorf("no explanation of the mismatch: %s", rec.Body.String())
	}

	if rec := post(t, h, "/bank/999999/unmatch", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unmatch of a missing transaction = %d, want 404", rec.Code)
	}
}

func TestBankUploadRejectsBadFiles(t *testing.T) {
	h, _ := newTestServer(t)

	if rec := uploadCSV(t, h, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty file = %d, want 422", rec.Code)
	}

	// A mapping pointed at text columns must fail on the mapping step, with the
	// file still in hand rather than thrown away.
	payload := base64.StdEncoding.EncodeToString([]byte(bankExport))
	rec := post(t, h, "/bank/import", url.Values{
		"payload": {payload}, "source_name": {"x.csv"}, "has_header": {"1"},
		"col_date": {"2"}, "col_amount": {"3"},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad mapping = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), payload) {
		t.Error("the mapping step lost the uploaded file on error")
	}
}
