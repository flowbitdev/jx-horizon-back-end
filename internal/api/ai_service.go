package api

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	htmlstd "html"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jx_api/internal/models"
	"jx_api/internal/storage"

	"github.com/google/generative-ai-go/genai"
	"github.com/rs/zerolog/log"
	xhtml "golang.org/x/net/html"
	"google.golang.org/api/option"
)

// ─── Types ──────────────────────────────────────────────────────────────────

type AIService struct {
	client    *genai.Client
	model     *genai.GenerativeModel
	modelName string
}

type GlobalMemory struct {
	Weaknesses      []string `json:"weaknesses"`
	Strengths       []string `json:"strengths"`
	Rules           []string `json:"rules"`
	StrategyProfile *struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Description string `json:"description"`
	} `json:"strategyProfile"`
}

type AIResponse struct {
	Text         string `json:"text"`
	MemoryUpdate *struct {
		Category string `json:"category"`
		Value    string `json:"value"`
	} `json:"memoryUpdate,omitempty"`
	ActionProposal *AIActionProposal `json:"actionProposal,omitempty"`
}

type AIToolSource struct {
	Type   string `json:"type"`
	Label  string `json:"label"`
	URL    string `json:"url,omitempty"`
	Status string `json:"status"`
	Value  string `json:"value,omitempty"`
	Kind   string `json:"kind,omitempty"`
}

type toolResult struct {
	Source  AIToolSource
	Context string
}

type yahooRSS struct {
	Items []struct {
		Title       string `xml:"title"`
		Link        string `xml:"link"`
		Description string `xml:"description"`
		PublishDate string `xml:"pubDate"`
	} `xml:"channel>item"`
}

