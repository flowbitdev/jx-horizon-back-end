package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"jx_api/internal/models"
	"github.com/google/uuid"
)

// nextClock atomically increments the user's vector clock and returns the new value.
func (s *DatabaseStorage) nextClock(ctx context.Context, userID uuid.UUID) (int64, error) {
	var clock int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO user_clocks (user_id, clock)
		VALUES ($1, 1)
		ON CONFLICT (user_id) DO UPDATE SET clock = user_clocks.clock + 1
		RETURNING clock
	`, userID).Scan(&clock)
	if err != nil {
		return 0, err
	}
	return clock, nil
}

// RecordEvent is the high-level helper used by API handlers that don't pre-allocate clocks.
// Returns the assigned clock value.
func (s *DatabaseStorage) RecordEvent(ctx context.Context, userID uuid.UUID, entityType string, entityID uuid.UUID, action string, payload []byte) (int64, error) {
	clock, err := s.nextClock(ctx, userID)
	if err != nil {
		return 0, err
	}
	if err := s.appendSyncEvent(ctx, userID, entityType, entityID, action, payload, clock); err != nil {
		return clock, err
	}
	if err := s.recomputeSectionHash(ctx, userID, entityType); err != nil {
		return clock, err
	}
	return clock, nil
}

// appendSyncEvent inserts one row into sync_queue at the given pre-allocated clock.
func (s *DatabaseStorage) appendSyncEvent(ctx context.Context, userID uuid.UUID, entityType string, entityID uuid.UUID, action string, payload []byte, clock int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sync_queue (user_id, entity_type, entity_id, action, payload, vector_clock)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, userID, entityType, entityID, action, string(payload), clock)
	return err
}

// UpdateSectionHash recomputes the hash for a section by hashing the canonical JSON of all
// rows in that section. Sections are: trades, journal, goals, strategies, chatList.
func (s *DatabaseStorage) recomputeSectionHash(ctx context.Context, userID uuid.UUID, section string) error {
	rows, err := s.fetchSectionRows(ctx, userID, section)
	if err != nil {
		return err
	}
	// Canonicalize: sort by ID, marshal to JSON
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	canon, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(canon)
	hash := hex.EncodeToString(sum[:])
	_, err = s.pool.Exec(ctx, `
		INSERT INTO section_hashes (user_id, section, hash, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id, section) DO UPDATE
		SET hash = EXCLUDED.hash, updated_at = now()
	`, userID, section, hash)
	return err
}

// fetchSectionRows returns the rows that comprise a section as a slice of generic
// {ID string, VectorClock int64, Data any} records for hashing.
func (s *DatabaseStorage) fetchSectionRows(ctx context.Context, userID uuid.UUID, section string) ([]sectionRow, error) {
	var query string
	switch section {
	case "trades":
		query = `SELECT id::text, vector_clock FROM trades WHERE user_id = $1`
	case "journal":
		query = `SELECT id::text, vector_clock FROM daily_journal WHERE user_id = $1`
	case "goals":
		query = `SELECT id::text, vector_clock FROM goals WHERE user_id = $1`
	case "strategies":
		query = `SELECT id::text, vector_clock FROM strategies WHERE user_id = $1`
	case "chatList":
		query = `SELECT id::text, vector_clock FROM chat_sessions WHERE user_id = $1`
	default:
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sectionRow{}
	for rows.Next() {
		var r sectionRow
		if err := rows.Scan(&r.ID, &r.VectorClock); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

type sectionRow struct {
	ID          string `json:"id"`
	VectorClock int64  `json:"vc"`
}

func (s *DatabaseStorage) GetSectionSnapshot(ctx context.Context, userID uuid.UUID) (map[string]SectionSnapshotEntry, error) {
	rows, err := s.pool.Query(ctx, `SELECT section, hash, updated_at FROM section_hashes WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SectionSnapshotEntry{}
	for rows.Next() {
		var name, hash string
		var ts interface{}
		if err := rows.Scan(&name, &hash, &ts); err != nil {
			return nil, err
		}
		out[name] = SectionSnapshotEntry{Hash: hash}
	}
	return out, nil
}

type SectionSnapshotEntry struct {
	Hash      string      `json:"hash"`
	VectorClock int64     `json:"vectorClock"`
	UpdatedAt interface{} `json:"updatedAt,omitempty"`
}

func (s *DatabaseStorage) AddToSyncQueue(ctx context.Context, event *models.SyncEvent) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sync_queue (id, user_id, entity_type, entity_id, action, payload, vector_clock, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, event.ID, event.UserID, event.EntityType, event.EntityID, event.Action, event.Payload, event.VectorClock, event.CreatedAt)
	return err
}

func (s *DatabaseStorage) GetSyncEventsSince(ctx context.Context, userID uuid.UUID, vectorClock int64) ([]models.SyncEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, entity_type, entity_id, action, payload, vector_clock, created_at
		FROM sync_queue
		WHERE user_id = $1 AND vector_clock > $2
		ORDER BY vector_clock ASC
	`, userID, vectorClock)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []models.SyncEvent
	for rows.Next() {
		var e models.SyncEvent
		err := rows.Scan(&e.ID, &e.UserID, &e.EntityType, &e.EntityID, &e.Action, &e.Payload, &e.VectorClock, &e.CreatedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}

// mustJSON marshals v to JSON, panicking on error (safe for trusted structs).
func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func (s *DatabaseStorage) GetLatestVectorClock(ctx context.Context, userID uuid.UUID) (int64, error) {
	var clock int64
	err := s.pool.QueryRow(ctx, "SELECT COALESCE(clock, 0) FROM user_clocks WHERE user_id = $1", userID).Scan(&clock)
	if err != nil {
		// No row = no events yet
		return 0, nil
	}
	return clock, nil
}
