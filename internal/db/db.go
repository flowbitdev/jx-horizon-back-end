package db

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

var (
	pool *pgxpool.Pool
	once sync.Once
)

// InitDB initializes the connection pool
func InitDB(ctx context.Context) (*pgxpool.Pool, error) {
	var err error
	once.Do(func() {
		connString := os.Getenv("DATABASE_URL")
		if os.Getenv("DB_MODE") == "local" {
			connString = os.Getenv("LOCAL_DATABASE_URL")
		}
		if connString == "" {
			log.Fatal().Msg("DATABASE_URL (or LOCAL_DATABASE_URL in local mode) environment variable is required")
		}

		config, pErr := pgxpool.ParseConfig(connString)
		if pErr != nil {
			log.Error().Err(pErr).Str("connString", connString).Msg("Failed to parse database config")
			err = pErr
			return
		}

		// Performance Tuning
		config.MaxConns = 20
		config.MinConns = 5
		config.MaxConnIdleTime = 30 * time.Minute
		config.MaxConnLifetime = 1 * time.Hour

		pool, err = pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create database pool")
			return
		}

		log.Info().Msg("PostgreSQL connection pool established")
	})

	return pool, err
}

// GetPool returns the existing database pool
func GetPool() *pgxpool.Pool {
	return pool
}

// CloseDB closes the connection pool
func CloseDB() {
	if pool != nil {
		pool.Close()
	}
}
