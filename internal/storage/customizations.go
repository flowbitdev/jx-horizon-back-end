package storage

import (
	"context"
	"time"

	"jx_api/internal/models"
	"github.com/google/uuid"
)

func (s *DatabaseStorage) GetCustomSetups(ctx context.Context, userID uuid.UUID) ([]models.CustomSetup, error) {
	rows, err := s.pool.Query(ctx, "SELECT id, user_id, name, created_at FROM custom_setups WHERE user_id = $1 ORDER BY created_at ASC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var setups []models.CustomSetup
	for rows.Next() {
		var cs models.CustomSetup
		err := rows.Scan(&cs.ID, &cs.UserID, &cs.Name, &cs.CreatedAt)
		if err != nil {
			return nil, err
		}
		setups = append(setups, cs)
	}
	if setups == nil {
		setups = make([]models.CustomSetup, 0)
	}
	return setups, nil
}

func (s *DatabaseStorage) CreateCustomSetup(ctx context.Context, userID uuid.UUID, name string) (*models.CustomSetup, error) {
	cs := &models.CustomSetup{
		ID:        uuid.New(),
		UserID:    userID,
		Name:      name,
		CreatedAt: time.Now(),
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO custom_setups (id, user_id, name, created_at)
		VALUES ($1, $2, $3, $4)
	`, cs.ID, cs.UserID, cs.Name, cs.CreatedAt)

	if err != nil {
		return nil, err
	}
	return cs, nil
}

func (s *DatabaseStorage) GetCustomSessions(ctx context.Context, userID uuid.UUID) ([]models.CustomSession, error) {
	rows, err := s.pool.Query(ctx, "SELECT id, user_id, name, created_at FROM custom_sessions WHERE user_id = $1 ORDER BY created_at ASC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []models.CustomSession
	for rows.Next() {
		var cs models.CustomSession
		err := rows.Scan(&cs.ID, &cs.UserID, &cs.Name, &cs.CreatedAt)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, cs)
	}
	if sessions == nil {
		sessions = make([]models.CustomSession, 0)
	}
	return sessions, nil
}

func (s *DatabaseStorage) CreateCustomSession(ctx context.Context, userID uuid.UUID, name string) (*models.CustomSession, error) {
	cs := &models.CustomSession{
		ID:        uuid.New(),
		UserID:    userID,
		Name:      name,
		CreatedAt: time.Now(),
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO custom_sessions (id, user_id, name, created_at)
		VALUES ($1, $2, $3, $4)
	`, cs.ID, cs.UserID, cs.Name, cs.CreatedAt)

	if err != nil {
		return nil, err
	}
	return cs, nil
}
