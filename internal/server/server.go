// Package server wires the HTTP routes onto the store.
package server

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chrthal/accountant/internal/i18n"
	"github.com/chrthal/accountant/internal/invoice"
	"github.com/chrthal/accountant/internal/llm"
	"github.com/chrthal/accountant/internal/money"
	"github.com/chrthal/accountant/internal/notify"
	"github.com/chrthal/accountant/internal/ocr"
	"github.com/chrthal/accountant/internal/report"
	"github.com/chrthal/accountant/internal/store"
	"github.com/chrthal/accountant/web"
)

// Server holds everything the handlers need.
type Server struct {
	store   *store.Store
	version string
	pages   map[string]*template.Template

	// Login (ADR 0017); empty password means the app is open.
	password   string
	sessionKey []byte

	llm      *llm.Client // nil unless BOOKKEEPER_LLM_URL is set (ADR 0020)
	llmState llmState

	reader receiptReader // Belege of a bulk upload (ADR 0042)

	// prefs are the display settings, cached so templates need no query.
	prefs atomic.Pointer[prefs]

	// Daily backups, for display only (ADR 0019).
	backupDir  string
	backupKeep int

	// envMailer is a mail server set by environment variables (ADR 0039);
	// it wins over one set in the web UI (ADR 0040), and the settings page
	// then shows it read-only. envMailServer names it.
	envMailer     notify.Mailer
	envMailServer string
	// sendWith turns an account into a mailer; tests replace it.
	sendWith func(*notify.SMTP) notify.Mailer
}

// WithEnvMailer tells the settings page that the mail server comes from the
// environment.
func WithEnvMailer(m notify.Mailer, describe string) Option {
	return func(s *Server) { s.envMailer, s.envMailServer = m, describe }
}

// WithMailerFactory replaces how a mail server set in the web UI sends, so
// tests need no network.
func WithMailerFactory(f func(*notify.SMTP) notify.Mailer) Option {
	return func(s *Server) { s.sendWith = f }
}

