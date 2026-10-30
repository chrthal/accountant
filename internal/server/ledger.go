package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/report"
	"github.com/chrthal/accountant/internal/store"
)

// handleIndex is the overview: the year at a glance, and what needs doing.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	year := yearParam(r)

	totals, err := s.store.Totals(ctx, year)
	if err != nil {
		s.fail(w, "totals", err)
		return
	}
	months, err := s.store.MonthlyTotals(ctx, year)
	if err != nil {
		s.fail(w, "monthly totals", err)
		return
	}
	threshold, err := s.store.Threshold(ctx, year)
	if err != nil {
		s.fail(w, "threshold", err)
		return
	}
	recent, err := s.store.FindEntries(ctx, store.EntryFilter{Year: year, Limit: 8})
	if err != nil {
		s.fail(w, "recent entries", err)
		return
	}
	unmatched, err := s.store.CountUnmatchedBank(ctx)
	if err != nil {
		s.fail(w, "unmatched bank", err)
		return
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	missing, err := s.store.CountWithoutReceipt(ctx, year)
	if err != nil {
		s.fail(w, "without receipt", err)
		return
	}
	open, err := s.store.OpenInvoices(ctx)
	if err != nil {
		s.fail(w, "open invoices", err)
		return
	}
	var openCents, overdueCents int64
	for _, inv := range open {
		openCents += inv.OpenCents()
		if inv.IsOverdue() {
			overdueCents += inv.OpenCents()
		}
	}

	bs, err := s.backupStatus(r)
	if err != nil {
		s.fail(w, "backup status", err)
		return
	}

	current := 0
	if now := time.Now(); now.Year() == year {
		current = int(now.Month())
	}
	svs := report.ComputeSVS(year, threshold.RevenueCents, totals.ProfitCents())
	c := buildChart(months, current)
	for i := range c.Bars {
		c.Bars[i].Label = s.T(c.Bars[i].Label)
	}
	m := buildMeter(threshold)
	// Only a new installation can be fresh, and it has no sender yet.
	fresh := false
	if settings.IssuerName == "" {
		if fresh, err = s.store.Fresh(ctx); err != nil {
			s.fail(w, "check fresh", err)
			return
		}
	}
	due, err := s.dueRecurring(r, settings.Kleinunternehmer)
	if err != nil {
		s.fail(w, "due recurring", err)
		return
	}
	s.render(w, http.StatusOK, "index.html", pageData{
		Fresh: fresh, DueRecurring: due,
		Year: year, Totals: totals, Threshold: &threshold, Chart: &c, Meter: &m,
		Entries: recent.Entries, Unmatched: unmatched, Settings: settings,
		Missing: missing, Invoices: open, OpenCents: openCents, OverdueCents: overdueCents,
		Backup: &bs, SVS: &svs, KUCoverage: settings.KUCoverage(fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year)),
	})
}

// handleEntries is the ledger: every booking, filterable, paged.
func (s *Server) handleEntries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	const perPage = 50
	f := store.EntryFilter{
		Direction: q.Get("direction"),
		Query:     q.Get("q"),
		Limit:     perPage,
	}
	// The year defaults to the current one; "alle" lifts the restriction.
	switch y := q.Get("year"); y {
	case "alle":
	case "":
		f.Year = time.Now().Year()
	default:
		f.Year, _ = strconv.Atoi(y)
	}
	f.Month, _ = strconv.Atoi(q.Get("month"))
	f.CategoryID, _ = strconv.ParseInt(q.Get("category"), 10, 64)
	f.WithoutReceipt = q.Get("beleg") == "fehlt"
	pageNo, _ := strconv.Atoi(q.Get("page"))
	pageNo = max(pageNo, 1)
	f.Offset = (pageNo - 1) * perPage

	page, err := s.store.FindEntries(ctx, f)
	if err != nil {
		s.fail(w, "find entries", err)
		return
	}
	categories, err := s.store.ListCategories(ctx)
	if err != nil {
		s.fail(w, "list categories", err)
		return
	}
	years, err := s.store.BookingYears(ctx)
	if err != nil {
		s.fail(w, "booking years", err)
		return
	}

	// The filter form's own values, with the defaults made explicit so the
	// selects show what is actually applied.
	filter := url.Values{}
	for _, k := range []string{"q", "direction", "month", "category", "beleg"} {
		if v := q.Get(k); v != "" {
			filter.Set(k, v)
		}
	}
	if f.Year > 0 {
		filter.Set("year", strconv.Itoa(f.Year))
	} else {
		filter.Set("year", "alle")
	}

	waiting, err := s.store.ImportQueue(ctx, store.ReceiptQueue)
	if err != nil {
		s.fail(w, "receipt queue", err)
		return
	}
	data := pageData{
		Page: &page, Filter: filter, Years: years, Categories: categories, Year: f.Year,
		QueuedImports: len(waiting),
	}
	if n := q.Get("booked_bulk"); n != "" {
		data.Notice = s.Tf("%s Beleg(e) gebucht.", n)
	}
	link := func(n int) string {
		v := url.Values{}
		for k := range filter {
			v.Set(k, filter.Get(k))
		}
		v.Set("page", strconv.Itoa(n))
		return "/entries?" + v.Encode()
	}
	if pageNo > 1 {
		data.PrevURL = link(pageNo - 1)
	}
	if f.Offset+len(page.Entries) < page.Total {
		data.NextURL = link(pageNo + 1)
	}
	s.render(w, http.StatusOK, "entries.html", data)
}

