package storage

import (
	"context"
	"encoding/json"
	"time"

	"jx_api/internal/models"
	"github.com/google/uuid"
)

func (s *DatabaseStorage) GetChatSessions(ctx context.Context, userID uuid.UUID) ([]models.ChatSession, error) {
	rows, err := s.pool.Query(ctx, "SELECT id, user_id, title, created_at, updated_at FROM chat_sessions WHERE user_id = $1 ORDER BY updated_at DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []models.ChatSession
	for rows.Next() {
		var sess models.ChatSession
		err := rows.Scan(&sess.ID, &sess.UserID, &sess.Title, &sess.CreatedAt, &sess.UpdatedAt)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

func (s *DatabaseStorage) CreateChatSession(ctx context.Context, session *models.ChatSession) error {
	if session.ID == uuid.Nil {
		session.ID = uuid.New()
	}
	now := time.Now()
	session.CreatedAt = now
	session.UpdatedAt = now

	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_sessions (id, user_id, title, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`, session.ID, session.UserID, session.Title, session.CreatedAt, session.UpdatedAt)
	if err != nil {
		return err
	}
	if _, err := s.RecordEvent(ctx, session.UserID, "chatList", session.ID, "create", []byte(`{}`)); err != nil {
		return err
	}
	return nil
}

func (s *DatabaseStorage) GetChatMessages(ctx context.Context, sessionID uuid.UUID, userID uuid.UUID) ([]models.AIChatMessage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, session_id, role, content, metadata, model_used, latency_ms, is_pending, vector_clock, timestamp
		FROM ai_chat_messages
		WHERE session_id = $1 AND user_id = $2
		ORDER BY timestamp ASC
	`, sessionID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []models.AIChatMessage
	for rows.Next() {
		var m models.AIChatMessage
		err := rows.Scan(&m.ID, &m.UserID, &m.SessionID, &m.Role, &m.Content, &m.Metadata, &m.ModelUsed, &m.LatencyMs, &m.IsPending, &m.VectorClock, &m.Timestamp)
		if err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, nil
}

func (s *DatabaseStorage) CreateChatMessage(ctx context.Context, msg *models.AIChatMessage) error {
	if msg.ID == uuid.Nil {
		msg.ID = uuid.New()
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}

	clock, err := s.nextClock(ctx, msg.UserID)
	if err != nil {
		return err
	}
	msg.VectorClock = clock

	_, err = s.pool.Exec(ctx, `
		INSERT INTO ai_chat_messages (id, user_id, session_id, role, content, metadata, model_used, latency_ms, is_pending, vector_clock, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, msg.ID, msg.UserID, msg.SessionID, msg.Role, msg.Content, msg.Metadata, msg.ModelUsed, msg.LatencyMs, msg.IsPending, msg.VectorClock, msg.Timestamp)
	if err != nil {
		return err
	}
	// Update session updated_at
	if _, err := s.pool.Exec(ctx, "UPDATE chat_sessions SET updated_at = $1 WHERE id = $2", time.Now(), msg.SessionID); err != nil {
		return err
	}
	// Record event for the chatList section hash (so the chat list shows the new message)
	payload := mustJSON(msg)
	return s.appendSyncEvent(ctx, msg.UserID, "chatList", msg.SessionID, "message_added", payload, clock)
}

func (s *DatabaseStorage) UpdatePendingChatMessage(ctx context.Context, sessionID uuid.UUID, userID uuid.UUID, content string, metadata map[string]interface{}, modelUsed *string, latencyMs *int) error {
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE ai_chat_messages
		SET content = $1, metadata = $2, model_used = $3, latency_ms = $4, is_pending = false
		WHERE id = (
			SELECT id
			FROM ai_chat_messages
			WHERE session_id = $5 AND user_id = $6 AND role = 'assistant' AND is_pending = true
			ORDER BY timestamp DESC
			LIMIT 1
		)
	`, content, metadataJSON, modelUsed, latencyMs, sessionID, userID)
	return err
}

func (s *DatabaseStorage) UpdateChatSessionTitle(ctx context.Context, sessionID uuid.UUID, userID uuid.UUID, title string) error {
	_, err := s.pool.Exec(ctx, "UPDATE chat_sessions SET title = $1, updated_at = $2 WHERE id = $3 AND user_id = $4", title, time.Now(), sessionID, userID)
	if err != nil {
		return err
	}
	clock, err := s.nextClock(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.appendSyncEvent(ctx, userID, "chatList", sessionID, "renamed", []byte(`{}`), clock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, userID, "chatList")
}

func (s *DatabaseStorage) DeleteChatSession(ctx context.Context, sessionID uuid.UUID, userID uuid.UUID) error {
	// Delete child messages first (FK should also cascade, but be safe)
	if _, err := s.pool.Exec(ctx, "DELETE FROM ai_chat_messages WHERE session_id = $1 AND user_id = $2", sessionID, userID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, "DELETE FROM chat_sessions WHERE id = $1 AND user_id = $2", sessionID, userID)
	if err != nil {
		return err
	}
	clock, err := s.nextClock(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.appendSyncEvent(ctx, userID, "chatList", sessionID, "delete", []byte(`{}`), clock); err != nil {
		return err
	}
	return s.recomputeSectionHash(ctx, userID, "chatList")
}
