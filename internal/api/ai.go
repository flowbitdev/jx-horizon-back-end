package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jx_api/internal/models"
	"jx_api/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type AIHandler struct {
	store     storage.IStorage
	aiService *AIService
}

type AIActionProposal struct {
	Type    string                 `json:"type"`
	Label   string                 `json:"label"`
	Method  string                 `json:"method"`
	URL     string                 `json:"url"`
	Payload map[string]interface{} `json:"payload"`
}

type AIWorkflowStep struct {
	Type    string `json:"type"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

type sseEvent struct {
	Type      string          `json:"type"`
	Step      *AIWorkflowStep `json:"step,omitempty"`
	TextChunk string          `json:"text,omitempty"`
	DoneData  interface{}     `json:"doneData,omitempty"`
}

func sendSSE(c *gin.Context, eventType string, step *AIWorkflowStep, text string, doneData interface{}) {
	payload, err := json.Marshal(sseEvent{
		Type:      eventType,
		Step:      step,
		TextChunk: text,
		DoneData:  doneData,
	})
	if err != nil {
		return
	}
	c.Writer.WriteString("data: " + string(payload) + "\n\n")
	c.Writer.Flush()
}

func NewAIHandler(store storage.IStorage, aiService *AIService) *AIHandler {
	return &AIHandler{store: store, aiService: aiService}
}

func (h *AIHandler) GetAIStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"configured": os.Getenv("GEMINI_API_KEY") != ""})
}

func (h *AIHandler) GetStrategyProfile(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	user, err := h.store.GetUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user"})
		return
	}

	hasProfile := false
	var profile interface{}
	if user.AIMemory != nil {
		if p, ok := user.AIMemory["strategyProfile"]; ok {
			hasProfile = true
			profile = p
		}
	}
	c.JSON(http.StatusOK, gin.H{"hasProfile": hasProfile, "profile": profile})
}

func (h *AIHandler) UpdateStrategyProfile(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	var req struct {
		StrategyProfile interface{} `json:"strategyProfile"`
		StrategyAnswers interface{} `json:"strategyAnswers"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := h.store.GetUser(c.Request.Context(), userID)
	if err != nil || user == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user"})
		return
	}

	memory := user.AIMemory
	if memory == nil {
		memory = make(map[string]interface{})
	}
	memory["strategyProfile"] = req.StrategyProfile
	memory["strategyAnswers"] = req.StrategyAnswers

	err = h.store.UpdateUser(c.Request.Context(), userID, map[string]interface{}{"ai_memory": memory})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update strategy profile"})
		return
	}

	updatedUser, _ := h.store.GetUser(c.Request.Context(), userID)
	c.JSON(http.StatusOK, updatedUser)
}

func (h *AIHandler) GetChatSessions(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	sessions, err := h.store.GetChatSessions(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch chat sessions"})
		return
	}
	c.JSON(http.StatusOK, sessions)
}

func (h *AIHandler) CreateChatSession(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	var req struct {
		Title string `json:"title"`
	}
	c.ShouldBindJSON(&req)

	if req.Title == "" {
		req.Title = "New Session"
	}

	session := &models.ChatSession{
		ID:     uuid.New(),
		UserID: userID,
		Title:  req.Title,
	}

	err := h.store.CreateChatSession(c.Request.Context(), session)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create session"})
		return
	}
	c.JSON(http.StatusCreated, session)
}

