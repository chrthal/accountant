package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ImportedInvoice is an invoice written with another tool, taken over with
// its original document (ADR 0031).
type ImportedInvoice struct {
	NewInvoice
	Number   string
	Original Attachment // the original document, from NewAttachment
	// QueueID, when set, is the waiting-list row this import comes from
	// (ADR 0032). It is removed in the same transaction.
	QueueID int64
}

// importSeqStart is where imported invoices whose number fits no scheme are
// counted. Year numbers stay below daySeqStart and day numbers below
// 1232000, so the three ranges cannot meet.
const importSeqStart = 2_000_000

var importNumberRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}/._-]{0,39}$`)

// ImportInvoice stores an invoice issued elsewhere: its number as printed,
// its figures as Build computes them from the lines, and the original PDF as
// its document. The number must be new. When it fits the configured scheme
// (ADR 0025), it takes its place in the sequence, so the next invoice this
// app issues continues after it.
func (s *Store) ImportInvoice(ctx context.Context, in ImportedInvoice) (Invoice, error) {
	in.Number = strings.TrimSpace(in.Number)
	if !importNumberRe.MatchString(in.Number) {
		return Invoice{}, fmt.Errorf("the invoice number must be 1–40 letters, digits or / . _ -")
	}
	if in.Original.ContentType != "application/pdf" {
		return Invoice{}, fmt.Errorf("the original must be a PDF file")
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return Invoice{}, err
	}
	if strings.TrimSpace(settings.IssuerName) == "" {
		return Invoice{}, fmt.Errorf("issuer details are not configured yet — set them under Einstellungen")
	}
	issued, err := time.Parse("2006-01-02", in.IssueDate)
	if err != nil {
		return Invoice{}, fmt.Errorf("issue date must be YYYY-MM-DD")
	}
	contact, err := s.contact(ctx, in.ContactID)
	if err != nil {
		return Invoice{}, err
	}
	// The exemption that applied on the invoice's own date, not today's.
	settings.Kleinunternehmer = settings.KUOn(in.IssueDate)
	doc, err := buildDocument(in.NewInvoice, settings, contact, in.Number)
	if err != nil {
		return Invoice{}, err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Invoice{}, err
	}
	defer tx.Rollback()

	var taken int
	if err := tx.QueryRowContext(ctx, s.Rebind(`SELECT COUNT(*) FROM invoices WHERE number = ?`), in.Number).Scan(&taken); err != nil {
		return Invoice{}, err
	}
	if taken > 0 {
		return Invoice{}, fmt.Errorf("an invoice with the number %s already exists", in.Number)
	}
	seq, err := s.importSeq(ctx, tx, settings, issued, in.Number)
	if err != nil {
		return Invoice{}, err
	}

	ku := 0
	if doc.Kleinunternehmer {
		ku = 1
	}
	var id int64
	err = tx.QueryRowContext(ctx, s.Rebind(`INSERT INTO invoices
		(year, seq, number, issue_date, service_date, due_date, contact_id,
		 recipient_name, recipient_address, recipient_uid,
		 issuer_name, issuer_address, issuer_uid, issuer_iban, issuer_bic, issuer_contact,
		 kleinunternehmer, note, net_cents, vat_cents, gross_cents, created_at, imported)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1) RETURNING id`),
		issued.Year(), seq, in.Number, doc.IssueDate, doc.ServiceDate, doc.DueDate, contact.ID,
		doc.Recipient.Name, doc.Recipient.Address, doc.Recipient.UID,
		doc.Issuer.Name, doc.Issuer.Address, doc.Issuer.UID, doc.PaymentIBAN, doc.PaymentBIC, doc.Contact,
		ku, doc.Note, doc.NetCents, doc.VATCents, doc.GrossCents, time.Now().UTC(),
	).Scan(&id)
	if err != nil {
		return Invoice{}, err
	}
	for _, l := range doc.Lines {
		if _, err := tx.ExecContext(ctx, s.Rebind(`INSERT INTO invoice_lines
			(invoice_id, position, description, quantity_milli, unit, unit_price_cents,
			 vat_rate, net_cents, vat_cents, gross_cents)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			id, l.Position, l.Description, l.QuantityMilli, l.Unit, l.UnitPriceCents,
			l.VATRate, l.NetCents, l.VATCents, l.GrossCents); err != nil {
			return Invoice{}, err
		}
	}
	o := in.Original
	if _, err := tx.ExecContext(ctx, s.Rebind(`INSERT INTO invoice_originals
		(invoice_id, filename, content_type, size_bytes, sha256, data, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`),
		id, o.Filename, o.ContentType, o.SizeBytes, o.SHA256, o.Data, time.Now().UTC()); err != nil {
		return Invoice{}, err
	}
	if in.QueueID != 0 {
		if _, err := tx.ExecContext(ctx, s.Rebind(`DELETE FROM invoice_import_queue WHERE id = ?`), in.QueueID); err != nil {
			return Invoice{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Invoice{}, err
	}
	return s.GetInvoice(ctx, id)
}

// importSeq places an imported number in the scheme it fits, so MAX(seq)
// continues after it. A number that fits no scheme, or whose place is taken,
// is counted in the import range, which numbering never reads.
func (s *Store) importSeq(ctx context.Context, tx *sql.Tx, settings Settings, issued time.Time, number string) (int, error) {
	seq := 0
	rest, ok := strings.CutPrefix(number, settings.InvoicePrefix)
	if ok {
		if y, n, found := strings.Cut(rest, "-"); found && y == strconv.Itoa(issued.Year()) && len(n) == 4 {
			if v, err := strconv.Atoi(n); err == nil && v > 0 {
				seq = v
			}
		}
		if len(rest) == 9 && rest[:6] == issued.Format("060102") {
			if v, err := strconv.Atoi(rest[6:]); err == nil && v > 0 {
				seq = (int(issued.Month())*100+issued.Day())*1000 + v
			}
		}
	}
	if seq > 0 {
		var taken int
		if err := tx.QueryRowContext(ctx, s.Rebind(`SELECT COUNT(*) FROM invoices WHERE year = ? AND seq = ?`),
			issued.Year(), seq).Scan(&taken); err != nil {
			return 0, err
		}
		if taken == 0 {
			return seq, nil
		}
	}
	var maxSeq sql.NullInt64
	if err := tx.QueryRowContext(ctx, s.Rebind(`SELECT MAX(seq) FROM invoices WHERE year = ? AND seq >= ?`),
		issued.Year(), importSeqStart).Scan(&maxSeq); err != nil {
		return 0, err
	}
	if maxSeq.Valid {
		return int(maxSeq.Int64) + 1, nil
	}
	return importSeqStart, nil
}

// InvoiceOriginal returns the original document of an imported invoice.
func (s *Store) InvoiceOriginal(ctx context.Context, invoiceID int64) (Attachment, error) {
	var a Attachment
	err := s.DB.QueryRowContext(ctx, s.Rebind(`SELECT filename, content_type, size_bytes, sha256, data, created_at
		FROM invoice_originals WHERE invoice_id = ?`), invoiceID).
		Scan(&a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.Data, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return Attachment{}, ErrNotFound
	}
	return a, err
}
