package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ShareChange is one version of a booking's business share.
type ShareChange struct {
	BusinessPct  int
	BusinessNote string
	At           time.Time
	Original     bool // as booked
}

// ErrSameShare is returned when a new share and note equal the current ones.
var ErrSameShare = errors.New("the business share is unchanged")

// ChangeBusinessShare changes the business share of an expense (ADR 0045).
// The booking is not touched: the change is a new row, and the history keeps
// every version. A Storno pair, income and an asset cannot change: a pair
// counts nowhere, income has no private share, and an asset's cost was
// fixed from the share when it was created.
func (s *Store) ChangeBusinessShare(ctx context.Context, id int64, pct int, note string) error {
	if pct < 1 || pct > 100 {
		return fmt.Errorf("the business share must be between 1 and 100 percent")
	}
	note = strings.TrimSpace(note)
	if pct == 100 {
		note = ""
	}
	e, err := s.GetEntry(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case e.Direction != "outgoing":
		return fmt.Errorf("only an expense can have a private share")
	case !e.Effective():
		return fmt.Errorf("a reversed booking cannot change")
	}
	if _, isAsset, err := s.AssetForEntry(ctx, id); err != nil {
		return err
	} else if isAsset {
		return fmt.Errorf("this booking is an asset; remove the asset first, change the share, then create it again")
	}
	if e.BusinessPct == pct && e.BusinessNote == note {
		return ErrSameShare
	}
	_, err = s.DB.ExecContext(ctx, s.Rebind(`INSERT INTO entry_shares (entry_id, business_pct, business_note, changed_at) VALUES (?, ?, ?, ?)`),
		id, pct, note, time.Now().UTC())
	return err
}

// ShareHistory lists a booking's business shares, oldest first, the one it
// was booked with included. It is empty when the share never changed.
func (s *Store) ShareHistory(ctx context.Context, id int64) ([]ShareChange, error) {
	rows, err := s.DB.QueryContext(ctx, s.Rebind(`SELECT business_pct, business_note, changed_at FROM entry_shares
		WHERE entry_id = ? ORDER BY id`), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var changes []ShareChange
	for rows.Next() {
		var c ShareChange
		if err := rows.Scan(&c.BusinessPct, &c.BusinessNote, &c.At); err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil || len(changes) == 0 {
		return nil, err
	}
	orig := ShareChange{Original: true}
	if err := s.DB.QueryRowContext(ctx, s.Rebind(`SELECT business_pct, business_note, created_at FROM entries WHERE id = ?`), id).
		Scan(&orig.BusinessPct, &orig.BusinessNote, &orig.At); err != nil {
		return nil, err
	}
	return append([]ShareChange{orig}, changes...), nil
}