// TradeSnapshot is a flat, pointer-free struct used ONLY for AI context.
// All nullable fields are resolved to concrete string values before this is built.
type TradeSnapshot struct {
	Symbol        string `json:"symbol"`
	Direction     string `json:"direction"`
	SetupType     string `json:"setupType"`
	Outcome       string `json:"outcome"`
	Entry         string `json:"entry"`
	Exit          string `json:"exit"`
	StopLoss      string `json:"stopLoss"`
	TakeProfit    string `json:"takeProfit"`
	LotSize       string `json:"lotSize"`
	RiskPercent   string `json:"riskPercent"`
	ProfitLoss    string `json:"profitLoss"`
	RR            string `json:"rr"`
	EmotionBefore string `json:"emotionBefore,omitempty"`
	EmotionDuring string `json:"emotionDuring,omitempty"`
	EmotionAfter  string `json:"emotionAfter,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// safeFloat64 dereferences a *float64 and formats it; returns fallback if nil.
func safeFloat64(ptr *float64, fallback string) string {
	if ptr == nil {
		return fallback
	}
	return fmt.Sprintf("%.2f", *ptr)
}

// safeString dereferences a *string; returns fallback if nil or empty.
func safeString(ptr *string, fallback string) string {
	if ptr == nil || *ptr == "" {
		return fallback
	}
	return *ptr
}

// safeInt dereferences a *int and formats it; returns fallback if nil.
func safeInt(ptr *int, fallback string) string {
	if ptr == nil {
		return fallback
	}
	return fmt.Sprintf("%d/10", *ptr)
}

// buildTradeSnapshot converts a models.Trade into a flat, safe TradeSnapshot.
func buildTradeSnapshot(t *models.Trade) TradeSnapshot {
	return TradeSnapshot{
		Symbol:        t.Symbol,
		Direction:     t.Direction,
		SetupType:     t.SetupType,
		Outcome:       strings.ToUpper(safeString(t.Outcome, "OPEN")),
		Entry:         fmt.Sprintf("%.2f", t.Entry),
		Exit:          safeFloat64(t.ExitPrice, "N/A (Still Open)"),
		StopLoss:      fmt.Sprintf("%.2f", t.StopLoss),
		TakeProfit:    fmt.Sprintf("%.2f", t.TakeProfit),
		LotSize:       fmt.Sprintf("%.2f", t.LotSize),
		RiskPercent:   fmt.Sprintf("%.2f", t.RiskPercent),
		ProfitLoss:    safeFloat64(t.ProfitLoss, "N/A"),
		RR:            safeFloat64(t.RR, "N/A"),
		EmotionBefore: safeInt(t.EmotionBefore, ""),
		EmotionDuring: safeInt(t.EmotionDuring, ""),
		EmotionAfter:  safeInt(t.EmotionAfter, ""),
		Notes:         safeString(t.Notes, ""),
	}
}

// snapshotToText converts a TradeSnapshot into a human-readable multi-line string.
func snapshotToText(s TradeSnapshot) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Symbol: %s | Direction: %s | Setup: %s | Outcome: %s\n", s.Symbol, s.Direction, s.SetupType, s.Outcome))
	sb.WriteString(fmt.Sprintf("Entry: %s | Exit: %s | Stop Loss: %s | Take Profit: %s\n", s.Entry, s.Exit, s.StopLoss, s.TakeProfit))
	sb.WriteString(fmt.Sprintf("Lot Size: %s | Risk%%: %s | P/L: %s | R:R: %s", s.LotSize, s.RiskPercent, s.ProfitLoss, s.RR))

	if s.EmotionBefore != "" {
		sb.WriteString(fmt.Sprintf("\nEmotion Before: %s", s.EmotionBefore))
	}
	if s.EmotionDuring != "" {
		sb.WriteString(fmt.Sprintf(" | Emotion During: %s", s.EmotionDuring))
	}
	if s.EmotionAfter != "" {
		sb.WriteString(fmt.Sprintf(" | Emotion After: %s", s.EmotionAfter))
	}
	if s.Notes != "" {
		sb.WriteString(fmt.Sprintf("\nNotes: %s", s.Notes))
	}

	return sb.String()
}

// snapshotToSummary converts a TradeSnapshot into a single-line summary for lists.
func snapshotToSummary(s TradeSnapshot) string {
	return fmt.Sprintf("%s %s %s | Entry: %s, Exit: %s, SL: %s, TP: %s | Outcome: %s, P/L: %s, R:R: %s",
		s.Symbol, s.Direction, s.SetupType, s.Entry, s.Exit, s.StopLoss, s.TakeProfit, s.Outcome, s.ProfitLoss, s.RR)
}

type PersonaID int

const (
	PersonaCoach PersonaID = iota
	PersonaRoast
	PersonaAnalyst
	PersonaRiskManager
	PersonaPsychologist
)

func ParsePersonaID(p string) PersonaID {
	switch strings.ToLower(p) {
	case "roast":
		return PersonaRoast
	case "analyst":
		return PersonaAnalyst
	case "risk manager", "risk_manager", "riskmanager", "risk mgr", "risk_mgr":
		return PersonaRiskManager
	case "psychologist":
		return PersonaPsychologist
	default:
		return PersonaCoach
	}
}

func (p PersonaID) String() string {
	switch p {
	case PersonaRoast:
		return "roast"
	case PersonaAnalyst:
		return "analyst"
	case PersonaRiskManager:
		return "risk manager"
	case PersonaPsychologist:
		return "psychologist"
	default:
		return "coach"
	}
}

// getPersonaPrompt returns the system prompt for the selected persona.
func getPersonaPrompt(persona PersonaID) string {
	base := `You are JX Horizon AI Coach, a highly capable trading coach and general-purpose AI assistant.

Core mission:
- Help the trader improve process, risk, discipline, execution quality, journaling consistency, and strategy clarity.
- While you focus on trading, you are a general-purpose AI assistant. If the user asks you to write code, design diagrams, create general PDFs, explain non-trading concepts, or research general topics, fulfill their request fully and helpfully. Do not refuse general-purpose requests or claim you only do trading.
- Use the user's available journal data, trades, goals, strategies, and uploaded context when present.
- If information is missing, say what is missing and give the next best safe recommendation.

Scope and response contract:
- Treat this chat as a live, tool-using assistant, not a static chatbot.
- For anything that can change over time, must be sourced, or depends on current state, use a tool instead of memory.
- Keep the workflow, final answer, and sources logically separate in the returned metadata. Do not blend reasoning into the answer text.
- Your visible answer must be only the final user-facing response. Never include internal reasoning, hidden planning, or tool-call narration in the answer text.
- When no tool is needed, write the answer in 1 to 4 short chunks: direct answer first, then concise supporting details. Do not preface with "The user said", "I should", "thinking", "analysis", or any meta commentary.
- If you need to mention context, put it after the direct answer in a separate short paragraph. Do not repeat the prompt or echo system instructions.
- Use live search for news and current web facts, live quote tools for prices, URL fetch for exact pages, and user-data tools for journal/trade/goal/strategy analysis.
- When the user asks for a chart, visual, diagram, SVG, PDF, report, or downloadable artifact, use the artifact tools and return a file that the client can render or download.
- For write actions, draft a confirmation proposal only. Never imply a mutation has been saved until the user confirms it.

Available tools and how to use them:
- User Data Tool: Read-only inspection of the authenticated user's trades, journal entries, goals, and strategies. Use it when the user asks about their history, performance, goals, playbook, logs, SQL/query/database, patterns, or asks for personalized recommendations. For large tables, request counts/summaries/aggregates first instead of dumping rows.
- Price Tool: Use for current pair/asset quote checks when the user asks for price, quote, rate, market level, or a symbol/pair price. When a live price observation is present, answer with the price in the first sentence. Never tell the user you cannot check prices when the price tool returned an observation; if it failed, say the tool failed.
- Freshness/Search Tool: If the user asks about anything that can change over time or if you need to fetch information about any topic, use a live tool. Only answer from model memory for stable general truths.
- Yahoo News Tool: Use for latest market/news/headline requests. Cite the shown source labels/links and separate news facts from your interpretation.
- DuckDuckGo Web Tool: Use for general web research/search requests on any topic. Summarize search results with source labels and never invent source details.
- Specific URL Tool: Use when the user gives a URL. Analyze only fetched/extracted URL content plus user context. If fetch failed, say it failed and ask for pasted content.
- Risk Tool: Use for risk management, position sizing, drawdown, expectancy, profit factor, win rate, average R, daily loss limit, and risk-of-ruin style questions.
- SVG Tool: Use when the user asks for an image, chart, visual, diagram, SVG, equity curve, framework, workflow, or printable visual. If you want to create a custom diagram, chart, flow, or visual, you must write the full, raw XML SVG code starting with <svg and ending with </svg> directly inside the tool input (e.g. tool_call{generate_svg{<svg ...>...</svg>}}). Do not write a description or JSON wrapper, write only the raw XML code. The server will save and render this custom SVG directly. Never inline raw SVG code in the main chat response; only put it inside the tool call. The client renders generated SVG files in Generated Files & Interactive Tools.
- PDF Tool: Use when the user asks for a report, export, summary PDF, review document, or printable plan. Pass any specific content, text, data, or formatting instructions you want to be included in the PDF as the tool input (e.g. generate_pdf{Any text/data/report contents...}). The server will generate a custom PDF with this exact content and return the file link. You can create a PDF about any topic or content the user requests.
- Script Tool: Arbitrary code execution is guarded. Execution must be offline after input data is staged, time-limited, memory/storage-limited, and unable to access the internet. You may draft scripts, formulas, SQL, and calculation plans, but do not claim code ran unless the tool reports it ran.
- Action Proposal Tool: For creating journal entries, goals, strategies, or trades, draft a proposed action and tell the user it requires confirmation. Never claim that data was saved until the confirmation action is completed by the client.

Tool discipline:
- Use tools only when they materially improve the answer. Do not call tools just to look busy.
- Use an internal action/observation loop to choose tools, but keep that reasoning out of the user-visible response.
- Treat changing facts as tool-required. Never answer current worth, current price, latest news, current market cap, current CEO/ownership, timelines, release dates, rules, schedules, or rates from memory alone.
- When tool results are available, ground your answer in them and mention the relevant sources/artifacts.
- If a tool result is failed, empty, stale, or partial, say that plainly and continue with the best available context.
- Never fabricate prices, news, search results, URLs, database rows, generated files, or confirmed writes.
- For user-data analysis, respect scope: only discuss the authenticated user's own data returned in context.
- For writes, always phrase as "I prepared a proposed action" or "Review and confirm this" until confirmed.
- After tool results are collected, append the instruction: respond with only the final answer, with no reasoning or tool narration.

Trading coaching standards:
- Always check risk first: invalidation, stop placement, R:R, position size, max daily loss, drawdown, overtrading, and whether the setup matches the playbook.
- Separate observations, diagnosis, and next action.
- Give direct, actionable steps: what to stop doing, what to keep, what to test next, and what to journal.
- If the user asks for trade entry advice, avoid pretending certainty. Frame scenarios, risk, invalidation, and decision rules.
- If the user asks for improvements, suggest one immediate behavior change, one tracking metric, and one rule to add or refine.
- When analyzing an open trade, focus on plan quality and risk management instead of pretending realized P/L exists.
- When analyzing closed trades, use Entry, Exit, Stop Loss, Take Profit, R:R, P/L, setup, session, emotions, screenshots, notes, and strategy fit when present.

Response style:
- Be concise but high signal.
- Use markdown headings and bullets when helpful.
- Do not write Thought, Action, Observation, Tool Call, Tool Result, AI Workflow, or Sources sections in the final answer. The client renders workflow and sources separately from metadata.
- Do not expose hidden chain-of-thought. If thinking mode is requested, improve the internal analysis depth, but keep the final answer clean and user-facing.
`

	switch persona {
	case PersonaRoast:
		return base + "\nPersona voice: ROAST Coach. Brutal, aggressive, cynical, and unforgiving. Attack sloppy process, oversized risk, revenge trading, and excuses. Be harsh about behavior, not personal identity. End with a concrete rule the trader must follow."
	case PersonaAnalyst:
		return base + "\nPersona voice: Market Analyst. Cold, logical, data-first. Emphasize sample size, expectancy, distribution, setup quality, regime context, and statistical validity. Avoid emotional language unless it affects execution data."
	case PersonaRiskManager:
		return base + "\nPersona voice: Risk Manager. Capital preservation is the priority. Lead with position size, stop loss, exposure, drawdown, max daily loss, correlation, and invalidation. If risk is unclear, stop and demand the missing risk inputs."
	case PersonaPsychologist:
		return base + "\nPersona voice: Trading Psychologist. Focus on discipline, emotional triggers, bias, self-sabotage, confidence calibration, boredom, fear, greed, FOMO, revenge trading, and routines. Convert insight into one repeatable behavioral protocol."
	default:
		return base + "\nPersona voice: Balanced Coach. Supportive but firm. Blend execution review, risk discipline, strategy fit, and psychology. Give the trader a clear next action without overloading them."
	}
}

// extractResponse pulls text from Gemini response candidates.
func extractResponse(resp *genai.GenerateContentResponse) string {
	var answerText string
	if resp == nil {
		return answerText
	}
	for _, cand := range resp.Candidates {
		if cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			if txt, ok := part.(genai.Text); ok {
				trimmed := strings.TrimSpace(string(txt))
				if trimmed != "" {
					answerText = trimmed
				}
			}
		}
	}
	return strings.TrimSpace(answerText)
}

func generateContentWithRetry(ctx context.Context, model *genai.GenerativeModel, parts ...genai.Part) (*genai.GenerateContentResponse, error) {
	var lastErr error
	backoffs := []time.Duration{0, 250 * time.Millisecond, 750 * time.Millisecond}
	for attempt := 0; attempt < len(backoffs); attempt++ {
		if backoffs[attempt] > 0 {
			time.Sleep(backoffs[attempt])
		}
		resp, err := model.GenerateContent(ctx, parts...)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		msg := strings.ToLower(err.Error())
		if !(strings.Contains(msg, "error 500") || strings.Contains(msg, "error 429") || strings.Contains(msg, "quota") || strings.Contains(msg, "rate limit") || strings.Contains(msg, "timeout") || strings.Contains(msg, "tempor") || strings.Contains(msg, "unavailable") || strings.Contains(msg, "internal")) {
			return nil, err
		}
		log.Warn().Err(err).Int("attempt", attempt+1).Msg("Transient Gemini generateContent failure; retrying")
	}
	return nil, lastErr
}

func getToolDeclarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "web_search",
			Description: "DuckDuckGo web search to retrieve current facts, market cap, CEO, valuation, or details about any general topic.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"query": {
						Type:        genai.TypeString,
						Description: "The search query string",
					},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "news_search",
			Description: "Yahoo News search to retrieve the latest financial or general headlines.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"query": {
						Type:        genai.TypeString,
						Description: "The news search query term",
					},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "price_check",
			Description: "Check the current live market quote for a symbol or pair (e.g. BTC/USD, EUR/USD, NVDA).",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"symbol": {
						Type:        genai.TypeString,
						Description: "The ticker symbol or currency pair name",
					},
				},
				Required: []string{"symbol"},
			},
		},
		{
			Name:        "fetch_url",
			Description: "Fetch and summarize the text content of a specific URL.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"url": {
						Type:        genai.TypeString,
						Description: "The absolute HTTP or HTTPS URL to fetch",
					},
				},
				Required: []string{"url"},
			},
		},
		{
			Name:        "execute_javascript",
			Description: "Runs arbitrary JavaScript code using Node.js on the server. Allows performing complex calculations, fetching dynamic APIs, processing data, or creating temporary files. Code runs in the project directory where npm packages like 'sharp' are installed.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"code": {
						Type:        genai.TypeString,
						Description: "The JavaScript source code to execute",
					},
				},
				Required: []string{"code"},
			},
		},
		{
			Name:        "generate_image",
			Description: "Dynamically generate an SVG image artifact. If svg_code is provided (standard XML starting with <svg> and ending with </svg>), it will be saved directly. If svg_code is omitted, a beautiful diagram will be dynamically designed based on the prompt description.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"prompt": {
						Type:        genai.TypeString,
						Description: "The descriptive prompt for the design",
					},
					"svg_code": {
						Type:        genai.TypeString,
						Description: "Optional raw XML SVG code matching the design",
					},
				},
				Required: []string{"prompt"},
			},
		},
		{
			Name:        "generate_pdf",
			Description: "Generates a customized PDF report document with the given text/markdown content, returning a download link.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"content": {
						Type:        genai.TypeString,
						Description: "The text or markdown report content",
					},
				},
				Required: []string{"content"},
			},
		},
		{
			Name:        "read_user_db",
			Description: "Query the authenticated user's portfolio data. Allows inspecting tables: 'trades' (logged trades), 'goals' (active targets), 'strategies' (trading playbook rules), or 'journal' (psychology logs).",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"table": {
						Type:        genai.TypeString,
						Description: "Name of the table to query: trades, goals, strategies, or journal",
					},
					"limit": {
						Type:        genai.TypeInteger,
						Description: "Optional limit for query results (default 10)",
					},
				},
				Required: []string{"table"},
			},
		},
		{
			Name:        "write_user_db",
			Description: "Draft a new entry or edit in the user's database. Prepares a proposed action configuration (e.g. 'create_trade', 'create_goal', 'create_strategy', 'create_journal') and returns it for user confirmation.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"action": {
						Type:        genai.TypeString,
						Description: "The write action name: create_trade, create_goal, create_strategy, create_journal",
					},
					"payload_json": {
						Type:        genai.TypeString,
						Description: "JSON payload containing the entry details matching schema structures",
					},
				},
				Required: []string{"action", "payload_json"},
			},
		},
	}
}

// ─── Constructor ────────────────────────────────────────────────────────────

func NewAIService() *AIService {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Warn().Msg("GEMINI_API_KEY not set")
		return &AIService{}
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Gemini client")
	}

	modelName := os.Getenv("GEMINI_MODEL")
	if modelName == "" {
		modelName = "gemma-4-31b-it"
	}
	model := client.GenerativeModel(modelName)

	// Register native function declarations for tool calling
	model.Tools = []*genai.Tool{
		{
			FunctionDeclarations: getToolDeclarations(),
		},
	}

	return &AIService{client: client, model: model, modelName: modelName}
}

func (s *AIService) BuildToolContext(ctx context.Context, message string, recentTrades []models.Trade) (string, []AIToolSource) {
	lower := strings.ToLower(message)
	var sources []AIToolSource
	var blocks []string

	for _, rawURL := range extractURLs(message) {
		result := fetchSpecificURL(ctx, rawURL)
		sources = append(sources, result.Source)
		if result.Context != "" {
			blocks = append(blocks, result.Context)
		}
	}

	if wantsNewsTool(lower) {
		result := fetchYahooNews(ctx, inferSearchQuery(message))
		sources = append(sources, result.Source)
		if result.Context != "" {
			blocks = append(blocks, result.Context)
		}
	}

	if wantsWebTool(lower) {
		result := fetchDuckDuckGo(ctx, inferSearchQuery(message))
		sources = append(sources, result.Source)
		if result.Context != "" {
			blocks = append(blocks, result.Context)
		}
	}

	if wantsPriceTool(lower) {
		symbol := extractLikelySymbol(message)
		source := AIToolSource{Type: "price", Label: "Pair price check", Status: "requested"}
		if symbol != "" {
			source.Label = "Pair price check: " + symbol
			if quote, provider, quoteURL, ok := fetchMarketQuote(ctx, symbol); ok {
				source.Status = "live"
				source.Value = quote
				source.URL = quoteURL
				blocks = append(blocks, fmt.Sprintf("Price tool observation from %s: %s = %s. Answer the user's price question directly in the first sentence, then mention this is a moving market snapshot.", provider, symbol, quote))
			} else {
				source.Status = "failed"
				source.Value = "No live quote returned"
				blocks = append(blocks, "Price tool observation: attempted to fetch "+symbol+" but no live quote was returned. Tell the user the price lookup failed; do not say you lack a price tool.")
			}
		} else {
			source.Status = "failed"
			source.Value = "No symbol detected"
			blocks = append(blocks, "Price tool observation: no symbol or pair was detected in the user's request. Ask for the symbol or pair.")
		}
		sources = append(sources, source)
	}

	if wantsRiskTool(lower) {
		result := buildRiskManagementTool(recentTrades)
		sources = append(sources, result.Source)
		if result.Context != "" {
			blocks = append(blocks, result.Context)
		}
	}

	if strings.Contains(lower, "script") || strings.Contains(lower, "execute") || strings.Contains(lower, "run code") {
		result := buildSafeScriptToolNotice()
		sources = append(sources, result.Source)
		blocks = append(blocks, result.Context)
	}

	if len(blocks) == 0 {
		return "", sources
	}
	return "\n\n### TOOL CONTEXT\n" + strings.Join(blocks, "\n"), sources
}

func wantsRiskTool(lower string) bool {
	return strings.Contains(lower, "risk") ||
		strings.Contains(lower, "position size") ||
		strings.Contains(lower, "lot size") ||
		strings.Contains(lower, "drawdown") ||
		strings.Contains(lower, "expectancy") ||
		strings.Contains(lower, "kelly") ||
		strings.Contains(lower, "risk management")
}

func wantsNewsTool(lower string) bool {
	return strings.Contains(lower, "news") ||
		strings.Contains(lower, "headline") ||
		strings.Contains(lower, "latest") ||
		strings.Contains(lower, "recent") ||
		strings.Contains(lower, "today") ||
		strings.Contains(lower, "this week")
}

func wantsWebTool(lower string) bool {
	return strings.Contains(lower, "search") ||
		strings.Contains(lower, "web") ||
		strings.Contains(lower, "google") ||
		strings.Contains(lower, "who is") ||
		strings.Contains(lower, "current") ||
		strings.Contains(lower, "market cap") ||
		strings.Contains(lower, "valuation") ||
		strings.Contains(lower, "timeline") ||
		strings.Contains(lower, "schedule") ||
		strings.Contains(lower, "release date") ||
		strings.Contains(lower, "ceo of") ||
		strings.Contains(lower, "worth")
}

func wantsPriceTool(lower string) bool {
	return strings.Contains(lower, "price") ||
		strings.Contains(lower, "quote") ||
		strings.Contains(lower, "market rate") ||
		strings.Contains(lower, "exchange rate") ||
		strings.Contains(lower, "worth") ||
		strings.Contains(lower, "market cap") ||
		extractLikelySymbol(lower) != ""
}

func buildRiskManagementTool(trades []models.Trade) toolResult {
	source := AIToolSource{Type: "risk", Label: "Interactive risk management calculator", Status: "live", Kind: "interactive-risk"}
	if len(trades) == 0 {
		return toolResult{Source: source, Context: "Risk tool: no trades available yet."}
	}
	var wins, losses int
	var totalR, grossWin, grossLoss, equity, peak, maxDrawdown float64
	for _, t := range trades {
		r := 0.0
		if t.RR != nil {
			r = *t.RR
		}
		pl := 0.0
		if t.ProfitLoss != nil {
			pl = *t.ProfitLoss
		}
		totalR += r
		equity += pl
		if equity > peak {
			peak = equity
		}
		if peak-equity > maxDrawdown {
			maxDrawdown = peak - equity
		}
		if pl > 0 {
			wins++
			grossWin += pl
		} else if pl < 0 {
			losses++
			grossLoss += -pl
		}
	}
	winRate := float64(wins) / float64(len(trades)) * 100
	profitFactor := grossWin
	if grossLoss > 0 {
		profitFactor = grossWin / grossLoss
	}
	avgR := totalR / float64(len(trades))
	source.Value = fmt.Sprintf("WR %.1f%% / AvgR %.2f / PF %.2f", winRate, avgR, profitFactor)
	context := fmt.Sprintf(`Risk management snapshot:
- Trades reviewed: %d
- Win rate: %.1f%%
- Average R multiple: %.2f
- Profit factor: %.2f
- Max drawdown by logged P/L sequence: %.2f
- Suggested coach focus: enforce max daily loss, validate position size before entry, compare setup expectancy before increasing risk.`, len(trades), winRate, avgR, profitFactor, maxDrawdown)
	context += "\nInteractive artifact: the client rendered a risk calculator so the user can adjust account size, risk percent, entry, stop, and target without asking the AI to recalculate every number."
	return toolResult{Source: source, Context: context}
}

func convertSVGToPNG(svgPath string) (string, error) {
	pngPath := strings.TrimSuffix(svgPath, ".svg") + ".png"
	cmdStr := fmt.Sprintf("require('sharp')('%s').png().toFile('%s')", filepath.ToSlash(svgPath), filepath.ToSlash(pngPath))
	cmd := exec.Command("node", "-e", cmdStr)
	cmd.Dir = ".."
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("sharp conversion failed: %w, output: %s", err, string(output))
	}
	return pngPath, nil
}

func (s *AIService) GenerateSVGFromPrompt(ctx context.Context, prompt string) (string, error) {
	if s.client == nil || s.model == nil {
		return "", fmt.Errorf("Gemini client not initialized")
	}

	systemPrompt := `You are an expert SVG designer. 
Generate a beautiful, modern, high-quality, fully responsive XML SVG diagram matching the user's description.
Use vibrant colors, dark mode theme (#0b1020 background by default unless requested otherwise), modern typography, and clean layouts.
Do not wrap your output in markdown blocks (like ` + "```" + `xml or ` + "```" + `svg). 
Output ONLY the raw SVG XML code starting with '<svg' and ending with '</svg>'. No explanation, no text outside the SVG.`

	resp, err := generateContentWithRetry(ctx, s.model, genai.Text(systemPrompt+"\n\nUser request: "+prompt))
	if err != nil {
		return "", err
	}

	rawText := extractResponse(resp)
	trimmed := strings.TrimSpace(rawText)
	if start := strings.Index(trimmed, "<svg"); start != -1 {
		if end := strings.LastIndex(trimmed, "</svg>"); end != -1 && end > start {
			return trimmed[start : end+6], nil
		}
	}
	return "", fmt.Errorf("invalid SVG code generated: %s", rawText)
}

func (s *AIService) generateSVGArtifact(ctx context.Context, message string, trades []models.Trade) toolResult {
	source := AIToolSource{Type: "svg", Label: "Generated image artifact", Status: "requested", Kind: "svg"}
	if err := os.MkdirAll("uploads", 0755); err != nil {
		source.Status = "failed"
		return toolResult{Source: source}
	}

	trimmed := strings.TrimSpace(message)
	var svg string
	if strings.Contains(trimmed, "<svg") {
		startIdx := strings.Index(trimmed, "<svg")
		endIdx := strings.LastIndex(trimmed, "</svg>")
		if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
			svg = trimmed[startIdx : endIdx+6]
			source.Label = "Custom Generated Image"
		}
	}

	if svg == "" {
		lower := strings.ToLower(message)
		if strings.Contains(lower, "standard equity curve") {
			return generateEquitySVG(trades)
		}
		if strings.Contains(lower, "standard market structure") {
			return generateMarketStructureSVG(message)
		}

		// Dynamic generation fallback
		generatedSvg, err := s.GenerateSVGFromPrompt(ctx, message)
		if err == nil && generatedSvg != "" {
			svg = generatedSvg
			source.Label = "AI Designed Image"
		} else {
			source.Status = "failed"
			source.Value = "Missing raw SVG content"
			errMsg := "Error: Failed to dynamically design the SVG image."
			if err != nil {
				errMsg += " Details: " + err.Error()
			}
			return toolResult{
				Source:  source,
				Context: errMsg,
			}
		}
	}

	svgName := "ai-custom-svg-" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".svg"
	source.Kind = "svg"
	source.Type = "svg"
	source.Status = "live"

	r2 := storage.NewR2ClientFromEnv()
	if r2.IsConfigured() {
		r2URL, err := r2.Upload(ctx, svgName, []byte(svg), "image/svg+xml")
		if err == nil && r2URL != "" {
			source.URL = r2URL
			source.Value = r2URL
			return toolResult{Source: source, Context: "Generated custom SVG image artifact: " + source.URL}
		}
		log.Warn().Err(err).Msg("R2 upload failed for AI SVG, falling back to local file")
	}

	svgPath := filepath.Join("uploads", svgName)
	if err := os.WriteFile(svgPath, []byte(svg), 0644); err != nil {
		source.Status = "failed"
		return toolResult{Source: source}
	}

	source.URL = "/uploads/" + filepath.Base(svgPath)
	source.Value = source.URL
	return toolResult{Source: source, Context: "Generated custom SVG image artifact: " + source.URL}
}

func generateMarketStructureSVG(message string) toolResult {
	source := AIToolSource{Type: "svg", Label: "Generated higher high structure", Status: "requested", Kind: "svg"}
	if err := os.MkdirAll("uploads", 0755); err != nil {
		source.Status = "failed"
		return toolResult{Source: source}
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="960" height="540" viewBox="0 0 960 540">
<rect width="960" height="540" rx="18" fill="#0b1020"/>
<g stroke="#1f2a44" stroke-width="1">
  <path d="M80 90H900M80 170H900M80 250H900M80 330H900M80 410H900"/>
  <path d="M120 60V460M280 60V460M440 60V460M600 60V460M760 60V460"/>
</g>
<text x="80" y="48" fill="#f8fafc" font-family="Arial" font-size="30" font-weight="800">Higher High Market Structure</text>
<text x="80" y="500" fill="#94a3b8" font-family="Arial" font-size="16">Uptrend structure: each major high breaks above the previous high while pullbacks hold higher lows.</text>
<polyline points="105,390 210,260 310,330 430,190 555,275 710,115 830,190" fill="none" stroke="#38bdf8" stroke-width="8" stroke-linecap="round" stroke-linejoin="round"/>
<polyline points="105,390 210,260 310,330 430,190 555,275 710,115 830,190" fill="none" stroke="#0ea5e9" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>
<g font-family="Arial" font-size="18" font-weight="700">
  <circle cx="210" cy="260" r="9" fill="#f97316"/><text x="174" y="235" fill="#fed7aa">High</text>
  <circle cx="430" cy="190" r="9" fill="#22c55e"/><text x="375" y="165" fill="#bbf7d0">Higher High</text>
  <circle cx="710" cy="115" r="9" fill="#22c55e"/><text x="647" y="90" fill="#bbf7d0">Higher High</text>
  <circle cx="310" cy="330" r="9" fill="#f97316"/><text x="282" y="362" fill="#fed7aa">Low</text>
  <circle cx="555" cy="275" r="9" fill="#22c55e"/><text x="508" y="308" fill="#bbf7d0">Higher Low</text>
</g>
<path d="M130 430C280 390 520 270 780 115" fill="none" stroke="#22c55e" stroke-width="3" stroke-dasharray="10 10"/>
<path d="M780 115l-30 4 18 24z" fill="#22c55e"/>
</svg>`
	name := "ai-market-structure-" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".svg"
	path := filepath.Join("uploads", name)
	if os.WriteFile(path, []byte(svg), 0644) != nil {
		source.Status = "failed"
		return toolResult{Source: source}
	}

	source.Status = "live"
	source.URL = "/uploads/" + filepath.Base(path)
	source.Value = source.URL
	return toolResult{Source: source, Context: "Generated market structure SVG image: " + source.URL + ". Tell the user the visual is attached in Generated Files & Interactive Tools; do not inline SVG code."}
}

func generatePromptSVG(message string) toolResult {
	source := AIToolSource{Type: "svg", Label: "Generated prompt image", Status: "requested", Kind: "svg"}
	if err := os.MkdirAll("uploads", 0755); err != nil {
		source.Status = "failed"
		return toolResult{Source: source}
	}
	title := extractSVGTitle(message)
	subtitle := "Generated by JX Horizon AI Coach"
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="960" height="540" viewBox="0 0 960 540">
<defs>
  <linearGradient id="bg" x1="0" y1="0" x2="1" y2="1">
    <stop offset="0%%" stop-color="#020617"/>
    <stop offset="55%%" stop-color="#0f172a"/>
    <stop offset="100%%" stop-color="#064e3b"/>
  </linearGradient>
</defs>
<rect width="960" height="540" rx="28" fill="url(#bg)"/>
<rect x="54" y="54" width="852" height="432" rx="24" fill="rgba(15,23,42,0.72)" stroke="#22c55e" stroke-opacity="0.35"/>
<circle cx="806" cy="132" r="58" fill="#22c55e" fill-opacity="0.16"/>
<circle cx="156" cy="390" r="82" fill="#38bdf8" fill-opacity="0.12"/>
<text x="90" y="134" fill="#f8fafc" font-family="Arial, sans-serif" font-size="42" font-weight="800">%s</text>
<text x="90" y="176" fill="#94a3b8" font-family="Arial, sans-serif" font-size="18">%s</text>
<g transform="translate(90 235)">
  <rect width="250" height="120" rx="18" fill="#111827" stroke="#334155"/>
  <text x="24" y="44" fill="#22c55e" font-family="Arial" font-size="16" font-weight="700">Plan</text>
  <text x="24" y="78" fill="#e2e8f0" font-family="Arial" font-size="14">Define risk and invalidation</text>
  <rect x="300" width="250" height="120" rx="18" fill="#111827" stroke="#334155"/>
  <text x="324" y="44" fill="#38bdf8" font-family="Arial" font-size="16" font-weight="700">Execute</text>
  <text x="324" y="78" fill="#e2e8f0" font-family="Arial" font-size="14">Follow rules without emotion</text>
  <rect x="600" width="250" height="120" rx="18" fill="#111827" stroke="#334155"/>
  <text x="624" y="44" fill="#f59e0b" font-family="Arial" font-size="16" font-weight="700">Review</text>
  <text x="624" y="78" fill="#e2e8f0" font-family="Arial" font-size="14">Journal patterns and improve</text>
</g>
<text x="90" y="438" fill="#cbd5e1" font-family="Arial" font-size="16">%s</text>
</svg>`, escapeXML(title), escapeXML(subtitle), escapeXML(trimForSVG(message, 115)))
	name := "ai-svg-" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".svg"
	path := filepath.Join("uploads", name)
	if os.WriteFile(path, []byte(svg), 0644) != nil {
		source.Status = "failed"
		return toolResult{Source: source}
	}

	source.Status = "live"
	source.URL = "/uploads/" + filepath.Base(path)
	source.Value = source.URL
	return toolResult{Source: source, Context: "Generated custom SVG image artifact: " + source.URL}
}

func generateEquitySVG(trades []models.Trade) toolResult {
	source := AIToolSource{Type: "svg", Label: "Generated equity curve", Status: "requested", Kind: "svg"}
	if len(trades) == 0 {
		source.Status = "empty"
		return toolResult{Source: source, Context: "SVG tool: no trades available to plot."}
	}
	if err := os.MkdirAll("uploads", 0755); err != nil {
		source.Status = "failed"
		return toolResult{Source: source}
	}
	width, height := 720.0, 320.0
	var values []float64
	equity := 0.0
	minVal, maxVal := 0.0, 0.0
	values = append(values, equity)
	for _, t := range trades {
		if t.ProfitLoss != nil {
			equity += *t.ProfitLoss
		}
		values = append(values, equity)
		if equity < minVal {
			minVal = equity
		}
		if equity > maxVal {
			maxVal = equity
		}
	}
	span := maxVal - minVal
	if span == 0 {
		span = 1
	}
	var points []string
	for i, v := range values {
		x := 40 + (float64(i)/float64(len(values)-1))*(width-80)
		y := height - 40 - ((v-minVal)/span)*(height-80)
		points = append(points, fmt.Sprintf("%.1f,%.1f", x, y))
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="720" height="320" viewBox="0 0 720 320">
<rect width="720" height="320" rx="16" fill="#0f172a"/>
<text x="40" y="34" fill="#f8fafc" font-family="Arial" font-size="18" font-weight="700">JX Horizon Equity Curve</text>
<text x="40" y="58" fill="#94a3b8" font-family="Arial" font-size="12">Generated from recent logged trade P/L</text>
<line x1="40" y1="280" x2="680" y2="280" stroke="#334155"/>
<line x1="40" y1="40" x2="40" y2="280" stroke="#334155"/>
<polyline points="%s" fill="none" stroke="#22c55e" stroke-width="4" stroke-linejoin="round" stroke-linecap="round"/>
<text x="40" y="305" fill="#94a3b8" font-family="Arial" font-size="12">Min %.2f</text>
<text x="600" y="305" fill="#94a3b8" font-family="Arial" font-size="12">Max %.2f</text>
</svg>`, strings.Join(points, " "), minVal, maxVal)
	name := "ai-equity-" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".svg"
	path := filepath.Join("uploads", name)
	if os.WriteFile(path, []byte(svg), 0644) != nil {
		source.Status = "failed"
		return toolResult{Source: source}
	}

	source.Status = "live"
	source.URL = "/uploads/" + filepath.Base(path)
	source.Value = source.URL
	return toolResult{Source: source, Context: "Generated equity curve SVG image: " + source.URL}
}

