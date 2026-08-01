package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"jx_api/internal/models"
)

func (s *DatabaseStorage) GetTradingProfile(ctx context.Context, userID uuid.UUID) (*models.TradingProfile, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, user_id, profile_data, ai_suggested_updates, created_at, updated_at
		FROM trading_profiles
		WHERE user_id = $1
	`, userID)

	var profile models.TradingProfile
	var profileData []byte
	var updates []byte
	if err := row.Scan(&profile.ID, &profile.UserID, &profileData, &updates, &profile.CreatedAt, &profile.UpdatedAt); err != nil {
		return nil, err
	}
	profile.ProfileData = map[string]interface{}{}
	if len(profileData) > 0 {
		_ = json.Unmarshal(profileData, &profile.ProfileData)
	}
	profile.AISuggestedUpdates = []map[string]interface{}{}
	if len(updates) > 0 {
		_ = json.Unmarshal(updates, &profile.AISuggestedUpdates)
	}
	return &profile, nil
}

func (s *DatabaseStorage) UpsertTradingProfile(ctx context.Context, profile *models.TradingProfile) error {
	if profile.ID == uuid.Nil {
		profile.ID = uuid.New()
	}
	if profile.ProfileData == nil {
		profile.ProfileData = map[string]interface{}{}
	}
	if profile.AISuggestedUpdates == nil {
		profile.AISuggestedUpdates = []map[string]interface{}{}
	}
	now := time.Now()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now

	profileData, err := json.Marshal(profile.ProfileData)
	if err != nil {
		return err
	}
	updates, err := json.Marshal(profile.AISuggestedUpdates)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO trading_profiles (id, user_id, profile_data, ai_suggested_updates, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id) DO UPDATE SET
			profile_data = EXCLUDED.profile_data,
			ai_suggested_updates = EXCLUDED.ai_suggested_updates,
			updated_at = EXCLUDED.updated_at
	`, profile.ID, profile.UserID, profileData, updates, profile.CreatedAt, profile.UpdatedAt)
	return err
}

func (s *DatabaseStorage) ListUserInsights(ctx context.Context, userID uuid.UUID) ([]models.UserInsight, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, content, category, weight, reference_count, last_referenced_at, created_at
		FROM user_insights
		WHERE user_id = $1
		ORDER BY weight DESC, reference_count DESC, created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var insights []models.UserInsight
	for rows.Next() {
		var insight models.UserInsight
		if err := rows.Scan(&insight.ID, &insight.UserID, &insight.Content, &insight.Category, &insight.Weight, &insight.ReferenceCount, &insight.LastReferencedAt, &insight.CreatedAt); err != nil {
			return nil, err
		}
		insights = append(insights, insight)
	}
	return insights, nil
}

func (s *DatabaseStorage) CreateUserInsight(ctx context.Context, insight *models.UserInsight) error {
	if insight.ID == uuid.Nil {
		insight.ID = uuid.New()
	}
	if insight.Weight == 0 {
		insight.Weight = 5
	}
	if insight.ReferenceCount == 0 {
		insight.ReferenceCount = 1
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO user_insights (id, user_id, content, category, weight, reference_count, last_referenced_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, insight.ID, insight.UserID, insight.Content, insight.Category, insight.Weight, insight.ReferenceCount, insight.LastReferencedAt, insight.CreatedAt)
	return err
}

func (s *DatabaseStorage) ListMessageFeedback(ctx context.Context, userID uuid.UUID, messageID uuid.UUID) ([]models.MessageFeedback, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, message_id, user_id, score, created_at
		FROM message_feedback
		WHERE user_id = $1 AND message_id = $2
		ORDER BY created_at DESC
	`, userID, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feedback []models.MessageFeedback
	for rows.Next() {
		var item models.MessageFeedback
		if err := rows.Scan(&item.ID, &item.MessageID, &item.UserID, &item.Score, &item.CreatedAt); err != nil {
			return nil, err
		}
		feedback = append(feedback, item)
	}
	return feedback, nil
}

func (s *DatabaseStorage) UpsertMessageFeedback(ctx context.Context, feedback *models.MessageFeedback) error {
	if feedback.ID == uuid.Nil {
		feedback.ID = uuid.New()
	}
	if feedback.CreatedAt.IsZero() {
		feedback.CreatedAt = time.Now()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO message_feedback (id, message_id, user_id, score, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (message_id, user_id) DO UPDATE SET
			score = EXCLUDED.score,
			created_at = EXCLUDED.created_at
	`, feedback.ID, feedback.MessageID, feedback.UserID, feedback.Score, feedback.CreatedAt)
	return err
}