// New returns the application's HTTP handler.
func New(st *store.Store, version string, opts ...Option) http.Handler {
	s := &Server{store: st, version: version}
	s.loadPrefs()
	s.pages = s.parsePages()
	for _, o := range opts {
		o(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(web.Static)))
	// The service worker has to be served from the root: its scope is the
	// directory it is served from, and it needs to cover the whole app.
	mux.HandleFunc("GET /sw.js", s.handleServiceWorker)

	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /entries", s.handleEntries)
	mux.HandleFunc("GET /entries/new", s.handleEntryForm)
	mux.HandleFunc("GET /entries/{id}", s.handleEntry)
	mux.HandleFunc("POST /entries", s.handleEntryCreate)
	mux.HandleFunc("POST /entries/{id}/reverse", s.handleEntryReverse)
	mux.HandleFunc("POST /entries/{id}/description", s.handleEntryRename)
	mux.HandleFunc("POST /entries/{id}/share", s.handleEntryShare)
	mux.HandleFunc("POST /entries/{id}/attachments", s.handleAttachmentAdd)
	mux.HandleFunc("GET /attachments/{id}", s.handleAttachment)
	mux.HandleFunc("GET /categories", s.handleCategories)
	mux.HandleFunc("POST /categories", s.handleCategoryCreate)
	mux.HandleFunc("GET /categories/{id}", s.handleCategoryEdit)
	mux.HandleFunc("POST /categories/{id}", s.handleCategoryUpdate)
	mux.HandleFunc("GET /contacts", s.handleContacts)
	mux.HandleFunc("POST /contacts", s.handleContactCreate)
	mux.HandleFunc("GET /contacts/{id}", s.handleContactEdit)
	mux.HandleFunc("POST /contacts/{id}", s.handleContactUpdate)
	mux.HandleFunc("POST /contacts/{id}/delete", s.handleContactDelete)
	mux.HandleFunc("GET /invoices", s.handleInvoices)
	mux.HandleFunc("GET /invoices/new", s.handleInvoiceForm)
	mux.HandleFunc("GET /invoices/import", s.handleInvoiceImportForm)
	mux.HandleFunc("POST /invoices/import", s.handleInvoiceImportUpload)
	mux.HandleFunc("POST /invoices/import/save", s.handleInvoiceImportSave)
	mux.HandleFunc("POST /invoices/import/batch", s.handleInvoiceImportBatch)
	mux.HandleFunc("GET /invoices/import/review", s.handleInvoiceImportReview)
	mux.HandleFunc("POST /invoices/import/review", s.handleInvoiceImportReviewPost)
	mux.HandleFunc("GET /invoices/import/queue/{id}", s.handleQueuedImportEdit)
	mux.HandleFunc("GET /invoices/import/queue/{id}/pdf", s.handleQueuedImportPDF)
	mux.HandleFunc("POST /invoices", s.handleInvoiceCreate)
	mux.HandleFunc("GET /recurring", s.handleRecurringList)
	mux.HandleFunc("GET /recurring/new", s.handleRecurringNew)
	mux.HandleFunc("POST /recurring", s.handleRecurringSave)
	mux.HandleFunc("GET /recurring/{id}", s.handleRecurringEdit)
	mux.HandleFunc("POST /recurring/{id}", s.handleRecurringSave)
	mux.HandleFunc("POST /recurring/{id}/{action}", s.handleRecurringAction)
	mux.HandleFunc("GET /invoice-drafts/{id}", s.handleDraftEdit)
	mux.HandleFunc("POST /invoice-drafts/{id}/delete", s.handleDraftDelete)
	mux.HandleFunc("GET /invoices/{id}", s.handleInvoice)
	mux.HandleFunc("GET /invoices/{id}/pdf", s.handleInvoicePDF)
	mux.HandleFunc("POST /invoices/{id}/payment", s.handleInvoicePayment)
	mux.HandleFunc("GET /receipts/new", s.handleReceiptForm)
	mux.HandleFunc("POST /receipts", s.handleReceiptUpload)
	mux.HandleFunc("POST /receipts/batch", s.handleReceiptBatch)
	mux.HandleFunc("GET /receipts/review", s.handleReceiptReview)
	mux.HandleFunc("POST /receipts/review", s.handleReceiptReviewPost)
	mux.HandleFunc("GET /receipts/queue/{id}", s.handleQueuedReceiptEdit)
	mux.HandleFunc("GET /receipts/queue/{id}/file", s.handleQueuedReceiptFile)
	mux.HandleFunc("GET /reports", s.handleReports)
	mux.HandleFunc("GET /reports/vat", s.handleVAT)
	mux.HandleFunc("GET /export/csv", s.handleExportCSV)
	mux.HandleFunc("GET /export/datev", s.handleExportDATEV)
	mux.HandleFunc("GET /export/archive", s.handleExportArchive)
	mux.HandleFunc("GET /bank", s.handleBank)
	mux.HandleFunc("POST /bank/upload", s.handleBankUpload)
	mux.HandleFunc("POST /bank/import", s.handleBankImport)
	mux.HandleFunc("POST /bank/{id}/match", s.handleBankMatch)
	mux.HandleFunc("POST /bank/{id}/unmatch", s.handleBankUnmatch)
	mux.HandleFunc("POST /bank/{id}/invoice", s.handleBankInvoice)
	mux.HandleFunc("POST /bank/auto", s.handleBankAutoMatch)
	mux.HandleFunc("POST /bank/{id}/private", s.handleBankPrivate)
	mux.HandleFunc("POST /bank/{id}/unprivate", s.handleBankUnprivate)
	mux.HandleFunc("POST /bank/private-own", s.handleBankPrivateOwn)
	mux.HandleFunc("POST /bank/{id}/to-private", s.handleBankToPrivate)
	mux.HandleFunc("POST /bank/private-own-booked", s.handleBankToPrivateOwn)
	mux.HandleFunc("GET /bank/drafts", s.handleDrafts)
	mux.HandleFunc("POST /bank/drafts", s.handleDraftsBook)
	mux.HandleFunc("POST /bank/drafts/suggest", s.handleDraftsSuggest)
	mux.HandleFunc("POST /entries/{id}/asset", s.handleAssetCreate)
	mux.HandleFunc("POST /assets/{id}/delete", s.handleAssetDelete)
	mux.HandleFunc("GET /assets", s.handleAssets)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("GET /help", func(w http.ResponseWriter, r *http.Request) {
		s.render(w, http.StatusOK, "help.html", pageData{})
	})
	mux.HandleFunc("GET /backup", s.handleBackupDownload)
	mux.HandleFunc("POST /restore", s.handleRestore)
	mux.HandleFunc("POST /settings", s.handleSettingsSave)
	mux.HandleFunc("POST /settings/notify-test", s.handleNotifyTest)
	mux.HandleFunc("POST /settings/mail-delete", s.handleMailServerDelete)
	mux.HandleFunc("GET /settings/logo", s.handleLogo)
	mux.HandleFunc("POST /settings/logo", s.handleLogoUpload)
	mux.HandleFunc("POST /settings/logo/delete", s.handleLogoDelete)

	return s.secure(mux)
}

