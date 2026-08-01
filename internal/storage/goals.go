package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"jx_api/internal/models"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

func (s *DatabaseStorage) GetGoal(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*models.Goal, error) {
	var g models.Goal
	err := s.pool.QueryRow(ctx, "SELECT id, user_id, name, target_metric, target_value, current_value, deadline, status, is_demo, is_backtest, start_date, archived_at, category, vector_clock, created_at FROM goals WHERE id = $1 AND user_id = $2", id, userID).Scan(
		&g.ID, &g.UserID, &g.Name, &g.TargetMetric, &g.TargetValue, &g.CurrentValue,
		&g.Deadline, &g.Status, &g.IsDemo, &g.IsBacktest, &g.StartDate, &g.ArchivedAt, &g.Category, &g.VectorClock, &g.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *DatabaseStorage) GetGoalsRaw(ctx context.Context, userID uuid.UUID, includeArchived bool) ([]models.Goal, error) {
	goals := []models.Goal{}
	query := "SELECT id, user_id, name, target_metric, target_value, current_value, deadline, status, is_demo, is_backtest, start_date, archived_at, category, vector_clock, created_at FROM goals WHERE user_id = $1"
	if !includeArchived {
		query += " AND archived_at IS NULL"
	}
	query += " ORDER BY deadline ASC NULLS LAST, created_at DESC"
	
	rows, err := s.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var g models.Goal
		err := rows.Scan(
			&g.ID, &g.UserID, &g.Name, &g.TargetMetric, &g.TargetValue, &g.CurrentValue,
			&g.Deadline, &g.Status, &g.IsDemo, &g.IsBacktest, &g.StartDate, &g.ArchivedAt, &g.Category, &g.VectorClock, &g.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		g.Milestones = []models.GoalMilestone{} // Initialize empty array
		goals = append(goals, g)
	}

	// Fetch milestones for all these goals in one query (N+1 fix)
	if len(goals) > 0 {
		goalIDs := make([]uuid.UUID, len(goals))
		for i, g := range goals {
			goalIDs[i] = g.ID
		}

		mRows, err := s.pool.Query(ctx, "SELECT id, goal_id, label, threshold_value, is_percentage, percentage_value, reached_at, created_at FROM goal_milestones WHERE goal_id = ANY($1) ORDER BY threshold_value ASC", goalIDs)
		if err == nil {
			defer mRows.Close()
			milestonesByGoal := make(map[uuid.UUID][]models.GoalMilestone)
			for mRows.Next() {
				var m models.GoalMilestone
				if scanErr := mRows.Scan(&m.ID, &m.GoalID, &m.Label, &m.ThresholdValue, &m.IsPercentage, &m.PercentageValue, &m.ReachedAt, &m.CreatedAt); scanErr == nil {
					milestonesByGoal[m.GoalID] = append(milestonesByGoal[m.GoalID], m)
				}
			}
			for i := range goals {
				if ms, ok := milestonesByGoal[goals[i].ID]; ok {
					goals[i].Milestones = ms
				}
			}
		}
	}

	return goals, nil
}

func (s *DatabaseStorage) CreateGoal(ctx context.Context, goal *models.Goal) error {
	if goal.ID == uuid.Nil {
		goal.ID = uuid.New()
	}
	if goal.CreatedAt.IsZero() {
		goal.CreatedAt = time.Now()
	}
	if goal.StartDate.IsZero() {
		goal.StartDate = time.Now()
	}

	if goal.Category == "" {
		goal.Category = "general"
	}

	clock, err := s.nextClock(ctx, goal.UserID)
	if err != nil {
		return err
	}
	goal.VectorClock = clock

	_, err = s.pool.Exec(ctx, `
		INSERT INTO goals (
			id, user_id, name, target_metric, target_value, current_value,
			deadline, status, is_demo, is_backtest, start_date, archived_at, category, vector_clock, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`, goal.ID, goal.UserID, goal.Name, goal.TargetMetric, goal.TargetValue, goal.CurrentValue,
		goal.Deadline, goal.Status, goal.IsDemo, goal.IsBacktest, goal.StartDate, goal.ArchivedAt, goal.Category, goal.VectorClock, goal.CreatedAt)
	if err != nil {
		return err
	}
	payload := mustJSON(goal)
	if err := s.appendSyncEvent(ctx, goal.UserID, "goals", goal.ID, "create", payload, clock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, goal.UserID, "goals")
}

func (s *DatabaseStorage) UpdateGoal(ctx context.Context, id uuid.UUID, userID uuid.UUID, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}

	// Map frontend camelCase keys to backend snake_case columns
	mapping := map[string]string{
		"name":         "name",
		"targetMetric": "target_metric",
		"targetValue":  "target_value",
		"currentValue": "current_value",
		"deadline":     "deadline",
		"status":       "status",
		"isDemo":       "is_demo",
		"isBacktest":   "is_backtest",
		"startDate":    "start_date",
		"archivedAt":   "archived_at",
		"category":     "category",
	}

	query := "UPDATE goals SET "
	args := []interface{}{id, userID}
	i := 3
	first := true

	// Sort keys for deterministic SQL generation
	keys := make([]string, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := updates[k]
		col, ok := mapping[k]
		if !ok {
			continue // Skip unknown fields
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
		return nil // No valid fields to update
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
	if err := s.appendSyncEvent(ctx, userID, "goals", id, "update", payload, newClock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, userID, "goals")
}

func (s *DatabaseStorage) DeleteGoal(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM goals WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		return err
	}
	newClock, err := s.nextClock(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.appendSyncEvent(ctx, userID, "goals", id, "delete", []byte(`{}`), newClock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, userID, "goals")
}

func (s *DatabaseStorage) SyncUserGoals(ctx context.Context, userID uuid.UUID) error {
	// Fetch all non-archived goals
	goals, err := s.GetGoalsRaw(ctx, userID, false)
	if err != nil {
		return err
	}

	now := time.Now()

	for _, g := range goals {
		err = nil
		// Optimization: if we wanted to filter by a specific trade, we could pass trade details.
		// For now, we leave the robust sync but ensure it skips quickly if not needed.
		var newValue float64
		switch g.TargetMetric {
		case "profit":
			err = s.pool.QueryRow(ctx, `
				SELECT COALESCE(SUM(profit_loss), 0) FROM trades 
				WHERE user_id = $1 AND is_demo = $2 AND is_backtest = $3 AND date >= $4
			`, userID, g.IsDemo, g.IsBacktest, g.StartDate).Scan(&newValue)
		case "win_rate":
			var total, wins int
			err = s.pool.QueryRow(ctx, `
				SELECT COUNT(*), COUNT(CASE WHEN outcome = 'win' THEN 1 END) 
				FROM trades 
				WHERE user_id = $1 AND is_demo = $2 AND is_backtest = $3 AND date >= $4
			`, userID, g.IsDemo, g.IsBacktest, g.StartDate).Scan(&total, &wins)
			if total > 0 {
				newValue = (float64(wins) / float64(total)) * 100
			}
		case "trades_count":
			var total int
			err = s.pool.QueryRow(ctx, `
				SELECT COUNT(*) FROM trades 
				WHERE user_id = $1 AND is_demo = $2 AND is_backtest = $3 AND date >= $4
			`, userID, g.IsDemo, g.IsBacktest, g.StartDate).Scan(&total)
			newValue = float64(total)
		case "risk_reward":
			err = s.pool.QueryRow(ctx, `
				SELECT COALESCE(AVG(rr), 0) FROM trades 
				WHERE user_id = $1 AND is_demo = $2 AND is_backtest = $3 AND date >= $4
			`, userID, g.IsDemo, g.IsBacktest, g.StartDate).Scan(&newValue)
		case "profit_factor":
			var grossWin, grossLoss float64
			err = s.pool.QueryRow(ctx, `
				SELECT 
					COALESCE(SUM(CASE WHEN profit_loss > 0 THEN profit_loss ELSE 0 END), 0),
					COALESCE(SUM(CASE WHEN profit_loss < 0 THEN ABS(profit_loss) ELSE 0 END), 0)
				FROM trades 
				WHERE user_id = $1 AND is_demo = $2 AND is_backtest = $3 AND date >= $4
			`, userID, g.IsDemo, g.IsBacktest, g.StartDate).Scan(&grossWin, &grossLoss)
			if grossLoss > 0 {
				newValue = grossWin / grossLoss
			} else {
				newValue = grossWin
			}
		case "avg_profit_per_trade":
			err = s.pool.QueryRow(ctx, `
				SELECT COALESCE(AVG(profit_loss), 0) FROM trades 
				WHERE user_id = $1 AND is_demo = $2 AND is_backtest = $3 AND date >= $4
			`, userID, g.IsDemo, g.IsBacktest, g.StartDate).Scan(&newValue)
		case "consecutive_wins":
			rows, e := s.pool.Query(ctx, `SELECT outcome FROM trades WHERE user_id = $1 AND is_demo = $2 AND is_backtest = $3 AND date >= $4 ORDER BY date ASC`, userID, g.IsDemo, g.IsBacktest, g.StartDate)
			if e == nil {
				maxStreak := 0
				currentStreak := 0
				for rows.Next() {
					var outcome *string
					if scanErr := rows.Scan(&outcome); scanErr == nil {
						if outcome != nil && *outcome == "win" {
							currentStreak++
							if currentStreak > maxStreak {
								maxStreak = currentStreak
							}
						} else if outcome != nil && *outcome == "loss" {
							currentStreak = 0
						}
					}
				}
				rows.Close()
				newValue = float64(maxStreak)
			} else {
				err = e
			}
		}

		if err != nil {
			log.Error().Err(err).Msgf("Error aggregating goal %s", g.ID)
			continue
		}

		// Update Status
		newStatus := "active"
		if newValue >= g.TargetValue {
			newStatus = "completed"
		} else if g.Deadline != nil && g.Deadline.Before(now) {
			newStatus = "failed"
		}

		// Only write to DB if the value or status actually changed
		if newValue != g.CurrentValue || newStatus != g.Status {
			tx, err := s.pool.Begin(ctx)
			if err != nil {
				continue
			}

			_, err = tx.Exec(ctx, `
				UPDATE goals 
				SET current_value = $1, status = $2, vector_clock = vector_clock + 1
				WHERE id = $3 AND user_id = $4
			`, newValue, newStatus, g.ID, userID)
			
			if err == nil {
				payloadMap := map[string]interface{}{
					"id":            g.ID,
					"user_id":       userID,
					"current_value": newValue,
					"status":        newStatus,
				}
				payloadBytes, _ := json.Marshal(payloadMap)
				clock, _ := s.nextClock(ctx, userID)
				_ = s.appendSyncEvent(ctx, userID, "goals", g.ID, "update", payloadBytes, clock)
				_ = tx.Commit(ctx)
			} else {
				_ = tx.Rollback(ctx)
				log.Error().Err(err).Msgf("Error updating goal %s", g.ID)
			}
		}
	}

	return nil
}

// --- Goal Milestones ---

func (s *DatabaseStorage) GetGoalMilestones(ctx context.Context, goalID uuid.UUID) ([]models.GoalMilestone, error) {
	milestones := []models.GoalMilestone{}
	rows, err := s.pool.Query(ctx, "SELECT id, goal_id, label, threshold_value, is_percentage, percentage_value, reached_at, created_at FROM goal_milestones WHERE goal_id = $1 ORDER BY threshold_value ASC", goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var m models.GoalMilestone
		if err := rows.Scan(&m.ID, &m.GoalID, &m.Label, &m.ThresholdValue, &m.IsPercentage, &m.PercentageValue, &m.ReachedAt, &m.CreatedAt); err != nil {
			return nil, err
		}
		milestones = append(milestones, m)
	}
	return milestones, nil
}

func (s *DatabaseStorage) CreateGoalMilestone(ctx context.Context, milestone *models.GoalMilestone, userID uuid.UUID) error {
	// Ownership verification
	var exists bool
	err := s.pool.QueryRow(ctx, "SELECT true FROM goals WHERE id = $1 AND user_id = $2", milestone.GoalID, userID).Scan(&exists)
	if err != nil || !exists {
		return fmt.Errorf("goal not found or unauthorized")
	}

	// Validation: Check that the threshold doesn't exceed the target value
	var targetValue float64
	err = s.pool.QueryRow(ctx, "SELECT target_value FROM goals WHERE id = $1", milestone.GoalID).Scan(&targetValue)
	if err == nil && milestone.ThresholdValue > targetValue {
		milestone.ThresholdValue = targetValue
	}

	// Validation: Prevent exact duplicate thresholds
	var dupCount int
	err = s.pool.QueryRow(ctx, "SELECT count(*) FROM goal_milestones WHERE goal_id = $1 AND threshold_value = $2", milestone.GoalID, milestone.ThresholdValue).Scan(&dupCount)
	if err == nil && dupCount > 0 {
		return fmt.Errorf("a milestone with this threshold already exists")
	}

	if milestone.ID == uuid.Nil {
		milestone.ID = uuid.New()
	}
	if milestone.CreatedAt.IsZero() {
		milestone.CreatedAt = time.Now()
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO goal_milestones (id, goal_id, label, threshold_value, is_percentage, percentage_value, reached_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, milestone.ID, milestone.GoalID, milestone.Label, milestone.ThresholdValue, milestone.IsPercentage, milestone.PercentageValue, milestone.ReachedAt, milestone.CreatedAt)
	return err
}

func (s *DatabaseStorage) UpdateGoalMilestone(ctx context.Context, id uuid.UUID, goalID uuid.UUID, userID uuid.UUID, updates map[string]interface{}) error {
	// Ownership verification
	var exists bool
	err := s.pool.QueryRow(ctx, "SELECT true FROM goals WHERE id = $1 AND user_id = $2", goalID, userID).Scan(&exists)
	if err != nil || !exists {
		return fmt.Errorf("goal not found or unauthorized")
	}

	if len(updates) == 0 {
		return nil
	}

	mapping := map[string]string{
		"label":           "label",
		"thresholdValue":  "threshold_value",
		"isPercentage":    "is_percentage",
		"percentageValue": "percentage_value",
		"reachedAt":       "reached_at",
	}

	query := "UPDATE goal_milestones SET "
	args := []interface{}{id, goalID}
	i := 3
	first := true
	for k, v := range updates {
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

	query += " WHERE id = $1 AND goal_id = $2"

	_, err = s.pool.Exec(ctx, query, args...)
	return err
}

func (s *DatabaseStorage) DeleteGoalMilestone(ctx context.Context, id uuid.UUID, goalID uuid.UUID, userID uuid.UUID) error {
	// Ownership verification via subquery
	_, err := s.pool.Exec(ctx, "DELETE FROM goal_milestones WHERE id = $1 AND goal_id = $2 AND EXISTS (SELECT 1 FROM goals WHERE id = $2 AND user_id = $3)", id, goalID, userID)
	return err
}