func (s *DatabaseStorage) ListAIActions(ctx context.Context, userID uuid.UUID, sessionID uuid.UUID) ([]models.AIAction, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, session_id, message_id, action_type, payload, status, created_at, executed_at
		FROM ai_actions
		WHERE user_id = $1 AND session_id = $2
		ORDER BY created_at ASC
	`, userID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var actions []models.AIAction
	for rows.Next() {
		var action models.AIAction
		var payload []byte
		if err := rows.Scan(&action.ID, &action.UserID, &action.SessionID, &action.MessageID, &action.ActionType, &payload, &action.Status, &action.CreatedAt, &action.ExecutedAt); err != nil {
			return nil, err
		}
		action.Payload = map[string]interface{}{}
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &action.Payload)
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func (s *DatabaseStorage) CreateAIAction(ctx context.Context, action *models.AIAction) error {
	if action.ID == uuid.Nil {
		action.ID = uuid.New()
	}
	if action.Status == "" {
		action.Status = "pending"
	}
	if action.Payload == nil {
		action.Payload = map[string]interface{}{}
	}
	payload, err := json.Marshal(action.Payload)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO ai_actions (id, user_id, session_id, message_id, action_type, payload, status, created_at, executed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, action.ID, action.UserID, action.SessionID, action.MessageID, action.ActionType, payload, action.Status, action.CreatedAt, action.ExecutedAt)
	return err
}

func (s *DatabaseStorage) UpdateAIActionStatus(ctx context.Context, id uuid.UUID, userID uuid.UUID, status string, executedAt *time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE ai_actions
		SET status = $1, executed_at = $2
		WHERE id = $3 AND user_id = $4
	`, status, executedAt, id, userID)
	return err
}

func (s *DatabaseStorage) ListMarketDataCache(ctx context.Context, symbols []string, marketType string) ([]models.MarketDataCache, error) {
	if len(symbols) == 0 {
		return []models.MarketDataCache{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT symbol, market_type, price, change_percent, volume, cached_at, expires_at
		FROM market_data_cache
		WHERE symbol = ANY($1) AND market_type = $2
		ORDER BY cached_at DESC
	`, symbols, marketType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []models.MarketDataCache
	for rows.Next() {
		var item models.MarketDataCache
		if err := rows.Scan(&item.Symbol, &item.MarketType, &item.Price, &item.ChangePercent, &item.Volume, &item.CachedAt, &item.ExpiresAt); err != nil {
			return nil, err
		}
		entries = append(entries, item)
	}
	return entries, nil
}

func (s *DatabaseStorage) UpsertMarketDataCache(ctx context.Context, entry *models.MarketDataCache) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO market_data_cache (symbol, market_type, price, change_percent, volume, cached_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (symbol, market_type) DO UPDATE SET
			price = EXCLUDED.price,
			change_percent = EXCLUDED.change_percent,
			volume = EXCLUDED.volume,
			cached_at = EXCLUDED.cached_at,
			expires_at = EXCLUDED.expires_at
	`, entry.Symbol, entry.MarketType, entry.Price, entry.ChangePercent, entry.Volume, entry.CachedAt, entry.ExpiresAt)
	return err
}
