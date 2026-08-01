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

func (s *DatabaseStorage) GetTrade(ctx context.Context, tradeID uuid.UUID, userID uuid.UUID) (*models.Trade, error) {
	var t models.Trade
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, strategy_id, symbol, direction, entry, exit_price,
		       stop_loss, take_profit, lot_size, risk_percent, risk_amount, profit_loss, rr,
		       outcome, notes, date, asset_class, setup_type, session,
		       tags, screenshot_url, is_backtest, is_demo, is_plan_compliant, rating,
		       emotion_before, emotion_during, emotion_after, ai_analysis, import_source, external_id, vector_clock, created_at
		FROM trades WHERE id = $1 AND user_id = $2
	`, tradeID, userID).Scan(
		&t.ID, &t.UserID, &t.StrategyID, &t.Symbol, &t.Direction, &t.Entry, &t.ExitPrice,
		&t.StopLoss, &t.TakeProfit, &t.LotSize, &t.RiskPercent, &t.RiskAmount, &t.ProfitLoss, &t.RR,
		&t.Outcome, &t.Notes, &t.Date, &t.AssetClass, &t.SetupType, &t.Session,
		&t.Tags, &t.ScreenshotURL, &t.IsBacktest, &t.IsDemo, &t.IsPlanCompliant, &t.Rating,
		&t.EmotionBefore, &t.EmotionDuring, &t.EmotionAfter, &t.AIAnalysis, &t.ImportSource, &t.ExternalID, &t.VectorClock, &t.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *DatabaseStorage) GetTrades(ctx context.Context, userID uuid.UUID) ([]models.Trade, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, strategy_id, symbol, direction, entry, exit_price,
		       stop_loss, take_profit, lot_size, risk_percent, risk_amount, profit_loss, rr,
		       outcome, notes, date, asset_class, setup_type, session,
		       tags, screenshot_url, is_backtest, is_demo, is_plan_compliant, rating,
		       emotion_before, emotion_during, emotion_after, ai_analysis, import_source, external_id, vector_clock, created_at
		FROM trades WHERE user_id = $1 ORDER BY date DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	trades := []models.Trade{}
	for rows.Next() {
		var t models.Trade
		err := rows.Scan(
			&t.ID, &t.UserID, &t.StrategyID, &t.Symbol, &t.Direction, &t.Entry, &t.ExitPrice,
			&t.StopLoss, &t.TakeProfit, &t.LotSize, &t.RiskPercent, &t.RiskAmount, &t.ProfitLoss, &t.RR,
			&t.Outcome, &t.Notes, &t.Date, &t.AssetClass, &t.SetupType, &t.Session,
			&t.Tags, &t.ScreenshotURL, &t.IsBacktest, &t.IsDemo, &t.IsPlanCompliant, &t.Rating,
			&t.EmotionBefore, &t.EmotionDuring, &t.EmotionAfter, &t.AIAnalysis, &t.ImportSource, &t.ExternalID, &t.VectorClock, &t.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		trades = append(trades, t)
	}
	return trades, nil
}

func (s *DatabaseStorage) CreateTrade(ctx context.Context, trade *models.Trade) error {
	if trade.ID == uuid.Nil {
		trade.ID = uuid.New()
	}
	if trade.Date.IsZero() {
		trade.Date = time.Now()
	}
	if trade.CreatedAt.IsZero() {
		trade.CreatedAt = time.Now()
	}

	// Pre-allocate the vector clock for this entity so the row + sync_queue share the same clock.
	clock, err := s.nextClock(ctx, trade.UserID)
	if err != nil {
		return err
	}
	trade.VectorClock = clock

	_, err = s.pool.Exec(ctx, `
		INSERT INTO trades (
			id, user_id, strategy_id, symbol, direction, entry, exit_price,
			stop_loss, take_profit, lot_size, risk_percent, risk_amount, profit_loss, rr,
			outcome, notes, date, asset_class, setup_type, session,
			tags, screenshot_url, is_backtest, is_demo, is_plan_compliant, rating,
			emotion_before, emotion_during, emotion_after, ai_analysis, import_source, external_id, vector_clock, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34)
	`, trade.ID, trade.UserID, trade.StrategyID, trade.Symbol, trade.Direction, trade.Entry, trade.ExitPrice,
		trade.StopLoss, trade.TakeProfit, trade.LotSize, trade.RiskPercent, trade.RiskAmount, trade.ProfitLoss, trade.RR,
		trade.Outcome, trade.Notes, trade.Date, trade.AssetClass, trade.SetupType, trade.Session,
		trade.Tags, trade.ScreenshotURL, trade.IsBacktest, trade.IsDemo, trade.IsPlanCompliant, trade.Rating,
		trade.EmotionBefore, trade.EmotionDuring, trade.EmotionAfter, trade.AIAnalysis, trade.ImportSource, trade.ExternalID, trade.VectorClock, trade.CreatedAt,
	)
	if err != nil {
		return err
	}
	// Append sync_queue entry + recompute section hash (without bumping clock again).
	payload := mustJSON(trade)
	if err := s.appendSyncEvent(ctx, trade.UserID, "trades", trade.ID, "create", payload, clock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, trade.UserID, "trades")
}

func (s *DatabaseStorage) UpdateTrade(ctx context.Context, tradeID uuid.UUID, userID uuid.UUID, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}

	mapping := map[string]string{
		"strategyId":      "strategy_id",
		"symbol":          "symbol",
		"direction":       "direction",
		"entry":           "entry",
		"exitPrice":       "exit_price",
		"stopLoss":        "stop_loss",
		"takeProfit":      "take_profit",
		"lotSize":         "lot_size",
		"riskPercent":     "risk_percent",
		"profitLoss":      "profit_loss",
		"riskAmount":      "risk_amount",
		"rr":              "rr",
		"outcome":         "outcome",
		"notes":           "notes",
		"date":            "date",
		"assetClass":      "asset_class",
		"setupType":       "setup_type",
		"session":         "session",
		"tags":            "tags",
		"screenshotUrl":   "screenshot_url",
		"isBacktest":      "is_backtest",
		"isPlanCompliant": "is_plan_compliant",
		"rating":          "rating",
		"emotionBefore":   "emotion_before",
		"emotionDuring":   "emotion_during",
		"emotionAfter":    "emotion_after",
		"aiAnalysis":      "ai_analysis",
		"isDemo":          "is_demo",
		"importSource":    "import_source",
		"externalId":      "external_id",
	}

	query := "UPDATE trades SET "
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

	query += fmt.Sprintf(" WHERE id = $%d AND user_id = $%d", i, i+1)
	args = append(args, tradeID, userID)

	res, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("no trade found to update")
	}

	// Bump vector_clock for this update
	newClock, err := s.nextClock(ctx, userID)
	if err != nil {
		return err
	}
	clockQuery := fmt.Sprintf("UPDATE trades SET vector_clock = $1 WHERE id = $2 AND user_id = $3")
	if _, err := s.pool.Exec(ctx, clockQuery, newClock, tradeID, userID); err != nil {
		return err
	}
	payload, _ := json.Marshal(updates)
	if err := s.appendSyncEvent(ctx, userID, "trades", tradeID, "update", payload, newClock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, userID, "trades")
}

func (s *DatabaseStorage) DeleteTrade(ctx context.Context, tradeID uuid.UUID, userID uuid.UUID) error {
	res, err := s.pool.Exec(ctx, "DELETE FROM trades WHERE id = $1 AND user_id = $2", tradeID, userID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("no trade found to delete")
	}
	newClock, err := s.nextClock(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.appendSyncEvent(ctx, userID, "trades", tradeID, "delete", []byte(`{}`), newClock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, userID, "trades")
}

// mustJSON marshals v to JSON, panicking on error (only used for trusted structs).