func extractSVGTitle(message string) string {
	title := extractQuoted(message)
	if title == "" {
		title = "AI Coach Visual"
	}
	return trimForSVG(title, 42)
}

func extractQuoted(message string) string {
	re := regexp.MustCompile(`"([^"]+)"|'([^']+)'`)
	match := re.FindStringSubmatch(message)
	if len(match) > 1 {
		if match[1] != "" {
			return match[1]
		}
		if len(match) > 2 {
			return match[2]
		}
	}
	return ""
}

func trimForSVG(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func escapeXML(value string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(value))
	return b.String()
}

func generateTradePDF(content string, trades []models.Trade) toolResult {
	source := AIToolSource{Type: "pdf", Label: "Generated trading report PDF", Status: "requested", Kind: "pdf"}
	if err := os.MkdirAll("uploads", 0755); err != nil {
		source.Status = "failed"
		return toolResult{Source: source}
	}
	text := content
	if text == "" {
		text = "JX Horizon AI Trade Report\\n\\n"
		text += fmt.Sprintf("Trades reviewed: %d\\n", len(trades))
		if len(trades) > 0 {
			risk := buildRiskManagementTool(trades)
			text += strings.ReplaceAll(risk.Context, "\n", "\\n")
		}
	}
	pdf := minimalPDF(text)
	name := "ai-report-" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".pdf"
	path := filepath.Join("uploads", name)
	if os.WriteFile(path, []byte(pdf), 0644) != nil {
		source.Status = "failed"
		return toolResult{Source: source}
	}
	source.Status = "live"
	source.URL = "/uploads/" + name
	source.Value = source.URL
	return toolResult{Source: source, Context: "Generated PDF trading report: " + source.URL}
}

