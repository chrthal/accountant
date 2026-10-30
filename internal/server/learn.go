package server

import (
	"context"
	"strings"

	"github.com/chrthal/accountant/internal/match"
	"github.com/chrthal/accountant/internal/store"
)

// history finds an earlier booking similar to a new bank line or Beleg
// (ADR 0044): after "wie zuletzt" for the same counterparty, before the
// category keywords and the model.
type history struct {
	past []store.PastBooking
	get  func(context.Context, int64) (store.Entry, error)
}

func (s *Server) loadHistory(ctx context.Context) (history, error) {
	past, err := s.store.PastBookings(ctx)
	return history{past: past, get: s.store.GetEntry}, err
}

// similar returns the newest earlier booking in direction whose contact,
// bank counterparty or description names the same party as name.
func (h history) similar(ctx context.Context, name, direction string) (*store.Entry, bool) {
	if strings.TrimSpace(name) == "" {
		return nil, false
	}
	for _, p := range h.past {
		if p.Direction != direction {
			continue
		}
		for _, n := range p.Names {
			if match.SimilarParty(name, n) {
				e, err := h.get(ctx, p.EntryID)
				if err != nil {
					return nil, false
				}
				return &e, true
			}
		}
	}
	return nil, false
}