// Each page is its own template set over the shared layout, so pages can define
// the same block names without colliding.
func (s *Server) parsePages() map[string]*template.Template {
	funcs := template.FuncMap{
		"eur":        s.eur,
		"vatRates":   func() []int { return money.ValidVATRates },
		"inc":        func(i int) int { return i + 1 },
		"dec":        func(i int) int { return i - 1 },
		"date":       s.fmtDate,
		"dateLayout": s.dateLayout,
		"datev":      func() bool { return s.prefs.Load().datev },
		"month":      s.monthName,
		"idstr": func(p *int64) string {
			if p == nil {
				return ""
			}
			return strconv.FormatInt(*p, 10)
		},
		"itoa":   strconv.Itoa,
		"i64":    func(i int64) string { return strconv.FormatInt(i, 10) },
		"months": func() []int { return []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12} },
		"sub64":  func(a, b int64) int64 { return a - b },
		"neg":    func(c int64) bool { return c < 0 },
		"abs": func(c int64) int64 {
			if c < 0 {
				return -c
			}
			return c
		},
		"query":       func(v url.Values) template.URL { return template.URL(v.Encode()) },
		"lineCtx":     newLineCtx,
		"ago":         s.ago,
		"t":           s.T,
		"tf":          s.Tf,
		"lang":        s.lang,
		"languages":   func() map[string]string { return i18n.Languages },
		"dateFormats": func() map[string]string { return store.DateFormats },
		"join":        strings.Join,
		"categoryForm": func(c store.Category) map[string]string {
			v := ""
			if c.DefaultVATRate != nil {
				v = strconv.Itoa(*c.DefaultVATRate)
			}
			return map[string]string{"keywords": c.Keywords, "default_vat_rate": v, "deductible_pct": strconv.Itoa(c.Deductible())}
		},
		"qty": money.FormatQuantity,
		"truncate": func(s string, n int) string {
			if t := truncate(s, n); t != strings.TrimSpace(s) {
				return t + "…"
			}
			return strings.TrimSpace(s)
		},
		"colCtx": func(name, label string, cols []string, sel string, optional bool) colCtx {
			return colCtx{Name: name, Label: label, Columns: cols, Selected: sel, Optional: optional}
		},
		"blankLine": func(rate string) lineForm { return lineForm{VATRate: rate} },
		"maxQueued": func() int { return store.MaxQueuedImports },
		"gwgLimit":  func() int64 { return store.GWGLimitCents },
		"intervals": func() []string { return store.Intervals },
		"hasSuffix": func(s, suffix string) bool { return strings.HasSuffix(strings.ToLower(s), suffix) },
	}
	pages := map[string]*template.Template{
		// The login page stands alone: the app shell would only show links
		// that all lead back to it.
		"login.html": template.Must(template.New("login.html").Funcs(funcs).
			ParseFS(web.Templates, "templates/login.html")),
	}
	for name := range navFor {
		files := []string{"templates/layout.html", "templates/partials.html", "templates/" + name}
		if name == "help.html" {
			files = append(files, "templates/help/*.html")
		}
		pages[name] = template.Must(template.New("layout.html").Funcs(funcs).ParseFS(web.Templates, files...))
	}
	return pages
}