func (h *AIHandler) DeleteChatSession(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("sessionId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid session ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)

	err = h.store.DeleteChatSession(c.Request.Context(), sessionID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete session"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AIHandler) GetChatMessages(c *gin.Context) {
	sessionID, _ := uuid.Parse(c.Param("sessionId"))
	userID := c.MustGet("user_id").(uuid.UUID)

	messages, err := h.store.GetChatMessages(c.Request.Context(), sessionID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch messages"})
		return
	}
	c.JSON(http.StatusOK, messages)
}

func (h *AIHandler) SendMessage(c *gin.Context) {
	var req struct {
		SessionID           interface{}            `json:"sessionId"` // Mapped natively if provided
		Message             string                 `json:"message"`
		Persona             string                 `json:"persona"`
		Ghost               bool                   `json:"ghost"`
		Thinking            bool                   `json:"thinking"`
		Files               []string               `json:"files"`
		ConversationHistory []models.AIChatMessage `json:"conversationHistory"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		// Just parse what we can
	}

	userID := c.MustGet("user_id").(uuid.UUID)

	var targetSessionId uuid.UUID
	if strID, ok := req.SessionID.(string); ok && strID != "" {
		parsed, err := uuid.Parse(strID)
		if err == nil {
			targetSessionId = parsed
		}
	}

	if !req.Ghost && targetSessionId == uuid.Nil {
		safeTitle := req.Message
		if len(safeTitle) > 30 {
			safeTitle = safeTitle[:30] + "..."
		}
		if safeTitle == "" {
			safeTitle = "New Chat"
		}

		newSess := &models.ChatSession{ID: uuid.New(), UserID: userID, Title: safeTitle}
		h.store.CreateChatSession(c.Request.Context(), newSess)
		targetSessionId = newSess.ID
	}

	user, err := h.store.GetUser(c.Request.Context(), userID)
	if err != nil || user == nil {
		log.Error().Err(err).Str("userID", userID.String()).Msg("Failed to fetch user for AI message")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user context"})
		return
	}

	var memory GlobalMemory
	if user.AIMemory != nil {
		b, _ := json.Marshal(user.AIMemory)
		json.Unmarshal(b, &memory)
	}

	var history []models.AIChatMessage
	if req.Ghost {
		history = req.ConversationHistory
	} else {
		history, _ = h.store.GetChatMessages(c.Request.Context(), targetSessionId, userID)
	}

	allTrades, _ := h.store.GetTrades(c.Request.Context(), userID)
	trades := allTrades
	if len(trades) > 20 {
		trades = trades[:20]
	}

	actionProposal := buildActionProposal(req.Message)
	var toolSources []AIToolSource
	var workflow []AIWorkflowStep
	aiMessage := req.Message
	startedAt := time.Now()
	if len(req.Files) > 0 {
		aiMessage += "\n\nAttached image/file URLs:\n- " + strings.Join(req.Files, "\n- ")
	}

	var assistantMetadata map[string]interface{}
	if !req.Ghost {
		userMsg := models.AIChatMessage{
			UserID:    userID,
			SessionID: targetSessionId,
			Role:      "user",
			Content:   req.Message,
			Metadata:  map[string]interface{}{"thinking": req.Thinking, "files": req.Files},
		}
		h.store.CreateChatMessage(c.Request.Context(), &userMsg)

		pendingMsg := models.AIChatMessage{
			UserID:    userID,
			SessionID: targetSessionId,
			Role:      "assistant",
			Content:   "",
			Metadata: map[string]interface{}{
				"status":   "pending",
				"persona":  req.Persona,
				"thoughts": []interface{}{},
				"sources":  []interface{}{},
			},
			IsPending: true,
		}
		h.store.CreateChatMessage(c.Request.Context(), &pendingMsg)

		if len(history) == 0 {
			safeTitle := req.Message
			if len(safeTitle) > 35 {
				safeTitle = safeTitle[:35] + "..."
			}
			h.store.UpdateChatSessionTitle(c.Request.Context(), targetSessionId, userID, safeTitle)
		}
	}

	if len(trades) > 5 {
		trades = trades[:5]
	}

	isStream := strings.Contains(c.GetHeader("Accept"), "text/event-stream")
	if isStream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("Transfer-Encoding", "chunked")
		c.Writer.Flush()
	}

	resp, err := h.runModelToolLoop(c, aiMessage, history, &memory, trades, ParsePersonaID(req.Persona), req.Thinking, userID, allTrades, actionProposal, &workflow, &toolSources, isStream)
	if err != nil {
		log.Error().Err(err).Msg("AI response generation failed")
		if isStream {
			sendSSE(c, "error", nil, err.Error(), nil)
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "AI service failed", "details": err.Error()})
		}
		return
	}
	resp.Text = stripWorkflowSections(resp.Text)
	if resp.ActionProposal != nil {
		actionProposal = resp.ActionProposal
	}

	if !req.Ghost {
		assistantMetadata = map[string]interface{}{"sources": toolSources, "thinking": req.Thinking, "workflow": workflow, "actionProposal": actionProposal, "status": "done"}
		modelName := h.aiService.modelName
		latency := int(time.Since(startedAt).Milliseconds())
		if latency < 0 {
			latency = 0
		}
		h.store.UpdatePendingChatMessage(c.Request.Context(), targetSessionId, userID, resp.Text, assistantMetadata, &modelName, &latency)
	}

	if resp.MemoryUpdate != nil && resp.MemoryUpdate.Category != "" && resp.MemoryUpdate.Value != "" {
		if user.AIMemory == nil {
			user.AIMemory = make(map[string]interface{})
		}
		cat := resp.MemoryUpdate.Category
		val := resp.MemoryUpdate.Value

		arr, ok := user.AIMemory[cat].([]interface{})
		if !ok {
			arr = []interface{}{}
		}

		found := false
		for _, item := range arr {
			if strItem, ok := item.(string); ok && strItem == val {
				found = true
				break
			}
		}
		if !found {
			user.AIMemory[cat] = append(arr, val)
			h.store.UpdateUser(c.Request.Context(), userID, map[string]interface{}{"ai_memory": user.AIMemory})
		}
	}

	if isStream {
		sendSSE(c, "done", nil, "", gin.H{
			"response":  resp.Text,
			"sessionId": targetSessionId,
			"metadata":  gin.H{"sources": toolSources, "thinking": req.Thinking, "workflow": workflow, "actionProposal": actionProposal},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"response":  resp.Text,
		"sessionId": targetSessionId,
		"metadata":  map[string]interface{}{"sources": toolSources, "thinking": req.Thinking, "workflow": workflow, "actionProposal": actionProposal},
	})
}

func artifactResponseText(message string, sources []AIToolSource) string {
	var artifacts []string
	hasSVG := false
	hasPDF := false
	hasInteractive := false
	for _, source := range sources {
		if source.Status != "live" {
			continue
		}
		switch source.Kind {
		case "svg":
			hasSVG = true
			artifacts = append(artifacts, source.Label)
		case "pdf":
			hasPDF = true
			artifacts = append(artifacts, source.Label)
		case "interactive-risk", "sandbox-plan":
			hasInteractive = true
			artifacts = append(artifacts, source.Label)
		}
	}
	if len(artifacts) == 0 {
		return ""
	}
	lower := strings.ToLower(message)
	if hasSVG && (strings.Contains(lower, "image") || strings.Contains(lower, "svg") || strings.Contains(lower, "visual") || strings.Contains(lower, "diagram") || strings.Contains(lower, "chart")) {
		return "I generated the visual and attached it in Generated Files & Interactive Tools."
	}
	if hasPDF {
		return "I generated the PDF report and attached it in Generated Files & Interactive Tools."
	}
	if hasInteractive {
		return "I prepared the interactive tool below so you can adjust the values live."
	}
	return ""
}

type modelToolCall struct {
	Name  string
	Input string
}

func (h *AIHandler) runModelToolLoop(c *gin.Context, message string, history []models.AIChatMessage, memory *GlobalMemory, trades []models.Trade, persona PersonaID, thinking bool, userID uuid.UUID, allTrades []models.Trade, actionProposal *AIActionProposal, workflow *[]AIWorkflowStep, sources *[]AIToolSource, isStream bool) (*AIResponse, error) {
	if h.aiService == nil || h.aiService.client == nil {
		return nil, fmt.Errorf("AI service is not configured (GEMINI_API_KEY missing)")
	}

	ctx := c.Request.Context()
	finalOnlyInstruction := "Now respond to the user with ONLY the final answer. Do not explain your reasoning. Do not mention what tools you used. Just give the clean, helpful response."

	model := h.aiService.client.GenerativeModel(h.aiService.modelName)
	personaPrompt := getPersonaPrompt(persona)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(personaPrompt + "\n\n" + finalOnlyInstruction)},
	}
	model.Tools = h.aiService.model.Tools
	chat := model.StartChat()

	chat.History = []*genai.Content{}

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

	currentInput := []genai.Part{genai.Text(message)}

	var dynamicActionProposal *AIActionProposal
	metaRetryCount := 0

	for i := 0; i < 10; i++ {
		resp, err := sendGeminiChatWithRetry(ctx, chat, currentInput...)
		if err != nil {
			if isTransientGeminiError(err) {
				fallbackText := "I hit a temporary model error while processing that request. Try again in a moment, or send a smaller prompt."
				respondStep := AIWorkflowStep{Type: "respond", Label: "Final response", Status: "failed", Summary: "Gemini returned a transient upstream error; surfaced a safe fallback."}
				*workflow = append(*workflow, respondStep)
				if isStream {
					sendSSE(c, "workflow_step", &respondStep, "", nil)
					sendSSE(c, "text_chunk", nil, fallbackText, nil)
				}
				return &AIResponse{Text: fallbackText, ActionProposal: dynamicActionProposal}, nil
			}
			return nil, err
		}

		var functionCalls []genai.FunctionCall
		var responseText string

		for _, cand := range resp.Candidates {
			if cand.Content != nil {
				var lastText string
				for _, part := range cand.Content.Parts {
					if fn, ok := part.(genai.FunctionCall); ok {
						functionCalls = append(functionCalls, fn)
					} else if txt, ok := part.(genai.Text); ok {
						trimmed := strings.TrimSpace(string(txt))
						if trimmed != "" {
							lastText = trimmed
						}
					}
				}
				if lastText != "" {
					responseText = lastText
				}
			}
		}

		if len(functionCalls) == 0 {
			log.Debug().
				Str("response_raw", truncateForLog(responseText, 1200)).
				Int("response_raw_len", len(responseText)).
				Msg("AI final text candidate")

			cleanedText := normalizeAssistantText(responseText)
			log.Debug().
				Str("response_cleaned", truncateForLog(cleanedText, 1200)).
				Int("response_cleaned_len", len(cleanedText)).
				Bool("looks_like_leak", looksLikeReasoningLeak(cleanedText)).
				Msg("AI cleaned final text")

			if looksLikeReasoningLeak(cleanedText) {
				metaRetryCount++
				log.Debug().
					Int("meta_retry_count", metaRetryCount).
					Msg("AI response rejected for internal reasoning leakage")
				if metaRetryCount >= 3 {
					fallbackText := "I checked the available context and am ready to help with the trading task. Send the exact symbol, setup, or data slice you want me to work with."
					respondStep := AIWorkflowStep{Type: "respond", Label: "Final response", Status: "done", Summary: "Returned a safe fallback after repeated internal reasoning leakage."}
					*workflow = append(*workflow, respondStep)
					if isStream {
						sendSSE(c, "workflow_step", &respondStep, "", nil)
						sendSSE(c, "text_chunk", nil, fallbackText, nil)
					}
					return &AIResponse{Text: fallbackText, ActionProposal: dynamicActionProposal}, nil
				}
				currentInput = []genai.Part{genai.Text(finalOnlyInstruction + " The previous draft contained internal reasoning or prompt-injection language. Return only the direct answer with no thoughts, roles, tooling notes, or analysis.")}
				continue
			}

			re := regexp.MustCompile(`\[MEMORY_UPDATE:\s*({[^\]]+})\s*\]`)
			match := re.FindStringSubmatch(cleanedText)
			var memoryUpdate *struct {
				Category string `json:"category"`
				Value    string `json:"value"`
			}
			if len(match) > 1 {
				err := json.Unmarshal([]byte(match[1]), &memoryUpdate)
				if err == nil {
					cleanedText = strings.TrimSpace(re.ReplaceAllString(cleanedText, ""))
				}
			}

			respondStep := AIWorkflowStep{Type: "respond", Label: "Final response", Status: "done", Summary: "Composed the final response based on observations."}
			*workflow = append(*workflow, respondStep)
			if isStream {
				sendSSE(c, "workflow_step", &respondStep, "", nil)

				chunkSize := 32
				for idx := 0; idx < len(cleanedText); idx += chunkSize {
					end := idx + chunkSize
					if end > len(cleanedText) {
						end = len(cleanedText)
					}
					sendSSE(c, "text_chunk", nil, cleanedText[idx:end], nil)
					time.Sleep(15 * time.Millisecond)
				}
			}

			return &AIResponse{
				Text:           cleanedText,
				MemoryUpdate:   memoryUpdate,
				ActionProposal: dynamicActionProposal,
			}, nil
		}

		var responses []genai.Part
		for _, fn := range functionCalls {
			step := AIWorkflowStep{Type: "tool_call", Label: prettyToolTitle(fn.Name), Status: "running", Summary: fmt.Sprintf("Arguments: %v", fn.Args)}
			*workflow = append(*workflow, step)
			if isStream {
				sendSSE(c, "workflow_step", &step, "", nil)
			}

			result, source, err := h.executeNativeTool(c, fn, userID, allTrades, sources, &dynamicActionProposal)

			status := "done"
			var summary string
			if err != nil {
				status = "failed"
				summary = "Error: " + err.Error()
				result = map[string]interface{}{"error": err.Error()}
			} else {
				if source.Type != "" {
					summary = source.Label + " " + source.Value
				} else {
					summary = fmt.Sprintf("Result: %v", result)
				}
			}

			// Update client workflow step status
			if isStream {
				updatedStep := step
				updatedStep.Status = status
				updatedStep.Summary = summary
				sendSSE(c, "workflow_step", &updatedStep, "", nil)
			}
			*workflow = append(*workflow, AIWorkflowStep{Type: "observation", Label: "Result", Status: status, Summary: summary})

			responses = append(responses, genai.FunctionResponse{
				Name:     fn.Name,
				Response: result,
			})
		}
		currentInput = append([]genai.Part{genai.Text(finalOnlyInstruction)}, responses...)
	}

	return &AIResponse{Text: "Exceeded maximum tool loop turns."}, nil
}

func looksLikeReasoningLeak(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	blockers := []string{
		"the user is testing my capabilities",
		"prompt inject",
		"tool definitions",
		"as jx horizon ai coach",
		"i should stay in character",
		"i should",
		"i need to",
		"i will",
		"thinking step by step",
		"based on the data",
		"the model is deciding",
		"the user wants",
		"so i can",
		"to give you a concrete idea",
	}
	for _, blocker := range blockers {
		if strings.Contains(lower, blocker) {
			return true
		}
	}
	return strings.HasPrefix(lower, "yes, i have a full suite") || strings.HasPrefix(lower, "the user")
}

func truncateForLog(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

func normalizeAssistantText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	trimmed = stripWorkflowSections(trimmed)
	lower := strings.ToLower(trimmed)
	if idx := strings.LastIndex(lower, "response:"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[idx+len("response:"):])
	}
	trimmed = strings.TrimSpace(strings.Trim(trimmed, `"'`))
	if trimmed == "" {
		return ""
	}

	if strings.HasPrefix(trimmed, "\"") && strings.Count(trimmed, "\"") >= 2 {
		lastQuote := strings.LastIndex(trimmed, "\"")
		if lastQuote > 0 && lastQuote < len(trimmed)-1 {
			body := strings.TrimSpace(trimmed[1:lastQuote])
			tail := strings.TrimSpace(trimmed[lastQuote+1:])
			if body != "" && tail == body {
				return body
			}
		}
	}
	if strings.HasPrefix(trimmed, "'") && strings.Count(trimmed, "'") >= 2 {
		lastQuote := strings.LastIndex(trimmed, "'")
		if lastQuote > 0 && lastQuote < len(trimmed)-1 {
			body := strings.TrimSpace(trimmed[1:lastQuote])
			tail := strings.TrimSpace(trimmed[lastQuote+1:])
			if body != "" && tail == body {
				return body
			}
		}
	}

	paragraphs := strings.Split(trimmed, "\n\n")
	for i := len(paragraphs) - 1; i >= 0; i-- {
		para := strings.TrimSpace(paragraphs[i])
		if para == "" {
			continue
		}
		lowerPara := strings.ToLower(para)
		if strings.HasPrefix(lowerPara, "plan:") ||
			strings.HasPrefix(lowerPara, "thought:") ||
			strings.HasPrefix(lowerPara, "action:") ||
			strings.HasPrefix(lowerPara, "observation:") ||
			strings.HasPrefix(lowerPara, "response:") ||
			strings.HasPrefix(lowerPara, "the user said") ||
			strings.HasPrefix(lowerPara, "i am jx horizon") {
			continue
		}
		if strings.HasPrefix(para, "1.") || strings.HasPrefix(para, "2.") || strings.HasPrefix(para, "- ") || strings.HasPrefix(para, "* ") {
			continue
		}
		return para
	}
	return trimmed
}

func sendGeminiChatWithRetry(ctx context.Context, chat *genai.ChatSession, parts ...genai.Part) (*genai.GenerateContentResponse, error) {
	var lastErr error
	backoffs := []time.Duration{0, 250 * time.Millisecond, 750 * time.Millisecond}
	for attempt := 0; attempt < len(backoffs); attempt++ {
		if backoffs[attempt] > 0 {
			time.Sleep(backoffs[attempt])
		}
		resp, err := chat.SendMessage(ctx, parts...)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isTransientGeminiError(err) {
			return nil, err
		}
		log.Warn().Err(err).Int("attempt", attempt+1).Msg("Transient Gemini chat failure; retrying")
	}
	return nil, lastErr
}

func isTransientGeminiError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "error 500") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "tempor") ||
		strings.Contains(msg, "unavailable") ||
		strings.Contains(msg, "internal")
}

