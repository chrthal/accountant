package store_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/bankcsv"
	"github.com/chrthal/accountant/internal/store"
)

func TestBackupCoversTheWholeSchema(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		if err := st.CheckBackupCoverage(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

// seedBooks fills a database with one of everything: settings, a contact and
// category, a mixed-rate invoice paid from a bank transfer (two entries, one
// match), a Storno pair, and a booking with a Beleg.
func seedBooks(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	contact := setupIssuer(t, st, false)
	cat, _ := st.CreateCategory(ctx, store.Category{Name: "Büro", Direction: "outgoing",
		DefaultKonto: "7600", DefaultGegenkonto: "2700"})

	inv, err := st.CreateInvoice(ctx, store.NewInvoice{
		ContactID: contact, IssueDate: "2026-08-18", ServiceDate: "2026-08-18", DueDate: "2026-09-01",
		Lines: []store.InvoiceLine{
			{Description: "Beratung", QuantityMilli: 1500, Unit: "Std.", UnitPriceCents: 12000, VATRate: 20},
			{Description: "Bücher", QuantityMilli: 2000, UnitPriceCents: 2490, VATRate: 10},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportBankRows(ctx, []bankcsv.Row{
		{BookedOn: "2026-08-25", AmountCents: inv.GrossCents, Counterparty: "Muster GmbH", Hash: "pay"},
	}, "elba.csv", nil); err != nil {
		t.Fatal(err)
	}
	ids, err := st.BookInvoicePayment(ctx, store.InvoicePayment{InvoiceID: inv.ID, PaidOn: "2026-08-25"})
	if err != nil {
		t.Fatal(err)
	}
	txs, _ := st.ListBankTransactions(ctx, true, 10)
	if err := st.MatchBankTransaction(ctx, txs[0].ID, ids...); err != nil {
		t.Fatal(err)
	}

	n := sampleEntry()
	n.CategoryID, n.Konto, n.Gegenkonto = &cat, "", ""
	paper, err := st.CreateEntry(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	png, err := os.ReadFile("../ocr/testdata/receipt.png")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := store.NewAttachment("bon.png", png)
	if _, err := st.AddAttachment(ctx, paper, a); err != nil {
		t.Fatal(err)
	}
	wrong := sampleEntry()
	wrong.Description = "Fehlbuchung"
	wid, _ := st.CreateEntry(ctx, wrong)
	if _, err := st.ReverseEntry(ctx, wid, "2026-03-20"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveBankMapping(ctx, "sig", bankcsv.Mapping{Date: 0, Amount: 3, Counterparty: -1, Purpose: 1}); err != nil {
		t.Fatal(err)
	}
}

// The ADR 0019 acceptance case: books backed up from each engine restore into
// each engine, every table comes back identical, and the restored database is
// a working one, including its id sequences.
func TestBackupRestoresIntoEveryEngine(t *testing.T) {
	for srcEngine, srcDSN := range dsns(t) {
		src, err := store.Open(srcDSN)
		if err != nil {
			t.Fatal(err)
		}
		seedBooks(t, src)
		var file bytes.Buffer
		sum, err := src.Backup(context.Background(), &file, "test")
		if err != nil {
			t.Fatalf("%s: backup: %v", srcEngine, err)
		}
		if sum.Rows["entries"] != 5 || sum.Rows["attachments"] != 1 || sum.Rows["bank_matches"] != 2 {
			t.Fatalf("%s: backup rows = %v", srcEngine, sum.Rows)
		}

		// A second backup of the same books is the reference to compare with.
		var again bytes.Buffer
		if _, err := src.Backup(context.Background(), &again, "test"); err != nil {
			t.Fatal(err)
		}
		src.Close()

		for dstEngine, dstDSN := range dsns(t) {
			t.Run(srcEngine+"_to_"+dstEngine, func(t *testing.T) {
				ctx := context.Background()
				got, err := store.Restore(ctx, dstDSN, bytes.NewReader(file.Bytes()))
				if err != nil {
					t.Fatalf("restore: %v", err)
				}
				if got.TotalRows() != sum.TotalRows() {
					t.Errorf("restored %d rows, backed up %d", got.TotalRows(), sum.TotalRows())
				}

				dst, err := store.Open(dstDSN)
				if err != nil {
					t.Fatal(err)
				}
				defer dst.Close()

				// Same content: a backup of the restore equals a backup of
				// the original, table by table.
				var round bytes.Buffer
				if _, err := dst.Backup(ctx, &round, "test"); err != nil {
					t.Fatal(err)
				}
				if a, b := tableHashes(t, again.Bytes()), tableHashes(t, round.Bytes()); a != b {
					t.Errorf("restored books differ from the original:\n%s\n%s", a, b)
				}

				// And it works: the derived figures are intact, and a new
				// booking gets a fresh id rather than colliding.
				inv, err := dst.GetInvoice(ctx, 1)
				if err != nil || !inv.IsPaid() {
					t.Errorf("invoice after restore: paid=%v err=%v", inv.IsPaid(), err)
				}
				if att, _ := dst.ListAttachments(ctx, 3); len(att) != 1 {
					t.Error("the Beleg did not survive the restore")
				}
				id, err := dst.CreateEntry(ctx, sampleEntry())
				if err != nil || id != 6 {
					t.Errorf("new booking after restore: id %d, err %v — the id sequence was not moved on", id, err)
				}
				if m, ok, _ := dst.SavedBankMapping(ctx, "sig"); !ok || m.Amount != 3 {
					t.Error("the remembered bank mapping did not survive")
				}

				// A restore never merges into existing books.
				if _, err := store.Restore(ctx, dstDSN, bytes.NewReader(file.Bytes())); err == nil {
					t.Error("a restore into a database with data was accepted")
				}
			})
		}
	}
}

// tableHashes extracts the per-table checksum lines of a backup.
func tableHashes(t *testing.T, file []byte) string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(gz)
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(l, `{"end":`) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestVerifyCatchesDamagedBackups(t *testing.T) {
	st, err := store.Open("sqlite://" + filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seedBooks(t, st)
	var file bytes.Buffer
	if _, err := st.Backup(context.Background(), &file, "test"); err != nil {
		t.Fatal(err)
	}
	if sum, err := store.VerifyBackup(bytes.NewReader(file.Bytes())); err != nil || sum.TotalRows() == 0 {
		t.Fatalf("verify a good backup: %v", err)
	}

	gz, _ := gzip.NewReader(bytes.NewReader(file.Bytes()))
	raw, _ := io.ReadAll(gz)
	rezip := func(b []byte) []byte {
		var out bytes.Buffer
		w := gzip.NewWriter(&out)
		w.Write(b)
		w.Close()
		return out.Bytes()
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")

	cases := map[string][]byte{
		"truncated":      rezip([]byte(strings.Join(lines[:len(lines)-3], "\n") + "\n")),
		"altered amount": rezip([]byte(strings.Replace(string(raw), `"Fehlbuchung"`, `"Fehlbuchunk"`, 1))),
		"not a backup":   rezip([]byte("hello\n")),
		"not gzip":       []byte("plain text"),
	}
	for name, data := range cases {
		if _, err := store.VerifyBackup(bytes.NewReader(data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
		dst := "sqlite://" + filepath.Join(t.TempDir(), name+".db")
		if _, err := store.Restore(context.Background(), dst, bytes.NewReader(data)); err == nil {
			t.Errorf("%s: restored", name)
		}
	}

	// A damaged table found halfway rolls back the whole restore.
	dst := "sqlite://" + filepath.Join(t.TempDir(), "rolled-back.db")
	store.Restore(context.Background(), dst, bytes.NewReader(cases["altered amount"]))
	check, _ := store.Open(dst)
	defer check.Close()
	if es, _ := check.ListEntries(context.Background(), 10); len(es) != 0 {
		t.Errorf("a failed restore left %d bookings behind", len(es))
	}
}

func TestLastBackupIsRecorded(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if at, _ := st.LastBackup(ctx); !at.IsZero() {
			t.Fatal("a fresh database reports a backup")
		}
		var file bytes.Buffer
		sum, err := st.Backup(ctx, &file, "test")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.MarkBackup(ctx, sum.CreatedAt); err != nil {
			t.Fatal(err)
		}
		if at, _ := st.LastBackup(ctx); !at.Equal(sum.CreatedAt.Truncate(1e9)) {
			t.Errorf("last backup = %v, want %v", at, sum.CreatedAt)
		}
		// Settings() ignores the key, so the settings page is unaffected.
		if _, err := st.Settings(ctx); err != nil {
			t.Error(err)
		}
	})
}
