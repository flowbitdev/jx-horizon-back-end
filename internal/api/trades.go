package api

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"jx_api/internal/models"
	"jx_api/internal/storage"
)

type TradeHandler struct {
	store storage.IStorage
}

func NewTradeHandler(store storage.IStorage) *TradeHandler {
	return &TradeHandler{store: store}
}

func (h *TradeHandler) GetTrades(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	limitStr := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 50
	}

	trades, err := h.store.GetTrades(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to fetch trades")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch trades"})
		return
	}

	// Filter by trade mode if specified
	isDemoParam := c.Query("isDemo")
	isBacktestParam := c.Query("isBacktest")
	isMissedParam := c.Query("isMissed")

	var filtered []models.Trade
	for _, t := range trades {
		if isDemoParam != "" && strconv.FormatBool(t.IsDemo) != isDemoParam {
			continue
		}
		if isBacktestParam != "" && strconv.FormatBool(t.IsBacktest) != isBacktestParam {
			continue
		}
		if isMissedParam == "true" {
			if t.Outcome == nil || *t.Outcome != "missed" {
				continue
			}
		}
		filtered = append(filtered, t)
	}
	if filtered == nil {
		filtered = []models.Trade{}
	}

	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	c.JSON(http.StatusOK, gin.H{"trades": filtered})
}

func (h *TradeHandler) CreateTrade(c *gin.Context) {
	var trade models.Trade
	if err := c.ShouldBindJSON(&trade); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get UserID from session
	userID := c.MustGet("user_id").(uuid.UUID)
	trade.UserID = userID

	if err := h.store.CreateTrade(c.Request.Context(), &trade); err != nil {
		log.Error().Err(err).Msg("Failed to create trade")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create trade"})
		return
	}

	c.JSON(http.StatusCreated, trade)
}

func (h *TradeHandler) UpdateTrade(c *gin.Context) {
	tradeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid trade ID"})
		return
	}

	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get userID from session
	userID := c.MustGet("user_id").(uuid.UUID)

	if err := h.store.UpdateTrade(c.Request.Context(), tradeID, userID, updates); err != nil {
		log.Error().Err(err).Msg("Failed to update trade")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update trade"})
		return
	}

	trade, err := h.store.GetTrade(c.Request.Context(), tradeID, userID)
	if err != nil || trade == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trade not found after update"})
		return
	}

	c.JSON(http.StatusOK, trade)
}

