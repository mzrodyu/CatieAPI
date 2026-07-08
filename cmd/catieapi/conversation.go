package main

import (
	"bufio"
	"bytes"
	"crypto/sha3"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// This file implements a reverse proxy over the ChatGPT website's unofficial
// chat endpoint (/backend-api/conversation). Instead of the Codex responses
// endpoint, it speaks the same protocol the web app uses: it first asks the
// sentinel service for chat requirements (a token plus a proof-of-work seed),
// solves the proof-of-work, then posts the conversation and translates the
// streamed events into the OpenAI chat.completion shape.

const (
	chatGPTWebConversationPath  = "conversation"
	chatGPTWebRequirementsPath  = "sentinel/chat-requirements"
	chatGPTWebUserAgent         = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	chatGPTWebProofRetryBudget  = 500000
	chatGPTWebDefaultDifficulty = "000000"
)

// chatGPTWebRequirements is the response from the chat-requirements endpoint.
type chatGPTWebRequirements struct {
	Token         string `json:"token"`
	Persona       string `json:"persona"`
	Arkose        struct {
		Required bool   `json:"required"`
		DX       string `json:"dx"`
	} `json:"arkose"`
	ProofOfWork struct {
		Required   bool   `json:"required"`
		Seed       string `json:"seed"`
		Difficulty string `json:"difficulty"`
	} `json:"proofofwork"`
}

// chatGPTWebConfigCache caches the browser-fingerprint config list per process.
var chatGPTWebConfigCache = buildChatGPTWebConfigList()

// callChatGPTWebConversation performs a non-streaming conversation call and
// returns an OpenAI chat.completion object.
func (s *Server) callChatGPTWebConversation(call GatewayCall, account OpenAIAccount, accessToken string) (gin.H, *ProviderError) {
	response, providerErr := s.doChatGPTWebConversation(call, account, accessToken, false)
	if providerErr != nil {
		return nil, providerErr
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_read_error", Message: err.Error(), Type: "api_error"}
	}
	text, parseErr := parseChatGPTWebStream(bytes.NewReader(content), nil)
	if parseErr != nil {
		return nil, parseErr
	}
	return chatCompletionFromText(call, text), nil
}

// streamChatGPTWebConversation performs a streaming conversation call and
// relays incremental deltas as OpenAI chat.completion.chunk events.
func (s *Server) streamChatGPTWebConversation(c *gin.Context, call GatewayCall, account OpenAIAccount, accessToken string) *ProviderError {
	response, providerErr := s.doChatGPTWebConversation(call, account, accessToken, true)
	if providerErr != nil {
		return providerErr
	}
	defer response.Body.Close()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	chunkID := newID("chatcmpl")
	created := unixNow()
	sentRole := false

	_, parseErr := parseChatGPTWebStream(response.Body, func(delta string) {
		deltaPayload := gin.H{"content": delta}
		if !sentRole {
			deltaPayload["role"] = "assistant"
			sentRole = true
		}
		writeSSEChunk(c, gin.H{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   call.Model.ID,
			"choices": []gin.H{{"index": 0, "delta": deltaPayload, "finish_reason": nil}},
		})
		c.Writer.Flush()
	})
	if parseErr != nil {
		return parseErr
	}

	writeSSEChunk(c, gin.H{
		"id":      chunkID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   call.Model.ID,
		"choices": []gin.H{{"index": 0, "delta": gin.H{}, "finish_reason": "stop"}},
	})
	_, _ = c.Writer.WriteString("data: [DONE]\n\n")
	c.Writer.Flush()
	return nil
}

// doChatGPTWebConversation solves requirements + proof-of-work and posts the
// conversation, returning the live upstream response for the caller to read.
func (s *Server) doChatGPTWebConversation(call GatewayCall, account OpenAIAccount, accessToken string, stream bool) (*http.Response, *ProviderError) {
	config := chatGPTWebConfigCache.pick()
	deviceID := randomUUID()

	requirements, providerErr := s.fetchChatGPTWebRequirements(accessToken, account, config, deviceID)
	if providerErr != nil {
		return nil, providerErr
	}
	if requirements.Arkose.Required {
		return nil, &ProviderError{
			Status:  http.StatusBadGateway,
			Code:    "upstream_challenge_required",
			Message: "Upstream requires an additional challenge that is not available for this account",
			Type:    "api_error",
		}
	}

	payload, providerErr := buildChatGPTWebConversationPayload(call, stream)
	if providerErr != nil {
		return nil, providerErr
	}
	request, err := http.NewRequest(http.MethodPost, joinURL(s.chatGPTAPIBase, chatGPTWebConversationPath), bytes.NewReader(payload))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_request_error", Message: err.Error(), Type: "api_error"}
	}
	s.setChatGPTWebHeaders(request, accessToken, account, config, deviceID)
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	setHeaderPreserveCase(request.Header, "Openai-Sentinel-Chat-Requirements-Token", requirements.Token)
	if requirements.ProofOfWork.Required {
		proof := solveChatGPTWebProof(requirements.ProofOfWork.Seed, requirements.ProofOfWork.Difficulty, config)
		setHeaderPreserveCase(request.Header, "Openai-Sentinel-Proof-Token", proof)
	}

	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_unreachable", Message: err.Error(), Type: "api_error"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		content, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		return nil, providerErrorFromUpstream(response.StatusCode, content)
	}
	return response, nil
}