func minimalPDF(text string) string {
	escaped := strings.ReplaceAll(text, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "(", "\\(")
	escaped = strings.ReplaceAll(escaped, ")", "\\)")
	escaped = strings.ReplaceAll(escaped, "\r\n", "\n")
	escaped = strings.ReplaceAll(escaped, "\\n", "\n")
	lines := strings.Split(escaped, "\n")
	var content strings.Builder
	content.WriteString("BT /F1 12 Tf 50 760 Td ")
	for i, line := range lines {
		if i > 0 {
			content.WriteString("0 -18 Td ")
		}
		content.WriteString("(" + line + ") Tj ")
	}
	content.WriteString("ET")
	stream := content.String()
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >> >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	}
	var out strings.Builder
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		out.WriteString(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, obj))
	}
	xrefOffset := out.Len()
	out.WriteString(fmt.Sprintf("xref\n0 %d\n", len(objects)+1))
	out.WriteString("0000000000 65535 f \n")
	for i := 1; i < len(offsets); i++ {
		out.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}
	out.WriteString(fmt.Sprintf("trailer << /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF", len(objects)+1, xrefOffset))
	return out.String()
}

func buildSafeScriptToolNotice() toolResult {
	return toolResult{
		Source:  AIToolSource{Type: "script", Label: "Offline script analysis sandbox", Status: "guarded", Kind: "sandbox-plan", Value: "offline, timed, memory-limited"},
		Context: "Script tool observation: execution is guarded. Target design is an offline sandbox with staged input data only, no internet after startup, time limit, memory/storage cap, and automatic abort on timeout. The AI may draft scripts/formulas now, but must not claim execution happened until the sandbox runner reports results.",
	}
}