// handleEntry shows one booking with everything attached to it.
func (s *Server) handleEntry(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	msg := ""
	for _, wv := range strings.Split(r.URL.Query().Get("warn"), ",") {
		switch wv {
		case "beleg":
			msg += s.T("Die Buchung ist gespeichert, der Beleg konnte aber nicht abgelegt werden. Bitte unten erneut anhängen.") + " "
		case "bank":
			msg += s.T("Die Buchung ist gespeichert, passt aber nicht zum Bankumsatz (Betrag oder Richtung geändert?). Bitte unter Bank zuordnen.") + " "
		}
	}
	s.renderEntry(w, r, id, http.StatusOK, strings.TrimSpace(msg))
}

func (s *Server) renderEntry(w http.ResponseWriter, r *http.Request, id int64, status int, errMsg string) {
	e, err := s.store.GetEntry(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "get entry", err)
		return
	}
	bank, err := s.store.BankTransactionsForEntry(r.Context(), id)
	if err != nil {
		s.fail(w, "bank for entry", err)
		return
	}
	attachments, err := s.store.ListAttachments(r.Context(), id)
	if err != nil {
		s.fail(w, "list attachments", err)
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	asset, isAsset, err := s.store.AssetForEntry(r.Context(), id)
	if err != nil {
		s.fail(w, "asset for entry", err)
		return
	}
	history, err := s.store.DescriptionHistory(r.Context(), id)
	if err != nil {
		s.fail(w, "description history", err)
		return
	}
	shares, err := s.store.ShareHistory(r.Context(), id)
	if err != nil {
		s.fail(w, "share history", err)
		return
	}
	pd := pageData{
		History: history, Shares: shares,
		Entry: &e, Transactions: bank, Attachments: attachments, Error: errMsg, Settings: settings,
		EntryVATBlocked: e.Direction == "outgoing" && settings.KUOn(e.BookedOn),
		Year:            time.Now().Year(),
	}
	if isAsset {
		pd.Asset = &asset
	}
	// What the asset would cost, and whether it is above the GWG limit:
	// net, or gross where the VAT was not deductible (ADR 0037).
	pd.AssetCostCents = e.NetCents
	if pd.EntryVATBlocked {
		pd.AssetCostCents = e.AmountGrossCents
	}
	s.render(w, status, "entry.html", pd)
}

