package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// User represents the users table
type User struct {
	ID             uuid.UUID              `json:"id" db:"id"`
	Username       string                 `json:"username" db:"username"`
	Name           *string                `json:"name" db:"name"`
	Email          *string                `json:"email" db:"email"`
	GoogleID       *string                `json:"googleId" db:"google_id"`
	AvatarURL      *string                `json:"avatarUrl" db:"avatar_url"`
	XP             int                    `json:"xp" db:"xp"`
	Rank           string                 `json:"rank" db:"rank"`
	ThemeColor     string                 `json:"themeColor" db:"theme_color"`
	Bio            *string                `json:"bio" db:"bio"`
	AccountSize    float64                `json:"accountSize" db:"account_size"`
	Currency       string                 `json:"currency" db:"currency"`
	MaxRiskPercent float64                `json:"maxRiskPercent" db:"max_risk_percent"`
	IsAdmin        bool                   `json:"isAdmin" db:"is_admin"`
	IsBanned       bool                   `json:"isBanned" db:"is_banned"`
	Favorites      []string               `json:"favorites" db:"favorites"`
	AIMemory       map[string]interface{} `json:"aiMemory" db:"ai_memory"`
	CreatedAt      time.Time              `json:"createdAt" db:"created_at"`
	LastLogin      *time.Time             `json:"lastLogin" db:"last_login"`
	Metadata       map[string]interface{} `json:"metadata" db:"metadata"`
	TosAcceptedAt  *time.Time             `json:"tosAcceptedAt" db:"tos_accepted_at"`
}

// Trade represents the trades table
type Trade struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	UserID          uuid.UUID  `json:"userId" db:"user_id"`
	StrategyID      *uuid.UUID `json:"strategyId" db:"strategy_id"`
	Symbol          string     `json:"symbol" db:"symbol"`
	Direction       string     `json:"direction" db:"direction"`
	Entry           float64    `json:"entry" db:"entry"`
	ExitPrice       *float64   `json:"exitPrice" db:"exit_price"`
	StopLoss        float64    `json:"stopLoss" db:"stop_loss"`
	TakeProfit      float64    `json:"takeProfit" db:"take_profit"`
	LotSize         float64    `json:"lotSize" db:"lot_size"`
	RiskPercent     float64    `json:"riskPercent" db:"risk_percent"`
	RiskAmount      *float64   `json:"riskAmount" db:"risk_amount"`
	ProfitLoss      *float64   `json:"profitLoss" db:"profit_loss"`
	RR              *float64   `json:"rr" db:"rr"`
	Outcome         *string    `json:"outcome" db:"outcome"`
	Notes           *string    `json:"notes" db:"notes"`
	Date            time.Time  `json:"date" db:"date"`
	AssetClass      string     `json:"assetClass" db:"asset_class"`
	SetupType       string     `json:"setupType" db:"setup_type"`
	Session         string     `json:"session" db:"session"`
	Tags            []string   `json:"tags" db:"tags"`
	ScreenshotURL   *string    `json:"screenshotUrl" db:"screenshot_url"`
	IsBacktest      bool       `json:"isBacktest" db:"is_backtest"`
	IsPlanCompliant *bool      `json:"isPlanCompliant" db:"is_plan_compliant"`
	Rating          *int       `json:"rating" db:"rating"`
	EmotionBefore   *int       `json:"emotionBefore" db:"emotion_before"`
	EmotionDuring   *int       `json:"emotionDuring" db:"emotion_during"`
	EmotionAfter    *int       `json:"emotionAfter" db:"emotion_after"`
	AIAnalysis      *string    `json:"aiAnalysis" db:"ai_analysis"`
	IsDemo          bool       `json:"isDemo" db:"is_demo"`
	ImportSource    *string    `json:"importSource" db:"import_source"`
	ExternalID      *string    `json:"externalId" db:"external_id"`
	VectorClock     int64      `json:"vectorClock" db:"vector_clock"`
	CreatedAt       time.Time  `json:"createdAt" db:"created_at"`
}

