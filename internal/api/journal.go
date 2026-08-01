package api

import (
	"net/http"
	"strconv"

	"jx_api/internal/models"
	"jx_api/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type JournalHandler struct {
	store storage.IStorage
}

func NewJournalHandler(store storage.IStorage) *JournalHandler {
	return &JournalHandler{store: store}
}

func (h *JournalHandler) GetJournalEntries(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	limitStr := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 50
	}

	entries, err := h.store.GetJournalEntries(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to fetch journal entries")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch journal entries"})
		return
	}

	if len(entries) > limit {
		entries = entries[:limit]
	}
	
	if entries == nil {
		entries = make([]models.DailyJournal, 0)
	}

	c.JSON(http.StatusOK, entries)
}

func (h *JournalHandler) CreateJournalEntry(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	
	var entry models.DailyJournal
	if err := c.ShouldBindJSON(&entry); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	entry.UserID = userID

	if err := h.store.CreateJournalEntry(c.Request.Context(), &entry); err != nil {
		log.Error().Err(err).Msg("Failed to create journal entry")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create journal entry"})
		return
	}

	c.JSON(http.StatusCreated, entry)
}

func (h *JournalHandler) UpdateJournalEntry(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	
	entryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid journal entry ID"})
		return
	}

	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Filter safety (in a real production app we'd validate keys properly)
	if err := h.store.UpdateJournalEntry(c.Request.Context(), entryID, userID, updates); err != nil {
		log.Error().Err(err).Msg("Failed to update journal entry")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update journal entry"})
		return
	}

	// Fetch the updated entry to return it full (matching frontend expectance)
	entry, err := h.store.GetJournalEntry(c.Request.Context(), entryID, userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to retrieve updated journal entry")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve updated journal entry"})
		return
	}

	c.JSON(http.StatusOK, entry)
}

func (h *JournalHandler) DeleteJournalEntry(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	
	entryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid journal entry ID"})
		return
	}

	if err := h.store.DeleteJournalEntry(c.Request.Context(), entryID, userID); err != nil {
		log.Error().Err(err).Msg("Failed to delete journal entry")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete journal entry"})
		return
	}

	c.Status(http.StatusNoContent)
}
