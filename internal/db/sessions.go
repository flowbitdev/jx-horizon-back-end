package db

import (
	"net/http"
	"os"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/rs/zerolog/log"
)

func InitSessionStore() (sessions.Store, error) {
	// Session Secret
	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		log.Fatal().Msg("SESSION_SECRET environment variable is required")
	}

	store := cookie.NewStore([]byte(secret))
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 30,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	log.Info().Msg("Cookie session store initialized (Secure=true, SameSite=Lax)")
	return store, nil
}
