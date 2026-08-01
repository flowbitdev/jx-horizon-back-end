package storage_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"jx_api/internal/models"
	"jx_api/internal/storage"
)

// connectTestDB opens a connection to the test PostgreSQL database.
// It requires TEST_DATABASE_URL to be set in the environment.
// If it's not set, the test is skipped rather than failed.
func connectTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	connStr := os.Getenv("TEST_DATABASE_URL")
	if connStr == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// createTestUser inserts a minimal user and returns it. Cleans up on test end.
func createTestUser(t *testing.T, store *storage.DatabaseStorage, suffix string) *models.User {
	t.Helper()
	email := "test+" + suffix + "@jxhorizon.test"
	googleID := "g_" + suffix
	name := "Test " + suffix
	u := &models.User{
		ID:        uuid.New(),
		Username:  "user_" + suffix,
		Email:     &email,
		GoogleID:  &googleID,
		Name:      &name,
		Rank:      "Novice",
		Currency:  "USD",
		Favorites: []string{},
		AIMemory: map[string]interface{}{
			"weaknesses": []string{},
			"strengths":  []string{},
			"rules":      []string{},
		},
		Metadata:    map[string]interface{}{},
		CreatedAt:   time.Now(),
		AccountSize: 10000,
	}
	if err := store.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("createTestUser: %v", err)
	}
	t.Cleanup(func() {
		pool := connectTestDB(t)
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", u.ID)
	})
	return u
}

// TestUserIsolation_TradesBelongToOwner verifies that user B cannot see user A's trades.
// This is the BLOCKER 5 acceptance criteria test.
func TestUserIsolation_TradesBelongToOwner(t *testing.T) {
	pool := connectTestDB(t)
	store := storage.NewDatabaseStorage(pool)
	ctx := context.Background()

	// Create two separate users.
	userA := createTestUser(t, store, "isolation_a")
	userB := createTestUser(t, store, "isolation_b")

	// Insert a trade for user A.
	tradeA := &models.Trade{
		ID:        uuid.New(),
		UserID:    userA.ID,
		Symbol:    "EUR/USD",
		Direction: "long",
		Entry:     1.1000,
		StopLoss:  1.0950,
		TakeProfit: 1.1100,
		AssetClass: "forex",
		SetupType:  "test",
		Session:    "london",
		Tags:       []string{},
		Date:       time.Now(),
		CreatedAt:  time.Now(),
	}
	if err := store.CreateTrade(ctx, tradeA); err != nil {
		t.Fatalf("CreateTrade for user A failed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM trades WHERE id = $1", tradeA.ID)
	})

	// Query trades AS user B — must return zero results.
	tradesForB, err := store.GetTrades(ctx, userB.ID)
	if err != nil {
		t.Fatalf("GetTrades for user B failed: %v", err)
	}

	for _, tr := range tradesForB {
		if tr.ID == tradeA.ID {
			t.Errorf("ISOLATION VIOLATION: user B can see trade %s belonging to user A", tradeA.ID)
		}
	}

	if len(tradesForB) > 0 {
		// Verify none of them belong to user A.
		for _, tr := range tradesForB {
			if tr.UserID == userA.ID {
				t.Errorf("ISOLATION VIOLATION: user B received a record owned by user A (trade %s)", tr.ID)
			}
		}
	}

	// Also verify user A CAN still see their own trade.
	tradesForA, err := store.GetTrades(ctx, userA.ID)
	if err != nil {
		t.Fatalf("GetTrades for user A failed: %v", err)
	}
	found := false
	for _, tr := range tradesForA {
		if tr.ID == tradeA.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("user A cannot see their own trade %s", tradeA.ID)
	}

	t.Logf("Isolation test passed: user B sees %d trades (none from user A)", len(tradesForB))
}

// TestEmptyUserID_GetTrades asserts that GetTrades with uuid.Nil returns no rows
// rather than every row in the table.
func TestEmptyUserID_GetTrades(t *testing.T) {
	pool := connectTestDB(t)
	store := storage.NewDatabaseStorage(pool)
	ctx := context.Background()

	trades, err := store.GetTrades(ctx, uuid.Nil)
	if err != nil {
		// An error (e.g. FK violation) is also acceptable — the important thing
		// is that we don't get rows from other users.
		t.Logf("GetTrades(uuid.Nil) returned error (acceptable): %v", err)
		return
	}
	// If it returns rows, they must all have UserID == uuid.Nil (impossible in real data).
	for _, tr := range trades {
		if tr.UserID != uuid.Nil {
			t.Errorf("GetTrades(uuid.Nil) returned trade %s owned by %s — isolation failure", tr.ID, tr.UserID)
		}
	}
}
