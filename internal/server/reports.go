package server

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/export"
	"github.com/chrthal/accountant/internal/invoice"
	"github.com/chrthal/accountant/internal/report"
	"github.com/chrthal/accountant/internal/store"
)

// year reads the ?year= parameter, defaulting to the current one.
func yearParam(r *http.Request) int {
	if v := r.URL.Query().Get("year"); v != "" {
		if y, err := strconv.Atoi(v); err == nil && y > 1990 && y < 2200 {
			return y
		}
	}
	return time.Now().Year()
}

func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	year := yearParam(r)

	totals, err := s.store.Totals(ctx, year)
	if err != nil {
		log.Printf("totals: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	threshold, err := s.store.Threshold(ctx, year)
	if err != nil {
		log.Printf("threshold: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		log.Printf("settings: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Surface what the DATEV export is still missing here, where the user is
	// about to click it, rather than only when it fails.
	datevCfg, err := s.store.DATEVConfig(ctx, year)
	if err != nil {
		log.Printf("datev config: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	datevProblem := ""
	if err := datevCfg.Validate(); err != nil {
		datevProblem = s.errText(err)
	}

	// The other thing DATEV needs and the ledger does not: an account tag on
	// every booking (ADR 0012). Listing the offenders here beats a 422 that
	// names them only after the click.
	bookings, err := s.store.BookingsForExport(ctx, year)
	if err != nil {
		log.Printf("bookings for export: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	noAccounts := export.MissingAccounts(bookings)

	categories, err := s.store.CategoryBreakdown(ctx, year)
	if err != nil {
		log.Printf("category breakdown: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	m := buildMeter(threshold)
	svs := report.ComputeSVS(year, threshold.RevenueCents, totals.ProfitCents())
	private, err := s.store.PrivateTotalsFor(r.Context(), year)
	if err != nil {
		s.fail(w, "private totals", err)
		return
	}

	s.render(w, http.StatusOK, "reports.html", pageData{
		Private:      &private,
		Version:      s.version,
		Year:         year,
		Totals:       totals,
		Threshold:    &threshold,
		Settings:     settings,
		DATEVProblem: datevProblem,
		NoAccounts:   noAccounts,
		CategoryRows: categories,
		SVS:          &svs,
		Meter:        &m,
	})
}

func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	year := yearParam(r)
	bookings, err := s.store.BookingsForExport(r.Context(), year)
	if err != nil {
		log.Printf("bookings for export: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	atts, err := s.store.AttachmentsForYear(r.Context(), year)
	if err != nil {
		s.fail(w, "attachments for export", err)
		return
	}
	names := map[int64][]string{}
	for _, a := range atts {
		names[a.EntryID] = append(names[a.EntryID], a.Filename)
	}
	for i := range bookings {
		bookings[i].Receipts = names[bookings[i].ID]
	}

	assetRows, err := s.store.AssetRows(r.Context(), year)
	if err != nil {
		s.fail(w, "asset rows", err)
		return
	}
	out := export.CSV(bookings, assetRows...)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", export.FileName("Buchungen", year, "csv")))
	w.Write(out)
}

func (s *Server) handleExportDATEV(w http.ResponseWriter, r *http.Request) {
	if !s.prefs.Load().datev {
		http.Error(w, s.T("Der DATEV-Export ist in den Einstellungen ausgeschaltet."), http.StatusNotFound)
		return
	}
	ctx := r.Context()
	year := yearParam(r)

	cfg, err := s.store.DATEVConfig(ctx, year)
	if err != nil {
		log.Printf("datev config: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	bookings, err := s.store.BookingsForExport(ctx, year)
	if err != nil {
		log.Printf("bookings for export: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	out, err := export.DATEV(bookings, cfg, year, time.Now())
	if err != nil {
		// Missing Berater-/Mandanten-Nummer is a configuration problem the user
		// can fix, not a server fault.
		http.Error(w, s.errText(err), http.StatusUnprocessableEntity)
		return
	}

	// cp1252, because that is what DATEV reads (ADR 0010).
	w.Header().Set("Content-Type", "text/csv; charset=windows-1252")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", export.FileName("EXTF-Buchungsstapel", year, "csv")))
	w.Write(out)
}

// handleVAT is the VAT overview per month or quarter (plan 0002, Phase 11).
func (s *Server) handleVAT(w http.ResponseWriter, r *http.Request) {
	year := yearParam(r)
	quarterly := r.URL.Query().Get("period") != "month"
	periods, err := s.store.VATPeriods(r.Context(), year, quarterly)
	if err != nil {
		s.fail(w, "vat periods", err)
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	var total store.VATPeriod
	for _, p := range periods {
		total.OutputNetCents += p.OutputNetCents
		total.OutputVATCents += p.OutputVATCents
		total.InputNetCents += p.InputNetCents
		total.InputVATCents += p.InputVATCents
		total.ExemptNetCents += p.ExemptNetCents
		total.InputVATBlockedCents += p.InputVATBlockedCents
		total.InputVATPrivateCents += p.InputVATPrivateCents
	}
	period := "quarter"
	if !quarterly {
		period = "month"
	}
	s.render(w, http.StatusOK, "vat.html", pageData{
		Year: year, Periods: periods, VATTotal: &total, Settings: settings,
		KUCoverage:   settings.KUCoverage(fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year)),
		KUPeriodText: s.kuPeriodText(settings),
		Form:         map[string]string{"period": period},
	})
}

// kuPeriodText describes the exempt period in words: "seit 01.01.2025",
// "bis 30.06.2026", or "01.01.2025 bis 30.06.2026".
func (s *Server) kuPeriodText(st store.Settings) string {
	p, ok := st.KUPeriod()
	switch {
	case !ok:
		return ""
	case p.From != "" && p.Until != "":
		return s.Tf("%s bis %s", s.fmtDate(p.From), s.fmtDate(p.Until))
	case p.Until != "":
		return s.Tf("bis %s", s.fmtDate(p.Until))
	case p.From != "":
		return s.Tf("seit %s", s.fmtDate(p.From))
	}
	return s.T("ohne Enddatum")
}

// handleExportArchive sends the year as a ZIP that can be read without this
// app: the bookings CSV, their Belege and the issued invoices (ADR 0029).
func (s *Server) handleExportArchive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	year := yearParam(r)
	bookings, err := s.store.BookingsForExport(ctx, year)
	if err != nil {
		s.fail(w, "bookings for archive", err)
		return
	}
	atts, err := s.store.AttachmentsForYear(ctx, year)
	if err != nil {
		s.fail(w, "attachments for archive", err)
		return
	}
	invoiceIDs, err := s.store.InvoiceIDsForYear(ctx, year)
	if err != nil {
		s.fail(w, "invoices for archive", err)
		return
	}
	logo, err := s.invoiceLogo(ctx)
	if err != nil {
		s.fail(w, "read logo", err)
		return
	}

	paths := map[int64][]string{}
	var receipts []export.ArchiveFile
	for _, a := range atts {
		p := export.ReceiptPath(year, a.BookedOn, a.EntryID, a.ID, a.Filename)
		paths[a.EntryID] = append(paths[a.EntryID], strings.TrimPrefix(p, export.ArchiveRoot(year)+"/"))
		id := a.ID
		receipts = append(receipts, export.ArchiveFile{Path: p, Open: func() ([]byte, error) {
			full, err := s.store.GetAttachment(ctx, id)
			return full.Data, err
		}})
	}
	for i := range bookings {
		bookings[i].Receipts = paths[bookings[i].ID]
	}
	var invoices []export.ArchiveFile
	for _, id := range invoiceIDs {
		inv, err := s.store.GetInvoice(ctx, id)
		if err != nil {
			s.fail(w, "get invoice for archive", err)
			return
		}
		invoices = append(invoices, export.ArchiveFile{Path: export.InvoicePath(year, inv.Number), Open: func() ([]byte, error) {
			if inv.Imported {
				o, err := s.store.InvoiceOriginal(ctx, inv.ID)
				return o.Data, err
			}
			return invoice.RenderPDF(inv.Document(), logo)
		}})
	}

	assetRows, err := s.store.AssetRows(ctx, year)
	if err != nil {
		s.fail(w, "asset rows for archive", err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", export.ArchiveRoot(year)+".zip"))
	// The status line is out once the ZIP starts; a failure after that can
	// only cut the download short. A ZIP without its closing central
	// directory is refused by every unzip tool, so it cannot pass as complete.
	if err := export.WriteArchive(w, year, export.CSV(bookings, assetRows...), receipts, invoices, time.Now()); err != nil {
		log.Printf("write archive %d: %v", year, err)
	}
}
