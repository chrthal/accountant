package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/invoice"
)

// Settings holds the issuer's own details. They are snapshotted onto every
// invoice at issue time (ADR 0008), so changing them here never rewrites an
// invoice that has already gone out.
type Settings struct {
	IssuerName    string
	IssuerAddress string
	IssuerUID     string
	IssuerIBAN    string // printed in the payment note, optional
	IssuerBIC     string // optional within the SEPA area
	// IssuerContact is free text for the invoice footer: phone, hours, email.
	IssuerContact string
	// InvoiceNumbering is NumberingYear or NumberingDay; InvoicePrefix goes
	// in front of the number in both schemes (ADR 0025).
	InvoiceNumbering string
	InvoicePrefix    string
	PaymentTermsDays int // default due date = issue date + this; 0 = none
	// DefaultVATOutgoing is the VAT rate for outgoing payments when neither an
	// earlier booking nor the category sets one (ADR 0021). Read through
	// OutgoingVAT, which also applies the Kleinunternehmer rule.
	DefaultVATOutgoing int
	Kleinunternehmer   bool
	// SVSExemption: the SVS Kleinunternehmer exemption has been applied for,
	// so the overview tracks its limits (ADR 0023).
	SVSExemption bool
	// Language is the UI language code (i18n.Languages); empty means German.
	Language string
	// DateFormat is the Go layout dates are shown in; see DateFormats.
	DateFormat string
	// The exempt period, see KUPeriod (ADR 0022). Until is only kept while
	// Kleinunternehmer is off: it records when the exemption ended.
	KleinunternehmerFrom  string
	KleinunternehmerUntil string

	// Email notifications (ADR 0039): the recipient, and which kinds are
	// mailed. The mail server itself is configured outside, never stored.
	NotifyEmail     string
	NotifyRecurring bool
	NotifyOverdue   bool
	NotifyBackup    bool
	NotifyQueues    bool

	// DATEV shows the DATEV export and the Konto/Gegenkonto fields (ADR 0028).
	// Off, they are hidden and the values already entered are kept.
	DATEV bool

	// Supplied by the Steuerberater; the DATEV export refuses to run without
	// them rather than inventing numbers (ADR 0010).
	DATEVConsultantNumber string
	DATEVClientNumber     string
	DATEVAccountLength    string
}

// OutgoingVAT is the default VAT rate for an outgoing payment. It is the rate
// on the supplier's receipt, so it applies under the Kleinunternehmerregelung
// too: the VAT is recorded as paid, but in the exempt period never deducted
// (ADR 0022, see KUOn).
func (s Settings) OutgoingVAT() int { return s.DefaultVATOutgoing }

// DefaultDateFormat is the Austrian way of writing a date.
const DefaultDateFormat = "02.01.2006"

// DateFormats are the date formats a user can choose, as Go layouts, with
// the label the settings page shows.
var DateFormats = map[string]string{
	"02.01.2006": "TT.MM.JJJJ (15.03.2026)",
	"2006-01-02": "JJJJ-MM-TT (2026-03-15)",
	"02/01/2006": "TT/MM/JJJJ (15/03/2026)",
	"01/02/2006": "MM/TT/JJJJ (03/15/2026)",
}

