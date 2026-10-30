package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// currentDescription is an entry's description as shown: the newest change
// (ADR 0043), else the one it was booked with. The query must name the
// entry e.
const currentDescription = `COALESCE((SELECT d.description FROM entry_descriptions d
	WHERE d.entry_id = e.id ORDER BY d.id DESC LIMIT 1), e.description)`

// DescriptionChange is one version of a booking's description.
type DescriptionChange struct {
	Description string
	At          time.Time
	Original    bool // as booked
}

// ErrSameDescription is returned when a new description equals the current one.
var ErrSameDescription = errors.New("the description is unchanged")

// RenameEntry changes the description of a booking. The booking itself is not
// touched (ADR 0007): the change is a new row, and the old text stays in the
// history.
func (s *Store) RenameEntry(ctx context.Context, id int64, description string) error {
	description = strings.TrimSpace(description)
	if description == "" {
		return fmt.Errorf("description is required")
	}
	if len([]rune(description)) > 200 {
		return fmt.Errorf("the description can be at most 200 characters")
	}
	e, err := s.GetEntry(ctx, id)
	if err != nil {
		return err
	}
	if e.Description == description {
		return ErrSameDescription
	}
	_, err = s.DB.ExecContext(ctx, s.Rebind(`INSERT INTO entry_descriptions (entry_id, description, changed_at) VALUES (?, ?, ?)`),
		id, description, time.Now().UTC())
	return err
}

// DescriptionHistory lists a booking's descriptions, oldest first, the one it
// was booked with included. It is empty when the description never changed.
func (s *Store) DescriptionHistory(ctx context.Context, id int64) ([]DescriptionChange, error) {
	rows, err := s.DB.QueryContext(ctx, s.Rebind(`SELECT description, changed_at FROM entry_descriptions
		WHERE entry_id = ? ORDER BY id`), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var changes []DescriptionChange
	for rows.Next() {
		var c DescriptionChange
		if err := rows.Scan(&c.Description, &c.At); err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil || len(changes) == 0 {
		return nil, err
	}
	var orig DescriptionChange
	if err := s.DB.QueryRowContext(ctx, s.Rebind(`SELECT description, created_at FROM entries WHERE id = ?`), id).
		Scan(&orig.Description, &orig.At); err != nil {
		return nil, err
	}
	orig.Original = true
	return append([]DescriptionChange{orig}, changes...), nil
}
