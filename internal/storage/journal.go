package storage

import (
	"context"
	"fmt"
	"sort"
	"time"

	"jx_api/internal/models"
	"github.com/google/uuid"
)

func (s *DatabaseStorage) GetJournalEntry(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*models.DailyJournal, error) {
	var e models.DailyJournal
	err := s.pool.QueryRow(ctx,
		"SELECT id, user_id, title, date, psychology_notes, market_conditions, mistakes, rating, vector_clock, created_at FROM daily_journal WHERE id = $1 AND user_id = $2",
		id, userID).Scan(&e.ID, &e.UserID, &e.Title, &e.Date, &e.PsychologyNotes, &e.MarketConditions, &e.Mistakes, &e.Rating, &e.VectorClock, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *DatabaseStorage) GetJournalEntries(ctx context.Context, userID uuid.UUID) ([]models.DailyJournal, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT id, user_id, title, date, psychology_notes, market_conditions, mistakes, rating, vector_clock, created_at FROM daily_journal WHERE user_id = $1 ORDER BY date DESC",
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []models.DailyJournal{}
	for rows.Next() {
		var e models.DailyJournal
		err := rows.Scan(&e.ID, &e.UserID, &e.Title, &e.Date, &e.PsychologyNotes, &e.MarketConditions, &e.Mistakes, &e.Rating, &e.VectorClock, &e.CreatedAt)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (s *DatabaseStorage) CreateJournalEntry(ctx context.Context, entry *models.DailyJournal) error {
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	clock, err := s.nextClock(ctx, entry.UserID)
	if err != nil {
		return err
	}
	entry.VectorClock = clock

	_, err = s.pool.Exec(ctx,
		"INSERT INTO daily_journal (id, user_id, title, date, psychology_notes, market_conditions, mistakes, rating, vector_clock, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)",
		entry.ID, entry.UserID, entry.Title, entry.Date, entry.PsychologyNotes, entry.MarketConditions, entry.Mistakes, entry.Rating, entry.VectorClock, entry.CreatedAt)
	if err != nil {
		return err
	}
	payload := mustJSON(entry)
	if err := s.appendSyncEvent(ctx, entry.UserID, "journal", entry.ID, "create", payload, clock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, entry.UserID, "journal")
}

func (s *DatabaseStorage) UpdateJournalEntry(ctx context.Context, id uuid.UUID, userID uuid.UUID, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}

	mapping := map[string]string{
		"title":            "title",
		"date":             "date",
		"psychologyNotes":  "psychology_notes",
		"marketConditions": "market_conditions",
		"mistakes":         "mistakes",
		"rating":           "rating",
	}

	query := "UPDATE daily_journal SET "
	args := []interface{}{}
	i := 1
	first := true

	keys := make([]string, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := updates[k]
		col, ok := mapping[k]
		if !ok {
			continue
		}
		if !first {
			query += ", "
		}
		query += fmt.Sprintf("%s = $%d", col, i)
		args = append(args, v)
		i++
		first = false
	}

	if i == 1 {
		return nil
	}

	newClock, err := s.nextClock(ctx, userID)
	if err != nil {
		return err
	}
	query += fmt.Sprintf(", vector_clock = $%d WHERE id = $%d AND user_id = $%d", i, i+1, i+2)
	args = append(args, newClock, id, userID)

	res, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("no journal entry found to update")
	}
	payload := mustJSON(updates)
	if err := s.appendSyncEvent(ctx, userID, "journal", id, "update", payload, newClock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, userID, "journal")
}

func (s *DatabaseStorage) DeleteJournalEntry(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	res, err := s.pool.Exec(ctx, "DELETE FROM daily_journal WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("no journal entry found to delete")
	}
	newClock, err := s.nextClock(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.appendSyncEvent(ctx, userID, "journal", id, "delete", []byte(`{}`), newClock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, userID, "journal")
}
