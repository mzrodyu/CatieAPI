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
	chatGPTWebUserAgent         = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36"
	chatGPTWebProofRetryBudget  = 500000
	chatGPTWebDefaultDifficulty = "000032"
	chatGPTWebProofPrefix       = "gAAAAAB"
	chatGPTWebRequirementsPrefix = "gAAAAAC"
	chatGPTWebSentinelSDKURL    = "https://chatgpt.com/backend-api/sentinel/sdk.js"
	chatGPTWebTimeLayout        = "Mon Jan 02 2006 15:04:05"
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
	config := newChatGPTWebConfig()
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
		proof, _ := solveChatGPTWebProof(requirements.ProofOfWork.Seed, requirements.ProofOfWork.Difficulty, config)
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
	seed := gin.H{"p": generateChatGPTWebRequirementsToken(config)}
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
	userAgent, _ := config[4].(string)
	if userAgent == "" {
		userAgent = chatGPTWebUserAgent
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	setHeaderPreserveCase(request.Header, "User-Agent", userAgent)
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

// chatGPTWebConfig is the 18-element browser environment array the sentinel
// scripts expect. The order and semantics of the slots are fixed by the
// protocol; only a few are randomized per request.
type chatGPTWebConfig [18]interface{}

var chatGPTWebNavigatorKeys = []string{
	"webdriver−false",
	"vendor−Google Inc.",
	"cookieEnabled−true",
	"product−Gecko",
	"appCodeName−Mozilla",
	"appName−Netscape",
	"language−en-US",
	"onLine−true",
	"hardwareConcurrency−8",
	"pdfViewerEnabled−true",
	"clipboard−[object Clipboard]",
	"credentials−[object CredentialsContainer]",
	"geolocation−[object Geolocation]",
	"mediaDevices−[object MediaDevices]",
	"permissions−[object Permissions]",
	"serviceWorker−[object ServiceWorkerContainer]",
	"storage−[object StorageManager]",
	"userAgentData−[object NavigatorUAData]",
	"registerProtocolHandler−function registerProtocolHandler() { [native code] }",
	"sendBeacon−function sendBeacon() { [native code] }",
	"share−function share() { [native code] }",
	"vibrate−function vibrate() { [native code] }",
}

var chatGPTWebDocumentKeys = []string{
	"_reactListening0dgrl8ns7ku",
	"_reactListening0zqzkjpxi9",
	"_reactListening3q4xk1kx6q",
	"_reactListeningmqp6r8g4v9",
	"_reactListeningo743lnnpvdg",
	"location",
}

var chatGPTWebWindowKeys = []string{
	"window", "self", "document", "name", "location", "customElements",
	"history", "navigation", "navigator", "origin", "screen", "innerWidth",
	"innerHeight", "devicePixelRatio", "performance", "crypto", "indexedDB",
	"sessionStorage", "localStorage", "fetch", "chrome", "caches",
	"__NEXT_DATA__", "__NEXT_P", "webpackChunk_N_E", "next",
}

var chatGPTWebCores = []int{8, 16, 24, 32}
var chatGPTWebScreens = []int{1920 + 1080, 2560 + 1440, 1920 + 1200, 2560 + 1600}

// newChatGPTWebConfig assembles a fresh environment array per request.
func newChatGPTWebConfig() chatGPTWebConfig {
	now := time.Now().In(time.FixedZone("EST", -5*3600))
	perf := float64(time.Now().UnixNano()%int64(3600*1e9)) / 1e6
	unixMS := float64(time.Now().UnixNano()) / 1e6
	return chatGPTWebConfig{
		chatGPTWebScreens[randomIndex(len(chatGPTWebScreens))],
		now.Format(chatGPTWebTimeLayout) + " GMT-0500 (Eastern Standard Time)",
		4294705152,
		0, // slot 3: iterated during PoW
		chatGPTWebUserAgent,
		chatGPTWebSentinelSDKURL,
		"",
		"en-US",
		"en-US,en",
		0, // slot 9: i >> 1 during PoW
		chatGPTWebNavigatorKeys[randomIndex(len(chatGPTWebNavigatorKeys))],
		chatGPTWebDocumentKeys[randomIndex(len(chatGPTWebDocumentKeys))],
		chatGPTWebWindowKeys[randomIndex(len(chatGPTWebWindowKeys))],
		perf,
		randomUUID(),
		"",
		chatGPTWebCores[randomIndex(len(chatGPTWebCores))],
		unixMS - perf,
	}
}

// randomIndex returns a non-negative index below n.
func randomIndex(n int) int {
	if n <= 0 {
		return 0
	}
	seed, _ := strconv.ParseInt(randomHex(4), 16, 64)
	i := int(seed) % n
	if i < 0 {
		i = -i
	}
	return i
}

// generateChatGPTWebRequirementsToken produces the token sent inside the
// requirements POST body under the "p" field.
func generateChatGPTWebRequirementsToken(config chatGPTWebConfig) string {
	answer, _ := solveChatGPTWebProof(fmt.Sprintf("%f", nowFloatSeed()), "0fffff", config)
	return chatGPTWebRequirementsPrefix + answer[len(chatGPTWebProofPrefix):]
}

// nowFloatSeed emulates Python's format(random.random()) — a random float in
// [0, 1) rendered as a decimal string. Used only as an entropy source.
func nowFloatSeed() float64 {
	seed, _ := strconv.ParseUint(randomHex(7), 16, 64)
	return float64(seed) / float64(1<<28)
}

// solveChatGPTWebProof finds a proof token whose SHA3-512(seed || base64(config))
// starts below the difficulty. Returns the encoded token and whether a real
// solution was found; falls back to a decoy on exhaustion (upstream may reject).
func solveChatGPTWebProof(seed string, difficulty string, config chatGPTWebConfig) (string, bool) {
	if strings.TrimSpace(difficulty) == "" {
		difficulty = chatGPTWebDefaultDifficulty
	}
	target, err := decodeHexDifficulty(difficulty)
	if err != nil {
		return chatGPTWebProofFallback(seed), false
	}
	seedBytes := []byte(seed)
	for i := 0; i < chatGPTWebProofRetryBudget; i++ {
		config[3] = i
		config[9] = i >> 1
		encoded, err := json.Marshal(config)
		if err != nil {
			break
		}
		candidate := base64.StdEncoding.EncodeToString(encoded)
		buf := make([]byte, 0, len(seedBytes)+len(candidate))
		buf = append(buf, seedBytes...)
		buf = append(buf, candidate...)
		digest := sha3.Sum512(buf)
		if compareChatGPTWebDigest(digest[:len(target)], target) {
			return chatGPTWebProofPrefix + candidate, true
		}
	}
	return chatGPTWebProofFallback(seed), false
}

// decodeHexDifficulty turns a hex difficulty string ("000032") into bytes.
func decodeHexDifficulty(difficulty string) ([]byte, error) {
	trimmed := difficulty
	if len(trimmed)%2 == 1 {
		trimmed += "0"
	}
	buf := make([]byte, len(trimmed)/2)
	for i := 0; i < len(buf); i++ {
		high := hexNibble(trimmed[2*i])
		low := hexNibble(trimmed[2*i+1])
		if high < 0 || low < 0 {
			return nil, fmt.Errorf("invalid difficulty %q", difficulty)
		}
		buf[i] = byte(high<<4 | low)
	}
	return buf, nil
}

func hexNibble(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}

// compareChatGPTWebDigest returns true when a <= b lexicographically.
func compareChatGPTWebDigest(a, b []byte) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) <= len(b)
}

func chatGPTWebProofFallback(seed string) string {
	fallback := base64.StdEncoding.EncodeToString([]byte(`"` + seed + `"`))
	return chatGPTWebProofPrefix + "wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D" + fallback
}
