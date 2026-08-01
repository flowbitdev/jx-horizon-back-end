package api

import (
	"net/http"

	"jx_api/internal/models"
	"jx_api/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type GoalHandler struct {
	store storage.IStorage
}

func NewGoalHandler(store storage.IStorage) *GoalHandler {
	return &GoalHandler{store: store}
}

func (h *GoalHandler) GetGoals(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	includeArchived := c.Query("includeArchived") == "true"
	goals, err := h.store.GetGoalsRaw(c.Request.Context(), userID, includeArchived)
	if err != nil {
		println("GetGoals error:", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch goals"})
		return
	}
	c.JSON(http.StatusOK, goals)
}

func (h *GoalHandler) CreateGoal(c *gin.Context) {
	var g models.Goal
	if err := c.ShouldBindJSON(&g); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	g.UserID = c.MustGet("user_id").(uuid.UUID)
	if err := h.store.CreateGoal(c.Request.Context(), &g); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create goal"})
		return
	}
	c.JSON(http.StatusCreated, g)
}

func (h *GoalHandler) UpdateGoal(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid goal ID format"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)

	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.store.UpdateGoal(c.Request.Context(), id, userID, updates); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update goal"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func (h *GoalHandler) DeleteGoal(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid goal ID format"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)

	if err := h.store.DeleteGoal(c.Request.Context(), id, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete goal"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// --- Goal Milestones ---

func (h *GoalHandler) GetGoalMilestones(c *gin.Context) {
	goalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid goal ID format"})
		return
	}
	
	// Add ownership check here
	userID := c.MustGet("user_id").(uuid.UUID)
	_, err = h.store.GetGoal(c.Request.Context(), goalID, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Goal not found"})
		return
	}

	milestones, err := h.store.GetGoalMilestones(c.Request.Context(), goalID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch milestones"})
		return
	}
	c.JSON(http.StatusOK, milestones)
}

func (h *GoalHandler) CreateGoalMilestone(c *gin.Context) {
	var milestone models.GoalMilestone
	if err := c.ShouldBindJSON(&milestone); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	goalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid goal ID format"})
		return
	}
	milestone.GoalID = goalID
	userID := c.MustGet("user_id").(uuid.UUID)

	if err := h.store.CreateGoalMilestone(c.Request.Context(), &milestone, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create milestone"})
		return
	}
	c.JSON(http.StatusCreated, milestone)
}

func (h *GoalHandler) UpdateGoalMilestone(c *gin.Context) {
	id, err := uuid.Parse(c.Param("milestoneId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid milestone ID format"})
		return
	}
	goalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid goal ID format"})
		return
	}

	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)

	if err := h.store.UpdateGoalMilestone(c.Request.Context(), id, goalID, userID, updates); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update milestone"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func (h *GoalHandler) DeleteGoalMilestone(c *gin.Context) {
	id, err := uuid.Parse(c.Param("milestoneId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid milestone ID format"})
		return
	}
	goalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid goal ID format"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)

	if err := h.store.DeleteGoalMilestone(c.Request.Context(), id, goalID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete milestone"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