// Strategy represents the strategies table
type Strategy struct {
	ID          uuid.UUID              `json:"id" db:"id"`
	UserID      uuid.UUID              `json:"userId" db:"user_id"`
	Name        string                 `json:"name" db:"name"`
	Description *string                `json:"description" db:"description"`
	Rules       map[string]interface{} `json:"rules" db:"rules"`
	Active      bool                   `json:"active" db:"active"`
	VectorClock int64                  `json:"vectorClock" db:"vector_clock"`
	CreatedAt   time.Time              `json:"createdAt" db:"created_at"`
}

// Goal represents the goals table
type Goal struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	UserID       uuid.UUID       `json:"userId" db:"user_id"`
	Name         string          `json:"name" db:"name"`
	TargetMetric string          `json:"targetMetric" db:"target_metric"`
	TargetValue  float64         `json:"targetValue" db:"target_value"`
	CurrentValue float64         `json:"currentValue" db:"current_value"`
	Deadline     *time.Time      `json:"deadline" db:"deadline"`
	Status       string          `json:"status" db:"status"`
	IsDemo       bool            `json:"isDemo" db:"is_demo"`
	IsBacktest   bool            `json:"isBacktest" db:"is_backtest"`
	StartDate    time.Time       `json:"startDate" db:"start_date"`
	ArchivedAt   *time.Time      `json:"archivedAt" db:"archived_at"`
	Category     string          `json:"category" db:"category"`
	CreatedAt    time.Time       `json:"createdAt" db:"created_at"`
	VectorClock  int64           `json:"vectorClock" db:"vector_clock"`
	Milestones   []GoalMilestone `json:"milestones"`
}

