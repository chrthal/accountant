package server

import (
	"testing"

	"github.com/chrthal/accountant/internal/ocr"
	"github.com/chrthal/accountant/internal/store"
)

// A read Beleg suggests its category by keyword, the category's VAT rate
// unless the receipt prints one, and a contact whose name fits (ADR 0033).
func TestReceiptProposal(t *testing.T) {
	twenty, ten := 20, 10
	cats := []store.Category{
		{ID: 1, Name: "Telefon & Internet", Direction: "outgoing", Keywords: "magenta", DefaultVATRate: &twenty},
		{ID: 2, Name: "Reisekosten", Direction: "outgoing", Keywords: "oebb", DefaultVATRate: &ten},
	}
	contacts := []store.Contact{{ID: 7, Name: "Magenta Telekom"}}
	settings := store.Settings{DefaultVATOutgoing: 13}
	a := store.Attachment{SHA256: "abc"}

	f := receiptProposal(ocr.Draft{Vendor: "MAGENTA TELEKOM", AmountCents: 4990, BookedOn: "2026-03-01"}, "", a, settings, cats, contacts, nil, "")
	if f["category_id"] != "1" || f["vat_rate"] != "20" || f["contact_id"] != "7" || f["amount"] != "49,90" || f["sha256"] != "abc" {
		t.Errorf("magenta receipt = %v", f)
	}
	f = receiptProposal(ocr.Draft{Vendor: "ÖBB", AmountCents: 1000, VATRate: 13, VATRateRead: true}, "", a, settings, cats, contacts, nil, "")
	if f["category_id"] != "2" || f["vat_rate"] != "13" {
		t.Errorf("a printed rate must win over the category's: %v", f)
	}
	f = receiptProposal(ocr.Draft{Vendor: "Unbekannt GmbH", AmountCents: 1000}, "", a, settings, cats, contacts, nil, "")
	if f["category_id"] != "" || f["vat_rate"] != "13" || f["contact_id"] != "" || f["direction"] != "outgoing" {
		t.Errorf("an unknown vendor = %v", f)
	}
}

// ADR 0044: a similar earlier booking wins over the keywords; a printed
// rate still wins over the booking's.
func TestReceiptProposalFromSimilarBooking(t *testing.T) {
	cat, contact := int64(5), int64(9)
	like := &store.Entry{ID: 42, CategoryID: &cat, ContactID: &contact, VATRate: 20, BusinessPct: 60, BusinessNote: "EVN"}
	cats := []store.Category{{ID: 1, Name: "Telefon", Direction: "outgoing", Keywords: "hetzner"}}
	f := receiptProposal(ocr.Draft{Vendor: "Hetzner Online", AmountCents: 1000}, "", store.Attachment{}, store.Settings{}, cats, nil, like, "ähnlich wie #42")
	if f["category_id"] != "5" || f["contact_id"] != "9" || f["vat_rate"] != "20" || f["business_pct"] != "60" || f["why"] != "ähnlich wie #42" {
		t.Errorf("proposal = %v", f)
	}
	f = receiptProposal(ocr.Draft{Vendor: "Hetzner", VATRate: 10, VATRateRead: true}, "", store.Attachment{}, store.Settings{}, cats, nil, like, "")
	if f["vat_rate"] != "10" {
		t.Errorf("a printed rate must win: %v", f)
	}
}
