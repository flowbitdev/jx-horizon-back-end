package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	appMiddleware "jx_api/internal/middleware"
)

// setupRouter builds a test Gin router with the given middleware applied to GET /test.
func setupRouter(mw gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		// Simulate an authenticated user so the rate limiter uses a stable key.
		c.Set("user_id", uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))
		c.Next()
	})
	r.POST("/mutation", mw, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

// TestSyncMutationRateLimit_Allows60_Blocks61 fires 70 requests for the same
// user and asserts that the 61st returns 429 with the expected JSON body.
func TestSyncMutationRateLimit_Allows60_Blocks61(t *testing.T) {
	r := setupRouter(appMiddleware.SyncMutationRateLimit())

	allowed := 0
	blocked := 0
	firstBlockedBody := ""

	for i := 1; i <= 70; i++ {
		req := httptest.NewRequest(http.MethodPost, "/mutation", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		switch w.Code {
		case http.StatusOK:
			allowed++
		case http.StatusTooManyRequests:
			blocked++
			if firstBlockedBody == "" {
				firstBlockedBody = w.Body.String()
			}
		default:
			t.Fatalf("unexpected status %d on request %d", w.Code, i)
		}
	}

	// Exactly 60 should succeed (burst size = 60).
	if allowed != 60 {
		t.Errorf("expected 60 allowed requests, got %d", allowed)
	}

	// Exactly 10 should be blocked.
	if blocked != 10 {
		t.Errorf("expected 10 blocked requests, got %d", blocked)
	}

	// The 61st (first blocked) must return the correct error JSON.
	if firstBlockedBody == "" {
		t.Fatal("expected a 429 response body, got empty string")
	}
	expectedKey := `"error":"rate_limit_exceeded"`
	if !containsString(firstBlockedBody, expectedKey) {
		t.Errorf("expected body to contain %q, got: %s", expectedKey, firstBlockedBody)
	}
	expectedKey2 := `"retry_after_seconds"`
	if !containsString(firstBlockedBody, expectedKey2) {
		t.Errorf("expected body to contain %q, got: %s", expectedKey2, firstBlockedBody)
	}
}

// TestSyncMutationRateLimit_RetryAfterHeader checks that the Retry-After HTTP
// header is set on a 429 response.
func TestSyncMutationRateLimit_RetryAfterHeader(t *testing.T) {
	r := setupRouter(appMiddleware.SyncMutationRateLimit())

	// Exhaust the burst.
	for i := 0; i < 60; i++ {
		req := httptest.NewRequest(http.MethodPost, "/mutation", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

	// Next request must be blocked.
	req := httptest.NewRequest(http.MethodPost, "/mutation", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header to be set on 429 response")
	}
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || searchString(s, sub))
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
