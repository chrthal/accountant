package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/money"
)

// MaxAttachmentSize is the largest Beleg accepted: a phone photo of a receipt
// is 2–5 MiB, and a scanned PDF invoice rarely more.
const MaxAttachmentSize = 10 << 20

// Attachment is a stored Beleg. Data is only loaded by GetAttachment.
type Attachment struct {
	ID          int64
	EntryID     int64
	Filename    string
	ContentType string
	SizeBytes   int64
	SHA256      string
	CreatedAt   time.Time
	Data        []byte
}

// IsImage reports whether the attachment can be shown inline as a picture.
func (a Attachment) IsImage() bool {
	return strings.HasPrefix(a.ContentType, "image/") && a.ContentType != "image/heic"
}

// allowedTypes are what a Beleg can be. The type is sniffed from the bytes,
// never taken from the browser's claim, because the file is served back
// later and must not become an HTML page.
var allowedTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true,
	"application/pdf": true,
}

// NewAttachment validates a file and fills in its type and checksum.
func NewAttachment(filename string, data []byte) (Attachment, error) {
	if len(data) == 0 {
		return Attachment{}, fmt.Errorf("the file is empty")
	}
	if len(data) > MaxAttachmentSize {
		return Attachment{}, fmt.Errorf("the file is larger than %d MiB", MaxAttachmentSize>>20)
	}
	ct := http.DetectContentType(data)
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	// HEIC (iPhone camera default) is not sniffed by the stdlib. Accept it by
	// its ISO-BMFF "ftyp" brand rather than by the file name alone.
	if !allowedTypes[ct] && len(data) > 12 && string(data[4:8]) == "ftyp" &&
		strings.Contains("heic heix mif1 msf1", string(data[8:12])) {
		ct = "image/heic"
	}
	if !allowedTypes[ct] && ct != "image/heic" {
		return Attachment{}, fmt.Errorf("only photos (JPEG, PNG, WebP, HEIC) and PDF files can be stored as a Beleg")
	}
	sum := sha256.Sum256(data)
	name := strings.TrimSpace(filepath.Base(strings.ReplaceAll(filename, "\\", "/")))
	if name == "." || name == "/" {
		name = ""
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return Attachment{Filename: name, ContentType: ct, SizeBytes: int64(len(data)),
		SHA256: hex.EncodeToString(sum[:]), Data: data}, nil
}

// AddAttachment stores a validated attachment on a booking. Belege are
// append-only (ADR 0016): adding one to an existing booking is allowed, since
// it adds evidence, while replacing or removing one is refused by the database.
func (s *Store) AddAttachment(ctx context.Context, entryID int64, a Attachment) (int64, error) {
	if _, err := s.GetEntry(ctx, entryID); err != nil {
		return 0, err
	}
	var id int64
	err := s.DB.QueryRowContext(ctx, s.Rebind(`INSERT INTO attachments
		(entry_id, filename, content_type, size_bytes, sha256, data, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`),
		entryID, a.Filename, a.ContentType, a.SizeBytes, a.SHA256, a.Data, time.Now().UTC()).Scan(&id)
	return id, err
}

// ListAttachments returns a booking's attachments without their data.
func (s *Store) ListAttachments(ctx context.Context, entryID int64) ([]Attachment, error) {
	rows, err := s.DB.QueryContext(ctx, s.Rebind(`SELECT id, entry_id, filename, content_type,
		size_bytes, sha256, created_at FROM attachments WHERE entry_id = ? ORDER BY id`), entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.EntryID, &a.Filename, &a.ContentType, &a.SizeBytes,
			&a.SHA256, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAttachment returns one attachment including its data.
func (s *Store) GetAttachment(ctx context.Context, id int64) (Attachment, error) {
	var a Attachment
	err := s.DB.QueryRowContext(ctx, s.Rebind(`SELECT id, entry_id, filename, content_type,
		size_bytes, sha256, created_at, data FROM attachments WHERE id = ?`), id).
		Scan(&a.ID, &a.EntryID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.CreatedAt, &a.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	return a, err
}

// CountWithoutReceipt counts a year's effective outgoing bookings that have no
// Beleg attached, which is the overview's "Belege fehlen" to-do. Income is
// left out, since the Beleg for income is the issued invoice.
func (s *Store) CountWithoutReceipt(ctx context.Context, year int) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, s.Rebind(`SELECT COUNT(*) FROM entries e
		WHERE e.direction = 'outgoing' AND e.booked_on >= ? AND e.booked_on <= ?
		  AND e.reverses_entry_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM entries r WHERE r.reverses_entry_id = e.id)
		  AND NOT EXISTS (SELECT 1 FROM attachments a WHERE a.entry_id = e.id)`),
		fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year)).Scan(&n)
	return n, err
}

// YearAttachment is a Beleg with the date of its booking, without its data.
type YearAttachment struct {
	Attachment
	BookedOn string
}

// AttachmentsForYear lists the Belege of every booking in the year, in
// ledger order, without their data: GetAttachment loads one at a time.
func (s *Store) AttachmentsForYear(ctx context.Context, year int) ([]YearAttachment, error) {
	rows, err := s.DB.QueryContext(ctx, s.Rebind(`SELECT a.id, a.entry_id, a.filename, a.content_type,
		a.size_bytes, a.sha256, a.created_at, e.booked_on
		FROM attachments a JOIN entries e ON e.id = a.entry_id
		WHERE e.booked_on >= ? AND e.booked_on <= ?
		ORDER BY e.booked_on, e.id, a.id`),
		fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []YearAttachment
	for rows.Next() {
		var a YearAttachment
		if err := rows.Scan(&a.ID, &a.EntryID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256,
			&a.CreatedAt, &a.BookedOn); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ReceiptHashes maps the SHA-256 of every stored Beleg to its booking, so a
// bulk upload can name a receipt that is booked already.
func (s *Store) ReceiptHashes(ctx context.Context) (map[string]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT sha256, entry_id FROM attachments`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var (
			h  string
			id int64
		)
		if err := rows.Scan(&h, &id); err != nil {
			return nil, err
		}
		out[h] = id
	}
	return out, rows.Err()
}

// CreateEntryWithReceipt books an entry with its Beleg in one transaction,
// and removes it from the receipt waiting list when queueID is set
// (ADR 0033): a bulk booking never leaves a booking without its receipt,
// nor a receipt that can be booked twice.
func (s *Store) CreateEntryWithReceipt(ctx context.Context, n NewEntry, receipt Attachment, queueID int64) (int64, error) {
	if err := n.Validate(); err != nil {
		return 0, err
	}
	if err := s.resolveAccounts(ctx, &n); err != nil {
		return 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	vat, net := money.SplitVAT(n.AmountGrossCents, n.VATRate)
	id, err := s.insertEntry(ctx, tx, n, vat, net, nil)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, s.Rebind(`INSERT INTO attachments
		(entry_id, filename, content_type, size_bytes, sha256, data, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`),
		id, receipt.Filename, receipt.ContentType, receipt.SizeBytes, receipt.SHA256, receipt.Data, time.Now().UTC()); err != nil {
		return 0, err
	}
	if queueID != 0 {
		if _, err := tx.ExecContext(ctx, s.Rebind(`DELETE FROM `+string(ReceiptQueue)+` WHERE id = ?`), queueID); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}
