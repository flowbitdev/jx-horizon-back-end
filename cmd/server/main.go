package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"jx_api/internal/api"
	"jx_api/internal/auth"
	"jx_api/internal/db"
	appMiddleware "jx_api/internal/middleware"
	"jx_api/internal/storage"
)

func main() {
	// ─── 1. Logger (Stdout/Stderr for 12-factor cloud apps) ──────────────────
	zerolog.TimeFieldFormat = time.RFC3339
	consoleWriter := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}
	log.Logger = zerolog.New(consoleWriter).With().Timestamp().Logger().Level(zerolog.DebugLevel)
	log.Info().Msg("Logger initialized")

	// ─── 2. Environment Variables ──────────────────────────────────────────────
	// Load .env file if present locally; in production environment variables are injected by cloud provider
	if err := godotenv.Load(".env"); err != nil {
		if err := godotenv.Load("../.env"); err != nil {
			log.Warn().Msg("No .env file found in . or ..")
		} else {
			log.Info().Msg("Loaded .env from ../.env")
		}
	} else {
		log.Info().Msg("Loaded .env from ./.env")
	}

	if os.Getenv("GOOGLE_CLIENT_ID") == "" {
		log.Warn().Msg("GOOGLE_CLIENT_ID is empty after loading .env. Auth will not work.")
	} else {
		log.Info().Msg(".env loaded successfully")
	}

	// ─── 3. Auth Init ─────────────────────────────────────────────────────────
	auth.InitAuth()

	// ─── 4. Database + Migrations ─────────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.InitDB(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}
	defer db.CloseDB()

	// Run migrations automatically on startup. Fatal if they fail.
	if err := runMigrations(); err != nil {
		log.Fatal().Err(err).Msg("Database migration failed — aborting startup")
	}

	// ─── 5. Handler Init ──────────────────────────────────────────────────────
	store := storage.NewDatabaseStorage(pool)
	tradeHandler := api.NewTradeHandler(store)
	authHandler := auth.NewAuthHandler(store)
	profileHandler := api.NewProfileHandler(store)
	strategyHandler := api.NewStrategyHandler(store)
	goalHandler := api.NewGoalHandler(store)
	customizationHandler := api.NewCustomizationHandler(store)
	journalHandler := api.NewJournalHandler(store)
	uploadHandler := api.NewUploadHandler()
	aiService := api.NewAIService()
	aiHandler := api.NewAIHandler(store, aiService)

	// ─── 6. Gin Setup ─────────────────────────────────────────────────────────
	if os.Getenv("NODE_ENV") == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	sessionStore, err := db.InitSessionStore()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize session store")
	}

	frontendURL := os.Getenv("FRONTEND_URL")
	allowedOrigins := []string{"http://localhost:5173", "http://localhost:5175", "http://127.0.0.1:5173"}
	if frontendURL != "" {
		allowedOrigins = append(allowedOrigins, frontendURL)
	}
	if extraOrigins := os.Getenv("ALLOWED_ORIGINS"); extraOrigins != "" {
		for _, o := range strings.Split(extraOrigins, ",") {
			trimmed := strings.TrimSpace(o)
			if trimmed != "" {
				allowedOrigins = append(allowedOrigins, trimmed)
			}
		}
	}

	corsConfig := cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Admin-Secret", "X-Gateway-Secret", "X-Request-ID"},
		ExposeHeaders:    []string{"Content-Length", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}

	r := gin.New()
	_ = r.SetTrustedProxies([]string{"10.0.0.0/8", "127.0.0.1"})
	r.Use(gin.Recovery())
	r.Use(appMiddleware.RequestID())
	r.Use(appMiddleware.StructuredLogger())
	r.Use(cors.New(corsConfig))
	r.Use(gzip.Gzip(gzip.DefaultCompression))
	r.Use(sessions.Sessions("jx_session", sessionStore))
	r.Use(gatewayGuard())


	// Serve uploaded files (auth-gated).
	if err := os.MkdirAll("uploads", 0755); err != nil {
		log.Fatal().Err(err).Msg("Failed to create uploads directory")
	}
	r2Client := storage.NewR2ClientFromEnv()

	uploadsGroup := r.Group("/uploads")
	uploadsGroup.Use(auth.AuthRequired())
	uploadsGroup.Use(appMiddleware.ReadRateLimit())
	uploadsGroup.GET("/*filepath", func(c *gin.Context) {
		filename := strings.TrimPrefix(c.Param("filepath"), "/")
		if filename == "" {
			c.Status(http.StatusNotFound)
			return
		}

		if r2Client.IsConfigured() {
			data, contentType, err := r2Client.Get(c.Request.Context(), filename)
			if err == nil && len(data) > 0 {
				if contentType != "" {
					c.Header("Content-Type", contentType)
				}
				c.Data(http.StatusOK, contentType, data)
				return
			}
		}

		// Local fallback
		localPath := filepath.Join("uploads", filename)
		if _, err := os.Stat(localPath); err == nil {
			c.File(localPath)
			return
		}

		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
	})

	// ─── 7. Routes ────────────────────────────────────────────────────────────
	apiGroup := r.Group("/api")

	// Tech-stack concealment (anti-fingerprinting).
	apiGroup.Use(func(c *gin.Context) {
		c.Header("Server", "")
		c.Header("X-Powered-By", "")
		c.Header("X-Framework", "Hydrogen/2.1")
		c.Next()
	})

	{
		// Public endpoints (no auth, no tenant guard).
		apiGroup.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok", "engine": "go"})
		})
		apiGroup.HEAD("/health", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})
		apiGroup.GET("/version", func(c *gin.Context) {
			version := os.Getenv("APP_VERSION")
			if version == "" {
				version = "1.0.0"
			}
			c.JSON(http.StatusOK, gin.H{"version": version})
		})

		// Auth routes (Google OAuth flow — unauthenticated).
		apiGroup.GET("/auth/google", authHandler.GoogleLogin)
		apiGroup.GET("/auth/google/callback", authHandler.GoogleCallback)
		apiGroup.GET("/auth/me", auth.AuthRequired(), profileHandler.GetProfile)
		apiGroup.POST("/auth/logout", authHandler.Logout)
		// ToS acceptance — requires auth but not full tenant guard (user may not have accepted yet).
		apiGroup.POST("/auth/accept-tos", auth.AuthRequired(), authHandler.AcceptTos)

		// ── Admin routes (ADMIN_SECRET header, not session-based) ─────────────
		adminGroup := apiGroup.Group("/admin")
		adminGroup.Use(adminSecretRequired())
		{
			adminGroup.POST("/backup", triggerBackupHandler())
		}

		// ── Protected routes (require valid session + tenant guard) ────────────
		protected := apiGroup.Group("/")
		protected.Use(auth.AuthRequired())
		protected.Use(appMiddleware.TenantGuard())
		{
			// Security enclave.
			protected.GET("/auth/enclave-seed", authHandler.GetEnclaveSeed)

			// Profile & Users (reads → read rate limit; writes → write rate limit).
			protected.GET("/user", appMiddleware.ReadRateLimit(), profileHandler.GetProfile)
			protected.GET("/auth/lease", appMiddleware.ReadRateLimit(), profileHandler.GetLease)
			protected.PATCH("/user", appMiddleware.WriteRateLimit(), profileHandler.UpdateProfile)
			protected.PUT("/users/profile", appMiddleware.WriteRateLimit(), profileHandler.UpdateProfile)
			protected.GET("/users/:id", appMiddleware.ReadRateLimit(), profileHandler.GetUser)
			protected.GET("/user/favorites", appMiddleware.ReadRateLimit(), profileHandler.GetFavorites)
			protected.POST("/user/favorites", appMiddleware.WriteRateLimit(), profileHandler.UpdateFavorites)

			// Honeypot decoy routes.
			protected.GET("/export/all-trades", func(c *gin.Context) {
				log.Warn().Str("ip", c.ClientIP()).Str("user_id", c.GetString("user_id")).Msg("Honeypot Triggered: /export/all-trades")
				c.JSON(200, gin.H{"status": "ok", "data": []gin.H{{"id": "fake_1", "symbol": "BTC/USD", "profit": -99999.00}}})
			})
			protected.GET("/admin/users", func(c *gin.Context) {
				log.Warn().Str("ip", c.ClientIP()).Msg("Honeypot Triggered: /admin/users")
				c.JSON(403, gin.H{"error": "admin_audit_triggered", "ip_logged": c.ClientIP()})
			})

			// Strategies.
			protected.GET("/strategies", appMiddleware.ReadRateLimit(), strategyHandler.GetStrategies)
			protected.POST("/strategies", appMiddleware.WriteRateLimit(), strategyHandler.CreateStrategy)
			protected.PATCH("/strategies/:id", appMiddleware.WriteRateLimit(), strategyHandler.UpdateStrategy)
			protected.DELETE("/strategies/:id", appMiddleware.WriteRateLimit(), strategyHandler.DeleteStrategy)

			// Goals.
			protected.GET("/goals", appMiddleware.ReadRateLimit(), goalHandler.GetGoals)
			protected.POST("/goals", appMiddleware.WriteRateLimit(), goalHandler.CreateGoal)
			protected.PATCH("/goals/:id", appMiddleware.WriteRateLimit(), goalHandler.UpdateGoal)
			protected.DELETE("/goals/:id", appMiddleware.WriteRateLimit(), goalHandler.DeleteGoal)

			// Goal Milestones.
			protected.GET("/goals/:id/milestones", appMiddleware.ReadRateLimit(), goalHandler.GetGoalMilestones)
			protected.POST("/goals/:id/milestones", appMiddleware.WriteRateLimit(), goalHandler.CreateGoalMilestone)
			protected.PATCH("/goals/:id/milestones/:milestoneId", appMiddleware.WriteRateLimit(), goalHandler.UpdateGoalMilestone)
			protected.DELETE("/goals/:id/milestones/:milestoneId", appMiddleware.WriteRateLimit(), goalHandler.DeleteGoalMilestone)

			// Custom Setups & Sessions.
			protected.GET("/custom-setups", appMiddleware.ReadRateLimit(), customizationHandler.GetCustomSetups)
			protected.POST("/custom-setups", appMiddleware.WriteRateLimit(), customizationHandler.CreateCustomSetup)
			protected.GET("/custom-sessions", appMiddleware.ReadRateLimit(), customizationHandler.GetCustomSessions)
			protected.POST("/custom-sessions", appMiddleware.WriteRateLimit(), customizationHandler.CreateCustomSession)

			// Journal.
			protected.GET("/journal", appMiddleware.ReadRateLimit(), journalHandler.GetJournalEntries)
			protected.POST("/journal", appMiddleware.WriteRateLimit(), journalHandler.CreateJournalEntry)
			protected.PATCH("/journal/:id", appMiddleware.WriteRateLimit(), journalHandler.UpdateJournalEntry)
			protected.PUT("/journal/:id", appMiddleware.WriteRateLimit(), journalHandler.UpdateJournalEntry)
			protected.DELETE("/journal/:id", appMiddleware.WriteRateLimit(), journalHandler.DeleteJournalEntry)

			// Trades.
			protected.GET("/trades", appMiddleware.ReadRateLimit(), tradeHandler.GetTrades)
			protected.POST("/trades", appMiddleware.WriteRateLimit(), tradeHandler.CreateTrade)
			protected.PATCH("/trades/:id", appMiddleware.WriteRateLimit(), tradeHandler.UpdateTrade)
			protected.PUT("/trades/:id", appMiddleware.WriteRateLimit(), tradeHandler.UpdateTrade)
			protected.DELETE("/trades/:id", appMiddleware.WriteRateLimit(), tradeHandler.DeleteTrade)
			protected.GET("/stats/basic", appMiddleware.ReadRateLimit(), tradeHandler.GetStats)
			protected.GET("/stats/periods", appMiddleware.ReadRateLimit(), tradeHandler.GetStatsPeriods)

			// AI Coach.
			protected.GET("/ai/status", appMiddleware.ReadRateLimit(), aiHandler.GetAIStatus)
			protected.GET("/ai/chats", appMiddleware.ReadRateLimit(), aiHandler.GetChatSessions)
			protected.POST("/ai/chats", appMiddleware.WriteRateLimit(), aiHandler.CreateChatSession)
			protected.DELETE("/ai/chats/:sessionId", appMiddleware.WriteRateLimit(), aiHandler.DeleteChatSession)
			protected.GET("/ai/chats/:sessionId", appMiddleware.ReadRateLimit(), aiHandler.GetChatMessages)
			protected.POST("/ai/chat", appMiddleware.WriteRateLimit(), aiHandler.SendMessage)
			protected.POST("/ai/analyze-trade/:tradeId", appMiddleware.WriteRateLimit(), aiHandler.AnalyzeTrade)
			protected.POST("/ai/recommendations", appMiddleware.WriteRateLimit(), aiHandler.GetRecommendations)
			protected.GET("/user/strategy-profile", appMiddleware.ReadRateLimit(), aiHandler.GetStrategyProfile)
			protected.POST("/user/strategy-profile", appMiddleware.WriteRateLimit(), aiHandler.UpdateStrategyProfile)

			// Sync (offline-first). Mutation endpoint has the tighter 60/min limit.
			syncHandler := api.NewSyncHandler(store)
			protected.GET("/sync/delta", appMiddleware.ReadRateLimit(), syncHandler.GetDelta)
			protected.GET("/sync/snapshot", appMiddleware.ReadRateLimit(), syncHandler.GetSnapshot)
			protected.GET("/sync/section/:name", appMiddleware.ReadRateLimit(), syncHandler.GetSection)
			protected.GET("/sync/section/messages", appMiddleware.ReadRateLimit(), syncHandler.GetSectionMessages)
			// Mutation endpoints — tightest limit (60/min).
			protected.POST("/sync/push", appMiddleware.SyncMutationRateLimit(), syncHandler.PushMutation)
			protected.POST("/sync/mutation", appMiddleware.SyncMutationRateLimit(), syncHandler.PushMutation)

			// Uploads.
			protected.POST("/upload", appMiddleware.WriteRateLimit(), uploadHandler.UploadImage)
		}
	}

	// ─── 8. Graceful Shutdown ─────────────────────────────────────────────────
	port := os.Getenv("PORT")
	if port == "" {
		port = "5000"
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		log.Info().Str("port", port).Msg("Server started")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Server failed")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("Shutting down server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal().Err(err).Msg("Server forced to shutdown")
	}
	log.Info().Msg("Server exiting")
}