// Settings reads the current settings; absent keys come back empty, except
// DefaultVATOutgoing, which defaults to 20 %.
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT "key", value FROM settings`)
	if err != nil {
		return Settings{}, err
	}
	defer rows.Close()

	out := Settings{DefaultVATOutgoing: 20, DateFormat: DefaultDateFormat, InvoiceNumbering: NumberingYear,
		NotifyRecurring: true, NotifyOverdue: true, NotifyBackup: true, NotifyQueues: true}
	datevSet := false
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Settings{}, err
		}
		switch k {
		case "issuer_name":
			out.IssuerName = v
		case "issuer_address":
			out.IssuerAddress = v
		case "issuer_uid":
			out.IssuerUID = v
		case "issuer_iban":
			out.IssuerIBAN = v
		case "issuer_bic":
			out.IssuerBIC = v
		case "issuer_contact":
			out.IssuerContact = v
		case "invoice_numbering":
			if v == NumberingDay {
				out.InvoiceNumbering = v
			}
		case "invoice_prefix":
			out.InvoicePrefix = v
		case "payment_terms_days":
			out.PaymentTermsDays = atoiOr(v, 0)
		case "default_vat_outgoing":
			out.DefaultVATOutgoing = atoiOr(v, 20)
		case "kleinunternehmer":
			out.Kleinunternehmer = v == "1"
		case "language":
			out.Language = v
		case "svs_exemption":
			out.SVSExemption = v == "1"
		case "date_format":
			if _, ok := DateFormats[v]; ok {
				out.DateFormat = v
			}
		case "kleinunternehmer_from":
			out.KleinunternehmerFrom = v
		case "kleinunternehmer_until":
			out.KleinunternehmerUntil = v
		case "notify_email":
			out.NotifyEmail = v
		case "notify_recurring":
			out.NotifyRecurring = v == "1"
		case "notify_overdue":
			out.NotifyOverdue = v == "1"
		case "notify_backup":
			out.NotifyBackup = v == "1"
		case "notify_queues":
			out.NotifyQueues = v == "1"
		case "datev_enabled":
			datevSet, out.DATEV = true, v == "1"
		case "datev_consultant_number":
			out.DATEVConsultantNumber = v
		case "datev_client_number":
			out.DATEVClientNumber = v
		case "datev_account_length":
			out.DATEVAccountLength = v
		}
	}
	if err := rows.Err(); err != nil {
		return Settings{}, err
	}
	// Before the switch existed, DATEV was always on. An installation that
	// entered its DATEV numbers keeps it; any other starts with it off.
	if !datevSet {
		out.DATEV = out.DATEVConsultantNumber != ""
	}
	return out, nil
}

// SaveSettings writes the settings, replacing what is there.
func (s *Store) SaveSettings(ctx context.Context, in Settings) error {
	if strings.TrimSpace(in.IssuerName) == "" {
		return fmt.Errorf("issuer name is required — it goes on every invoice (§ 11 Abs 1 Z 1 UStG)")
	}
	prev, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	in.KleinunternehmerFrom = strings.TrimSpace(in.KleinunternehmerFrom)
	in.KleinunternehmerUntil = strings.TrimSpace(in.KleinunternehmerUntil)
	if in.Kleinunternehmer {
		in.KleinunternehmerUntil = "" // it has not ended
	}
	if err := validateKU(in, prev); err != nil {
		return err
	}
	if e := strings.TrimSpace(in.NotifyEmail); e != "" {
		if _, err := mail.ParseAddress(e); err != nil || strings.ContainsAny(e, "<> ,") {
			return fmt.Errorf("the notification address is not a valid email address")
		}
	}
	switch in.Language {
	case "", "de", "en":
	default:
		return fmt.Errorf("unknown language %q", in.Language)
	}
	if in.DateFormat == "" {
		in.DateFormat = DefaultDateFormat
	}
	if _, ok := DateFormats[in.DateFormat]; !ok {
		return fmt.Errorf("unknown date format %q", in.DateFormat)
	}
	switch in.DefaultVATOutgoing {
	case 0, 10, 13, 20:
	default:
		return fmt.Errorf("the default VAT rate must be one of 0, 10, 13, 20")
	}
	in.InvoicePrefix = strings.TrimSpace(in.InvoicePrefix)
	if !prefixRe.MatchString(in.InvoicePrefix) {
		return fmt.Errorf("the invoice number prefix may have up to 8 letters, digits or dashes")
	}
	switch in.InvoiceNumbering {
	case "":
		in.InvoiceNumbering = NumberingYear
	case NumberingYear, NumberingDay:
	default:
		return fmt.Errorf("unknown invoice numbering %q", in.InvoiceNumbering)
	}
	if in.PaymentTermsDays < 0 || in.PaymentTermsDays > 365 {
		return fmt.Errorf("payment terms must be between 0 and 365 days")
	}
	ku := "0"
	if in.Kleinunternehmer {
		ku = "1"
	}
	values := map[string]string{
		"issuer_name":            strings.TrimSpace(in.IssuerName),
		"issuer_address":         in.IssuerAddress,
		"issuer_uid":             strings.TrimSpace(in.IssuerUID),
		"issuer_iban":            strings.TrimSpace(in.IssuerIBAN),
		"issuer_bic":             strings.ToUpper(strings.TrimSpace(in.IssuerBIC)),
		"issuer_contact":         strings.TrimSpace(in.IssuerContact),
		"invoice_numbering":      in.InvoiceNumbering,
		"invoice_prefix":         in.InvoicePrefix,
		"payment_terms_days":     fmt.Sprint(in.PaymentTermsDays),
		"default_vat_outgoing":   fmt.Sprint(in.DefaultVATOutgoing),
		"kleinunternehmer":       ku,
		"date_format":            in.DateFormat,
		"language":               in.Language,
		"svs_exemption":          boolString(in.SVSExemption),
		"datev_enabled":          boolString(in.DATEV),
		"notify_email":           strings.TrimSpace(in.NotifyEmail),
		"notify_recurring":       boolString(in.NotifyRecurring),
		"notify_overdue":         boolString(in.NotifyOverdue),
		"notify_backup":          boolString(in.NotifyBackup),
		"notify_queues":          boolString(in.NotifyQueues),
		"kleinunternehmer_from":  in.KleinunternehmerFrom,
		"kleinunternehmer_until": in.KleinunternehmerUntil,

		"datev_consultant_number": strings.TrimSpace(in.DATEVConsultantNumber),
		"datev_client_number":     strings.TrimSpace(in.DATEVClientNumber),
		"datev_account_length":    strings.TrimSpace(in.DATEVAccountLength),
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for k, v := range values {
		// Delete-then-insert rather than an upsert: the syntax for upserts is
		// one of the places the two dialects diverge, and this is a four-row
		// table written by one person.
		if _, err := tx.ExecContext(ctx, s.Rebind(`DELETE FROM settings WHERE "key" = ?`), k); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			s.Rebind(`INSERT INTO settings ("key", value) VALUES (?, ?)`), k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InvoiceLine is one stored position of an invoice.
type InvoiceLine struct {
	Position       int
	Description    string
	QuantityMilli  int64
	Unit           string
	UnitPriceCents int64
	VATRate        int
	NetCents       int64
	VATCents       int64
	GrossCents     int64
}

// Invoice is a stored, issued invoice. Like ledger entries, invoices are
// immutable once written (ADR 0008).
type Invoice struct {
	ID          int64
	Year        int
	Seq         int
	Number      string
	IssueDate   string
	ServiceDate string
	DueDate     string // empty on invoices issued before ADR 0015
	ContactID   int64

	RecipientName    string
	RecipientAddress string
	RecipientUID     string
	IssuerName       string
	IssuerAddress    string
	IssuerUID        string
	IssuerIBAN       string
	IssuerBIC        string
	IssuerContact    string

	Kleinunternehmer bool
	Note             string
	NetCents         int64
	VATCents         int64
	GrossCents       int64
	CreatedAt        time.Time
	// Imported marks an invoice taken over from another tool; its document
	// is the original PDF (ADR 0031).
	Imported bool

	// Derived from the ledger, not stored on the invoice: the sum of effective
	// income entries that point at it (ADR 0015).
	PaidCents  int64
	LastPaidOn string

	Lines []InvoiceLine
}

// IsPaid reports whether effective payments cover the invoice.
func (i Invoice) IsPaid() bool { return i.GrossCents > 0 && i.PaidCents >= i.GrossCents }

// IsOverdue reports whether an unpaid invoice is past its due date.
func (i Invoice) IsOverdue() bool {
	return !i.IsPaid() && i.DueDate != "" && i.DueDate < time.Now().Format("2006-01-02")
}

// OpenCents is what is still owed.
func (i Invoice) OpenCents() int64 { return max(i.GrossCents-i.PaidCents, 0) }

// Document rebuilds the renderable document from the stored numbers. It does
// not recompute anything: what was issued is what gets printed (ADR 0008).
func (i Invoice) Document() invoice.Document {
	doc := invoice.Document{
		Number:      i.Number,
		IssueDate:   i.IssueDate,
		ServiceDate: i.ServiceDate,
		DueDate:     i.DueDate,
		PaymentIBAN: i.IssuerIBAN,
		PaymentBIC:  i.IssuerBIC,
		Contact:     i.IssuerContact,
		Issuer: invoice.Party{
			Name: i.IssuerName, Address: i.IssuerAddress, UID: i.IssuerUID,
		},
		Recipient: invoice.Party{
			Name: i.RecipientName, Address: i.RecipientAddress, UID: i.RecipientUID,
		},
		Kleinunternehmer: i.Kleinunternehmer,
		Note:             i.Note,
		NetCents:         i.NetCents,
		VATCents:         i.VATCents,
		GrossCents:       i.GrossCents,
	}

	byRate := map[int]int{} // rate -> index into doc.VATGroups
	for _, l := range i.Lines {
		doc.Lines = append(doc.Lines, invoice.Line{
			Position: l.Position, Description: l.Description,
			QuantityMilli: l.QuantityMilli, Unit: l.Unit,
			UnitPriceCents: l.UnitPriceCents, VATRate: l.VATRate,
			NetCents: l.NetCents, VATCents: l.VATCents, GrossCents: l.GrossCents,
		})
		idx, ok := byRate[l.VATRate]
		if !ok {
			doc.VATGroups = append(doc.VATGroups, invoice.VATGroup{Rate: l.VATRate})
			idx = len(doc.VATGroups) - 1
			byRate[l.VATRate] = idx
		}
		doc.VATGroups[idx].NetCents += l.NetCents
		doc.VATGroups[idx].VATCents += l.VATCents
	}
	return doc
}

// NewInvoice is the input for CreateInvoice.
type NewInvoice struct {
	ContactID   int64
	IssueDate   string
	ServiceDate string
	DueDate     string // optional
	Note        string
	Lines       []InvoiceLine // amounts are computed, not taken from the caller
	// DraftID, when set, is the draft this invoice is issued from. It is
	// deleted in the same transaction, so a draft is never issued twice.
	DraftID int64
	// RecurringID, when set, is the recurring invoice this one is for. Its
	// next date moves on by one interval in the same transaction (ADR 0038).
	RecurringID int64
}

// Invoice numbering schemes (ADR 0025).
const (
	// NumberingYear is prefix + YYYY-NNNN, counted through the year (ADR 0008).
	NumberingYear = "year"
	// NumberingDay is prefix + YYMMDD + NNN, counted per day, so a number does
	// not tell a customer how many invoices were written that year.
	NumberingDay = "day"
)

var prefixRe = regexp.MustCompile(`^[A-Za-z0-9-]{0,8}$`)

// Both schemes share UNIQUE (year, seq). A day number stores MMDD*1000 + n as
// seq, which is always 101001 or more; a year number stays below that.
const daySeqStart = 100_000

// nextNumber reads the next free seq for the scheme and renders the number.
func (s *Store) nextNumber(ctx context.Context, tx *sql.Tx, settings Settings, issued time.Time) (int, string, error) {
	lo, hi := 1, daySeqStart-1
	if settings.InvoiceNumbering == NumberingDay {
		lo = (int(issued.Month())*100+issued.Day())*1000 + 1
		hi = lo + 998
	}
	var maxSeq sql.NullInt64
	if err := tx.QueryRowContext(ctx, s.Rebind("SELECT MAX(seq) FROM invoices WHERE year = ? AND seq BETWEEN ? AND ?"),
		issued.Year(), lo, hi).Scan(&maxSeq); err != nil {
		return 0, "", err
	}
	seq := lo
	if maxSeq.Valid {
		seq = int(maxSeq.Int64) + 1
	}
	render := func(seq int) string {
		if settings.InvoiceNumbering == NumberingDay {
			return fmt.Sprintf("%s%s%03d", settings.InvoicePrefix, issued.Format("060102"), seq%1000)
		}
		return fmt.Sprintf("%s%04d-%04d", settings.InvoicePrefix, issued.Year(), seq)
	}
	// An imported invoice may already carry the number this seq renders to,
	// e.g. after a prefix change. Such numbers are skipped, never reused.
	for ; seq <= hi; seq++ {
		var taken int
		if err := tx.QueryRowContext(ctx, s.Rebind("SELECT COUNT(*) FROM invoices WHERE number = ?"), render(seq)).Scan(&taken); err != nil {
			return 0, "", err
		}
		if taken == 0 {
			return seq, render(seq), nil
		}
	}
	return 0, "", fmt.Errorf("no invoice numbers left for %s", issued.Format("2006-01-02"))
}

// CreateInvoice issues an invoice: it takes the next number in the chosen
// scheme, validates and totals the document, and writes it with its lines.
//
// The number is MAX(seq)+1 inside the inserting transaction, guarded by
// UNIQUE (year, seq) and UNIQUE (number) (ADR 0008). If two invoices are
// created at the same instant, the loser retries rather than reusing a number.
func (s *Store) CreateInvoice(ctx context.Context, in NewInvoice) (Invoice, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return Invoice{}, err
	}
	if strings.TrimSpace(settings.IssuerName) == "" {
		return Invoice{}, fmt.Errorf("issuer details are not configured yet — set them under Einstellungen")
	}

	contact, err := s.contact(ctx, in.ContactID)
	if err != nil {
		return Invoice{}, err
	}

	issueDate, err := time.Parse("2006-01-02", in.IssueDate)
	if err != nil {
		return Invoice{}, fmt.Errorf("issue date must be YYYY-MM-DD")
	}
	const attempts = 3
	for attempt := range attempts {
		inv, err := s.createInvoiceOnce(ctx, in, settings, contact, issueDate)
		if err == nil {
			return inv, nil
		}
		// A duplicate number means someone else took it between the read and
		// the write; anything else is a real failure.
		if attempt == attempts-1 || !isUniqueViolation(err) {
			return Invoice{}, err
		}
	}
	return Invoice{}, fmt.Errorf("could not allocate an invoice number")
}

func (s *Store) createInvoiceOnce(ctx context.Context, in NewInvoice, settings Settings, contact Contact, issued time.Time) (Invoice, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Invoice{}, err
	}
	defer tx.Rollback()

	year := issued.Year()
	seq, number, err := s.nextNumber(ctx, tx, settings, issued)
	if err != nil {
		return Invoice{}, err
	}

	doc, err := buildDocument(in, settings, contact, number)
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
		 kleinunternehmer, note, net_cents, vat_cents, gross_cents, created_at, recurring_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`),
		year, seq, number, doc.IssueDate, doc.ServiceDate, doc.DueDate, contact.ID,
		doc.Recipient.Name, doc.Recipient.Address, doc.Recipient.UID,
		doc.Issuer.Name, doc.Issuer.Address, doc.Issuer.UID, doc.PaymentIBAN, doc.PaymentBIC, doc.Contact,
		ku, doc.Note, doc.NetCents, doc.VATCents, doc.GrossCents, time.Now().UTC(), nullID(in.RecurringID),
	).Scan(&id)
	if err != nil {
		return Invoice{}, err
	}
	if in.RecurringID != 0 {
		if err := s.advanceRecurring(ctx, tx, in.RecurringID); err != nil {
			return Invoice{}, err
		}
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

	if in.DraftID != 0 {
		if _, err := tx.ExecContext(ctx, s.Rebind(`DELETE FROM invoice_drafts WHERE id = ?`), in.DraftID); err != nil {
			return Invoice{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Invoice{}, err
	}
	return s.GetInvoice(ctx, id)
}

// buildDocument validates the § 11 completeness rules and computes every
// amount, for an invoice about to be issued or for a preview.
func buildDocument(in NewInvoice, settings Settings, contact Contact, number string) (invoice.Document, error) {
	docIn := invoice.Input{
		Number:      number,
		IssueDate:   in.IssueDate,
		ServiceDate: in.ServiceDate,
		DueDate:     in.DueDate,
		PaymentIBAN: settings.IssuerIBAN,
		PaymentBIC:  settings.IssuerBIC,
		Contact:     settings.IssuerContact,
		Issuer: invoice.Party{
			Name: settings.IssuerName, Address: settings.IssuerAddress, UID: settings.IssuerUID,
		},
		Recipient: invoice.Party{
			Name: contact.Name, Address: contact.Address, UID: contact.UID,
		},
		Kleinunternehmer: settings.Kleinunternehmer,
		Note:             in.Note,
	}
	for _, l := range in.Lines {
		docIn.Lines = append(docIn.Lines, invoice.Line{
			Description: l.Description, QuantityMilli: l.QuantityMilli, Unit: l.Unit,
			UnitPriceCents: l.UnitPriceCents, VATRate: l.VATRate,
		})
	}
	return invoice.Build(docIn)
}

// PreviewNumber stands in for the number on a draft preview: the number is
// only taken when the invoice is issued.
const PreviewNumber = "ENTWURF"

// PreviewInvoice builds the document a draft would become, with the current
// settings and PreviewNumber, without writing anything.
func (s *Store) PreviewInvoice(ctx context.Context, in NewInvoice) (invoice.Document, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return invoice.Document{}, err
	}
	if strings.TrimSpace(settings.IssuerName) == "" {
		return invoice.Document{}, fmt.Errorf("issuer details are not configured yet — set them under Einstellungen")
	}
	contact, err := s.contact(ctx, in.ContactID)
	if err != nil {
		return invoice.Document{}, err
	}
	doc, err := buildDocument(in, settings, contact, PreviewNumber)
	doc.Draft = true
	return doc, err
}

// isUniqueViolation reports whether err is a duplicate-key error. Both drivers
// report it as text rather than a typed error we can match portably.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate key")
}