func (h *TradeHandler) DeleteTrade(c *gin.Context) {
	tradeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid trade ID"})
		return
	}

	// Get userID from session
	userID := c.MustGet("user_id").(uuid.UUID)

	if err := h.store.DeleteTrade(c.Request.Context(), tradeID, userID); err != nil {
		log.Error().Err(err).Msg("Failed to delete trade")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete trade"})
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *TradeHandler) GetStats(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	
	queryUserID := c.Query("userId")
	if queryUserID != "" {
		if parsed, err := uuid.Parse(queryUserID); err == nil {
			userID = parsed
		}
	}

	trades, err := h.store.GetTrades(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch trades"})
		return
	}

	// Filter by trade mode
	isDemoParam := c.Query("isDemo")
	isBacktestParam := c.Query("isBacktest")
	isMissedParam := c.Query("isMissed")

	var filteredTrades []models.Trade
	for _, t := range trades {
		if isDemoParam != "" && strconv.FormatBool(t.IsDemo) != isDemoParam {
			continue
		}
		if isBacktestParam != "" && strconv.FormatBool(t.IsBacktest) != isBacktestParam {
			continue
		}
		if isMissedParam == "true" {
			if t.Outcome == nil || *t.Outcome != "missed" {
				continue
			}
		}
		filteredTrades = append(filteredTrades, t)
	}

	totalTrades := len(filteredTrades)
	if totalTrades == 0 {
		c.JSON(http.StatusOK, gin.H{
			"winRate":      0,
			"avgRR":        0,
			"totalPnL":     0,
			"totalTrades":  0,
			"profitFactor": 0,
			"maxDrawdown":  0,
			"equityCurve":  []interface{}{},
			"avgEmotion":   0,
		})
		return
	}

	var winCount int
	var totalPnL float64
	var grossWin float64
	var grossLoss float64
	var sumRR float64
	var rrCount int
	var emotionSum int
	var emotionCount int

	for _, t := range filteredTrades {
		if t.Outcome != nil && *t.Outcome == "win" {
			winCount++
		}
		if t.ProfitLoss != nil {
			pnl := *t.ProfitLoss
			totalPnL += pnl
			if pnl > 0 {
				grossWin += pnl
			} else if pnl < 0 {
				grossLoss += math.Abs(pnl)
			}
		}
		if t.RR != nil && *t.RR >= 0 {
			sumRR += *t.RR
			rrCount++
		}

		if t.EmotionBefore != nil {
			emotionSum += *t.EmotionBefore
			emotionCount++
		}
		if t.EmotionDuring != nil {
			emotionSum += *t.EmotionDuring
			emotionCount++
		}
		if t.EmotionAfter != nil {
			emotionSum += *t.EmotionAfter
			emotionCount++
		}
	}

	winRate := (float64(winCount) / float64(totalTrades)) * 100
	avgRR := 0.0
	if rrCount > 0 {
		avgRR = sumRR / float64(rrCount)
	}

	profitFactor := 0.0
	if grossLoss > 0 {
		profitFactor = grossWin / grossLoss
	} else if grossWin > 0 {
		profitFactor = grossWin
	}

	avgEmotion := 0.0
	if emotionCount > 0 {
		avgEmotion = float64(emotionSum) / float64(emotionCount)
	}

	// Sort chronological for equity curve
	sort.Slice(filteredTrades, func(i, j int) bool {
		return filteredTrades[i].Date.Before(filteredTrades[j].Date)
	})

	var peak float64
	var maxDrawdown float64
	var runningEquity float64

	type EquityPoint struct {
		Date   string  `json:"date"`
		Equity float64 `json:"equity"`
	}
	var equityCurve []EquityPoint

	for _, t := range filteredTrades {
		pnl := 0.0
		if t.ProfitLoss != nil {
			pnl = *t.ProfitLoss
		}
		runningEquity += pnl

		if runningEquity > peak {
			peak = runningEquity
		}

		if peak > 0 {
			drawdown := ((peak - runningEquity) / peak) * 100
			if drawdown > maxDrawdown {
				maxDrawdown = drawdown
			}
		} else if runningEquity < peak {
			drawdown := math.Abs(peak - runningEquity)
			if drawdown > maxDrawdown {
				maxDrawdown = drawdown
			}
		}

		equityCurve = append(equityCurve, EquityPoint{
			Date:   t.Date.Format("1/2/2006"),
			Equity: runningEquity,
		})
	}

	if equityCurve == nil {
		equityCurve = make([]EquityPoint, 0)
	}

	c.JSON(http.StatusOK, gin.H{
		"winRate":      winRate,
		"avgRR":        avgRR,
		"totalPnL":     totalPnL,
		"totalTrades":  totalTrades,
		"profitFactor": profitFactor,
		"maxDrawdown":  maxDrawdown,
		"equityCurve":  equityCurve,
		"avgEmotion":   avgEmotion,
	})
}