func (h *AIHandler) executeNativeTool(c *gin.Context, fn genai.FunctionCall, userID uuid.UUID, allTrades []models.Trade, sources *[]AIToolSource, dynamicActionProposal **AIActionProposal) (map[string]interface{}, AIToolSource, error) {
	ctx := c.Request.Context()
	var source AIToolSource
	var result map[string]interface{}
	var err error

	switch fn.Name {
	case "web_search":
		query, _ := fn.Args["query"].(string)
		res := fetchDuckDuckGo(ctx, query)
		*sources = append(*sources, res.Source)
		result = map[string]interface{}{"result": res.Context}
		source = res.Source

	case "news_search":
		query, _ := fn.Args["query"].(string)
		res := fetchYahooNews(ctx, query)
		*sources = append(*sources, res.Source)
		result = map[string]interface{}{"result": res.Context}
		source = res.Source

	case "price_check":
		symbol, _ := fn.Args["symbol"].(string)
		source = AIToolSource{Type: "price", Label: "Pair price check: " + symbol, Status: "requested"}
		if quote, provider, quoteURL, ok := fetchMarketQuote(ctx, symbol); ok {
			source.Status = "live"
			source.Value = quote
			source.URL = quoteURL
			*sources = append(*sources, source)
			result = map[string]interface{}{"price": quote, "provider": provider}
		} else {
			source.Status = "failed"
			source.Value = "No live quote returned"
			*sources = append(*sources, source)
			err = fmt.Errorf("price lookup failed for %s", symbol)
		}

	case "fetch_url":
		urlVal, _ := fn.Args["url"].(string)
		res := fetchSpecificURL(ctx, urlVal)
		*sources = append(*sources, res.Source)
		result = map[string]interface{}{"result": res.Context}
		source = res.Source

	case "execute_javascript":
		code, _ := fn.Args["code"].(string)
		source = AIToolSource{Type: "script", Label: "JavaScript sandbox execution", Status: "live", Kind: "sandbox-plan", Value: "Completed"}
		*sources = append(*sources, source)
		result, err = executeJavaScriptTool(code)

	case "generate_image":
		prompt, _ := fn.Args["prompt"].(string)
		svgCode, _ := fn.Args["svg_code"].(string)

		var res toolResult
		if svgCode != "" {
			res = h.aiService.generateSVGArtifact(ctx, svgCode, allTrades)
		} else {
			res = h.aiService.generateSVGArtifact(ctx, prompt, allTrades)
		}
		*sources = append(*sources, res.Source)
		result = map[string]interface{}{"url": res.Source.URL, "status": res.Source.Status}
		source = res.Source

	case "generate_pdf":
		content, _ := fn.Args["content"].(string)
		res := generateTradePDF(content, allTrades)
		*sources = append(*sources, res.Source)
		result = map[string]interface{}{"url": res.Source.URL, "status": res.Source.Status}
		source = res.Source

	case "read_user_db":
		table, _ := fn.Args["table"].(string)
		limitVal, _ := fn.Args["limit"].(float64)
		limit := int(limitVal)
		if limit <= 0 {
			limit = 10
		}

		context, dataSources := h.buildUserDataToolContext(c, table+" query", userID, allTrades)
		source = AIToolSource{Type: "data", Label: fmt.Sprintf("Read db: %s", table), Status: "live", Value: formatCount(len(dataSources)) + " tables"}
		*sources = append(*sources, dataSources...)
		result = map[string]interface{}{"context": context}

	case "write_user_db":
		action, _ := fn.Args["action"].(string)
		payloadJSON, _ := fn.Args["payload_json"].(string)

		var payload map[string]interface{}
		json.Unmarshal([]byte(payloadJSON), &payload)

		proposal := &AIActionProposal{
			Type:    action,
			Label:   strings.ReplaceAll(strings.Title(strings.ReplaceAll(action, "_", " ")), "Create", "Draft"),
			Method:  "POST",
			Payload: payload,
		}

		switch action {
		case "create_trade":
			proposal.URL = "/api/trades"
		case "create_goal":
			proposal.URL = "/api/goals"
		case "create_strategy":
			proposal.URL = "/api/strategies"
		case "create_journal":
			proposal.URL = "/api/journal"
		}

		source = AIToolSource{Type: "action", Label: "Proposed write action: " + action, Status: "waiting_confirmation"}
		*sources = append(*sources, source)
		result = map[string]interface{}{
			"action_proposal": proposal,
			"status":          "waiting_user_confirmation",
		}
		*dynamicActionProposal = proposal

	default:
		err = fmt.Errorf("unknown tool: %s", fn.Name)
	}

	return result, source, err
}