// runMigrations runs all pending up-migrations from the migrations/ directory.
// Returns nil if migrations are already up-to-date.
func runMigrations() error {
	migrationsPath := os.Getenv("MIGRATIONS_PATH")
	if migrationsPath == "" {
		migrationsPath = "migrations"
	}

	if _, err := os.Stat(migrationsPath); os.IsNotExist(err) {
		log.Info().Str("path", migrationsPath).Msg("Migrations directory not found — skipping startup migration check")
		return nil
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if os.Getenv("DB_MODE") == "local" {
		databaseURL = os.Getenv("LOCAL_DATABASE_URL")
	} else if databaseURL == "" {
		databaseURL = os.Getenv("LOCAL_DATABASE_URL")
	}

	pgx5URL := databaseURL
	if len(pgx5URL) > 10 && pgx5URL[:11] == "postgresql:" {
		pgx5URL = "pgx5:" + pgx5URL[11:]
	} else if len(pgx5URL) > 9 && pgx5URL[:10] == "postgres:/" {
		pgx5URL = "pgx5:/" + pgx5URL[10:]
	}

	sourceURL := "file://" + migrationsPath
	log.Info().Str("source", sourceURL).Msg("Running database migrations")

	m, err := migrate.New(sourceURL, pgx5URL)
	if err != nil {
		log.Warn().Err(err).Msg("Could not initialize migration driver — skipping automatic migration")
		return nil
	}
	defer m.Close()

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			log.Info().Msg("Database schema is up-to-date — no migrations needed")
			return nil
		}
		log.Warn().Err(err).Msg("Migration error encountered — skipping non-blocking migration error")
		return nil
	}

	version, dirty, _ := m.Version()
	log.Info().Uint("version", version).Bool("dirty", dirty).Msg("Migrations applied successfully")
	return nil
}

