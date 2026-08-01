package api

import (
	"net/http"

	"jx_api/internal/auth"
	"jx_api/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ProfileHandler struct {
	store storage.IStorage
}

func NewProfileHandler(store storage.IStorage) *ProfileHandler {
	return &ProfileHandler{store: store}
}

func (h *ProfileHandler) GetProfile(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	user, err := h.store.GetUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch profile"})
		return
	}
	c.JSON(http.StatusOK, user)
}

func (h *ProfileHandler) GetLease(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	lease, err := auth.GenerateLease(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate lease", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, lease)
}

func (h *ProfileHandler) UpdateProfile(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	allowedFields := map[string]bool{
		"name":              true,
		"username":          true,
		"avatar_url":        true,
		"theme_color":       true,
		"bio":               true,
		"account_size":      true,
		"currency":          true,
		"max_risk_percent":  true,
		"favorites":         true,
	}

	filteredUpdates := make(map[string]interface{})

	// Handle special frontend cases like 'profilePicture' mapping to 'avatar_url'
	if pp, ok := updates["profilePicture"]; ok {
		filteredUpdates["avatar_url"] = pp
	}

	for k, v := range updates {
		if allowedFields[k] {
			filteredUpdates[k] = v
		}
	}

	if err := h.store.UpdateUser(c.Request.Context(), userID, filteredUpdates); err != nil {
		// Handle unique constraint on username simple check
		// For pgx usually err.Error() contains "unique constraint"
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update profile", "details": err.Error()})
		return
	}

	user, err := h.store.GetUser(c.Request.Context(), userID)
	if err != nil || user == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch updated profile"})
		return
	}
	c.JSON(http.StatusOK, user)
}

func (h *ProfileHandler) GetUser(c *gin.Context) {
	userIdStr := c.Param("id")
	userID, err := uuid.Parse(userIdStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	user, err := h.store.GetUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user"})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	// Map to public profile DTO
	publicProfile := gin.H{
		"id":           user.ID,
		"username":     user.Username,
		"name":         user.Name,
		"avatarUrl":    user.AvatarURL,
		"bio":          user.Bio,
		"rank":         user.Rank,
		"xp":           user.XP,
		"createdAt":    user.CreatedAt,
		"themeColor":   user.ThemeColor,
	}

	c.JSON(http.StatusOK, publicProfile)
}

func (h *ProfileHandler) GetFavorites(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	user, err := h.store.GetUser(c.Request.Context(), userID)
	if err != nil || user == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user"})
		return
	}
	
	if user.Favorites == nil {
		c.JSON(http.StatusOK, make([]string, 0))
		return
	}
	c.JSON(http.StatusOK, user.Favorites)
}

func (h *ProfileHandler) UpdateFavorites(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	var req struct {
		Favorites []string `json:"favorites"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.store.UpdateUser(c.Request.Context(), userID, map[string]interface{}{"favorites": req.Favorites}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update favorites"})
		return
	}

	c.Status(http.StatusOK)
}