// fetchChatGPTWebRequirements requests a chat-requirements token + proof seed.
func (s *Server) fetchChatGPTWebRequirements(accessToken string, account OpenAIAccount, config chatGPTWebConfig, deviceID string) (*chatGPTWebRequirements, *ProviderError) {
	seed := gin.H{"p": generateChatGPTWebProofSeed(config)}
	body, _ := json.Marshal(seed)
	request, err := http.NewRequest(http.MethodPost, joinURL(s.chatGPTAPIBase, chatGPTWebRequirementsPath), bytes.NewReader(body))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_request_error", Message: err.Error(), Type: "api_error"}
	}
	s.setChatGPTWebHeaders(request, accessToken, account, config, deviceID)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_unreachable", Message: err.Error(), Type: "api_error"}
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_read_error", Message: err.Error(), Type: "api_error"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, providerErrorFromUpstream(response.StatusCode, content)
	}
	var requirements chatGPTWebRequirements
	if err := json.Unmarshal(content, &requirements); err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_invalid_json", Message: "Upstream returned invalid requirements JSON", Type: "api_error"}
	}
	if strings.TrimSpace(requirements.Token) == "" {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_no_requirements", Message: "Upstream returned no requirements token", Type: "api_error"}
	}
	return &requirements, nil
}

// setChatGPTWebHeaders applies the shared header set used by the web client.
func (s *Server) setChatGPTWebHeaders(request *http.Request, accessToken string, account OpenAIAccount, config chatGPTWebConfig, deviceID string) {
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	setHeaderPreserveCase(request.Header, "User-Agent", config.UserAgent)
	setHeaderPreserveCase(request.Header, "Oai-Device-Id", deviceID)
	setHeaderPreserveCase(request.Header, "Oai-Language", "en-US")
	setHeaderPreserveCase(request.Header, "Origin", "https://chatgpt.com")
	setHeaderPreserveCase(request.Header, "Referer", "https://chatgpt.com/")
	if accountID := strings.TrimSpace(account.AccountID); accountID != "" {
		setHeaderPreserveCase(request.Header, "Chatgpt-Account-Id", accountID)
	}
}

// buildChatGPTWebConversationPayload converts the OpenAI chat request into the
// web conversation body.
func buildChatGPTWebConversationPayload(call GatewayCall, stream bool) ([]byte, *ProviderError) {
	messages := make([]gin.H, 0, len(call.Body.Messages))
	for _, message := range call.Body.Messages {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			role = "user"
		}
		if role == "assistant" {
			role = "assistant"
		} else if role != "system" {
			role = "user"
		}
		messages = append(messages, gin.H{
			"id":     randomUUID(),
			"author": gin.H{"role": role},
			"content": gin.H{
				"content_type": "text",
				"parts":        []string{chatMessageContentToText(message.Content)},
			},
		})
	}
	payload := gin.H{
		"action":                       "next",
		"messages":                     messages,
		"parent_message_id":            randomUUID(),
		"model":                        chatGPTWebModelID(call.Model.ID),
		"timezone_offset_min":          0,
		"suggestions":                  []string{},
		"history_and_training_disabled": true,
		"conversation_mode":            gin.H{"kind": "primary_assistant"},
		"websocket_request_id":         randomUUID(),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Failed to encode upstream request", Type: "invalid_request_error"}
	}
	return encoded, nil
}

// chatGPTWebModelID maps a public model id to the web app's model slug.
func chatGPTWebModelID(modelID string) string {
	trimmed := strings.TrimSpace(strings.ToLower(modelID))
	switch {
	case trimmed == "":
		return "auto"
	case strings.HasPrefix(trimmed, "gpt-4o"):
		return "gpt-4o"
	case strings.HasPrefix(trimmed, "gpt-4"):
		return "gpt-4"
	case strings.HasPrefix(trimmed, "o1"), strings.HasPrefix(trimmed, "o3"):
		return trimmed
	default:
		return "auto"
	}
}

// parseChatGPTWebStream reads the conversation SSE stream. When onDelta is set
// each incremental text delta is emitted; the full assembled text is returned.
func parseChatGPTWebStream(reader io.Reader, onDelta func(delta string)) (string, *ProviderError) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)
	full := ""
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		text := chatGPTWebMessageText(event)
		if text == "" {
			continue
		}
		if len(text) >= len(full) && strings.HasPrefix(text, full) {
			delta := text[len(full):]
			full = text
			if delta != "" && onDelta != nil {
				onDelta(delta)
			}
		} else {
			full = text
			if onDelta != nil {
				onDelta(text)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return full, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_read_error", Message: err.Error(), Type: "api_error"}
	}
	return full, nil
}

