package storage

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"jx_api/internal/models"
)

type IStorage interface {
	// Users
	GetUser(ctx context.Context, id uuid.UUID) (*models.User, error)
	GetUserByUsername(ctx context.Context, username string) (*models.User, error)
	GetUserByGoogleID(ctx context.Context, googleID string) (*models.User, error)
	CreateUser(ctx context.Context, user *models.User) error
	UpdateUser(ctx context.Context, id uuid.UUID, user map[string]interface{}) error
	UpsertUser(ctx context.Context, user *models.User) (*models.User, error)
	AcceptTos(ctx context.Context, userID uuid.UUID) error

	// Strategies
	GetStrategy(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*models.Strategy, error)
	GetStrategies(ctx context.Context, userID uuid.UUID) ([]models.Strategy, error)
	CreateStrategy(ctx context.Context, strategy *models.Strategy) error
	UpdateStrategy(ctx context.Context, id uuid.UUID, userID uuid.UUID, updates map[string]interface{}) error
	DeleteStrategy(ctx context.Context, id uuid.UUID, userID uuid.UUID) error

	// Goals
	GetGoal(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*models.Goal, error)
	GetGoalsRaw(ctx context.Context, userID uuid.UUID, includeArchived bool) ([]models.Goal, error)
	CreateGoal(ctx context.Context, goal *models.Goal) error
	UpdateGoal(ctx context.Context, id uuid.UUID, userID uuid.UUID, updates map[string]interface{}) error
	DeleteGoal(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
	SyncUserGoals(ctx context.Context, userID uuid.UUID) error

	// Goal Milestones
	GetGoalMilestones(ctx context.Context, goalID uuid.UUID) ([]models.GoalMilestone, error)
	CreateGoalMilestone(ctx context.Context, milestone *models.GoalMilestone, userID uuid.UUID) error
	UpdateGoalMilestone(ctx context.Context, id uuid.UUID, goalID uuid.UUID, userID uuid.UUID, updates map[string]interface{}) error
	DeleteGoalMilestone(ctx context.Context, id uuid.UUID, goalID uuid.UUID, userID uuid.UUID) error

	// Chat
	GetChatSessions(ctx context.Context, userID uuid.UUID) ([]models.ChatSession, error)
	CreateChatSession(ctx context.Context, session *models.ChatSession) error
	GetChatMessages(ctx context.Context, sessionID uuid.UUID, userID uuid.UUID) ([]models.AIChatMessage, error)
	CreateChatMessage(ctx context.Context, msg *models.AIChatMessage) error
	UpdatePendingChatMessage(ctx context.Context, sessionID uuid.UUID, userID uuid.UUID, content string, metadata map[string]interface{}, modelUsed *string, latencyMs *int) error
	GetTradingProfile(ctx context.Context, userID uuid.UUID) (*models.TradingProfile, error)
	UpsertTradingProfile(ctx context.Context, profile *models.TradingProfile) error
	ListUserInsights(ctx context.Context, userID uuid.UUID) ([]models.UserInsight, error)
	CreateUserInsight(ctx context.Context, insight *models.UserInsight) error
	ListMessageFeedback(ctx context.Context, userID uuid.UUID, messageID uuid.UUID) ([]models.MessageFeedback, error)
	UpsertMessageFeedback(ctx context.Context, feedback *models.MessageFeedback) error
	ListAIActions(ctx context.Context, userID uuid.UUID, sessionID uuid.UUID) ([]models.AIAction, error)
	CreateAIAction(ctx context.Context, action *models.AIAction) error
	UpdateAIActionStatus(ctx context.Context, id uuid.UUID, userID uuid.UUID, status string, executedAt *time.Time) error
	ListMarketDataCache(ctx context.Context, symbols []string, marketType string) ([]models.MarketDataCache, error)
	UpsertMarketDataCache(ctx context.Context, entry *models.MarketDataCache) error

	// Trades
	GetTrade(ctx context.Context, tradeID uuid.UUID, userID uuid.UUID) (*models.Trade, error)
	GetTrades(ctx context.Context, userID uuid.UUID) ([]models.Trade, error)
	CreateTrade(ctx context.Context, trade *models.Trade) error
	UpdateTrade(ctx context.Context, tradeID uuid.UUID, userID uuid.UUID, trade map[string]interface{}) error
	DeleteTrade(ctx context.Context, tradeID uuid.UUID, userID uuid.UUID) error

	// Journal
	GetJournalEntry(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*models.DailyJournal, error)
	GetJournalEntries(ctx context.Context, userID uuid.UUID) ([]models.DailyJournal, error)
	CreateJournalEntry(ctx context.Context, entry *models.DailyJournal) error
	UpdateJournalEntry(ctx context.Context, id uuid.UUID, userID uuid.UUID, updates map[string]interface{}) error
	DeleteJournalEntry(ctx context.Context, id uuid.UUID, userID uuid.UUID) error

	// Customizations
	GetCustomSetups(ctx context.Context, userID uuid.UUID) ([]models.CustomSetup, error)
	CreateCustomSetup(ctx context.Context, userID uuid.UUID, name string) (*models.CustomSetup, error)
	GetCustomSessions(ctx context.Context, userID uuid.UUID) ([]models.CustomSession, error)
	CreateCustomSession(ctx context.Context, userID uuid.UUID, name string) (*models.CustomSession, error)

	// AI Chat Extensions
	UpdateChatSessionTitle(ctx context.Context, sessionID uuid.UUID, userID uuid.UUID, title string) error
	DeleteChatSession(ctx context.Context, sessionID uuid.UUID, userID uuid.UUID) error

	// Sync (Layer 3)
	AddToSyncQueue(ctx context.Context, event *models.SyncEvent) error
	GetSyncEventsSince(ctx context.Context, userID uuid.UUID, vectorClock int64) ([]models.SyncEvent, error)
	GetLatestVectorClock(ctx context.Context, userID uuid.UUID) (int64, error)
	RecordEvent(ctx context.Context, userID uuid.UUID, entityType string, entityID uuid.UUID, action string, payload []byte) (int64, error)
	GetSectionSnapshot(ctx context.Context, userID uuid.UUID) (map[string]SectionSnapshotEntry, error)
}

type DatabaseStorage struct {
	pool *pgxpool.Pool
}

func NewDatabaseStorage(pool *pgxpool.Pool) *DatabaseStorage {
	return &DatabaseStorage{pool: pool}
}

// User and Trade methods are implemented in separate files in this package.