func (s *Store) contact(ctx context.Context, id int64) (Contact, error) {
	var c Contact
	err := s.DB.QueryRowContext(ctx,
		s.Rebind(`SELECT id, kind, name, uid, address, email, created_at FROM contacts WHERE id = ?`), id).
		Scan(&c.ID, &c.Kind, &c.Name, &c.UID, &c.Address, &c.Email, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return Contact{}, fmt.Errorf("contact not found — an invoice needs a recipient")
	}
	return c, err
}

// invoiceSelect derives the paid amount from the ledger: effective entries
// (neither a Storno nor reversed) that point at the invoice.
const invoiceSelect = `SELECT v.id, v.year, v.seq, v.number, v.issue_date, v.service_date, v.due_date,
	v.contact_id, v.recipient_name, v.recipient_address, v.recipient_uid,
	v.issuer_name, v.issuer_address, v.issuer_uid, v.issuer_iban, v.issuer_bic, v.issuer_contact,
	v.kleinunternehmer, v.note, v.net_cents, v.vat_cents, v.gross_cents, v.created_at, v.imported,
	COALESCE(p.paid, 0), COALESCE(p.last_paid, '')
	FROM invoices v
	LEFT JOIN (
		SELECT e.invoice_id, SUM(e.amount_gross_cents) AS paid, MAX(e.booked_on) AS last_paid
		FROM entries e
		WHERE e.invoice_id IS NOT NULL AND e.reverses_entry_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM entries r WHERE r.reverses_entry_id = e.id)
		GROUP BY e.invoice_id
	) p ON p.invoice_id = v.id`