// chatGPTWebMessageText extracts assistant text from one conversation event.
func chatGPTWebMessageText(event map[string]interface{}) string {
	message, ok := event["message"].(map[string]interface{})
	if !ok {
		return ""
	}
	if author, ok := message["author"].(map[string]interface{}); ok {
		if role, _ := author["role"].(string); role != "assistant" {
			return ""
		}
	}
	content, ok := message["content"].(map[string]interface{})
	if !ok {
		return ""
	}
	if contentType, _ := content["content_type"].(string); contentType != "text" && contentType != "" {
		return ""
	}
	parts, ok := content["parts"].([]interface{})
	if !ok {
		return ""
	}
	builder := strings.Builder{}
	for _, part := range parts {
		if text, ok := part.(string); ok {
			builder.WriteString(text)
		}
	}
	return builder.String()
}

// chatMessageContentToText flattens an OpenAI message content into plain text.
func chatMessageContentToText(content interface{}) string {
	switch value := content.(type) {
	case string:
		return value
	case []interface{}:
		builder := strings.Builder{}
		for _, item := range value {
			if part, ok := item.(map[string]interface{}); ok {
				if text, ok := part["text"].(string); ok {
					builder.WriteString(text)
				}
			}
		}
		return builder.String()
	default:
		return ""
	}
}

// chatCompletionFromText wraps assembled text into an OpenAI chat.completion.
func chatCompletionFromText(call GatewayCall, text string) gin.H {
	return gin.H{
		"id":      newID("chatcmpl"),
		"object":  "chat.completion",
		"created": unixNow(),
		"model":   call.Model.ID,
		"choices": []gin.H{{
			"index":         0,
			"message":       gin.H{"role": "assistant", "content": text},
			"finish_reason": "stop",
		}},
		"usage": gin.H{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	}
}

// --- proof-of-work ---------------------------------------------------------

// chatGPTWebConfig is a lightweight browser fingerprint used by the proof seed.
type chatGPTWebConfig struct {
	UserAgent string
	Core      int
	Screen    int
	Fields    []string
}

type chatGPTWebConfigList struct {
	items []chatGPTWebConfig
}

func (l chatGPTWebConfigList) pick() chatGPTWebConfig {
	if len(l.items) == 0 {
		return chatGPTWebConfig{UserAgent: chatGPTWebUserAgent, Core: 8, Screen: 3}
	}
	index := int(time.Now().UnixNano()) % len(l.items)
	if index < 0 {
		index = -index
	}
	return l.items[index]
}

func buildChatGPTWebConfigList() chatGPTWebConfigList {
	cores := []int{2, 4, 6, 8, 12, 16, 24}
	screens := []int{1, 2, 3, 4, 6}
	agents := []string{
		chatGPTWebUserAgent,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	}
	items := make([]chatGPTWebConfig, 0, len(cores)*len(screens)*len(agents))
	for _, agent := range agents {
		for _, core := range cores {
			for _, screen := range screens {
				items = append(items, chatGPTWebConfig{UserAgent: agent, Core: core, Screen: screen})
			}
		}
	}
	return chatGPTWebConfigList{items: items}
}

// generateChatGPTWebProofSeed builds the base64 seed the requirements call
// expects (a compact browser-environment fingerprint).
func generateChatGPTWebProofSeed(config chatGPTWebConfig) string {
	parts := chatGPTWebProofParts(config, "", 0)
	encoded, _ := json.Marshal(parts)
	return base64.StdEncoding.EncodeToString(encoded)
}

// chatGPTWebProofParts assembles the environment array used inside the proof.
func chatGPTWebProofParts(config chatGPTWebConfig, seed string, index int) []interface{} {
	now := time.Now().UTC()
	loadTime := float64(now.UnixNano()%100000) / 1000
	return []interface{}{
		config.Core + config.Screen,
		now.Format("Mon Jan 02 2006 15:04:05 GMT-0700 (Coordinated Universal Time)"),
		nil,
		loadTime,
		config.UserAgent,
		nil,
		nil,
		"en-US",
		"en-US,en",
		index,
		"_reactListening" + randomHex(4),
		"location",
		seed,
		float64(config.Core),
		now.UnixMilli(),
	}
}

// solveChatGPTWebProof finds a proof token whose hash meets the difficulty. If
// no answer is found within the retry budget it returns a safe fallback token.
func solveChatGPTWebProof(seed string, difficulty string, config chatGPTWebConfig) string {
	if strings.TrimSpace(difficulty) == "" {
		difficulty = chatGPTWebDefaultDifficulty
	}
	prefixLen := len(difficulty)
	parts := chatGPTWebProofParts(config, seed, 0)
	for attempt := 0; attempt < chatGPTWebProofRetryBudget; attempt++ {
		parts[3] = attempt
		parts[9] = attempt
		encoded, err := json.Marshal(parts)
		if err != nil {
			break
		}
		candidate := base64.StdEncoding.EncodeToString(encoded)
		sum := sha3.Sum512([]byte(seed + candidate))
		hex := fmt.Sprintf("%x", sum)
		if len(hex) >= prefixLen && hex[:prefixLen] <= difficulty {
			return "gAAAAAB" + candidate
		}
	}
	fallback := base64.StdEncoding.EncodeToString([]byte(`"` + seed + `"`))
	return "gAAAAABwQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D" + fallback
}

var _ = strconv.Itoa
