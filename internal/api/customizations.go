package api

import (
	"net/http"

	"jx_api/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type CustomizationHandler struct {
	store storage.IStorage
}

func NewCustomizationHandler(store storage.IStorage) *CustomizationHandler {
	return &CustomizationHandler{store: store}
}

func (h *CustomizationHandler) GetCustomSetups(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	setups, err := h.store.GetCustomSetups(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to fetch custom setups")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch custom setups"})
		return
	}

	c.JSON(http.StatusOK, setups)
}

func (h *CustomizationHandler) CreateCustomSetup(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Name required"})
		return
	}

	setup, err := h.store.CreateCustomSetup(c.Request.Context(), userID, req.Name)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create custom setup")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create custom setup"})
		return
	}

	c.JSON(http.StatusCreated, setup)
}

func (h *CustomizationHandler) GetCustomSessions(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	sessions, err := h.store.GetCustomSessions(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to fetch custom sessions")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch custom sessions"})
		return
	}

	c.JSON(http.StatusOK, sessions)
}

func (h *CustomizationHandler) CreateCustomSession(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Name required"})
		return
	}

	session, err := h.store.CreateCustomSession(c.Request.Context(), userID, req.Name)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create custom session")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create custom session"})
		return
	}

	c.JSON(http.StatusCreated, session)
}
