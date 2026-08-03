package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"jx_api/internal/models"
	"jx_api/internal/storage"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var (
	googleOauthConfig *oauth2.Config
)

func InitAuth() {
	frontendURL := os.Getenv("FRONTEND_URL")

	googleOauthConfig = &oauth2.Config{
		RedirectURL:  strings.TrimSuffix(frontendURL, "/") + "/api/auth/google/callback",
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		Scopes:       []string{"https://www.googleapis.com/auth/userinfo.profile", "https://www.googleapis.com/auth/userinfo.email"},
		Endpoint:     google.Endpoint,
	}
	log.Info().Str("clientID", googleOauthConfig.ClientID).Str("redirectURL", googleOauthConfig.RedirectURL).Msg("Auth initialized")
}

type AuthHandler struct {
	store storage.IStorage
}

func NewAuthHandler(store storage.IStorage) *AuthHandler {
	return &AuthHandler{store: store}
}

func getOAuthConfig(c *gin.Context) *oauth2.Config {
	cfg := *googleOauthConfig

	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL != "" {
		cfg.RedirectURL = strings.TrimSuffix(frontendURL, "/") + "/api/auth/google/callback"
		log.Info().Str("redirectURL", cfg.RedirectURL).Msg("Using FRONTEND_URL for OAuth redirect URL")
		return &cfg
	}

	backendURL := os.Getenv("BACKEND_URL")
	if backendURL != "" {
		cfg.RedirectURL = strings.TrimSuffix(backendURL, "/") + "/api/auth/google/callback"
		log.Info().Str("redirectURL", cfg.RedirectURL).Msg("Using BACKEND_URL for OAuth redirect URL")
		return &cfg
	}

	log.Warn().Str("redirectURL", cfg.RedirectURL).Msg("Using default base OAuth redirect URL")
	return &cfg
}

func (h *AuthHandler) GoogleLogin(c *gin.Context) {
	cfg := getOAuthConfig(c)
	url := cfg.AuthCodeURL("state")
	log.Info().Str("event", "oauth_redirect").Str("redirectURL", cfg.RedirectURL).Str("ip", c.ClientIP()).Msg("Google OAuth login initiated")
	c.Redirect(http.StatusTemporaryRedirect, url)
}

