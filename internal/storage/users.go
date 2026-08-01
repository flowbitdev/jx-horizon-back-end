package storage

import (
	"context"
	"fmt"
	"time"

	"jx_api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

func (s *DatabaseStorage) GetUser(ctx context.Context, id uuid.UUID) (*models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx,
		`SELECT id, username, name, email, google_id, avatar_url,
		        xp, rank, theme_color, bio, account_size, currency,
		        max_risk_percent, is_admin, is_banned, favorites, ai_memory,
		        created_at, last_login, metadata, tos_accepted_at
		 FROM users WHERE id = $1`, id).Scan(
		&u.ID, &u.Username, &u.Name, &u.Email, &u.GoogleID, &u.AvatarURL,
		&u.XP, &u.Rank, &u.ThemeColor, &u.Bio, &u.AccountSize, &u.Currency,
		&u.MaxRiskPercent, &u.IsAdmin, &u.IsBanned, &u.Favorites, &u.AIMemory,
		&u.CreatedAt, &u.LastLogin, &u.Metadata, &u.TosAcceptedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *DatabaseStorage) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx,
		`SELECT id, username, name, email, google_id, avatar_url,
		        xp, rank, theme_color, bio, account_size, currency,
		        max_risk_percent, is_admin, is_banned, favorites, ai_memory,
		        created_at, last_login, metadata, tos_accepted_at
		 FROM users WHERE username = $1`, username).Scan(
		&u.ID, &u.Username, &u.Name, &u.Email, &u.GoogleID, &u.AvatarURL,
		&u.XP, &u.Rank, &u.ThemeColor, &u.Bio, &u.AccountSize, &u.Currency,
		&u.MaxRiskPercent, &u.IsAdmin, &u.IsBanned, &u.Favorites, &u.AIMemory,
		&u.CreatedAt, &u.LastLogin, &u.Metadata, &u.TosAcceptedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *DatabaseStorage) GetUserByGoogleID(ctx context.Context, googleID string) (*models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx,
		`SELECT id, username, name, email, google_id, avatar_url,
		        xp, rank, theme_color, bio, account_size, currency,
		        max_risk_percent, is_admin, is_banned, favorites, ai_memory,
		        created_at, last_login, metadata, tos_accepted_at
		 FROM users WHERE google_id = $1`, googleID).Scan(
		&u.ID, &u.Username, &u.Name, &u.Email, &u.GoogleID, &u.AvatarURL,
		&u.XP, &u.Rank, &u.ThemeColor, &u.Bio, &u.AccountSize, &u.Currency,
		&u.MaxRiskPercent, &u.IsAdmin, &u.IsBanned, &u.Favorites, &u.AIMemory,
		&u.CreatedAt, &u.LastLogin, &u.Metadata, &u.TosAcceptedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		log.Error().Err(err).Str("googleID", googleID).Msg("Failed to get user by google_id")
		return nil, err
	}
	return &u, nil
}

func (s *DatabaseStorage) CreateUser(ctx context.Context, user *models.User) error {
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now()
	}
	if user.Rank == "" {
		user.Rank = "Novice"
	}
	if user.ThemeColor == "" {
		user.ThemeColor = "orange"
	}
	if user.AccountSize == 0 {
		user.AccountSize = 10000.0
	}
	if user.Currency == "" {
		user.Currency = "USD"
	}
	if user.MaxRiskPercent == 0 {
		user.MaxRiskPercent = 2.0
	}
	if user.Favorites == nil {
		user.Favorites = []string{}
	}
	if user.AIMemory == nil {
		user.AIMemory = map[string]interface{}{
			"weaknesses": []string{},
			"strengths":  []string{},
			"rules":      []string{},
		}
	}


	if user.Metadata == nil {
		user.Metadata = map[string]interface{}{}
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (
			id, username, name, email, google_id, avatar_url,
			xp, rank, theme_color, bio, account_size, currency,
			max_risk_percent, is_admin, is_banned, favorites, ai_memory,
			created_at, last_login, metadata, tos_accepted_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
	`, user.ID, user.Username, user.Name, user.Email, user.GoogleID, user.AvatarURL,
		user.XP, user.Rank, user.ThemeColor, user.Bio, user.AccountSize, user.Currency,
		user.MaxRiskPercent, user.IsAdmin, user.IsBanned, user.Favorites, user.AIMemory,
		user.CreatedAt, user.LastLogin, user.Metadata, user.TosAcceptedAt,
	)
	if err != nil {
		log.Error().Err(err).Str("username", user.Username).Msg("Failed to create user")
	}
	return err
}

// AcceptTos records the ToS acceptance timestamp for a user.
func (s *DatabaseStorage) AcceptTos(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET tos_accepted_at = NOW() WHERE id = $1`, userID)
	return err
}

func (s *DatabaseStorage) UpdateUser(ctx context.Context, id uuid.UUID, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}

	query := "UPDATE users SET "
	args := []interface{}{}
	i := 1

	for k, v := range updates {
		if i > 1 {
			query += ", "
		}
		query += fmt.Sprintf("%s = $%d", k, i)
		args = append(args, v)
		i++
	}

	query += fmt.Sprintf(" WHERE id = $%d", i)
	args = append(args, id)

	_, err := s.pool.Exec(ctx, query, args...)
	return err
}

func (s *DatabaseStorage) UpsertUser(ctx context.Context, user *models.User) (*models.User, error) {
	existing, err := s.GetUserByGoogleID(ctx, *user.GoogleID)
	if err != nil {
		return nil, err
	}

	if existing == nil {
		// Create new user
		if user.ID == uuid.Nil {
			user.ID = uuid.New()
		}
		user.CreatedAt = time.Now()
		user.LastLogin = &user.CreatedAt
		err = s.CreateUser(ctx, user)
		if err != nil {
			return nil, err
		}
		return user, nil
	}

	// Update existing user
	updates := map[string]interface{}{
		"last_login": time.Now(),
		"avatar_url": user.AvatarURL,
		"name":       user.Name,
		"email":      user.Email,
	}
	err = s.UpdateUser(ctx, existing.ID, updates)
	if err != nil {
		return nil, err
	}

	// Re-fetch updated user
	return s.GetUser(ctx, existing.ID)
}