// handleEntryRename changes a booking's description (ADR 0043). The booking
// stays as it is; the change is added to its history.
func (s *Server) handleEntryRename(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || r.ParseForm() != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	err = s.store.RenameEntry(r.Context(), id, r.FormValue("description"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrSameDescription), err == nil:
		http.Redirect(w, r, "/entries/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	default:
		s.renderEntry(w, r, id, http.StatusUnprocessableEntity, s.errText(err))
	}
}

// handleEntryShare changes the business share of an expense (ADR 0045). The
// booking stays as it is; the change is added to its history.
func (s *Server) handleEntryShare(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || r.ParseForm() != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	pct, err := businessPct(r.FormValue("business_pct"))
	if err != nil {
		s.renderEntry(w, r, id, http.StatusUnprocessableEntity, s.T("Der betriebliche Anteil muss zwischen 1 und 100 % liegen."))
		return
	}
	err = s.store.ChangeBusinessShare(r.Context(), id, pct, r.FormValue("business_note"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrSameShare), err == nil:
		http.Redirect(w, r, "/entries/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	default:
		s.renderEntry(w, r, id, http.StatusUnprocessableEntity, s.errText(err))
	}
}

// handleAssetCreate writes an expense off over its useful life (ADR 0037).
func (s *Server) handleAssetCreate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || r.ParseForm() != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	years, err := strconv.Atoi(strings.TrimSpace(r.FormValue("years")))
	if err != nil {
		s.renderEntry(w, r, id, http.StatusUnprocessableEntity, s.T("Bitte die Nutzungsdauer in Jahren angeben."))
		return
	}
	if _, err := s.store.CreateAsset(r.Context(), id, r.FormValue("name"), years, s.parseDate(r.FormValue("in_use_from"))); err != nil {
		s.renderEntry(w, r, id, http.StatusUnprocessableEntity, s.errText(err))
		return
	}
	http.Redirect(w, r, "/entries/"+strconv.FormatInt(id, 10)+"#anlage", http.StatusSeeOther)
}

func (s *Server) handleAssetDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || r.ParseForm() != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteAsset(r.Context(), id); err != nil {
		s.fail(w, "delete asset", err)
		return
	}
	back := "/assets"
	if e, err := strconv.ParseInt(r.FormValue("entry_id"), 10, 64); err == nil {
		back = "/entries/" + strconv.FormatInt(e, 10) + "#anlage"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// handleAssets is the Anlagenverzeichnis of a year.
func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	year := yearParam(r)
	assets, err := s.store.AssetsInYear(r.Context(), year)
	if err != nil {
		s.fail(w, "assets", err)
		return
	}
	s.render(w, http.StatusOK, "assets.html", pageData{Year: year, Assets: assets})
}

func (s *Server) handleContactEdit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	c, err := s.store.GetContact(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "get contact", err)
		return
	}
	usage, err := s.store.ContactUsage(r.Context(), id)
	if err != nil {
		s.fail(w, "contact usage", err)
		return
	}
	recurring, err := s.store.ListRecurring(r.Context(), id)
	if err != nil {
		s.fail(w, "list recurring", err)
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	s.render(w, http.StatusOK, "contact_edit.html", pageData{Contact: &c, Usage: &usage,
		Recurring: s.recurringRows(recurring, settings.Kleinunternehmer)})
}

func (s *Server) handleContactUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || r.ParseForm() != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	c := store.Contact{
		ID: id, Kind: r.FormValue("kind"), Name: r.FormValue("name"),
		UID: r.FormValue("uid"), Address: r.FormValue("address"), Email: r.FormValue("email"),
	}
	switch err := s.store.UpdateContact(r.Context(), c); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		usage, _ := s.store.ContactUsage(r.Context(), id)
		s.render(w, http.StatusUnprocessableEntity, "contact_edit.html", pageData{Contact: &c, Usage: &usage, Error: s.errText(err)})
	default:
		http.Redirect(w, r, "/contacts", http.StatusSeeOther)
	}
}

func (s *Server) handleCategoryEdit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	c, err := s.store.GetCategory(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "get category", err)
		return
	}
	s.render(w, http.StatusOK, "category_edit.html", pageData{Category: &c})
}

func (s *Server) handleCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || r.ParseForm() != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	c := store.Category{
		ID: id, Name: r.FormValue("name"), Direction: r.FormValue("direction"),
		DefaultKonto:      strings.TrimSpace(r.FormValue("default_konto")),
		DefaultGegenkonto: strings.TrimSpace(r.FormValue("default_gegenkonto")),
		Keywords:          r.FormValue("keywords"),
		DefaultVATRate:    vatParam(r.FormValue("default_vat_rate")),
		DeductiblePct:     pctParam(r.FormValue("deductible_pct")),
	}
	switch err := s.store.UpdateCategory(r.Context(), c); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.render(w, http.StatusUnprocessableEntity, "category_edit.html", pageData{Category: &c, Error: s.errText(err)})
	default:
		http.Redirect(w, r, "/categories", http.StatusSeeOther)
	}
}

// fail logs an unexpected error and answers 500 without leaking it.
func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	log.Printf("%s: %v", what, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// handleContactDelete removes a contact nothing refers to (store.DeleteContact).
func (s *Server) handleContactDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch err := s.store.DeleteContact(r.Context(), id); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrContactInUse):
		c, _ := s.store.GetContact(r.Context(), id)
		usage, _ := s.store.ContactUsage(r.Context(), id)
		s.render(w, http.StatusConflict, "contact_edit.html", pageData{Contact: &c, Usage: &usage,
			Error: s.T("Dieser Kontakt wird von Rechnungen oder Buchungen verwendet und kann nicht gelöscht werden.")})
	case err != nil:
		s.fail(w, "delete contact", err)
	default:
		http.Redirect(w, r, "/contacts?deleted=1", http.StatusSeeOther)
	}
}
