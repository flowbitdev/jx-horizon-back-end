package api

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"jx_api/internal/storage"
)

type UploadHandler struct {
	r2 *storage.R2Client
}

func NewUploadHandler() *UploadHandler {
	return &UploadHandler{
		r2: storage.NewR2ClientFromEnv(),
	}
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
	allowedExts := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".mp4": true, ".webm": true, ".svg": true}
	if !allowedExts[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file type. Allowed: jpg, jpeg, png, webp, mp4, webm, svg"})
		return
	}

	srcFile, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open uploaded file"})
		return
	}
	defer srcFile.Close()

	fileBytes, err := io.ReadAll(srcFile)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read uploaded file"})
		return
	}

	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	newFilename := uuid.New().String() + ext

	// Cloudflare R2 Upload Path
	if h.r2.IsConfigured() {
		_, err := h.r2.Upload(c.Request.Context(), newFilename, fileBytes, contentType)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{
				"url": fmt.Sprintf("/uploads/%s", newFilename),
			})
			return
		}
		log.Warn().Err(err).Msg("R2 upload failed, falling back to local storage")
	}

	// Local Fallback Path
	savePath := filepath.Join("uploads", newFilename)
	if err := c.SaveUploadedFile(file, savePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"url": fmt.Sprintf("/uploads/%s", newFilename),
	})
}