func inferSearchQuery(message string) string {
	query := strings.TrimSpace(message)
	query = regexp.MustCompile(`(?i)\b(search|web|google|news|latest|headline|headlines|for|about|on)\b`).ReplaceAllString(query, " ")
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return "forex market trading"
	}
	if len(query) > 120 {
		query = query[:120]
	}
	return query
}

func extractURLs(message string) []string {
	re := regexp.MustCompile(`https?://[^\s<>"')]+`)
	matches := re.FindAllString(message, -1)
	seen := map[string]bool{}
	var urls []string
	for _, item := range matches {
		item = strings.TrimRight(item, ".,;:")
		if !seen[item] {
			seen[item] = true
			urls = append(urls, item)
		}
	}
	return urls
}

func fetchYahooNews(ctx context.Context, query string) toolResult {
	feedURL := "https://news.search.yahoo.com/rss?p=" + url.QueryEscape(query)
	source := AIToolSource{Type: "news", Label: "Yahoo News: " + query, URL: feedURL, Status: "requested"}
	body, ok := fetchURLBytes(ctx, feedURL, 1_000_000)
	if !ok {
		source.Status = "failed"
		return toolResult{Source: source}
	}
	var feed yahooRSS
	if xml.Unmarshal(body, &feed) != nil || len(feed.Items) == 0 {
		source.Status = "empty"
		return toolResult{Source: source}
	}
	source.Status = "live"
	var lines []string
	for i, item := range feed.Items {
		if i >= 5 {
			break
		}
		title := strings.TrimSpace(htmlstd.UnescapeString(item.Title))
		desc := cleanText(item.Description)
		if len(desc) > 220 {
			desc = desc[:220] + "..."
		}
		lines = append(lines, fmt.Sprintf("- %s\n  Source: %s\n  Summary: %s", title, item.Link, desc))
	}
	return toolResult{
		Source:  source,
		Context: "Yahoo News results for " + query + ":\n" + strings.Join(lines, "\n"),
	}
}

