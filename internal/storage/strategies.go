package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"jx_api/internal/models"
	"github.com/google/uuid"
)

func (s *DatabaseStorage) GetStrategy(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*models.Strategy, error) {
	var st models.Strategy
	err := s.pool.QueryRow(ctx, "SELECT id, user_id, name, description, rules, active, vector_clock, created_at FROM strategies WHERE id = $1 AND user_id = $2", id, userID).Scan(
		&st.ID, &st.UserID, &st.Name, &st.Description, &st.Rules, &st.Active, &st.VectorClock, &st.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *DatabaseStorage) GetStrategies(ctx context.Context, userID uuid.UUID) ([]models.Strategy, error) {
	rows, err := s.pool.Query(ctx, "SELECT id, user_id, name, description, rules, active, vector_clock, created_at FROM strategies WHERE user_id = $1 ORDER BY created_at DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	strategies := []models.Strategy{}
	for rows.Next() {
		var st models.Strategy
		err := rows.Scan(
			&st.ID, &st.UserID, &st.Name, &st.Description, &st.Rules, &st.Active, &st.VectorClock, &st.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		strategies = append(strategies, st)
	}
	return strategies, nil
}

func (s *DatabaseStorage) CreateStrategy(ctx context.Context, strategy *models.Strategy) error {
	if strategy.ID == uuid.Nil {
		strategy.ID = uuid.New()
	}
	if strategy.CreatedAt.IsZero() {
		strategy.CreatedAt = time.Now()
	}
	if strategy.Rules == nil {
		strategy.Rules = make(map[string]interface{})
	}
	strategy.Active = true

	rulesBytes, err := json.Marshal(strategy.Rules)
	if err != nil {
		rulesBytes = []byte("{}")
	}

	clock, err := s.nextClock(ctx, strategy.UserID)
	if err != nil {
		return err
	}
	strategy.VectorClock = clock

	_, err = s.pool.Exec(ctx, `
		INSERT INTO strategies (id, user_id, name, description, rules, active, vector_clock, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, strategy.ID, strategy.UserID, strategy.Name, strategy.Description, string(rulesBytes), strategy.Active, strategy.VectorClock, strategy.CreatedAt)
	if err != nil {
		return err
	}
	payload := mustJSON(strategy)
	if err := s.appendSyncEvent(ctx, strategy.UserID, "strategies", strategy.ID, "create", payload, clock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, strategy.UserID, "strategies")
}

func (s *DatabaseStorage) UpdateStrategy(ctx context.Context, id uuid.UUID, userID uuid.UUID, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}

	mapping := map[string]string{
		"name":        "name",
		"description": "description",
		"rules":       "rules",
		"active":      "active",
	}

	query := "UPDATE strategies SET "
	args := []interface{}{id, userID}
	i := 3
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

	if i == 3 {
		return nil
	}

	newClock, err := s.nextClock(ctx, userID)
	if err != nil {
		return err
	}
	query += fmt.Sprintf(", vector_clock = $%d WHERE id = $1 AND user_id = $2", i)
	args = append(args, newClock)

	_, err = s.pool.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	payload := mustJSON(updates)
	if err := s.appendSyncEvent(ctx, userID, "strategies", id, "update", payload, newClock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, userID, "strategies")
}

func (s *DatabaseStorage) DeleteStrategy(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM strategies WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		return err
	}
	newClock, err := s.nextClock(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.appendSyncEvent(ctx, userID, "strategies", id, "delete", []byte(`{}`), newClock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, userID, "strategies")
}
