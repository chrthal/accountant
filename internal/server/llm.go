package server

import (
	"context"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/chrthal/accountant/internal/llm"
	"github.com/chrthal/accountant/internal/store"
)

// WithLLM turns on booking suggestions from a local model (ADR 0020).
func WithLLM(c *llm.Client) Option {
	return func(s *Server) { s.llm = c }
}

type llmState struct {
	running atomic.Bool
}

func (s *Server) llmRunning() bool { return s.llm != nil && s.llmState.running.Load() }

// startSuggestions fills in model suggestions in the background, unless a run
// is already going. Each answer takes a second or two on a CPU, so the page
// does not wait for it.
func (s *Server) startSuggestions() {
	if s.llm == nil || !s.llmState.running.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.llmState.running.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		n, err := s.suggestMissing(ctx)
		if err != nil {
			log.Printf("llm suggestions: %v (after %d)", err, n)
			return
		}
		if n > 0 {
			log.Printf("llm suggestions: %d new", n)
		}
	}()
}

// suggestMissing asks the model only about the open bank lines that the rules
// leave empty: no invoice to pay, no earlier booking to copy, and no stored
// suggestion yet. The model is the fallback, not the first step.
func (s *Server) suggestMissing(ctx context.Context) (int, error) {
	drafts, _, err := s.buildDrafts(ctx)
	if err != nil {
		return 0, err
	}
	cats, err := s.store.ListCategories(ctx)
	if err != nil {
		return 0, err
	}
	cons, err := s.store.ListContacts(ctx)
	if err != nil {
		return 0, err
	}
	catOpts := make([]llm.Option, len(cats))
	for i, c := range cats {
		catOpts[i] = llm.Option{ID: c.ID, Name: c.Name, Direction: c.Direction}
	}
	conOpts := make([]llm.Option, len(cons))
	for i, c := range cons {
		conOpts[i] = llm.Option{ID: c.ID, Name: c.Name}
	}

	n := 0
	for _, d := range drafts {
		if d.Source != "" {
			continue
		}
		p, err := s.llm.Propose(ctx, llm.Line{BookedOn: d.Tx.BookedOn, AmountCents: d.Tx.AmountCents,
			Counterparty: d.Tx.Counterparty, Purpose: d.Tx.Purpose}, catOpts, conOpts)
		if err != nil {
			return n, err
		}
		if err := s.store.SaveBankSuggestion(ctx, store.BankSuggestion{
			BankTransactionID: d.Tx.ID, Description: p.Description, CategoryID: p.CategoryID,
			ContactID: p.ContactID, VATRate: p.VATRate, Unsure: p.Unsure, Model: s.llm.Model,
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Server) handleDraftsSuggest(w http.ResponseWriter, r *http.Request) {
	s.startSuggestions()
	http.Redirect(w, r, "/bank/drafts", http.StatusSeeOther)
}