func executeJavaScriptTool(code string) (map[string]interface{}, error) {
	tmpDir := filepath.Join("uploads", "tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return nil, err
	}
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("script-%d.js", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, []byte(code), 0644); err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile)

	cmd := exec.Command("node", tmpFile)
	cmd.Dir = ".."
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := map[string]interface{}{
		"stdout": stdout.String(),
		"stderr": stderr.String(),
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result, nil
}

func parseAndStripThoughts(text string, workflow *[]AIWorkflowStep, isStream bool, c *gin.Context) string {
	return normalizeAssistantText(text)
}

func stripDirectiveBlocks(content string) string {
	content = regexp.MustCompile(`(?is)<thought>.*?</thought>`).ReplaceAllString(content, "")
	content = regexp.MustCompile(`(?is)thought\s*\{.*?\}`).ReplaceAllString(content, "")
	content = regexp.MustCompile(`(?is)tool[_\s-]*call\s*\{\s*[a-zA-Z_]+\s*\{.*?\}\s*\}`).ReplaceAllString(content, "")
	content = regexp.MustCompile(`(?is)observation\s*\{.*?\}`).ReplaceAllString(content, "")
	return strings.TrimSpace(content)
}

func stripWorkflowSections(content string) string {
	if content == "" {
		return ""
	}
	content = regexp.MustCompile(`(?is)`+"```svg\\s*<svg[\\s\\S]*?</svg>\\s*```").ReplaceAllString(content, "")
	content = regexp.MustCompile(`(?is)<svg[\\s\\S]*?</svg>`).ReplaceAllString(content, "")
	content = stripDirectiveBlocks(content)
	lines := strings.Split(content, "\n")
	cleaned := make([]string, 0, len(lines))
	skipFence := false
	fenceRE := regexp.MustCompile(`(?i)^\s*` + "```" + `(thought|reasoning|workflow|react|svg)?\s*$`)
	sectionRE := regexp.MustCompile(`(?i)^\s*(#{1,6}\s*)?(thought|action|observation|tool call|tool result|ai workflow|workflow|sources)\s*:?.*$`)
	for _, line := range lines {
		if fenceRE.MatchString(line) {
			skipFence = !skipFence
			continue
		}
		if skipFence {
			continue
		}
		if sectionRE.MatchString(line) {
			continue
		}
		cleaned = append(cleaned, line)
	}
	result := strings.TrimSpace(strings.Join(cleaned, "\n"))
	paragraphs := strings.Split(result, "\n\n")
	filtered := make([]string, 0, len(paragraphs))
	metaBlockers := []string{
		"the user is testing my capabilities",
		"prompt inject",
		"tool definitions",
		"as jx horizon ai coach",
		"i should stay in character",
		"i should",
		"i need to",
		"thinking step by step",
		"the model is deciding",
		"based on the data",
	}
	for _, para := range paragraphs {
		lower := strings.ToLower(strings.TrimSpace(para))
		if lower == "" {
			continue
		}
		skip := false
		for _, blocker := range metaBlockers {
			if strings.Contains(lower, blocker) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		filtered = append(filtered, para)
	}
	result = strings.TrimSpace(strings.Join(filtered, "\n\n"))
	blankRE := regexp.MustCompile(`\n{3,}`)
	return blankRE.ReplaceAllString(result, "\n\n")
}

func buildWorkflowSteps(thinking bool, sources []AIToolSource, action *AIActionProposal) []AIWorkflowStep {
	steps := []AIWorkflowStep{
		{Type: "thought", Label: "Thought", Status: "done", Summary: "Parsed the request, selected likely tools, and decided what evidence is needed before answering."},
	}
	if thinking {
		steps = append(steps,
			AIWorkflowStep{Type: "thought", Label: "Deep thought", Status: "done", Summary: "Ran a deeper checkpoint across objective, missing inputs, risk, alternatives, and next action."},
			AIWorkflowStep{Type: "thought", Label: "Risk thought", Status: "done", Summary: "Checked risk, invalidation, sizing, psychology, and process quality before using observations."},
		)
	}
	for _, source := range sources {
		summary := source.Label
		if source.Value != "" {
			summary += " -> " + source.Value
		}
		steps = append(steps,
			AIWorkflowStep{Type: "action", Label: "Action: " + source.Type, Status: "done", Summary: "Called the " + source.Type + " tool."},
			AIWorkflowStep{Type: "observation", Label: "Observation: " + source.Type, Status: source.Status, Summary: summary},
		)
	}
	if action != nil {
		steps = append(steps,
			AIWorkflowStep{Type: "action", Label: "Action: " + action.Type, Status: "waiting_confirmation", Summary: "Prepared a client-confirmed action proposal."},
			AIWorkflowStep{Type: "observation", Label: "Observation: confirmation required", Status: "waiting_confirmation", Summary: "Nothing is saved until the user reviews and confirms the proposed action."},
		)
	}
	steps = append(steps, AIWorkflowStep{Type: "respond", Label: "Final response", Status: "done", Summary: "Composed the final coach response from the observations and available user context."})
	return steps
}

func (h *AIHandler) buildUserDataToolContext(c *gin.Context, message string, userID uuid.UUID, trades []models.Trade) (string, []AIToolSource) {
	lower := strings.ToLower(message)
	wantsData := strings.Contains(lower, "sql") ||
		strings.Contains(lower, "query") ||
		strings.Contains(lower, "journal") ||
		strings.Contains(lower, "trade") ||
		strings.Contains(lower, "goal") ||
		strings.Contains(lower, "strategy") ||
		strings.Contains(lower, "playbook") ||
		strings.Contains(lower, "log")
	if !wantsData {
		return "", nil
	}

	var sources []AIToolSource
	var b strings.Builder
	b.WriteString("\n\n### USER DATA TOOL CONTEXT\n")
	b.WriteString("Read-only inspection of the authenticated user's journal database. Raw SQL is not executed from chat; this is a safe SQL-like data view.\n")

	if strings.Contains(lower, "trade") || strings.Contains(lower, "sql") || strings.Contains(lower, "query") {
		sources = append(sources, AIToolSource{Type: "data", Label: "Trades table", Status: "live", Value: formatCount(len(trades))})
		b.WriteString("\nRecent trades:\n")
		limit := len(trades)
		if limit > 10 {
			limit = 10
		}
		b.WriteString("Total trade rows available: " + formatCount(len(trades)) + ". Showing sample rows: " + formatCount(limit) + ". Use aggregate calculations instead of asking the model to inspect every row directly.\n")
		for i := 0; i < limit; i++ {
			t := trades[i]
			pl := "N/A"
			if t.ProfitLoss != nil {
				pl = formatFloat(*t.ProfitLoss)
			}
			rr := "N/A"
			if t.RR != nil {
				rr = formatFloat(*t.RR)
			}
			b.WriteString("- " + t.Date.Format("2006-01-02") + " " + t.Symbol + " " + t.Direction + " setup=" + t.SetupType + " P/L=" + pl + " RR=" + rr + "\n")
		}
	}

	if strings.Contains(lower, "journal") || strings.Contains(lower, "log") || strings.Contains(lower, "sql") || strings.Contains(lower, "query") {
		entries, err := h.store.GetJournalEntries(c.Request.Context(), userID)
		if err == nil {
			sources = append(sources, AIToolSource{Type: "data", Label: "Journal table", Status: "live", Value: formatCount(len(entries))})
			b.WriteString("\nRecent journal entries:\n")
			limit := len(entries)
			if limit > 5 {
				limit = 5
			}
			b.WriteString("Total journal rows available: " + formatCount(len(entries)) + ". Showing sample rows: " + formatCount(limit) + ". For large analysis, use counts, filters, and the guarded script-analysis workflow.\n")
			for i := 0; i < limit; i++ {
				e := entries[i]
				title := safePtr(e.Title, "Untitled")
				b.WriteString("- " + e.Date.Format("2006-01-02") + " " + title + " rating=" + formatIntPtr(e.Rating) + "\n")
			}
		}
	}

	if strings.Contains(lower, "goal") || strings.Contains(lower, "sql") || strings.Contains(lower, "query") {
		goals, err := h.store.GetGoalsRaw(c.Request.Context(), userID, false)
		if err == nil {
			sources = append(sources, AIToolSource{Type: "data", Label: "Goals table", Status: "live", Value: formatCount(len(goals))})
			b.WriteString("\nActive goals:\n")
			for _, g := range goals {
				b.WriteString("- " + g.Name + " metric=" + g.TargetMetric + " current=" + formatFloat(g.CurrentValue) + " target=" + formatFloat(g.TargetValue) + " status=" + g.Status + "\n")
			}
		}
	}

	if strings.Contains(lower, "strategy") || strings.Contains(lower, "playbook") || strings.Contains(lower, "sql") || strings.Contains(lower, "query") {
		strategies, err := h.store.GetStrategies(c.Request.Context(), userID)
		if err == nil {
			sources = append(sources, AIToolSource{Type: "data", Label: "Strategies table", Status: "live", Value: formatCount(len(strategies))})
			b.WriteString("\nStrategies:\n")
			for _, s := range strategies {
				b.WriteString("- " + s.Name + " active=" + formatBool(s.Active) + " description=" + safePtr(s.Description, "") + "\n")
			}
		}
	}

	return b.String(), sources
}

func buildActionProposal(message string) *AIActionProposal {
	lower := strings.ToLower(message)
	wantsCreate := strings.Contains(lower, "add") || strings.Contains(lower, "create") || strings.Contains(lower, "save") || strings.Contains(lower, "log")
	if !wantsCreate {
		return nil
	}

	now := time.Now()

	if strings.Contains(lower, "trade") {
		return &AIActionProposal{
			Type:   "create_trade",
			Label:  "Create trade",
			Method: "POST",
			URL:    "/api/trades",
			Payload: map[string]interface{}{
				"date":          now.Format(time.RFC3339),
				"symbol":        extractSymbolFromMessage(message, "UNKNOWN"),
				"assetClass":    "FOREX",
				"exchange":      "",
				"direction":     inferDirection(message),
				"entry":         0,
				"stopLoss":      0,
				"takeProfit":    0,
				"exitPrice":     0,
				"lotSize":       0,
				"riskPercent":   0,
				"riskAmount":    0,
				"profitLoss":    0,
				"rr":            0,
				"setupType":     "AI Draft",
				"session":       "",
				"notes":         message,
				"tags":          []string{"ai-draft"},
				"screenshotUrl": "",
				"isDemo":        false,
				"isBacktest":    false,
				"brokerTradeId": "",
				"aiAnalysis":    "",
				"rating":        5,
				"emotionBefore": 5,
				"emotionDuring": 5,
				"emotionAfter":  5,
				"outcome":       "break_even",
			},
		}
	}

	if strings.Contains(lower, "goal") {
		return &AIActionProposal{
			Type:   "create_goal",
			Label:  "Create goal",
			Method: "POST",
			URL:    "/api/goals",
			Payload: map[string]interface{}{
				"name":         extractQuotedOrFallback(message, "AI Coach Goal"),
				"targetMetric": inferGoalMetric(message),
				"targetValue":  1,
				"tradeMode":    inferTradeMode(message),
				"isDemo":       strings.Contains(lower, "demo"),
				"isBacktest":   strings.Contains(lower, "backtest"),
				"deadline":     now.AddDate(0, 1, 0).Format(time.RFC3339),
				"startDate":    now.Format(time.RFC3339),
				"category":     "ai-coach",
			},
		}
	}

	if strings.Contains(lower, "strategy") || strings.Contains(lower, "playbook") || strings.Contains(lower, "setup") {
		return &AIActionProposal{
			Type:   "create_strategy",
			Label:  "Create strategy",
			Method: "POST",
			URL:    "/api/strategies",
			Payload: map[string]interface{}{
				"name":        extractQuotedOrFallback(message, "AI Draft Strategy"),
				"description": message,
				"rules": map[string]interface{}{
					"entry":      []string{"Define entry rule before activating this strategy."},
					"exit":       []string{"Define stop and take-profit rule before activating this strategy."},
					"management": []string{"Risk per trade must remain within account rules."},
				},
				"active": true,
			},
		}
	}

	if strings.Contains(lower, "journal") || strings.Contains(lower, "log") {
		title := "AI Coach Journal Log"
		if strings.Contains(lower, "today") {
			title = "Today's AI Coach Log"
		}
		return &AIActionProposal{
			Type:   "create_journal_entry",
			Label:  "Create journal entry",
			Method: "POST",
			URL:    "/api/journal",
			Payload: map[string]interface{}{
				"title":            title,
				"date":             now.Format(time.RFC3339),
				"psychologyNotes":  message,
				"marketConditions": "",
				"mistakes":         "",
				"rating":           5,
			},
		}
	}

	return nil
}

func extractQuotedOrFallback(message string, fallback string) string {
	re := regexp.MustCompile(`"([^"]+)"|'([^']+)'`)
	match := re.FindStringSubmatch(message)
	if len(match) > 1 {
		if match[1] != "" {
			return match[1]
		}
		if len(match) > 2 && match[2] != "" {
			return match[2]
		}
	}
	return fallback
}

func extractSymbolFromMessage(message string, fallback string) string {
	re := regexp.MustCompile(`(?i)\b([A-Z]{3,6}[/\-]?[A-Z]{3,6}|[A-Z]{2,6}USD)\b`)
	match := re.FindStringSubmatch(strings.ToUpper(message))
	if len(match) > 1 {
		return strings.ReplaceAll(match[1], "-", "/")
	}
	return fallback
}

func inferDirection(message string) string {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "sell") || strings.Contains(lower, "short") {
		return "SELL"
	}
	return "BUY"
}