func scanInvoice(row interface{ Scan(...any) error }) (Invoice, error) {
	var (
		i        Invoice
		ku, impd int
	)
	err := row.Scan(&i.ID, &i.Year, &i.Seq, &i.Number, &i.IssueDate, &i.ServiceDate, &i.DueDate,
		&i.ContactID, &i.RecipientName, &i.RecipientAddress, &i.RecipientUID,
		&i.IssuerName, &i.IssuerAddress, &i.IssuerUID, &i.IssuerIBAN, &i.IssuerBIC, &i.IssuerContact,
		&ku, &i.Note, &i.NetCents, &i.VATCents, &i.GrossCents, &i.CreatedAt, &impd,
		&i.PaidCents, &i.LastPaidOn)
	i.Kleinunternehmer, i.Imported = ku == 1, impd == 1
	return i, err
}

// GetInvoice returns one invoice with its lines.
func (s *Store) GetInvoice(ctx context.Context, id int64) (Invoice, error) {
	inv, err := scanInvoice(s.DB.QueryRowContext(ctx, s.Rebind(invoiceSelect+" WHERE v.id = ?"), id))
	if err == sql.ErrNoRows {
		return Invoice{}, ErrNotFound
	}
	if err != nil {
		return Invoice{}, err
	}

	rows, err := s.DB.QueryContext(ctx, s.Rebind(`SELECT position, description, quantity_milli,
		unit, unit_price_cents, vat_rate, net_cents, vat_cents, gross_cents
		FROM invoice_lines WHERE invoice_id = ? ORDER BY position`), id)
	if err != nil {
		return Invoice{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var l InvoiceLine
		if err := rows.Scan(&l.Position, &l.Description, &l.QuantityMilli, &l.Unit,
			&l.UnitPriceCents, &l.VATRate, &l.NetCents, &l.VATCents, &l.GrossCents); err != nil {
			return Invoice{}, err
		}
		inv.Lines = append(inv.Lines, l)
	}
	return inv, rows.Err()
}

// ListInvoices returns invoices newest first, without their lines.
func (s *Store) ListInvoices(ctx context.Context, limit int) ([]Invoice, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx,
		s.Rebind(invoiceSelect+" ORDER BY v.year DESC, v.seq DESC LIMIT ?"), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Invoice
	for rows.Next() {
		inv, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// ErrAlreadyPaid is returned when an invoice already has an effective payment.
var ErrAlreadyPaid = errors.New("invoice already has a booked payment")

// InvoicePayment is the input for BookInvoicePayment.
type InvoicePayment struct {
	InvoiceID  int64
	PaidOn     string // 'YYYY-MM-DD', the day the money arrived (cash basis)
	CategoryID *int64 // optional; supplies account defaults like any booking
	// AmountCents is what arrived; 0 means everything still open. Less than
	// the open amount is a part payment (ADR 0036).
	AmountCents int64
}

// ErrOverpaid is returned for a payment larger than what is still open.
var ErrOverpaid = errors.New("the payment is more than the open amount")

// BookInvoicePayment posts the income for a payment on an invoice: one entry
// per VAT rate.
//
// A payment of everything still open books each rate's remaining net and VAT
// exactly, so a fully paid invoice shows in the ledger the VAT it charged, to
// the cent (ADR 0015). A part payment is split over the rates in proportion
// to what is still open per rate, its VAT from that rate's open VAT; the
// remainder goes to the rate with the largest fraction (ADR 0036).
func (s *Store) BookInvoicePayment(ctx context.Context, p InvoicePayment) ([]int64, error) {
	if _, err := time.Parse("2006-01-02", p.PaidOn); err != nil {
		return nil, fmt.Errorf("payment date must be YYYY-MM-DD")
	}
	inv, err := s.GetInvoice(ctx, p.InvoiceID)
	if err != nil {
		return nil, err
	}
	if inv.IsPaid() {
		return nil, ErrAlreadyPaid
	}
	amount := p.AmountCents
	if amount == 0 {
		amount = inv.OpenCents()
	}
	if amount < 0 {
		return nil, fmt.Errorf("the payment must be more than zero")
	}
	if amount > inv.OpenCents() {
		return nil, ErrOverpaid
	}

	var konto, gegenkonto string
	if p.CategoryID != nil {
		c, err := s.GetCategory(ctx, *p.CategoryID)
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("category does not exist")
		}
		if err != nil {
			return nil, err
		}
		konto, gegenkonto = c.DefaultKonto, c.DefaultGegenkonto
	}

	parts, err := s.paymentParts(ctx, inv, amount)
	if err != nil {
		return nil, err
	}
	completes := amount == inv.OpenCents()
	label := "Zahlung"
	switch {
	case !completes:
		label = "Teilzahlung"
	case inv.PaidCents > 0:
		label = "Restzahlung"
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var ids []int64
	for _, part := range parts {
		// A rate with nothing left, or a cent that rounded away, has
		// nothing to book; the ledger refuses zero amounts anyway.
		if part.gross <= 0 {
			continue
		}
		desc := label + " Rechnung " + inv.Number + " · " + inv.RecipientName
		if len(parts) > 1 {
			desc = fmt.Sprintf("%s Rechnung %s (%d %%) · %s", label, inv.Number, part.rate, inv.RecipientName)
		}
		n := NewEntry{
			BookedOn:         p.PaidOn,
			Direction:        "income",
			Description:      desc,
			AmountGrossCents: part.gross,
			VATRate:          part.rate,
			Konto:            konto,
			Gegenkonto:       gegenkonto,
			ContactID:        &inv.ContactID,
			CategoryID:       p.CategoryID,
			invoiceID:        &inv.ID,
		}
		if err := n.Validate(); err != nil {
			return nil, err
		}
		id, err := s.insertEntry(ctx, tx, n, part.vat, part.gross-part.vat, nil)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("the invoice has no amount to book")
	}
	return ids, tx.Commit()
}

type paymentPart struct {
	rate       int
	gross, vat int64
}

// paymentParts splits a payment over the invoice's VAT rates. What each rate
// still has open is its invoiced gross and VAT minus the effective payments
// booked at that rate.
func (s *Store) paymentParts(ctx context.Context, inv Invoice, amount int64) ([]paymentPart, error) {
	rows, err := s.DB.QueryContext(ctx, s.Rebind(`SELECT e.vat_rate, SUM(e.amount_gross_cents), SUM(e.vat_cents)
		FROM entries e
		WHERE e.invoice_id = ? AND e.reverses_entry_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM entries r WHERE r.reverses_entry_id = e.id)
		GROUP BY e.vat_rate`), inv.ID)
	if err != nil {
		return nil, err
	}
	paidGross, paidVAT := map[int]int64{}, map[int]int64{}
	for rows.Next() {
		var (
			rate   int
			g, vat int64
		)
		if err := rows.Scan(&rate, &g, &vat); err != nil {
			rows.Close()
			return nil, err
		}
		paidGross[rate], paidVAT[rate] = g, vat
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var parts []paymentPart
	var open int64
	for _, g := range inv.Document().VATGroups {
		p := paymentPart{rate: g.Rate, gross: g.NetCents + g.VATCents - paidGross[g.Rate], vat: g.VATCents - paidVAT[g.Rate]}
		if p.gross > 0 {
			parts = append(parts, p)
			open += p.gross
		}
	}
	if amount == open {
		return parts, nil // the rest, exactly
	}
	return splitPayment(parts, amount, open), nil
}

// splitPayment shares amount out over parts in proportion to each part's
// open gross, by largest remainder, and gives each share its rate's
// proportion of the open VAT, rounded half up.
func splitPayment(parts []paymentPart, amount, open int64) []paymentPart {
	out := make([]paymentPart, len(parts))
	type frac struct {
		i   int
		rem int64
	}
	var given int64
	fracs := make([]frac, len(parts))
	for i, p := range parts {
		share := amount * p.gross / open
		out[i] = paymentPart{rate: p.rate, gross: share}
		fracs[i] = frac{i, amount * p.gross % open}
		given += share
	}
	sort.SliceStable(fracs, func(a, b int) bool { return fracs[a].rem > fracs[b].rem })
	for k := 0; given < amount; k++ {
		out[fracs[k%len(fracs)].i].gross++
		given++
	}
	for i, p := range parts {
		if out[i].gross > 0 && p.gross > 0 {
			out[i].vat = (2*out[i].gross*p.vat + p.gross) / (2 * p.gross)
		}
	}
	return out
}

// EntriesForInvoice returns every entry booked against an invoice, including
// reversed payments and, via the reversal link, their Stornos.
func (s *Store) EntriesForInvoice(ctx context.Context, invoiceID int64) ([]Entry, error) {
	rows, err := s.DB.QueryContext(ctx, s.Rebind(entrySelect+`
		WHERE e.invoice_id = ? OR e.reverses_entry_id IN (SELECT id FROM entries WHERE invoice_id = ?)
		ORDER BY e.booked_on, e.id`), invoiceID, invoiceID)
	if err != nil {
		return nil, err
	}
	return scanEntries(rows)
}

func boolString(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// InvoiceIDsForYear lists the invoices issued in the year, in number order.
func (s *Store) InvoiceIDsForYear(ctx context.Context, year int) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, s.Rebind(`SELECT id FROM invoices
		WHERE issue_date >= ? AND issue_date <= ? ORDER BY issue_date, id`),
		fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
