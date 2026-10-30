package store_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

func TestAttachmentsAreKeptAndAppendOnly(t *testing.T) {
	png, err := os.ReadFile("../ocr/testdata/receipt.png")
	if err != nil {
		t.Fatal(err)
	}
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		id, _ := st.CreateEntry(ctx, sampleEntry())

		if n, _ := st.CountWithoutReceipt(ctx, 2026); n != 1 {
			t.Errorf("without receipt = %d, want 1", n)
		}
		p, _ := st.FindEntries(ctx, store.EntryFilter{WithoutReceipt: true})
		if p.Total != 1 {
			t.Errorf("filter without receipt = %d, want 1", p.Total)
		}

		a, err := store.NewAttachment(`C:\fakepath\beleg.png`, png)
		if err != nil {
			t.Fatalf("new attachment: %v", err)
		}
		if a.ContentType != "image/png" || a.Filename != "beleg.png" || !a.IsImage() {
			t.Errorf("attachment = %q %q", a.ContentType, a.Filename)
		}
		aid, err := st.AddAttachment(ctx, id, a)
		if err != nil {
			t.Fatalf("add: %v", err)
		}

		got, err := st.GetAttachment(ctx, aid)
		if err != nil || !bytes.Equal(got.Data, png) || got.SHA256 != a.SHA256 {
			t.Fatalf("round trip failed: %v", err)
		}
		list, _ := st.ListAttachments(ctx, id)
		if len(list) != 1 || list[0].Data != nil {
			t.Errorf("list = %d entries (data must not be loaded)", len(list))
		}
		e, _ := st.GetEntry(ctx, id)
		if e.Attachments != 1 {
			t.Errorf("entry attachment count = %d, want 1", e.Attachments)
		}
		if n, _ := st.CountWithoutReceipt(ctx, 2026); n != 0 {
			t.Errorf("without receipt after attaching = %d, want 0", n)
		}

		// § 132 BAO: a Beleg cannot be swapped or removed, by anyone.
		if _, err := st.DB.ExecContext(ctx, st.Rebind("UPDATE attachments SET filename = 'x' WHERE id = ?"), aid); err == nil {
			t.Error("UPDATE on attachments was allowed")
		}
		if _, err := st.DB.ExecContext(ctx, st.Rebind("DELETE FROM attachments WHERE id = ?"), aid); err == nil {
			t.Error("DELETE on attachments was allowed")
		}
		if _, err := st.AddAttachment(ctx, 99999, a); err == nil {
			t.Error("an attachment for a missing booking was accepted")
		}
		if _, err := st.GetAttachment(ctx, 99999); err != store.ErrNotFound {
			t.Errorf("missing attachment = %v", err)
		}
	})
}

func TestNewAttachmentSniffsTheType(t *testing.T) {
	for name, data := range map[string][]byte{
		"html pretending": []byte("<html><script>alert(1)</script></html>"),
		"empty":           {},
		"too big":         make([]byte, store.MaxAttachmentSize+1),
	} {
		if _, err := store.NewAttachment("x.jpg", data); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	pdf := []byte("%PDF-1.4\n%âãÏÓ\n1 0 obj\n")
	if a, err := store.NewAttachment("r.pdf", pdf); err != nil || a.ContentType != "application/pdf" || a.IsImage() {
		t.Errorf("pdf: %+v %v", a.ContentType, err)
	}
	heic := append([]byte{0, 0, 0, 24}, []byte("ftypheic0000mif1heic")...)
	if a, err := store.NewAttachment("IMG_1.HEIC", heic); err != nil || a.ContentType != "image/heic" {
		t.Errorf("heic: %q %v", a.ContentType, err)
	}
}
