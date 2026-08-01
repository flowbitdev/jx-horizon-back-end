package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"jx_api/internal/models"
	"jx_api/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type SyncHandler struct {
	store storage.IStorage
}

func NewSyncHandler(store storage.IStorage) *SyncHandler {
	return &SyncHandler{store: store}
}

// GetSnapshot returns the per-section content hash + latest vector clock for the user.
// The client compares each hash against its local cached hash; if different, it requests that section.
func (h *SyncHandler) GetSnapshot(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	snap, err := h.store.GetSectionSnapshot(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch sync snapshot"})
		return
	}
	clock, _ := h.store.GetLatestVectorClock(c.Request.Context(), userID)
	c.JSON(http.StatusOK, gin.H{
		"sections":     snap,
		"vector_clock": clock,
		"server_time":  time.Now().UTC().Format(time.RFC3339),
	})
}

// GetSection returns the full data for one section (used by client when its local hash differs).
// Supported sections: trades, journal, goals, strategies, chatList.
func (h *SyncHandler) GetSection(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	section := c.Param("name")

	ctx := c.Request.Context()
	switch section {
	case "trades":
		data, err := h.store.GetTrades(ctx, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch trades"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"section": "trades", "data": data})
	case "journal":
		data, err := h.store.GetJournalEntries(ctx, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch journal"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"section": "journal", "data": data})
	case "goals":
		data, err := h.store.GetGoalsRaw(ctx, userID, true) // include archived
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch goals"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"section": "goals", "data": data})
	case "strategies":
		data, err := h.store.GetStrategies(ctx, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch strategies"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"section": "strategies", "data": data})
	case "chatList":
		data, err := h.store.GetChatSessions(ctx, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch chat sessions"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"section": "chatList", "data": data})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown section: " + section})
	}
}

// GetSectionMessages returns messages for a single chat session (per-session cache).
func (h *SyncHandler) GetSectionMessages(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	sessionIDStr := c.Query("sessionId")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid sessionId"})
		return
	}
	data, err := h.store.GetChatMessages(c.Request.Context(), sessionID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch chat messages"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"session_id": sessionID, "messages": data})
}

// GetDelta handles fetching new sync events since a given vector clock (event log model).
func (h *SyncHandler) GetDelta(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	lastClock, err := parseClock(c.Query("last_clock"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid vector clock"})
		return
	}

	events, err := h.store.GetSyncEventsSince(c.Request.Context(), userID, lastClock)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch sync delta"})
		return
	}

	latestClock, _ := h.store.GetLatestVectorClock(c.Request.Context(), userID)

	c.JSON(http.StatusOK, gin.H{
		"events":        events,
		"current_clock": latestClock,
	})
}

const MAX_SYNC_PAYLOAD_BYTES = 102_400 // 100 KB max per mutation payload

// PushMutation handles a queued offline mutation from a client.
// Supported entities: trades | journal | goals | strategies | milestones.
// Chat mutations are not supported offline (chat requires live AI connection).
func (h *SyncHandler) PushMutation(c *gin.Context) {
	if c.Request.ContentLength > MAX_SYNC_PAYLOAD_BYTES {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Payload exceeds 100KB limit"})
		return
	}

	var req struct {
		EntityType string          `json:"entity_type"`
		EntityID   uuid.UUID       `json:"entity_id"`
		Action     string          `json:"action"` // create | update | delete
		Payload    json.RawMessage `json:"payload"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid mutation payload"})
		return
	}

	if len(req.Payload) > MAX_SYNC_PAYLOAD_BYTES {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Mutation payload exceeds 100KB limit"})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)
	ctx := c.Request.Context()

	clock, err := h.applyMutation(ctx, userID, req.EntityType, req.EntityID, req.Action, req.Payload)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "applied", "vector_clock": clock})
}

// applyMutation dispatches a queued mutation to the right storage handler.
// Last-Writer-Wins via the server-assigned clock.
func (h *SyncHandler) applyMutation(ctx context.Context, userID uuid.UUID, entityType string, entityID uuid.UUID, action string, payload json.RawMessage) (int64, error) {
	switch entityType {
	case "trades":
		return h.applyTradeMutation(ctx, userID, entityID, action, payload)
	case "journal":
		return h.applyJournalMutation(ctx, userID, entityID, action, payload)
	case "goals":
		return h.applyGoalMutation(ctx, userID, entityID, action, payload)
	case "strategies":
		return h.applyStrategyMutation(ctx, userID, entityID, action, payload)
	case "milestones", "goal_milestones":
		return h.applyMilestoneMutation(ctx, userID, entityID, action, payload)
	default:
		return 0, fmt.Errorf("unsupported entity_type for offline mutation: %s", entityType)
	}
}

func (h *SyncHandler) applyMilestoneMutation(ctx context.Context, userID, entityID uuid.UUID, action string, payload json.RawMessage) (int64, error) {
	switch action {
	case "create":
		var m models.GoalMilestone
		if err := json.Unmarshal(payload, &m); err != nil {
			return 0, fmt.Errorf("invalid milestone payload: %w", err)
		}
		if m.ID == uuid.Nil {
			m.ID = entityID
		}
		if err := h.store.CreateGoalMilestone(ctx, &m, userID); err != nil {
			return 0, err
		}
		clock, _ := h.store.GetLatestVectorClock(ctx, userID)
		return clock, nil
	case "delete":
		var m models.GoalMilestone
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &m)
		}
		if err := h.store.DeleteGoalMilestone(ctx, entityID, m.GoalID, userID); err != nil {
			return 0, err
		}
		clock, _ := h.store.GetLatestVectorClock(ctx, userID)
		return clock, nil
	default:
		return 0, fmt.Errorf("unsupported action for milestones: %s", action)
	}
}

func (h *SyncHandler) applyTradeMutation(ctx context.Context, userID, entityID uuid.UUID, action string, payload json.RawMessage) (int64, error) {
	switch action {
	case "create":
		var t models.Trade
		if err := json.Unmarshal(payload, &t); err != nil {
			return 0, fmt.Errorf("invalid trade payload: %w", err)
		}
		// Server-side validation bounds checks
		if t.Symbol == "" {
			return 0, fmt.Errorf("trade symbol is required")
		}
		if t.Entry < 0 || (t.ExitPrice != nil && *t.ExitPrice < 0) {
			return 0, fmt.Errorf("trade prices cannot be negative")
		}
		if t.LotSize < 0 {
			return 0, fmt.Errorf("trade lot size cannot be negative")
		}
		t.UserID = userID
		if t.ID == uuid.Nil {
			t.ID = entityID
		}
		if err := h.store.CreateTrade(ctx, &t); err != nil {
			return 0, err
		}
		return t.VectorClock, nil
	case "update":
		var updates map[string]interface{}
		if err := json.Unmarshal(payload, &updates); err != nil {
			return 0, fmt.Errorf("invalid trade updates: %w", err)
		}
		if ep, ok := updates["entryPrice"].(float64); ok && ep < 0 {
			return 0, fmt.Errorf("entry price cannot be negative")
		}
		if err := h.store.UpdateTrade(ctx, entityID, userID, updates); err != nil {
			return 0, err
		}
		// UpdateTrade bumps the clock internally; fetch latest to return
		clock, _ := h.store.GetLatestVectorClock(ctx, userID)
		return clock, nil
	case "delete":
		if err := h.store.DeleteTrade(ctx, entityID, userID); err != nil {
			return 0, err
		}
		clock, _ := h.store.GetLatestVectorClock(ctx, userID)
		return clock, nil
	default:
		return 0, fmt.Errorf("unsupported action for trades: %s", action)
	}
}

func (h *SyncHandler) applyJournalMutation(ctx context.Context, userID, entityID uuid.UUID, action string, payload json.RawMessage) (int64, error) {
	switch action {
	case "create":
		var e models.DailyJournal
		if err := json.Unmarshal(payload, &e); err != nil {
			return 0, fmt.Errorf("invalid journal payload: %w", err)
		}
		e.UserID = userID
		if e.ID == uuid.Nil {
			e.ID = entityID
		}
		if err := h.store.CreateJournalEntry(ctx, &e); err != nil {
			return 0, err
		}
		return e.VectorClock, nil
	case "update":
		var updates map[string]interface{}
		if err := json.Unmarshal(payload, &updates); err != nil {
			return 0, fmt.Errorf("invalid journal updates: %w", err)
		}
		if err := h.store.UpdateJournalEntry(ctx, entityID, userID, updates); err != nil {
			return 0, err
		}
		clock, _ := h.store.GetLatestVectorClock(ctx, userID)
		return clock, nil
	case "delete":
		if err := h.store.DeleteJournalEntry(ctx, entityID, userID); err != nil {
			return 0, err
		}
		clock, _ := h.store.GetLatestVectorClock(ctx, userID)
		return clock, nil
	default:
		return 0, fmt.Errorf("unsupported action for journal: %s", action)
	}
}

func (h *SyncHandler) applyGoalMutation(ctx context.Context, userID, entityID uuid.UUID, action string, payload json.RawMessage) (int64, error) {
	switch action {
	case "create":
		var g models.Goal
		if err := json.Unmarshal(payload, &g); err != nil {
			return 0, fmt.Errorf("invalid goal payload: %w", err)
		}
		g.UserID = userID
		if g.ID == uuid.Nil {
			g.ID = entityID
		}
		if err := h.store.CreateGoal(ctx, &g); err != nil {
			return 0, err
		}
		return g.VectorClock, nil
	case "update":
		var updates map[string]interface{}
		if err := json.Unmarshal(payload, &updates); err != nil {
			return 0, fmt.Errorf("invalid goal updates: %w", err)
		}
		if err := h.store.UpdateGoal(ctx, entityID, userID, updates); err != nil {
			return 0, err
		}
		clock, _ := h.store.GetLatestVectorClock(ctx, userID)
		return clock, nil
	case "delete":
		if err := h.store.DeleteGoal(ctx, entityID, userID); err != nil {
			return 0, err
		}
		clock, _ := h.store.GetLatestVectorClock(ctx, userID)
		return clock, nil
	default:
		return 0, fmt.Errorf("unsupported action for goals: %s", action)
	}
}

func (h *SyncHandler) applyStrategyMutation(ctx context.Context, userID, entityID uuid.UUID, action string, payload json.RawMessage) (int64, error) {
	switch action {
	case "create":
		var s models.Strategy
		if err := json.Unmarshal(payload, &s); err != nil {
			return 0, fmt.Errorf("invalid strategy payload: %w", err)
		}
		s.UserID = userID
		if s.ID == uuid.Nil {
			s.ID = entityID
		}
		if err := h.store.CreateStrategy(ctx, &s); err != nil {
			return 0, err
		}
		return s.VectorClock, nil
	case "update":
		var updates map[string]interface{}
		if err := json.Unmarshal(payload, &updates); err != nil {
			return 0, fmt.Errorf("invalid strategy updates: %w", err)
		}
		if err := h.store.UpdateStrategy(ctx, entityID, userID, updates); err != nil {
			return 0, err
		}
		clock, _ := h.store.GetLatestVectorClock(ctx, userID)
		return clock, nil
	case "delete":
		if err := h.store.DeleteStrategy(ctx, entityID, userID); err != nil {
			return 0, err
		}
		clock, _ := h.store.GetLatestVectorClock(ctx, userID)
		return clock, nil
	default:
		return 0, fmt.Errorf("unsupported action for strategies: %s", action)
	}
}

func parseClock(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}
