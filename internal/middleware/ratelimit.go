// Package middleware provides Gin middleware for JX Horizon.
package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

// limitTier represents a rate limit configuration.
type limitTier struct {
	// r is the number of tokens added per second (tokens/second = requests/minute / 60).
	r rate.Limit
	// burst is the maximum burst size (equal to the per-minute cap).
	burst int
}

var (
	// syncMutationLimit: 60 requests per minute → 1 r/s, burst 60.
	syncMutationLimit = limitTier{r: rate.Limit(1), burst: 60}
	// writeLimitConfig: 120 requests per minute → 2 r/s, burst 120.
	writeLimit = limitTier{r: rate.Limit(2), burst: 120}
	// readLimitConfig: 300 requests per minute → 5 r/s, burst 300.
	readLimit = limitTier{r: rate.Limit(5), burst: 300}
)

// userLimiter holds the rate.Limiter plus the time it was last accessed,
// so the cleanup goroutine can remove stale entries.
type userLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// limiterStore manages a per-user rate limiter map for a given tier.
type limiterStore struct {
	mu       sync.RWMutex
	limiters map[string]*userLimiter
	cfg      limitTier
}

func newLimiterStore(cfg limitTier) *limiterStore {
	s := &limiterStore{
		limiters: make(map[string]*userLimiter),
		cfg:      cfg,
	}
	go s.cleanupLoop()
	return s
}

// get returns (or lazily creates) the rate.Limiter for a given user ID.
func (s *limiterStore) get(userID string) *rate.Limiter {
	// Fast path: read lock.
	s.mu.RLock()
	ul, ok := s.limiters[userID]
	s.mu.RUnlock()
	if ok {
		s.mu.Lock()
		ul.lastSeen = time.Now()
		s.mu.Unlock()
		return ul.limiter
	}

	// Slow path: create limiter.
	s.mu.Lock()
	defer s.mu.Unlock()
	// Double-check after acquiring write lock.
	if ul, ok = s.limiters[userID]; ok {
		ul.lastSeen = time.Now()
		return ul.limiter
	}
	l := rate.NewLimiter(s.cfg.r, s.cfg.burst)
	s.limiters[userID] = &userLimiter{limiter: l, lastSeen: time.Now()}
	return l
}

// cleanupLoop removes entries that have been inactive for more than 5 minutes.
func (s *limiterStore) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		threshold := time.Now().Add(-5 * time.Minute)
		s.mu.Lock()
		for id, ul := range s.limiters {
			if ul.lastSeen.Before(threshold) {
				delete(s.limiters, id)
			}
		}
		s.mu.Unlock()
	}
}

// retryAfterSeconds returns how many whole seconds until the next token is available.
func retryAfterSeconds(l *rate.Limiter) int {
	reservation := l.Reserve()
	delay := reservation.Delay()
	reservation.Cancel() // We only peeked, don't consume.
	if delay <= 0 {
		return 1
	}
	secs := int(delay.Seconds()) + 1
	return secs
}

// Global stores — one per tier, created once at init.
var (
	syncMutationStore = newLimiterStore(syncMutationLimit)
	writeStore        = newLimiterStore(writeLimit)
	readStore         = newLimiterStore(readLimit)
)

// userIDFromContext extracts the authenticated user_id string from the Gin context.
// Falls back to the client IP when unauthenticated (e.g. public health endpoints).
func userIDFromContext(c *gin.Context) string {
	if v, ok := c.Get("user_id"); ok {
		if id, ok := v.(uuid.UUID); ok {
			return id.String()
		}
	}
	return c.ClientIP()
}

// rateLimitMiddleware is the generic factory that applies a given store's limit.
func rateLimitMiddleware(store *limiterStore, tierName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := userIDFromContext(c)
		l := store.get(key)
		if !l.Allow() {
			retry := retryAfterSeconds(l)
			log.Warn().
				Str("tier", tierName).
				Str("key", key).
				Str("path", c.Request.URL.Path).
				Int("retry_after_seconds", retry).
				Msg("Rate limit exceeded")
			c.Header("Retry-After", time.Now().Add(time.Duration(retry)*time.Second).UTC().Format(http.TimeFormat))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":                "rate_limit_exceeded",
				"retry_after_seconds":  retry,
			})
			return
		}
		c.Next()
	}
}

// SyncMutationRateLimit enforces 60 req/min for POST /api/sync/mutation.
func SyncMutationRateLimit() gin.HandlerFunc {
	return rateLimitMiddleware(syncMutationStore, "sync_mutation")
}

// WriteRateLimit enforces 120 req/min for general write endpoints.
func WriteRateLimit() gin.HandlerFunc {
	return rateLimitMiddleware(writeStore, "write")
}

// ReadRateLimit enforces 300 req/min for read endpoints.
func ReadRateLimit() gin.HandlerFunc {
	return rateLimitMiddleware(readStore, "read")
}
