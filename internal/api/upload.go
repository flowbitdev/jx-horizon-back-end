package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type UploadHandler struct{}

func NewUploadHandler() *UploadHandler {
	return &UploadHandler{}
}

func (h *UploadHandler) UploadImage(c *gin.Context) {
	// Try 'file' first (new frontend), then 'image' (legacy)
	file, err := c.FormFile("file")
	if err != nil {
		file, err = c.FormFile("image")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No file provided (use field name 'file')"})
			return
		}
	}

	// Enforce 25MB max size cap
	if file.Size > 25<<20 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File size exceeds 25MB limit"})
		return
	}

	// Validate allowed extensions
	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowedExts := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".mp4": true, ".webm": true}
	if !allowedExts[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file type. Allowed: jpg, jpeg, png, webp, mp4, webm"})
		return
	}

	newFilename := uuid.New().String() + ext
	savePath := filepath.Join("uploads", newFilename)

	if err := c.SaveUploadedFile(file, savePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	// Return the relative URL
	// In development, this will be http://localhost:5000/uploads/uuid.ext
	c.JSON(http.StatusOK, gin.H{
		"url": fmt.Sprintf("/uploads/%s", newFilename),
	})
}
