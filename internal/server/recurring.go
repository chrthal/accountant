package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/invoice"
	"github.com/chrthal/accountant/internal/money"
	"github.com/chrthal/accountant/internal/store"
)

// Recurring invoices (ADR 0038): templates the app reminds about. Nothing is
// sent or issued by itself; "Rechnung erstellen" opens the ordinary invoice
// form, and issuing it moves the next date on.

// dueWithinDays is how early a recurring invoice shows up as due, so there is
// time to prepare it.
const dueWithinDays = 7

// fillPeriod puts the invoiced period in for {Zeitraum} or {Period}; the
// English UI names the second.
func fillPeriod(text, period string) string {
	return strings.NewReplacer("{Zeitraum}", period, "{Period}", period).Replace(text)
}

// recurringRow is a recurring invoice as lists show it.
type recurringRow struct {
	store.Recurring
	GrossCents int64
	Due        bool
	Overdue    bool
}

func (s *Server) recurringRows(list []store.Recurring, ku bool) []recurringRow {
	now := time.Now()
	out := make([]recurringRow, 0, len(list))
	for _, r := range list {
		row := recurringRow{Recurring: r, Due: r.Due(now, dueWithinDays), Overdue: r.Overdue(now)}
		for _, l := range r.Lines {
			qty, err1 := money.ParseQuantity(l.Quantity)
			price, err2 := money.Parse(l.UnitPrice)
			rate, err3 := strconv.Atoi(l.VATRate)
			if err1 != nil || err2 != nil || err3 != nil {
				continue
			}
			net := money.LineNet(price, qty)
			row.GrossCents += net
			if !ku {
				row.GrossCents += money.VATFromNet(net, rate)
			}
		}
		out = append(out, row)
	}
	return out
}

// dueRecurring is the recurring invoices due within dueWithinDays, for the
// reminders on the overview and the invoice list.
func (s *Server) dueRecurring(r *http.Request, ku bool) ([]recurringRow, error) {
	due, err := s.store.DueRecurring(r.Context(), time.Now(), dueWithinDays)
	if err != nil {
		return nil, err
	}
	return s.recurringRows(due, ku), nil
}

func (s *Server) handleRecurringList(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListRecurring(r.Context(), 0)
	if err != nil {
		s.fail(w, "list recurring", err)
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	notice := map[string]string{
		"saved":   s.T("Wiederkehrende Rechnung gespeichert."),
		"skipped": s.T("Zeitraum übersprungen; die nächste Rechnung ist einen Zeitraum später fällig."),
		"deleted": s.T("Wiederkehrende Rechnung gelöscht."),
	}[r.URL.Query().Get("done")]
	s.render(w, http.StatusOK, "recurring.html", pageData{Recurring: s.recurringRows(list, settings.Kleinunternehmer), Notice: notice})
}

// handleRecurringNew opens an empty form, one for a customer, or one copied
// from an issued invoice ("Als wiederkehrend anlegen").
func (s *Server) handleRecurringNew(w http.ResponseWriter, r *http.Request) {
	form := map[string]string{"interval": "monthly", "active": "1", "contact_id": r.URL.Query().Get("contact")}
	var lines []lineForm
	if from, err := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64); err == nil {
		inv, err := s.store.GetInvoice(r.Context(), from)
		if err == nil {
			form["contact_id"] = strconv.FormatInt(inv.ContactID, 10)
			form["note"] = strings.TrimSpace(strings.TrimPrefix(inv.Note, invoice.KleinunternehmerNote))
			if next, err := store.NextDate(inv.IssueDate, "monthly"); err == nil {
				form["next_due"] = next
			}
			if len(inv.Lines) > 0 {
				form["name"] = inv.Lines[0].Description
			}
			for _, l := range inv.Lines {
				lines = append(lines, lineForm{Description: l.Description, Quantity: money.FormatQuantity(l.QuantityMilli),
					Unit: l.Unit, UnitPrice: money.Format(l.UnitPriceCents), VATRate: strconv.Itoa(l.VATRate)})
			}
		}
	}
	if form["next_due"] == "" {
		form["next_due"] = time.Now().Format("2006-01-02")
	}
	s.renderRecurringForm(w, r, http.StatusOK, form, lines, "")
}

func (s *Server) handleRecurringEdit(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.recurringFromPath(w, r)
	if !ok {
		return
	}
	form := map[string]string{"id": strconv.FormatInt(rec.ID, 10), "name": rec.Name, "contact_id": strconv.FormatInt(rec.ContactID, 10),
		"interval": rec.Interval, "next_due": rec.NextDue, "note": rec.Note}
	if rec.Active {
		form["active"] = "1"
	}
	lines := make([]lineForm, 0, len(rec.Lines))
	for _, l := range rec.Lines {
		lines = append(lines, lineForm(l))
	}
	s.renderRecurringForm(w, r, http.StatusOK, form, lines, "")
}