func fetchDuckDuckGo(ctx context.Context, query string) toolResult {
	searchURL := "https://duckduckgo.com/html/?q=" + url.QueryEscape(query)
	source := AIToolSource{Type: "web", Label: "DuckDuckGo: " + query, URL: searchURL, Status: "requested"}
	body, ok := fetchURLBytes(ctx, searchURL, 1_000_000)
	if !ok {
		source.Status = "failed"
		return toolResult{Source: source}
	}
	text := string(body)
	resultRE := regexp.MustCompile(`(?s)<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	snippetRE := regexp.MustCompile(`(?s)<a[^>]+class="result__snippet"[^>]*>(.*?)</a>|<div[^>]+class="result__snippet"[^>]*>(.*?)</div>`)
	results := resultRE.FindAllStringSubmatch(text, 5)
	snippets := snippetRE.FindAllStringSubmatch(text, 5)
	if len(results) == 0 {
		source.Status = "empty"
		return toolResult{Source: source}
	}
	source.Status = "live"
	var lines []string
	for i, result := range results {
		title := cleanText(result[2])
		link := decodeDuckDuckGoLink(htmlstd.UnescapeString(result[1]))
		snippet := ""
		if i < len(snippets) {
			if snippets[i][1] != "" {
				snippet = cleanText(snippets[i][1])
			} else {
				snippet = cleanText(snippets[i][2])
			}
		}
		lines = append(lines, fmt.Sprintf("- %s\n  Source: %s\n  Snippet: %s", title, link, snippet))
	}
	return toolResult{
		Source:  source,
		Context: "DuckDuckGo web results for " + query + ":\n" + strings.Join(lines, "\n"),
	}
}

func fetchSpecificURL(ctx context.Context, rawURL string) toolResult {
	source := AIToolSource{Type: "url", Label: "URL: " + rawURL, URL: rawURL, Status: "requested"}
	body, ok := fetchURLBytes(ctx, rawURL, 1_500_000)
	if !ok {
		source.Status = "failed"
		return toolResult{Source: source}
	}
	title, text := extractHTMLText(body)
	if text == "" {
		text = cleanText(string(body))
	}
	if len(text) > 2500 {
		text = text[:2500] + "..."
	}
	source.Status = "live"
	if title != "" {
		source.Label = "URL: " + title
	}
	return toolResult{
		Source:  source,
		Context: fmt.Sprintf("Fetched URL content:\nTitle: %s\nSource: %s\nExtracted text:\n%s", title, rawURL, text),
	}
}

func fetchURLBytes(ctx context.Context, rawURL string, limit int64) ([]byte, bool) {
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; JXHorizonAI/1.0)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	return body, err == nil
}

func extractHTMLText(body []byte) (string, string) {
	doc, err := xhtml.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", ""
	}
	var title string
	var parts []string
	var walk func(*xhtml.Node, bool)
	walk = func(n *xhtml.Node, skip bool) {
		if n.Type == xhtml.ElementNode {
			tag := strings.ToLower(n.Data)
			if tag == "script" || tag == "style" || tag == "noscript" || tag == "svg" {
				skip = true
			}
			if tag == "title" && n.FirstChild != nil {
				title = cleanText(n.FirstChild.Data)
			}
		}
		if !skip && n.Type == xhtml.TextNode {
			text := cleanText(n.Data)
			if len(text) > 1 {
				parts = append(parts, text)
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, skip)
		}
	}
	walk(doc, false)
	return title, strings.Join(parts, " ")
}

func cleanText(value string) string {
	value = htmlstd.UnescapeString(value)
	value = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(value, " ")
	value = strings.Join(strings.Fields(value), " ")
	return strings.TrimSpace(value)
}

func decodeDuckDuckGoLink(value string) string {
	parsed, err := url.Parse(value)
	if err == nil {
		if uddg := parsed.Query().Get("uddg"); uddg != "" {
			if decoded, err := url.QueryUnescape(uddg); err == nil {
				return decoded
			}
			return uddg
		}
	}
	return value
}

func extractLikelySymbol(message string) string {
	upper := strings.ToUpper(message)
	blocked := map[string]bool{
		"GENERATE": true,
		"PICTURE":  true,
		"DIAGRAM":  true,
		"VISUAL":   true,
		"HIGHER":   true,
		"LOWER":    true,
		"CREATE":   true,
		"REPORT":   true,
		"CURRENT":  true,
		"LATEST":   true,
	}
	common := map[string]string{
		"BITCOIN":  "BTC/USD",
		"BTC":      "BTC/USD",
		"ETHEREUM": "ETH/USD",
		"ETH":      "ETH/USD",
		"SOLANA":   "SOL/USD",
		"SOL":      "SOL/USD",
		"XRP":      "XRP/USD",
		"BNB":      "BNB/USD",
		"ADA":      "ADA/USD",
		"DOGE":     "DOGE/USD",
		"GOLD":     "XAU/USD",
		"XAU":      "XAU/USD",
	}
	for token, symbol := range common {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(token) + `\b`).MatchString(upper) {
			return symbol
		}
	}
	re := regexp.MustCompile(`(?i)\b([A-Z]{3,6}[/\-][A-Z]{3,6}|[A-Z]{6,12}|[A-Z]{2,6}USD)\b`)
	match := re.FindStringSubmatch(upper)
	if len(match) < 2 {
		return ""
	}
	raw := strings.ReplaceAll(match[1], "-", "/")
	if blocked[raw] {
		return ""
	}
	if !strings.Contains(raw, "/") && len(raw) == 6 {
		return raw[:3] + "/" + raw[3:]
	}
	if !strings.Contains(raw, "/") && len(raw) > 6 {
		return ""
	}
	return raw
}

func fetchMarketQuote(ctx context.Context, symbol string) (string, string, string, bool) {
	if quote, quoteURL, ok := fetchTwelveDataQuote(ctx, symbol); ok {
		return quote, "Twelve Data", quoteURL, true
	}
	if quote, quoteURL, ok := fetchYahooFinanceQuote(ctx, symbol); ok {
		return quote, "Yahoo Finance", quoteURL, true
	}
	return "", "", "", false
}

func fetchTwelveDataQuote(ctx context.Context, symbol string) (string, string, bool) {
	key := os.Getenv("TWELVE_DATA_API_KEY")
	if key == "" {
		return "", "", false
	}
	reqCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("https://api.twelvedata.com/quote?symbol=%s&apikey=%s", url.QueryEscape(symbol), url.QueryEscape(key))
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var parsed map[string]interface{}
	if json.Unmarshal(body, &parsed) != nil {
		return "", "", false
	}
	if price, ok := parsed["close"].(string); ok && price != "" {
		return price, endpoint, true
	}
	if price, ok := parsed["price"].(string); ok && price != "" {
		return price, endpoint, true
	}
	return "", "", false
}

func fetchYahooFinanceQuote(ctx context.Context, symbol string) (string, string, bool) {
	yahooSymbol := toYahooFinanceSymbol(symbol)
	if yahooSymbol == "" {
		return "", "", false
	}
	reqCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	endpoint := "https://query1.finance.yahoo.com/v7/finance/quote?symbols=" + url.QueryEscape(yahooSymbol)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; JXHorizonAI/1.0)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	var parsed struct {
		QuoteResponse struct {
			Result []struct {
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				Currency           string  `json:"currency"`
			} `json:"result"`
		} `json:"quoteResponse"`
	}
	if json.Unmarshal(body, &parsed) != nil || len(parsed.QuoteResponse.Result) == 0 {
		return "", "", false
	}
	price := parsed.QuoteResponse.Result[0].RegularMarketPrice
	if price <= 0 {
		return "", "", false
	}
	precision := 2
	if price < 10 {
		precision = 4
	}
	value := strconv.FormatFloat(price, 'f', precision, 64)
	if parsed.QuoteResponse.Result[0].Currency != "" {
		value += " " + parsed.QuoteResponse.Result[0].Currency
	}
	return value, endpoint, true
}

func toYahooFinanceSymbol(symbol string) string {
	normalized := strings.ToUpper(strings.ReplaceAll(symbol, "-", "/"))
	crypto := map[string]string{
		"BTC/USD":  "BTC-USD",
		"ETH/USD":  "ETH-USD",
		"SOL/USD":  "SOL-USD",
		"XRP/USD":  "XRP-USD",
		"BNB/USD":  "BNB-USD",
		"ADA/USD":  "ADA-USD",
		"DOGE/USD": "DOGE-USD",
	}
	if value, ok := crypto[normalized]; ok {
		return value
	}
	if normalized == "XAU/USD" {
		return "GC=F"
	}
	if strings.Contains(normalized, "/") {
		parts := strings.Split(normalized, "/")
		if len(parts) == 2 && len(parts[0]) == 3 && len(parts[1]) == 3 {
			return parts[0] + parts[1] + "=X"
		}
	}
	if len(normalized) == 6 {
		return normalized + "=X"
	}
	return normalized
}

// ─── GetAIResponse (Chat) ───────────────────────────────────────────────────

func (s *AIService) GetAIResponse(ctx context.Context, message string, history []models.AIChatMessage, memory *GlobalMemory, recentTrades []models.Trade, persona PersonaID) (*AIResponse, error) {
	if s.client == nil {
		return &AIResponse{Text: "AI service not configured."}, nil
	}

	// Build memory context
	memoryContext := "No specific rules provided."
	if memory != nil {
		strengths := strings.Join(memory.Strengths, ", ")
		weaknesses := strings.Join(memory.Weaknesses, ", ")
		rules := strings.Join(memory.Rules, ", ")
		profile := "Not defined yet"
		if memory.StrategyProfile != nil {
			profile = fmt.Sprintf("%s (%s): %s", memory.StrategyProfile.Name, memory.StrategyProfile.Type, memory.StrategyProfile.Description)
		}

		memoryContext = fmt.Sprintf("\nUSER'S TRADING PROFILE: %s\nUSER'S KNOWN STRENGTHS: %s\nUSER'S KNOWN WEAKNESSES: %s\nUSER'S TRADING RULES: %s\n",
			profile, strengths, weaknesses, rules)
	}

	// Build trade context using snapshots (pointer-safe)
	marketContext := "No recent trades available."
	if len(recentTrades) > 0 {
		var summaries []string
		for i := range recentTrades {
			snap := buildTradeSnapshot(&recentTrades[i])
			summaries = append(summaries, snapshotToSummary(snap))
		}
		marketContext = fmt.Sprintf("RECENT TRADES (Last %d):\n%s", len(recentTrades), strings.Join(summaries, "\n"))
	}

	// Log what we're sending to the AI for debugging
	log.Debug().Str("marketContext", marketContext).Msg("AI chat trade context")

	personaPrompt := getPersonaPrompt(persona)

	systemPrompt := fmt.Sprintf("%s\n\n### GLOBAL MEMORY CONTEXT:\n%s\n\n### RECENT MARKET CONTEXT:\n%s\n\nCRITICAL INSTRUCTIONS: Ground advice in probability. If you identify a new strength or weakness, use the memory update format: [MEMORY_UPDATE: {\"category\": \"weaknesses\", \"value\": \"...\"}]",
		personaPrompt, memoryContext, marketContext)

	chat := s.model.StartChat()
	chat.History = []*genai.Content{
		{Role: "user", Parts: []genai.Part{genai.Text("SYSTEM INSTRUCTIONS: " + systemPrompt)}},
		{Role: "model", Parts: []genai.Part{genai.Text("Understood. I am ready to coach with the " + persona.String() + " persona.")}},
	}

	for _, m := range history {
		role := "user"
		if m.Role == "model" || m.Role == "assistant" {
			role = "model"
		}
		chat.History = append(chat.History, &genai.Content{
			Role:  role,
			Parts: []genai.Part{genai.Text(m.Content)},
		})
	}

	resp, err := chat.SendMessage(ctx, genai.Text(message))
	if err != nil {
		log.Error().Err(err).Msg("Gemini chat.SendMessage failed")
		return nil, err
	}

	fullText := extractResponse(resp)

	// Parse memory update
	re := regexp.MustCompile(`\[MEMORY_UPDATE:\s*({[^\]]+})\s*\]`)
	match := re.FindStringSubmatch(fullText)
	var memoryUpdate *struct {
		Category string `json:"category"`
		Value    string `json:"value"`
	}

	if len(match) > 1 {
		err := json.Unmarshal([]byte(match[1]), &memoryUpdate)
		if err == nil {
			fullText = strings.TrimSpace(re.ReplaceAllString(fullText, ""))
		}
	}

	return &AIResponse{Text: fullText, MemoryUpdate: memoryUpdate}, nil
}

// ─── AnalyzeTrade ───────────────────────────────────────────────────────────

func (s *AIService) AnalyzeTrade(ctx context.Context, trade *models.Trade, memory *GlobalMemory, persona PersonaID) (string, error) {
	if s.client == nil {
		return "AI service not configured.", nil
	}

	personaPrompt := getPersonaPrompt(persona)

	// Build the trade context using the snapshot (100% pointer-safe)
	snap := buildTradeSnapshot(trade)
	tradeContext := snapshotToText(snap)

	// Log exactly what we're sending to the AI
	log.Info().Str("tradeContext", tradeContext).Str("persona", persona.String()).Msg("AnalyzeTrade: sending context to AI")

	prompt := fmt.Sprintf(`%s

Analyze the following trade execution. Focus on risk management, strategy compliance, and potential emotional factors. Be concise but impactful.

If the trade outcome is OPEN (not yet closed), analyze the setup quality, entry positioning relative to stop loss and take profit, and risk management instead of P/L results.

If P/L or R:R show "N/A", it means the trade is still open — do NOT say the data is missing or broken, simply note the trade is still active.

TRADE DATA:
%s`, personaPrompt, tradeContext)

	resp, err := generateContentWithRetry(ctx, s.model, genai.Text(prompt))
	if err != nil {
		return "", err
	}

	return extractResponse(resp), nil
}

// ─── GetPortfolioRecommendations ────────────────────────────────────────────

func (s *AIService) GetPortfolioRecommendations(ctx context.Context, trades []models.Trade, memory *GlobalMemory) (string, error) {
	if s.client == nil {
		return "AI service not configured.", nil
	}

	var sb strings.Builder
	for i := range trades {
		snap := buildTradeSnapshot(&trades[i])
		sb.WriteString(fmt.Sprintf("- %s %s (%s): Outcome=%s, P/L=%s, R:R=%s\n",
			snap.Direction, snap.Symbol, snap.SetupType, snap.Outcome, snap.ProfitLoss, snap.RR))
	}

	// Log for debugging
	log.Debug().Str("trades", sb.String()).Msg("GetPortfolioRecommendations: trade data")

	prompt := fmt.Sprintf("You are an analytical portfolio manager. Review these recent trades and provide 3 concrete, actionable pieces of advice for the trader based on their performance patterns.\n\n%s", sb.String())

	resp, err := generateContentWithRetry(ctx, s.model, genai.Text(prompt))
	if err != nil {
		return "", err
	}

	return extractResponse(resp), nil
}