func inferTradeMode(message string) string {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "demo") {
		return "demo"
	}
	if strings.Contains(lower, "backtest") {
		return "backtest"
	}
	return "real"
}

func inferGoalMetric(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "win"):
		return "win_rate"
	case strings.Contains(lower, "trade count") || strings.Contains(lower, "number of trades"):
		return "trades_count"
	case strings.Contains(lower, "rr") || strings.Contains(lower, "risk reward"):
		return "risk_reward"
	case strings.Contains(lower, "profit factor"):
		return "profit_factor"
	case strings.Contains(lower, "streak"):
		return "consecutive_wins"
	default:
		return "profit"
	}
}

func safePtr(value *string, fallback string) string {
	if value == nil || *value == "" {
		return fallback
	}
	return *value
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func formatCount(value int) string {
	return strconv.Itoa(value)
}

func formatBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func formatIntPtr(value *int) string {
	if value == nil {
		return "N/A"
	}
	return strconv.Itoa(*value)
}

func prettyToolTitle(name string) string {
	switch name {
	case "web_search":
		return "Web search"
	case "news_search":
		return "News search"
	case "price_check":
		return "Price check"
	case "fetch_url":
		return "URL fetch"
	case "execute_javascript":
		return "Script execution"
	case "generate_image":
		return "Image generation"
	case "generate_pdf":
		return "PDF generation"
	case "read_user_db":
		return "User data"
	case "write_user_db":
		return "Draft action"
	default:
		return strings.ReplaceAll(strings.Title(strings.ReplaceAll(name, "_", " ")), "Db", "DB")
	}
}

func (h *AIHandler) AnalyzeTrade(c *gin.Context) {
	tradeID, err := uuid.Parse(c.Param("tradeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid trade ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)

	trade, err := h.store.GetTrade(c.Request.Context(), tradeID, userID)
	if err != nil || trade == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trade not found"})
		return
	}

	var req struct {
		Persona string `json:"persona"`
	}
	c.ShouldBindJSON(&req)
	persona := req.Persona
	if persona == "" {
		persona = "coach"
	}

	user, _ := h.store.GetUser(c.Request.Context(), userID)
	var memory GlobalMemory
	if user.AIMemory != nil {
		b, _ := json.Marshal(user.AIMemory)
		json.Unmarshal(b, &memory)
	}

	analysis, err := h.aiService.AnalyzeTrade(c.Request.Context(), trade, &memory, ParsePersonaID(persona))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to analyze trade"})
		return
	}

	h.store.UpdateTrade(c.Request.Context(), tradeID, userID, map[string]interface{}{"ai_analysis": analysis})
	c.JSON(http.StatusOK, gin.H{"analysis": analysis})
}

func (h *AIHandler) GetRecommendations(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	var req struct {
		Limit int `json:"limit"`
	}
	c.ShouldBindJSON(&req)
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}

	trades, err := h.store.GetTrades(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch trades"})
		return
	}

	var filtered []models.Trade
	for _, t := range trades {
		filtered = append(filtered, t)
		if len(filtered) >= limit {
			break
		}
	}

	user, _ := h.store.GetUser(c.Request.Context(), userID)
	var memory GlobalMemory
	if user.AIMemory != nil {
		b, _ := json.Marshal(user.AIMemory)
		json.Unmarshal(b, &memory)
	}

	recs, err := h.aiService.GetPortfolioRecommendations(c.Request.Context(), filtered, &memory)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate recommendations"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recommendations": recs})
}