func (s *Server) renderRecurringForm(w http.ResponseWriter, r *http.Request, status int, form map[string]string, lines []lineForm, msg string) {
	contacts, err := s.store.ListContacts(r.Context())
	if err != nil {
		s.fail(w, "list contacts", err)
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	for len(lines) < 2 {
		lines = append(lines, lineForm{})
	}
	defaultRate := "20"
	if settings.Kleinunternehmer {
		defaultRate = "0"
	}
	for i := range lines {
		if lines[i].VATRate == "" {
			lines[i].VATRate = defaultRate
		}
	}
	form["default_rate"] = defaultRate
	var rec *store.Recurring
	if id, err := strconv.ParseInt(form["id"], 10, 64); err == nil {
		if got, err := s.store.GetRecurring(r.Context(), id); err == nil {
			rec = &got
		}
	}
	s.render(w, status, "recurring_form.html", pageData{Contacts: contacts, Settings: settings, Form: form, Lines: lines,
		Error: msg, RecurringOne: rec})
}

// handleRecurringSave creates or updates a recurring invoice. The positions
// are checked as an invoice's would be, so a template never fails only when
// it is due.
func (s *Server) handleRecurringSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	form, lines := s.readInvoiceForm(r)
	for _, k := range []string{"id", "name", "interval", "next_due", "active"} {
		form[k] = r.FormValue(k)
	}
	if id := r.PathValue("id"); id != "" {
		form["id"] = id
	}
	form["next_due"] = s.parseDate(form["next_due"])
	if _, msg := s.parseInvoiceForm(form, lines); msg != "" {
		s.renderRecurringForm(w, r, http.StatusUnprocessableEntity, form, lines, msg)
		return
	}
	rec := store.Recurring{Name: form["name"], Interval: form["interval"], NextDue: form["next_due"], Note: form["note"],
		Active: form["active"] == "1"}
	rec.ID, _ = strconv.ParseInt(form["id"], 10, 64)
	rec.ContactID, _ = strconv.ParseInt(form["contact_id"], 10, 64)
	for _, l := range lines {
		if strings.TrimSpace(l.Description) != "" || strings.TrimSpace(l.UnitPrice) != "" {
			rec.Lines = append(rec.Lines, store.DraftLine(l))
		}
	}
	if _, err := s.store.SaveRecurring(r.Context(), rec); err != nil {
		s.renderRecurringForm(w, r, http.StatusUnprocessableEntity, form, lines, s.errText(err))
		return
	}
	http.Redirect(w, r, "/recurring?done=saved", http.StatusSeeOther)
}

func (s *Server) recurringFromPath(w http.ResponseWriter, r *http.Request) (store.Recurring, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return store.Recurring{}, false
	}
	rec, err := s.store.GetRecurring(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Recurring{}, false
	}
	if err != nil {
		s.fail(w, "get recurring", err)
		return store.Recurring{}, false
	}
	return rec, true
}

// handleRecurringAction skips a period, pauses, resumes or deletes.
func (s *Server) handleRecurringAction(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.recurringFromPath(w, r)
	if !ok {
		return
	}
	var err error
	done := "saved"
	switch r.PathValue("action") {
	case "skip":
		err, done = s.store.SkipRecurring(r.Context(), rec.ID), "skipped"
	case "pause":
		err = s.store.SetRecurringActive(r.Context(), rec.ID, false)
	case "resume":
		err = s.store.SetRecurringActive(r.Context(), rec.ID, true)
	case "delete":
		err, done = s.store.DeleteRecurring(r.Context(), rec.ID), "deleted"
	default:
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, store.ErrRecurringUsed) {
		s.renderRecurringForm(w, r, http.StatusConflict, map[string]string{"id": strconv.FormatInt(rec.ID, 10), "name": rec.Name,
			"contact_id": strconv.FormatInt(rec.ContactID, 10), "interval": rec.Interval, "next_due": rec.NextDue, "note": rec.Note,
			"active": map[bool]string{true: "1"}[rec.Active]}, linesOf(rec), s.errText(err))
		return
	}
	if err != nil {
		s.fail(w, "recurring action", err)
		return
	}
	http.Redirect(w, r, "/recurring?done="+done, http.StatusSeeOther)
}

func linesOf(rec store.Recurring) []lineForm {
	out := make([]lineForm, 0, len(rec.Lines))
	for _, l := range rec.Lines {
		out = append(out, lineForm(l))
	}
	return out
}

// recurringInvoiceForm fills the invoice form for a recurring invoice's next
// period: its customer, positions with {Zeitraum} filled in, the period's
// date as the service date, and the template's id, so issuing moves it on.
func (s *Server) recurringInvoiceForm(r *http.Request, form map[string]string) ([]lineForm, string, error) {
	id, err := strconv.ParseInt(r.URL.Query().Get("recurring"), 10, 64)
	if err != nil {
		return nil, "", nil
	}
	rec, err := s.store.GetRecurring(r.Context(), id)
	if err != nil {
		return nil, "", err
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		return nil, "", err
	}
	period := rec.Period()
	form["recurring_id"] = strconv.FormatInt(rec.ID, 10)
	form["contact_id"] = strconv.FormatInt(rec.ContactID, 10)
	form["service_date"] = rec.NextDue
	form["note"] = fillPeriod(rec.Note, period)
	var lines []lineForm
	for _, l := range rec.Lines {
		lf := lineForm(l)
		lf.Description = fillPeriod(lf.Description, period)
		if settings.KUOn(form["issue_date"]) {
			lf.VATRate = "0"
		}
		lines = append(lines, lf)
	}
	return lines, s.Tf("Wiederkehrende Rechnung „%s“ für %s. Beim Ausstellen rückt die nächste auf einen Zeitraum später.", rec.Name, period), nil
}