func (h *TradeHandler) GetStatsPeriods(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	trades, err := h.store.GetTrades(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch trades"})
		return
	}

	// Filter by trade mode
	isDemoParam := c.Query("isDemo")
	isBacktestParam := c.Query("isBacktest")
	isMissedParam := c.Query("isMissed")

	var filteredTrades []models.Trade
	for _, t := range trades {
		if isDemoParam != "" && strconv.FormatBool(t.IsDemo) != isDemoParam {
			continue
		}
		if isBacktestParam != "" && strconv.FormatBool(t.IsBacktest) != isBacktestParam {
			continue
		}
		if isMissedParam == "true" {
			if t.Outcome == nil || *t.Outcome != "missed" {
				continue
			}
		}
		filteredTrades = append(filteredTrades, t)
	}

	// Calculate Pnl by Month
	type MonthPnL struct {
		Name string  `json:"name"`
		PnL  float64 `json:"pnl"`
	}
	pnlMap := make(map[string]float64)

	// Calculate Setup Performance
	type SetupPerf struct {
		Name    string  `json:"name"`
		PnL     float64 `json:"pnl"`
		Wins    int     `json:"wins"`
		Total   int     `json:"total"`
		WinRate float64 `json:"winRate"`
	}
	setupMap := make(map[string]*SetupPerf)

	// Calculate RR Distribution
	rrBuckets := []struct {
		Name  string
		Min   float64
		Max   float64
		Count int
	}{
		{Name: "< 0R", Min: -math.MaxFloat64, Max: 0, Count: 0},
		{Name: "0-1R", Min: 0, Max: 1, Count: 0},
		{Name: "1-2R", Min: 1, Max: 2, Count: 0},
		{Name: "2-3R", Min: 2, Max: 3, Count: 0},
		{Name: "3-5R", Min: 3, Max: 5, Count: 0},
		{Name: "5R+", Min: 5, Max: math.MaxFloat64, Count: 0},
	}

	for _, t := range filteredTrades {
		// Month
		month := t.Date.Format("Jan 2006")
		pnl := 0.0
		if t.ProfitLoss != nil {
			pnl = *t.ProfitLoss
		}
		pnlMap[month] += pnl

		// Setup
		setupName := "Unknown"
		if t.SetupType != "" {
			setupName = t.SetupType
		}
		if _, exists := setupMap[setupName]; !exists {
			setupMap[setupName] = &SetupPerf{Name: setupName}
		}
		setup := setupMap[setupName]
		setup.PnL += pnl
		setup.Total++
		if t.Outcome != nil && *t.Outcome == "win" {
			setup.Wins++
		}
		setup.WinRate = (float64(setup.Wins) / float64(setup.Total)) * 100

		// RR
		rr := 0.0
		if t.RR != nil {
			rr = *t.RR
		}
		for i := range rrBuckets {
			if rr >= rrBuckets[i].Min && rr < rrBuckets[i].Max {
				rrBuckets[i].Count++
				break
			}
		}
	}

	var pnlByMonth []MonthPnL
	var sortedMonths []string
	for m := range pnlMap {
		sortedMonths = append(sortedMonths, m)
	}
	sort.Slice(sortedMonths, func(i, j int) bool {
		t1, _ := time.Parse("Jan 2006", sortedMonths[i])
		t2, _ := time.Parse("Jan 2006", sortedMonths[j])
		return t1.Before(t2)
	})

	for _, m := range sortedMonths {
		pnlByMonth = append(pnlByMonth, MonthPnL{Name: m, PnL: pnlMap[m]})
	}

	var setupPerformance []SetupPerf
	for _, sp := range setupMap {
		setupPerformance = append(setupPerformance, *sp)
	}

	type RRBucketOut struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	var rrDistribution []RRBucketOut
	for _, b := range rrBuckets {
		rrDistribution = append(rrDistribution, RRBucketOut{Name: b.Name, Value: b.Count})
	}

	if pnlByMonth == nil {
		pnlByMonth = make([]MonthPnL, 0)
	}
	if setupPerformance == nil {
		setupPerformance = make([]SetupPerf, 0)
	}

	c.JSON(http.StatusOK, gin.H{
		"pnlByMonth":       pnlByMonth,
		"setupPerformance": setupPerformance,
		"rrDistribution":   rrDistribution,
	})
}