// adminSecretRequired is a middleware that checks the ADMIN_SECRET header.
// It is used for admin-only endpoints that are not gated by the user session.
func adminSecretRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		secret := os.Getenv("ADMIN_SECRET")
		if secret == "" {
			// If ADMIN_SECRET is not configured, block all admin access.
			log.Error().Msg("ADMIN_SECRET env var is not set — blocking admin access")
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Admin access not configured"})
			return
		}
		provided := c.GetHeader("X-Admin-Secret")
		if provided != secret {
			log.Warn().Str("ip", c.ClientIP()).Msg("Admin endpoint: invalid or missing X-Admin-Secret")
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Forbidden"})
			return
		}
		c.Next()
	}
}

// triggerBackupHandler returns a Gin handler that runs scripts/backup-pg.sh as a subprocess.
func triggerBackupHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		log.Info().Str("ip", c.ClientIP()).Msg("On-demand backup triggered via admin API")

		script := os.Getenv("BACKUP_SCRIPT_PATH")
		if script == "" {
			script = "scripts/backup-pg.sh"
		}

		cmd := exec.CommandContext(c.Request.Context(), "bash", script)
		cmd.Env = append(os.Environ()) // inherit all env vars

		out, err := cmd.CombinedOutput()
		if err != nil {
			log.Error().Err(err).Str("output", string(out)).Msg("Backup script failed")
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "backup_failed",
				"error":  err.Error(),
				"output": string(out),
			})
			return
		}

		log.Info().Str("output", string(out)).Msg("Backup script completed successfully")
		c.JSON(http.StatusOK, gin.H{
			"status":    "backup_initiated",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// gatewayGuard verifies X-Gateway-Secret header from reverse proxy / edge gateway if GATEWAY_SECRET is configured
func gatewayGuard() gin.HandlerFunc {
	secret := os.Getenv("GATEWAY_SECRET")
	return func(c *gin.Context) {
		if secret == "" {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		if path == "/api/health" || path == "/version" {
			c.Next()
			return
		}
		provided := c.GetHeader("X-Gateway-Secret")
		if provided != secret {
			log.Warn().Str("ip", c.ClientIP()).Str("path", path).Msg("Direct backend access attempt blocked by Gateway Guard")
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "Not Found"})
			return
		}
		c.Next()
	}
}