func (h *AuthHandler) GoogleCallback(c *gin.Context) {
	code := c.Query("code")
	cfg := getOAuthConfig(c)
	token, err := cfg.Exchange(c.Request.Context(), code)
	if err != nil {
		log.Error().Err(err).Str("event", "oauth_token_exchange_failed").Msg("Failed to exchange token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to exchange token"})
		return
	}

	// Fetch user info from Google.
	client := cfg.Client(c.Request.Context(), token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		log.Error().Err(err).Str("event", "oauth_userinfo_failed").Msg("Failed to fetch user info")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user info"})
		return
	}
	defer resp.Body.Close()

	var googleUser struct {
		ID      string `json:"id"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&googleUser); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to decode user info"})
		return
	}

	// Upsert user in storage.
	u := &models.User{
		GoogleID:  &googleUser.ID,
		Email:     &googleUser.Email,
		Name:      &googleUser.Name,
		AvatarURL: &googleUser.Picture,
		Username:  googleUser.Email,
	}

	log.Info().Str("googleID", *u.GoogleID).Str("email", *u.Email).Str("event", "user_upsert").Msg("Attempting upsert user")
	upsertedUser, err := h.store.UpsertUser(c.Request.Context(), u)
	if err != nil {
		log.Error().Err(err).Str("googleID", *u.GoogleID).Str("event", "user_upsert_failed").Msg("Failed to upsert user")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upsert user: " + err.Error()})
		return
	}
	log.Info().
		Str("user_id", upsertedUser.ID.String()).
		Str("event", "login_success").
		Bool("tos_accepted", upsertedUser.TosAcceptedAt != nil).
		Msg("User authenticated")

	// Create session.
	session := sessions.Default(c)
	session.Set("user_id", upsertedUser.ID.String())
	session.Set("tos_accepted", upsertedUser.TosAcceptedAt != nil)
	if err := session.Save(); err != nil {
		log.Error().Err(err).Str("event", "session_save_failed").Msg("Failed to save session")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save session"})
		return
	}

	// Determine post-login frontend redirect target
	redirectURL := ""
	if host := c.GetHeader("X-Forwarded-Host"); host != "" && !strings.Contains(host, "localhost") && !strings.Contains(host, "127.0.0.1") && !strings.Contains(host, "replit.app") && !strings.Contains(host, "replit.dev") {
		proto := c.GetHeader("X-Forwarded-Proto")
		if proto == "" {
			proto = "https"
		}
		redirectURL = proto + "://" + host
	} else {
		redirectURL = os.Getenv("FRONTEND_URL")
	}

	// New users who haven't accepted ToS get redirected to the acceptance page.
	if upsertedUser.TosAcceptedAt == nil {
		c.Redirect(http.StatusTemporaryRedirect, strings.TrimSuffix(redirectURL, "/")+"/accept-tos")
		return
	}

	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

func (h *AuthHandler) Logout(c *gin.Context) {
	session := sessions.Default(c)
	userIDRaw := session.Get("user_id")
	session.Clear()
	if err := session.Save(); err != nil {
		log.Error().Err(err).Str("event", "logout_session_clear_failed").Msg("Failed to clear session")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to logout"})
		return
	}
	if userIDRaw != nil {
		log.Info().Str("user_id", userIDRaw.(string)).Str("event", "logout").Msg("User logged out")
	}
	c.JSON(http.StatusOK, gin.H{"message": "Logged out successfully"})
}

// AcceptTos records ToS acceptance for the authenticated user.
func (h *AuthHandler) AcceptTos(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.store.AcceptTos(c.Request.Context(), userID); err != nil {
		log.Error().Err(err).Str("user_id", userID.String()).Str("event", "tos_accept_failed").Msg("Failed to record ToS acceptance")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record ToS acceptance"})
		return
	}

	session := sessions.Default(c)
	session.Set("tos_accepted", true)
	_ = session.Save()

	log.Info().
		Str("user_id", userID.String()).
		Str("event", "tos_accepted").
		Time("accepted_at", time.Now()).
		Msg("User accepted ToS")
	c.JSON(http.StatusOK, gin.H{"status": "tos_accepted", "accepted_at": time.Now().UTC().Format(time.RFC3339)})
}

func (h *AuthHandler) GetEnclaveSeed(c *gin.Context) {
	userId, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	secret := os.Getenv("ENCLAVE_SECRET")
	if secret == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ENCLAVE_SECRET not configured"})
		return
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(userId.(uuid.UUID).String()))
	seedBytes := mac.Sum(nil)
	seedBase64 := base64.StdEncoding.EncodeToString(seedBytes)

	c.JSON(http.StatusOK, gin.H{
		"seed":       seedBase64,
		"iterations": 210000,
	})
}

func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		// AGENT ACCESS: Authenticate via X-Agent-Key header
		agentKey := c.GetHeader("X-Agent-Key")
		expectedSecret := os.Getenv("ENCLAVE_SECRET")
		if expectedSecret == "" {
			expectedSecret = os.Getenv("SESSION_SECRET")
		}
		if agentKey != "" && expectedSecret != "" && agentKey == expectedSecret {
			agentID := uuid.MustParse("a0000000-0000-4000-a000-000000000001")
			c.Set("user_id", agentID)
			log.Info().
				Str("event", "agent_api_action").
				Str("method", c.Request.Method).
				Str("path", c.Request.URL.Path).
				Str("agent_id", agentID.String()).
				Msg("AGENT_ACTION: Authenticated agent API request executed")
			c.Next()
			return
		}

		// BENCH MODE: bypass auth for load testing.
		if os.Getenv("BENCH_MODE") == "true" {
			benchUserID := uuid.MustParse("00000000-0000-0000-0000-000000000099")
			if override := os.Getenv("BENCH_USER_ID"); override != "" {
				if parsed, err := uuid.Parse(override); err == nil {
					benchUserID = parsed
				}
			}
			c.Set("user_id", benchUserID)
			c.Next()
			return
		}

		session := sessions.Default(c)
		userID := session.Get("user_id")

		if userID == nil {
			log.Warn().
				Str("path", c.Request.URL.Path).
				Str("event", "auth_required_failed").
				Msg("Unauthenticated request to protected route")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}

		// Enforce ToS acceptance check for protected API routes (except accept-tos, logout, and me)
		path := c.Request.URL.Path
		tosAccepted := session.Get("tos_accepted")
		if tosAccepted != nil && tosAccepted == false && path != "/api/auth/accept-tos" && path != "/api/auth/logout" && path != "/api/auth/me" && path != "/api/user" {
			log.Warn().
				Str("path", path).
				Str("user_id", userID.(string)).
				Str("event", "tos_acceptance_required").
				Msg("User attempted protected access before accepting ToS")
			c.JSON(http.StatusForbidden, gin.H{"error": "tos_acceptance_required"})
			c.Abort()
			return
		}

		id, err := uuid.Parse(userID.(string))
		if err != nil {
			log.Warn().
				Str("raw_user_id", userID.(string)).
				Str("event", "session_invalid_uuid").
				Msg("Session contained invalid UUID")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: invalid session"})
			c.Abort()
			return
		}
		c.Set("user_id", id)
		c.Next()
	}
}