func (t *Trade) UnmarshalJSON(data []byte) error {
	type Alias Trade
	aux := &struct {
		StopLossAlt    *float64 `json:"stop_loss"`
		TakeProfitAlt  *float64 `json:"take_profit"`
		LotSizeAlt     *float64 `json:"lot_size"`
		RiskPercentAlt *float64 `json:"risk_percent"`
		AssetClassAlt  *string  `json:"asset_class"`
		SetupTypeAlt   *string  `json:"setup_type"`
		ProfitLossAlt  *float64 `json:"profit_loss"`
		*Alias
	}{
		Alias: (*Alias)(t),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.StopLossAlt != nil && t.StopLoss == 0 {
		t.StopLoss = *aux.StopLossAlt
	}
	if aux.TakeProfitAlt != nil && t.TakeProfit == 0 {
		t.TakeProfit = *aux.TakeProfitAlt
	}
	if aux.LotSizeAlt != nil && t.LotSize == 0 {
		t.LotSize = *aux.LotSizeAlt
	}
	if aux.RiskPercentAlt != nil && t.RiskPercent == 0 {
		t.RiskPercent = *aux.RiskPercentAlt
	}
	if aux.AssetClassAlt != nil && t.AssetClass == "" {
		t.AssetClass = *aux.AssetClassAlt
	}
	if aux.SetupTypeAlt != nil && t.SetupType == "" {
		t.SetupType = *aux.SetupTypeAlt
	}
	if aux.ProfitLossAlt != nil && t.ProfitLoss == nil {
		t.ProfitLoss = aux.ProfitLossAlt
	}
	return nil
}

func (g *Goal) UnmarshalJSON(data []byte) error {
	type Alias Goal
	aux := &struct {
		TitleAlt        *string  `json:"title"`
		TargetValueAlt  *float64 `json:"target_value"`
		CurrentValueAlt *float64 `json:"current_value"`
		TargetMetricAlt *string  `json:"target_metric"`
		*Alias
	}{
		Alias: (*Alias)(g),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.TitleAlt != nil && g.Name == "" {
		g.Name = *aux.TitleAlt
	}
	if aux.TargetValueAlt != nil && g.TargetValue == 0 {
		g.TargetValue = *aux.TargetValueAlt
	}
	if aux.CurrentValueAlt != nil && g.CurrentValue == 0 {
		g.CurrentValue = *aux.CurrentValueAlt
	}
	if aux.TargetMetricAlt != nil && g.TargetMetric == "" {
		g.TargetMetric = *aux.TargetMetricAlt
	}
	return nil
}

type GoalMilestone struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	GoalID          uuid.UUID  `json:"goalId" db:"goal_id"`
	Label           string     `json:"label" db:"label"`
	ThresholdValue  float64    `json:"thresholdValue" db:"threshold_value"`
	IsPercentage    bool       `json:"isPercentage" db:"is_percentage"`
	PercentageValue *float64   `json:"percentageValue" db:"percentage_value"`
	ReachedAt       *time.Time `json:"reachedAt" db:"reached_at"`
	CreatedAt       time.Time  `json:"createdAt" db:"created_at"`
}

func (m *GoalMilestone) UnmarshalJSON(data []byte) error {
	type Alias GoalMilestone
	aux := &struct {
		TitleAlt          *string  `json:"title"`
		ThresholdValueAlt *float64 `json:"threshold_value"`
		*Alias
	}{
		Alias: (*Alias)(m),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.TitleAlt != nil && m.Label == "" {
		m.Label = *aux.TitleAlt
	}
	if aux.ThresholdValueAlt != nil && m.ThresholdValue == 0 {
		m.ThresholdValue = *aux.ThresholdValueAlt
	}
	return nil
}

// AIPersona represents the ai_personas table
type AIPersona struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	UserID    *uuid.UUID `json:"user_id" db:"user_id"`
	Name      string     `json:"name" db:"name"`
	Prompt    string     `json:"prompt" db:"prompt"`
	Icon      string     `json:"icon" db:"icon"`
	IsSystem  bool       `json:"is_system" db:"is_system"`
	IsActive  bool       `json:"is_active" db:"is_active"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}

// ChatSession represents the chat_sessions table
type ChatSession struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	UserID       uuid.UUID  `json:"user_id" db:"user_id"`
	Title        string     `json:"title" db:"title"`
	Persona      string     `json:"persona" db:"persona"`
	ThinkingMode bool       `json:"thinkingMode" db:"thinking_mode"`
	GhostMode    bool       `json:"ghostMode" db:"ghost_mode"`
	TokenUsage   int        `json:"tokenUsage" db:"token_usage"`
	ModelUsed    *string    `json:"modelUsed" db:"model_used"`
	DeletedAt    *time.Time `json:"deletedAt" db:"deleted_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

// AIChatMessage represents the ai_chat_messages table
type AIChatMessage struct {
	ID          uuid.UUID              `json:"id" db:"id"`
	UserID      uuid.UUID              `json:"userId" db:"user_id"`
	SessionID   uuid.UUID              `json:"sessionId" db:"session_id"`
	Role        string                 `json:"role" db:"role"`
	Content     string                 `json:"content" db:"content"`
	Metadata    map[string]interface{} `json:"metadata" db:"metadata"`
	ModelUsed   *string                `json:"modelUsed" db:"model_used"`
	LatencyMs   *int                   `json:"latencyMs" db:"latency_ms"`
	IsPending   bool                   `json:"isPending" db:"is_pending"`
	VectorClock int64                  `json:"vectorClock" db:"vector_clock"`
	Timestamp   time.Time              `json:"timestamp" db:"timestamp"`
}

type TradingProfile struct {
	ID                 uuid.UUID                `json:"id" db:"id"`
	UserID             uuid.UUID                `json:"userId" db:"user_id"`
	ProfileData        map[string]interface{}   `json:"profileData" db:"profile_data"`
	AISuggestedUpdates []map[string]interface{} `json:"aiSuggestedUpdates" db:"ai_suggested_updates"`
	CreatedAt          time.Time                `json:"createdAt" db:"created_at"`
	UpdatedAt          time.Time                `json:"updatedAt" db:"updated_at"`
}

type UserInsight struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	UserID           uuid.UUID  `json:"userId" db:"user_id"`
	Content          string     `json:"content" db:"content"`
	Category         string     `json:"category" db:"category"`
	Weight           int        `json:"weight" db:"weight"`
	ReferenceCount   int        `json:"referenceCount" db:"reference_count"`
	LastReferencedAt *time.Time `json:"lastReferencedAt" db:"last_referenced_at"`
	CreatedAt        time.Time  `json:"createdAt" db:"created_at"`
}

type MessageFeedback struct {
	ID        uuid.UUID `json:"id" db:"id"`
	MessageID uuid.UUID `json:"messageId" db:"message_id"`
	UserID    uuid.UUID `json:"userId" db:"user_id"`
	Score     int       `json:"score" db:"score"`
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
}

type AIAction struct {
	ID         uuid.UUID              `json:"id" db:"id"`
	UserID     uuid.UUID              `json:"userId" db:"user_id"`
	SessionID  uuid.UUID              `json:"sessionId" db:"session_id"`
	MessageID  *uuid.UUID             `json:"messageId" db:"message_id"`
	ActionType string                 `json:"actionType" db:"action_type"`
	Payload    map[string]interface{} `json:"payload" db:"payload"`
	Status     string                 `json:"status" db:"status"`
	CreatedAt  time.Time              `json:"createdAt" db:"created_at"`
	ExecutedAt *time.Time             `json:"executedAt" db:"executed_at"`
}

type MarketDataCache struct {
	Symbol        string    `json:"symbol" db:"symbol"`
	MarketType    string    `json:"marketType" db:"market_type"`
	Price         *float64  `json:"price" db:"price"`
	ChangePercent *float64  `json:"changePercent" db:"change_percent"`
	Volume        *int64    `json:"volume" db:"volume"`
	CachedAt      time.Time `json:"cachedAt" db:"cached_at"`
	ExpiresAt     time.Time `json:"expiresAt" db:"expires_at"`
}

// SyncEvent represents the sync_queue table (Layer 3)
type SyncEvent struct {
	ID          uuid.UUID `json:"id" db:"id"`
	UserID      uuid.UUID `json:"user_id" db:"user_id"`
	EntityType  string    `json:"entity_type" db:"entity_type"`
	EntityID    uuid.UUID `json:"entity_id" db:"entity_id"`
	Action      string    `json:"action" db:"action"`
	Payload     string    `json:"payload" db:"payload"`
	VectorClock int64     `json:"vector_clock" db:"vector_clock"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// DailyJournal represents the daily_journal table
type DailyJournal struct {
	ID               uuid.UUID `json:"id" db:"id"`
	UserID           uuid.UUID `json:"userId" db:"user_id"`
	Title            *string   `json:"title" db:"title"`
	Date             time.Time `json:"date" db:"date"`
	PsychologyNotes  *string   `json:"psychologyNotes" db:"psychology_notes"`
	MarketConditions *string   `json:"marketConditions" db:"market_conditions"`
	Mistakes         *string   `json:"mistakes" db:"mistakes"`
	Rating           *int      `json:"rating" db:"rating"`
	CreatedAt        time.Time `json:"createdAt" db:"created_at"`
	VectorClock      int64     `json:"vectorClock" db:"vector_clock"`
}

// CustomSetup represents the custom_setups table
type CustomSetup struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    uuid.UUID `json:"user_id" db:"user_id"`
	Name      string    `json:"name" db:"name"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// CustomSession represents the custom_sessions table
type CustomSession struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    uuid.UUID `json:"user_id" db:"user_id"`
	Name      string    `json:"name" db:"name"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
