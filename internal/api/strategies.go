package api

import (
	"net/http"

	"jx_api/internal/models"
	"jx_api/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type StrategyHandler struct {
	store storage.IStorage
}

func NewStrategyHandler(store storage.IStorage) *StrategyHandler {
	return &StrategyHandler{store: store}
}

func (h *StrategyHandler) GetStrategies(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	strategies, err := h.store.GetStrategies(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch strategies"})
		return
	}
	c.JSON(http.StatusOK, strategies)
}

func (h *StrategyHandler) CreateStrategy(c *gin.Context) {
	var st models.Strategy
	if err := c.ShouldBindJSON(&st); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	st.UserID = c.MustGet("user_id").(uuid.UUID)
	if err := h.store.CreateStrategy(c.Request.Context(), &st); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create strategy: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, st)
}

func (h *StrategyHandler) UpdateStrategy(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid strategy ID format"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)

	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.store.UpdateStrategy(c.Request.Context(), id, userID, updates); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update strategy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func (h *StrategyHandler) DeleteStrategy(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid strategy ID format"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)

	if err := h.store.DeleteStrategy(c.Request.Context(), id, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete strategy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