// navFor is every page template and the navigation item it belongs to, which
// render marks as current. A page missing here does not exist.
var navFor = map[string]string{
	"index.html":                 "overview",
	"entries.html":               "entries",
	"entry.html":                 "entries",
	"entry_form.html":            "entries",
	"contacts.html":              "contacts",
	"contact_edit.html":          "contacts",
	"categories.html":            "categories",
	"category_edit.html":         "categories",
	"invoices.html":              "invoices",
	"invoice_form.html":          "invoices",
	"invoice_import.html":        "invoices",
	"invoice_import_review.html": "invoices",
	"invoice.html":               "invoices",
	"settings.html":              "settings",
	"bank.html":                  "bank",
	"bank_mapping.html":          "bank",
	"drafts.html":                "bank",
	"reports.html":               "reports",
	"vat.html":                   "vat",
	"receipt_form.html":          "receipts",
	"receipt_review.html":        "receipts",
	"help.html":                  "help",
	"assets.html":                "assets",
	"recurring.html":             "invoices",
	"recurring_form.html":        "invoices",
}

// render writes a page, or logs and 500s if the template fails. Templates are
// executed into a buffer first so a mid-render failure cannot emit half a page
// with a 200 already on the wire.
func (s *Server) render(w http.ResponseWriter, status int, page string, data any) {
	tmpl, ok := s.pages[page]
	if !ok {
		log.Printf("render: unknown page %q", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if pd, ok := data.(pageData); ok {
		if pd.Nav == "" {
			pd.Nav = navFor[page]
		}
		pd.Version = s.version
		pd.AuthEnabled = s.password != ""
		data = pd
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Printf("render %s: %v", page, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

// pageData is what every template gets: the app version plus whatever the
// handler adds.
type pageData struct {
	Version       string
	Nav           string // current navigation item, set by render
	AuthEnabled   bool
	Error         string
	Notice        string
	Entries       []store.Entry
	Contacts      []store.Contact
	Categories    []store.Category
	Invoices      []store.Invoice
	InvoiceDrafts []invoiceDraftRow
	ImportRows    []queueRow
	QueuedImports int    // invoices waiting for review in a bulk import
	Pending       int    // Belege of a bulk upload not read yet (ADR 0042)
	OwnCount      int    // open bank lines to or from the owner (ADR 0035)
	OwnBooked     int    // such lines booked as income or expense
	MailServer    string // the mail server set by the environment, "" if none
	// StoredMail is the mail server set in the web UI, without its
	// password (ADR 0040).
	StoredMail   *mailView
	Private      *store.PrivateTotals
	Transactions []store.BankTransaction
	Import       *store.ImportResult
	Columns      []string
	Preview      [][]string
	Threshold    *report.Threshold
	SVS          *report.SVS
	DATEVProblem string
	NoAccounts   []int64
	OCR          *ocr.Draft
	OCRAvailable bool
	PDFAvailable bool
	Totals       store.Totals
	Settings     store.Settings
	Form         map[string]string
	Lines        []lineForm
	Year         int

	// Overview and ledger.
	Entry       *store.Entry
	History     []store.DescriptionChange // a booking's descriptions, when changed (ADR 0043)
	Shares      []store.ShareChange       // a booking's business shares, when changed (ADR 0045)
	Page        *store.EntryPage
	Filter      url.Values
	Years       []int
	Chart       *chart
	Meter       *meter
	Unmatched   int
	PrevURL     string
	NextURL     string
	Contact     *store.Contact
	Usage       *store.ContactUsage
	Invoice     *store.Invoice
	Attachments []store.Attachment
	Missing     int // outgoing bookings without a Beleg
	Backup      *backupStatus
	HasLogo     bool
	Fresh       bool // a new installation: nothing in it but the default categories
	// Assets (ADR 0037): the booking's own, the Anlagenverzeichnis, and what
	// a booking would cost as an asset.
	Asset          *store.Asset
	Assets         []store.Asset
	AssetCostCents int64
	// Recurring invoices (ADR 0038): a list, the one in the form, and the
	// ones due soon.
	Recurring    []recurringRow
	RecurringOne *store.Recurring
	DueRecurring []recurringRow
	SureCount    int // transfers the bulk action would book
	// Kleinunternehmer coverage of the page's year or period (ADR 0022).
	KUCoverage      string
	KUPeriodText    string
	EntryVATBlocked bool
	Drafts          []draft
	LLMEnabled      bool
	LLMRunning      bool
	Periods         []store.VATPeriod
	VATTotal        *store.VATPeriod
	VATGroups       []invoice.VATGroup
	OpenCents       int64
	OverdueCents    int64
	CategoryRows    []store.CategoryTotals
	Category        *store.Category
	Month           int
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.store.Health(ctx); err != nil {
		log.Printf("healthz: %v", err)
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok"))
}

func (s *Server) handleEntryReverse(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad entry id", http.StatusBadRequest)
		return
	}

	_, err = s.store.ReverseEntry(r.Context(), id, time.Now().Format("2006-01-02"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "entry not found", http.StatusNotFound)
	case errors.Is(err, store.ErrAlreadyReversed):
		http.Error(w, "entry has already been reversed", http.StatusConflict)
	case err != nil:
		log.Printf("reverse entry %d: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	default:
		http.Redirect(w, r, "/entries/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	}
}

func (s *Server) handleContacts(w http.ResponseWriter, r *http.Request) {
	contacts, err := s.store.ListContacts(r.Context())
	if err != nil {
		log.Printf("list contacts: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	notice := ""
	if r.URL.Query().Get("deleted") == "1" {
		notice = s.T("Kontakt gelöscht.")
	}
	s.render(w, http.StatusOK, "contacts.html", pageData{
		Contacts: contacts, Form: map[string]string{"kind": "customer"}, Notice: notice,
	})
}

func (s *Server) handleContactCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	c := store.Contact{
		Kind:    r.FormValue("kind"),
		Name:    r.FormValue("name"),
		UID:     r.FormValue("uid"),
		Address: r.FormValue("address"),
		Email:   r.FormValue("email"),
	}
	if _, err := s.store.CreateContact(r.Context(), c); err != nil {
		contacts, _ := s.store.ListContacts(r.Context())
		s.render(w, http.StatusUnprocessableEntity, "contacts.html", pageData{
			Version: s.version, Contacts: contacts, Error: s.errText(err),
			Form: map[string]string{
				"kind": c.Kind, "name": c.Name, "uid": c.UID,
				"address": c.Address, "email": c.Email,
			},
		})
		return
	}
	http.Redirect(w, r, "/contacts", http.StatusSeeOther)
}

// Categories exist mainly to carry the Konto/Gegenkonto defaults (ADR 0012):
// the account framework is configured once, here, by whoever knows it, instead
// of retyped on every booking by whoever does not.
func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := s.store.ListCategories(r.Context())
	if err != nil {
		log.Printf("list categories: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, http.StatusOK, "categories.html", pageData{
		Version: s.version, Categories: categories,
		Form: map[string]string{"direction": "outgoing"},
	})
}

func (s *Server) handleCategoryCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	c := store.Category{
		Name:              r.FormValue("name"),
		Direction:         r.FormValue("direction"),
		DefaultKonto:      strings.TrimSpace(r.FormValue("default_konto")),
		DefaultGegenkonto: strings.TrimSpace(r.FormValue("default_gegenkonto")),
		Keywords:          r.FormValue("keywords"),
		DefaultVATRate:    vatParam(r.FormValue("default_vat_rate")),
		DeductiblePct:     pctParam(r.FormValue("deductible_pct")),
	}
	if _, err := s.store.CreateCategory(r.Context(), c); err != nil {
		categories, _ := s.store.ListCategories(r.Context())
		s.render(w, http.StatusUnprocessableEntity, "categories.html", pageData{
			Version: s.version, Categories: categories, Error: s.errText(err),
			Form: map[string]string{
				"name": c.Name, "direction": c.Direction,
				"default_konto": c.DefaultKonto, "default_gegenkonto": c.DefaultGegenkonto,
				"keywords": c.Keywords, "default_vat_rate": r.FormValue("default_vat_rate"), "deductible_pct": r.FormValue("deductible_pct"),
			},
		})
		return
	}
	http.Redirect(w, r, "/categories", http.StatusSeeOther)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		log.Printf("settings: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	notice := ""
	switch {
	case r.URL.Query().Get("saved") == "1":
		notice = s.T("Einstellungen gespeichert.")
	case r.URL.Query().Get("logo") == "1":
		notice = s.T("Logo gespeichert. Es erscheint auf allen Rechnungs-PDFs.")
	case r.URL.Query().Get("logo") == "0":
		notice = s.T("Logo entfernt.")
	case r.URL.Query().Get("mailremoved") == "1":
		notice = s.T("Mailserver entfernt, samt Passwort.")
	case r.URL.Query().Get("mailtest") == "1":
		notice = s.T("Test-Mail verschickt. Bitte im Postfach nachsehen, auch im Spam-Ordner.")
	case r.URL.Query().Get("restored") != "":
		notice = s.Tf("Sicherung übernommen: %s Datensätze, geprüft. Die Bücher sind wieder da.", r.URL.Query().Get("restored"))
	}
	s.renderSettings(w, r, http.StatusOK, settings, notice, "")
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, status int, settings store.Settings, notice, errText string) {
	bs, err := s.backupStatus(r)
	if err != nil {
		s.fail(w, "backup status", err)
		return
	}
	_, hasLogo, err := s.store.GetLogo(r.Context())
	if err != nil {
		s.fail(w, "read logo", err)
		return
	}
	fresh, err := s.store.Fresh(r.Context())
	if err != nil {
		s.fail(w, "check fresh", err)
		return
	}
	years, withData, err := s.store.ExportYears(r.Context())
	if err != nil {
		s.fail(w, "export years", err)
		return
	}
	// The tax adviser usually gets the year that has just ended.
	exportYear := time.Now().Year()
	if withData[exportYear-1] {
		exportYear--
	}
	var storedMail *mailView
	if m, ok, err := s.store.GetMailServer(r.Context()); err != nil {
		s.fail(w, "mail server", err)
		return
	} else if ok {
		storedMail = &mailView{Host: m.Host, Port: m.Port, TLS: m.TLS, User: m.Username, From: m.From, HasPassword: m.Password != ""}
	}
	s.render(w, status, "settings.html", pageData{
		Version: s.version, Settings: settings, Notice: notice, Error: errText, Backup: &bs, HasLogo: hasLogo, Fresh: fresh, MailServer: s.envMailServer, StoredMail: storedMail,
		Years: years, Year: exportYear,
	})
}

// settingsError shows the settings page with the stored values and a message.
func (s *Server) settingsError(w http.ResponseWriter, r *http.Request, msg string) {
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	s.renderSettings(w, r, http.StatusUnprocessableEntity, settings, "", msg)
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	settings := store.Settings{
		IssuerName:            r.FormValue("issuer_name"),
		IssuerAddress:         r.FormValue("issuer_address"),
		IssuerUID:             r.FormValue("issuer_uid"),
		IssuerIBAN:            r.FormValue("issuer_iban"),
		IssuerBIC:             r.FormValue("issuer_bic"),
		IssuerContact:         r.FormValue("issuer_contact"),
		InvoiceNumbering:      r.FormValue("invoice_numbering"),
		InvoicePrefix:         r.FormValue("invoice_prefix"),
		Kleinunternehmer:      r.FormValue("kleinunternehmer") == "1",
		KleinunternehmerFrom:  s.parseDate(r.FormValue("kleinunternehmer_from")),
		KleinunternehmerUntil: s.parseDate(r.FormValue("kleinunternehmer_until")),
		DateFormat:            r.FormValue("date_format"),
		SVSExemption:          r.FormValue("svs_exemption") == "1",
		DATEV:                 r.FormValue("datev_enabled") == "1",
		NotifyEmail:           r.FormValue("notify_email"),
		NotifyRecurring:       r.FormValue("notify_recurring") == "1",
		NotifyOverdue:         r.FormValue("notify_overdue") == "1",
		NotifyBackup:          r.FormValue("notify_backup") == "1",
		NotifyQueues:          r.FormValue("notify_queues") == "1",
		Language:              r.FormValue("language"),
		DATEVConsultantNumber: r.FormValue("datev_consultant_number"),
		DATEVClientNumber:     r.FormValue("datev_client_number"),
		DATEVAccountLength:    r.FormValue("datev_account_length"),
	}
	terms, err := strconv.Atoi(strings.TrimSpace(r.FormValue("payment_terms_days")))
	if err != nil && strings.TrimSpace(r.FormValue("payment_terms_days")) != "" {
		terms = -1 // let SaveSettings reject it with its own message
	}
	settings.PaymentTermsDays = terms
	// A missing field must not silently become 0 % (ADR 0021).
	settings.DefaultVATOutgoing = 20
	if v := strings.TrimSpace(r.FormValue("default_vat_outgoing")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			n = -1 // rejected by SaveSettings with its own message
		}
		settings.DefaultVATOutgoing = n
	}
	// The mail server is checked before anything is saved, so a typo there
	// does not leave the other settings half written.
	mail, mailSet, err := s.mailServerFromForm(r)
	if err != nil {
		s.renderSettings(w, r, http.StatusUnprocessableEntity, settings, "", s.errText(err))
		return
	}
	if err := s.store.SaveSettings(r.Context(), settings); err != nil {
		s.renderSettings(w, r, http.StatusUnprocessableEntity, settings, "", s.errText(err))
		return
	}
	if mailSet {
		if err := s.store.SaveMailServer(r.Context(), mail); err != nil {
			s.fail(w, "save mail server", err)
			return
		}
	}
	s.loadPrefs()
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// mailServerFromForm reads the mail server fields (ADR 0040). An empty
// password keeps the stored one, so it never has to travel to the page.
// set is false when the form has no mail server, or when the environment
// provides it.
func (s *Server) mailServerFromForm(r *http.Request) (store.MailServer, bool, error) {
	host := strings.TrimSpace(r.FormValue("smtp_host"))
	if s.envMailer != nil || host == "" {
		return store.MailServer{}, false, nil
	}
	m := store.MailServer{Host: host, TLS: r.FormValue("smtp_tls"), Username: strings.TrimSpace(r.FormValue("smtp_user")),
		Password: r.FormValue("smtp_password"), From: strings.TrimSpace(r.FormValue("smtp_from"))}
	if m.TLS == "" {
		m.TLS = notify.StartTLS
	}
	port, err := strconv.Atoi(strings.TrimSpace(r.FormValue("smtp_port")))
	if err != nil {
		port = map[string]int{notify.TLS: 465, notify.NoTLS: 25}[m.TLS]
		if port == 0 {
			port = 587
		}
	}
	m.Port = port
	if m.Password == "" {
		if stored, ok, err := s.store.GetMailServer(r.Context()); err != nil {
			return store.MailServer{}, false, err
		} else if ok {
			m.Password = stored.Password
		}
	}
	if err := notify.FromStore(m).Check(); err != nil {
		return store.MailServer{}, false, err
	}
	return m, true, nil
}

// handleMailServerDelete removes the mail server set in the web UI.
func (s *Server) handleMailServerDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteMailServer(r.Context()); err != nil {
		s.fail(w, "delete mail server", err)
		return
	}
	http.Redirect(w, r, "/settings?mailremoved=1#notify", http.StatusSeeOther)
}

func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	f, err := web.Static.Open("sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	// The worker itself must not be cached by the browser, or a fix to it can
	// never reach an installed app.
	w.Header().Set("Cache-Control", "no-cache")
	io.Copy(w, f)
}

// mailView is the stored mail server as the settings page shows it: the
// password only as "set" or "not set".
type mailView struct {
	Host        string
	Port        int
	TLS         string
	User        string
	From        string
	HasPassword bool
}

// pctParam reads a deductible share; empty or unreadable means 100 %.
func pctParam(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 100
	}
	return n
}

// vatParam reads an optional VAT rate field: empty means none. Anything that
// is not a number is passed on as -1 so the store rejects it with its message.
func vatParam(v string) *int {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		n = -1
	}
	return &n
}

// prefs are user display preferences from settings.
type prefs struct {
	dateLayout string
	lang       string
	datev      bool
}

// loadPrefs reads the display preferences. It runs at start and after every
// settings save; a failed read keeps the defaults rather than failing pages.
func (s *Server) loadPrefs() {
	p := &prefs{dateLayout: store.DefaultDateFormat, lang: i18n.Default}
	if st, err := s.store.Settings(context.Background()); err == nil {
		p.dateLayout = st.DateFormat
		if _, ok := i18n.Languages[st.Language]; ok {
			p.lang = st.Language
		}
		p.datev = st.DATEV
	}
	s.prefs.Store(p)
}

// fmtDate renders 'YYYY-MM-DD' in the chosen date format. Anything
// unparseable is returned unchanged rather than hidden.
func (s *Server) fmtDate(iso string) string {
	return formatDate(iso, s.prefs.Load().dateLayout)
}

// parseDate reads a date typed in the chosen format, or as ISO, and returns
// it as ISO. The date fields are text fields because a browser draws
// <input type="date"> in its own locale and ignores the setting. Single-digit
// days and months are accepted. Anything else comes back unchanged, so the
// store rejects it with its own message.
func (s *Server) parseDate(v string) string {
	v = strings.TrimSpace(v)
	layout := s.prefs.Load().dateLayout
	short := strings.NewReplacer("02", "2", "01", "1").Replace(layout)
	for _, l := range []string{"2006-01-02", layout, short} {
		if t, err := time.Parse(l, v); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return v
}

// dateLayout is the chosen date format as a Go layout, for app.js.
func (s *Server) dateLayout() string { return s.prefs.Load().dateLayout }

// lang is the current UI language.
func (s *Server) lang() string { return s.prefs.Load().lang }

// T translates a message into the UI language (ADR 0024).
func (s *Server) T(msg string) string { return i18n.T(s.lang(), msg) }

// Tf translates a format string and fills it in.
func (s *Server) Tf(msg string, args ...any) string { return i18n.Tf(s.lang(), msg, args...) }

// errText translates an error message for the user. Store errors are
// written in English and the de catalog maps them; a few package errors are
// written in German and the en catalog maps them. Anything without an entry,
// such as a message with values in it, is shown as written.
func (s *Server) errText(err error) string { return i18n.T(s.lang(), err.Error()) }

// eur formats an amount for the UI language.
func (s *Server) eur(cents int64) string { return money.FormatLang(cents, s.lang()) }
