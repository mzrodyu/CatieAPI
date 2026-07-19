package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// This file implements the inbound Anthropic Messages API (POST /v1/messages).
// It lets Anthropic-native clients (such as Claude Code) talk to CatieAPI using
// their own protocol: the request is translated into the internal OpenAI chat
// flow, routed through the same channels, quota, and logging as every other
// gateway call, and the OpenAI chat.completion result is translated back into
// an Anthropic message (or an Anthropic SSE stream).

// anthropicMessagesRequest is the subset of the Anthropic Messages request body
// the gateway understands. Unknown fields are ignored so future client versions
// keep working for the common chat and tool-use paths.
type anthropicMessagesRequest struct {
	Model         string                    `json:"model"`
	MaxTokens     int                       `json:"max_tokens"`
	System        interface{}               `json:"system"`
	Messages      []anthropicInboundMessage `json:"messages"`
	Temperature   *float64                  `json:"temperature"`
	TopP          *float64                  `json:"top_p"`
	StopSequences []string                  `json:"stop_sequences"`
	Stream        bool                      `json:"stream"`
	Tools         []interface{}             `json:"tools"`
	ToolChoice    interface{}               `json:"tool_choice"`
}

type anthropicInboundMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// anthropicMessages handles POST /v1/messages. It mirrors the non-streaming
// chat orchestration in handleChatCompletionWithTransform, but authenticates,
// errors, and responds in Anthropic's shape. Streaming clients are served from
// a buffered upstream response re-emitted as Anthropic SSE events, which keeps
// the cross-protocol translation reliable without a second streaming codepath.
func (s *Server) anthropicMessages(c *gin.Context) {
	startedAt := time.Now()

	var req anthropicMessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "Invalid JSON body: "+err.Error())
		return
	}

	messages := anthropicMessagesToChatMessages(req)
	body := ChatRequest{
		Model:    req.Model,
		Stream:   req.Stream,
		Messages: messages,
		Payload:  anthropicPayloadFromRequest(req),
	}

	s.mu.Lock()
	auth := s.findUserByAPIKeyLocked(apiTokenFromRequest(c))
	if auth == nil {
		s.logGatewayFailureLocked(c, "invalid_api_key", "", "", body.Model, "")
		s.mu.Unlock()
		writeAnthropicError(c, http.StatusUnauthorized, "authentication_error", "Invalid CatieAPI key")
		return
	}
	if auth.User.Status == "limited" {
		s.logGatewayFailureLocked(c, "insufficient_quota", auth.User.ID, auth.Key.Prefix, body.Model, "")
		s.mu.Unlock()
		writeAnthropicError(c, http.StatusPaymentRequired, "invalid_request_error", "Insufficient quota")
		return
	}
	if !s.checkRateLimitLocked(auth.Key) {
		s.logGatewayFailureLocked(c, "rate_limit_exceeded", auth.User.ID, auth.Key.Prefix, body.Model, "")
		s.mu.Unlock()
		writeAnthropicError(c, http.StatusTooManyRequests, "rate_limit_error", "Rate limit exceeded")
		return
	}
	if len(messages) == 0 {
		s.logGatewayFailureLocked(c, "invalid_messages", auth.User.ID, auth.Key.Prefix, body.Model, "")
		s.mu.Unlock()
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "messages must contain at least one entry")
		return
	}

	model := s.resolveModelLocked(body.Model)
	if model == nil || model.Status != "available" {
		name := body.Model
		if strings.TrimSpace(name) == "" {
			name = "<model>"
		}
		s.logGatewayFailureLocked(c, "model_not_available", auth.User.ID, auth.Key.Prefix, body.Model, "")
		s.mu.Unlock()
		writeAnthropicError(c, http.StatusNotFound, "not_found_error", "No available model: "+name)
		return
	}
	if !apiKeyAllowsModel(auth.Key, model.ID) {
		s.logGatewayFailureLocked(c, "model_not_allowed", auth.User.ID, auth.Key.Prefix, model.ID, "")
		s.mu.Unlock()
		writeAnthropicError(c, http.StatusForbidden, "permission_error", "API key is not allowed to use model: "+model.ID)
		return
	}
	channels := s.channelCandidatesLocked(model.ID)
	if len(channels) == 0 {
		s.logGatewayFailureLocked(c, "model_not_available", auth.User.ID, auth.Key.Prefix, model.ID, "")
		s.mu.Unlock()
		writeAnthropicError(c, http.StatusServiceUnavailable, "api_error", "No available channel for model: "+model.ID)
		return
	}
	billingModel := modelWithChannelPricing(*model, channels[0])
	if billingModel.PricingConfigured && auth.User.Balance <= 0 {
		s.logGatewayFailureLocked(c, "insufficient_quota", auth.User.ID, auth.Key.Prefix, model.ID, channels[0].Name)
		s.mu.Unlock()
		writeAnthropicError(c, http.StatusPaymentRequired, "invalid_request_error", "Insufficient quota")
		return
	}

	authUserID := auth.User.ID
	authKeyID := auth.Key.ID
	authKeyPrefix := auth.Key.Prefix
	modelCopy := *model
	requestID := newID("req")
	s.mu.Unlock()

	// Always call the upstream without streaming; an Anthropic stream is then
	// re-emitted from the complete response when the client asked for one.
	nonStreamBody := body
	nonStreamBody.Stream = false

	var responseBody gin.H
	var providerErr *ProviderError
	var selectedChannel Channel
	attempts := 0
	for index, channel := range channels {
		call := GatewayCall{RequestID: requestID, Model: modelCopy, Channel: channel, Body: nonStreamBody}
		attempts++
		responseBody, providerErr = s.callProvider(call)
		if providerErr == nil {
			selectedChannel = channel
			s.updateChannelRuntimeHealth(channel.ID, true, "")
			break
		}
		selectedChannel = channel
		if shouldMarkChannelUnhealthy(providerErr) {
			s.updateChannelRuntimeHealth(channel.ID, false, providerErr.Message)
		}
		if !retryableProviderError(providerErr) || index == len(channels)-1 {
			break
		}
	}
	if providerErr != nil {
		s.recordFailedCall(authUserID, authKeyPrefix, modelCopy.ID, selectedChannel.Name, requestID, providerErr.Code, attempts, startedAt)
		writeAnthropicError(c, providerErr.Status, anthropicErrorType(providerErr), firstNonEmptyString(providerErr.Message, "Upstream request failed"))
		return
	}

	billingModel = modelWithChannelPricing(modelCopy, selectedChannel)
	inputTokens, outputTokens := callTokenUsage(responseBody, body.Messages, false)
	cost := calculateCallCost(billingModel, responseBody, body.Messages, false)
	s.recordSuccessfulCall(authUserID, authKeyID, authKeyPrefix, modelCopy.ID, selectedChannel.Name, requestID, cost, inputTokens, outputTokens, attempts, startedAt)

	message := anthropicMessageFromChatCompletion(responseBody, modelCopy.ID, requestID)
	if body.Stream {
		writeAnthropicMessageStream(c, message)
		return
	}
	c.JSON(http.StatusOK, message)
}

// logGatewayFailureLocked appends a failed request log entry without writing a
// response body, so the Anthropic handler can record the failure and then emit
// an Anthropic-shaped error. The caller must hold s.mu.
func (s *Server) logGatewayFailureLocked(c *gin.Context, code, userID, keyPrefix, modelID, channelName string) {
	log := RequestLog{
		ID:        requestIDFromContext(c),
		Status:    "failed",
		ErrorCode: code,
		CreatedAt: now(),
	}
	if userID != "" {
		log.UserID = &userID
	}
	if keyPrefix != "" {
		log.APIKeyPrefix = &keyPrefix
	}
	if modelID != "" {
		log.Model = &modelID
	}
	if channelName != "" {
		log.Channel = &channelName
	}
	s.state.Logs = append(s.state.Logs, log)
	s.saveStateLocked()
}

// anthropicMessagesToChatMessages flattens the Anthropic system prompt and
// message list into the internal OpenAI-style message slice.
func anthropicMessagesToChatMessages(req anthropicMessagesRequest) []ChatMessage {
	messages := []ChatMessage{}
	if system := strings.TrimSpace(anthropicSystemToText(req.System)); system != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: system})
	}
	for _, message := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "assistant" && role != "system" {
			role = "user"
		}
		messages = append(messages, ChatMessage{Role: role, Content: anthropicContentToChatContent(message.Content)})
	}
	return messages
}

// anthropicPayloadFromRequest maps the Anthropic sampling parameters and tool
// definitions onto the OpenAI-compatible request payload forwarded upstream.
func anthropicPayloadFromRequest(req anthropicMessagesRequest) map[string]interface{} {
	payload := map[string]interface{}{}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		payload["top_p"] = *req.TopP
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	if len(req.StopSequences) > 0 {
		payload["stop"] = req.StopSequences
	}
	if tools := anthropicToolsToOpenAI(req.Tools); len(tools) > 0 {
		payload["tools"] = tools
		if choice := anthropicToolChoiceToOpenAI(req.ToolChoice); choice != nil {
			payload["tool_choice"] = choice
		}
	}
	return payload
}

// anthropicSystemToText renders the system field, which may be a plain string
// or an array of text blocks, into a single string.
func anthropicSystemToText(system interface{}) string {
	switch typed := system.(type) {
	case string:
		return typed
	case []interface{}:
		parts := []string{}
		for _, raw := range typed {
			if block, ok := raw.(map[string]interface{}); ok {
				if strings.EqualFold(completionPrompt(block["type"]), "text") {
					parts = append(parts, completionPrompt(block["text"]))
				}
				continue
			}
			parts = append(parts, completionPrompt(raw))
		}
		return strings.Join(parts, "\n\n")
	default:
		return completionPrompt(system)
	}
}

// anthropicContentToChatContent converts one Anthropic message content value
// into the internal content. Text stays a string; when images are present the
// content becomes an OpenAI multimodal parts array. tool_use and tool_result
// blocks are rendered as text so multi-turn tool conversations stay coherent.
func anthropicContentToChatContent(content interface{}) interface{} {
	switch typed := content.(type) {
	case string:
		return typed
	case []interface{}:
		texts := []string{}
		parts := []interface{}{}
		hasImage := false
		appendText := func(text string) {
			if strings.TrimSpace(text) == "" {
				return
			}
			texts = append(texts, text)
			parts = append(parts, map[string]interface{}{"type": "text", "text": text})
		}
		for _, raw := range typed {
			block, ok := raw.(map[string]interface{})
			if !ok {
				appendText(completionPrompt(raw))
				continue
			}
			switch strings.ToLower(completionPrompt(block["type"])) {
			case "text":
				appendText(completionPrompt(block["text"]))
			case "image":
				if url := anthropicImageBlockURL(block); url != "" {
					hasImage = true
					parts = append(parts, map[string]interface{}{
						"type":      "image_url",
						"image_url": map[string]interface{}{"url": url},
					})
				}
			case "tool_use":
				input, _ := json.Marshal(block["input"])
				appendText(fmt.Sprintf("[tool_use %s %s]", completionPrompt(block["name"]), string(input)))
			case "tool_result":
				appendText("[tool_result] " + anthropicToolResultText(block["content"]))
			default:
				appendText(completionPrompt(block))
			}
		}
		if hasImage {
			return parts
		}
		return strings.Join(texts, "\n")
	default:
		return completionPrompt(content)
	}
}

// anthropicImageBlockURL turns an Anthropic image block into a URL the OpenAI
// multimodal content understands: base64 sources become data URLs.
func anthropicImageBlockURL(block map[string]interface{}) string {
	source, ok := block["source"].(map[string]interface{})
	if !ok {
		return ""
	}
	switch strings.ToLower(completionPrompt(source["type"])) {
	case "base64":
		mediaType := completionPrompt(source["media_type"])
		data := completionPrompt(source["data"])
		if mediaType != "" && data != "" {
			return "data:" + mediaType + ";base64," + data
		}
	case "url":
		return completionPrompt(source["url"])
	}
	return ""
}

// anthropicToolResultText extracts the textual payload from a tool_result block
// whose content may be a string or an array of blocks.
func anthropicToolResultText(content interface{}) string {
	switch typed := content.(type) {
	case string:
		return typed
	case []interface{}:
		parts := []string{}
		for _, raw := range typed {
			if block, ok := raw.(map[string]interface{}); ok {
				if strings.EqualFold(completionPrompt(block["type"]), "text") {
					parts = append(parts, completionPrompt(block["text"]))
					continue
				}
			}
			parts = append(parts, completionPrompt(raw))
		}
		return strings.Join(parts, "\n")
	default:
		return completionPrompt(content)
	}
}

// anthropicToolsToOpenAI converts Anthropic tool definitions into OpenAI
// function tools so upstream models can be offered the same tools.
func anthropicToolsToOpenAI(tools []interface{}) []interface{} {
	converted := []interface{}{}
	for _, raw := range tools {
		tool, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name := completionPrompt(tool["name"])
		if strings.TrimSpace(name) == "" {
			continue
		}
		parameters := tool["input_schema"]
		if parameters == nil {
			parameters = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		converted = append(converted, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        name,
				"description": completionPrompt(tool["description"]),
				"parameters":  parameters,
			},
		})
	}
	return converted
}

// anthropicToolChoiceToOpenAI maps Anthropic tool_choice onto the OpenAI form.
func anthropicToolChoiceToOpenAI(choice interface{}) interface{} {
	selection, ok := choice.(map[string]interface{})
	if !ok {
		return nil
	}
	switch strings.ToLower(completionPrompt(selection["type"])) {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "none":
		return "none"
	case "tool":
		if name := completionPrompt(selection["name"]); strings.TrimSpace(name) != "" {
			return map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": name}}
		}
		return "required"
	}
	return nil
}

// anthropicMessageFromChatCompletion converts an OpenAI chat.completion object
// into an Anthropic message response, including tool_use blocks derived from
// OpenAI tool_calls.
func anthropicMessageFromChatCompletion(chat gin.H, modelID string, requestID string) gin.H {
	normalized := reencodeToMap(chat)

	text := ""
	finishReason := ""
	var toolCalls []interface{}
	if choices, ok := normalized["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			finishReason = completionPrompt(choice["finish_reason"])
			if message, ok := choice["message"].(map[string]interface{}); ok {
				text = completionPrompt(message["content"])
				if calls, ok := message["tool_calls"].([]interface{}); ok {
					toolCalls = calls
				}
			}
		}
	}

	content := []interface{}{}
	if strings.TrimSpace(text) != "" {
		content = append(content, map[string]interface{}{"type": "text", "text": text})
	}
	for _, raw := range toolCalls {
		call, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		function, _ := call["function"].(map[string]interface{})
		var input interface{} = map[string]interface{}{}
		if arguments := completionPrompt(function["arguments"]); strings.TrimSpace(arguments) != "" {
			if json.Unmarshal([]byte(arguments), &input) != nil {
				input = map[string]interface{}{"value": arguments}
			}
		}
		content = append(content, map[string]interface{}{
			"type":  "tool_use",
			"id":    firstNonEmptyString(completionPrompt(call["id"]), newID("toolu")),
			"name":  completionPrompt(function["name"]),
			"input": input,
		})
	}
	if len(content) == 0 {
		content = append(content, map[string]interface{}{"type": "text", "text": ""})
	}

	stopReason := anthropicStopReason(finishReason)
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
	}

	inputTokens, outputTokens := 0, 0
	if usage, ok := normalized["usage"].(map[string]interface{}); ok {
		inputTokens, _ = asInt(usage["prompt_tokens"])
		outputTokens, _ = asInt(usage["completion_tokens"])
	}

	id := firstNonEmptyString(completionPrompt(normalized["id"]), requestID)
	if !strings.HasPrefix(id, "msg_") {
		id = "msg_" + id
	}

	return gin.H{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         modelID,
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         gin.H{"input_tokens": inputTokens, "output_tokens": outputTokens},
	}
}

// writeAnthropicMessageStream re-emits a completed Anthropic message as the SSE
// event sequence Anthropic clients expect. Text is chunked so clients still see
// incremental output even though the upstream call was non-streaming.
func writeAnthropicMessageStream(c *gin.Context, message gin.H) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	normalized := reencodeToMap(message)
	messageID := firstNonEmptyString(completionPrompt(normalized["id"]), newID("msg"))
	modelID := completionPrompt(normalized["model"])
	stopReason := firstNonEmptyString(completionPrompt(normalized["stop_reason"]), "end_turn")
	content, _ := normalized["content"].([]interface{})

	inputTokens, outputTokens := 0, 0
	if usage, ok := normalized["usage"].(map[string]interface{}); ok {
		inputTokens, _ = asInt(usage["input_tokens"])
		outputTokens, _ = asInt(usage["output_tokens"])
	}

	writeAnthropicSSE(c, "message_start", gin.H{
		"type": "message_start",
		"message": gin.H{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         modelID,
			"content":       []interface{}{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         gin.H{"input_tokens": inputTokens, "output_tokens": 0},
		},
	})
	writeAnthropicSSE(c, "ping", gin.H{"type": "ping"})

	for index, raw := range content {
		block, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		switch strings.ToLower(completionPrompt(block["type"])) {
		case "text":
			writeAnthropicSSE(c, "content_block_start", gin.H{
				"type":          "content_block_start",
				"index":         index,
				"content_block": gin.H{"type": "text", "text": ""},
			})
			text := completionPrompt(block["text"])
			if strings.TrimSpace(text) != "" {
				for _, chunk := range splitFakeStreamText(text) {
					writeAnthropicSSE(c, "content_block_delta", gin.H{
						"type":  "content_block_delta",
						"index": index,
						"delta": gin.H{"type": "text_delta", "text": chunk},
					})
				}
			}
			writeAnthropicSSE(c, "content_block_stop", gin.H{"type": "content_block_stop", "index": index})
		case "tool_use":
			writeAnthropicSSE(c, "content_block_start", gin.H{
				"type":  "content_block_start",
				"index": index,
				"content_block": gin.H{
					"type":  "tool_use",
					"id":    completionPrompt(block["id"]),
					"name":  completionPrompt(block["name"]),
					"input": gin.H{},
				},
			})
			inputJSON, _ := json.Marshal(block["input"])
			writeAnthropicSSE(c, "content_block_delta", gin.H{
				"type":  "content_block_delta",
				"index": index,
				"delta": gin.H{"type": "input_json_delta", "partial_json": string(inputJSON)},
			})
			writeAnthropicSSE(c, "content_block_stop", gin.H{"type": "content_block_stop", "index": index})
		}
	}

	writeAnthropicSSE(c, "message_delta", gin.H{
		"type":  "message_delta",
		"delta": gin.H{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": gin.H{"output_tokens": outputTokens},
	})
	writeAnthropicSSE(c, "message_stop", gin.H{"type": "message_stop"})
}

// writeAnthropicSSE writes one named Anthropic SSE event and flushes it.
func writeAnthropicSSE(c *gin.Context, event string, payload gin.H) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = c.Writer.WriteString("event: " + event + "\n")
	_, _ = c.Writer.WriteString("data: " + string(encoded) + "\n\n")
	c.Writer.Flush()
}

// writeAnthropicError renders an Anthropic-shaped error envelope.
func writeAnthropicError(c *gin.Context, status int, errorType, message string) {
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errorType, "message": message},
	})
}

// anthropicErrorType maps an upstream HTTP status onto an Anthropic error type.
func anthropicErrorType(providerErr *ProviderError) string {
	if providerErr == nil {
		return "api_error"
	}
	switch providerErr.Status {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	}
	if providerErr.Status >= 500 {
		return "api_error"
	}
	if providerErr.Status >= 400 {
		return "invalid_request_error"
	}
	return "api_error"
}

// anthropicStopReason maps an OpenAI finish_reason onto an Anthropic stop_reason.
func anthropicStopReason(finishReason string) string {
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	case "content_filter":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

// reencodeToMap normalizes a gin.H (which may embed gin.H / []gin.H produced by
// the mock provider) into a plain map with only map[string]interface{} and
// []interface{} nesting, so downstream type assertions behave consistently.
func reencodeToMap(value gin.H) map[string]interface{} {
	data, err := json.Marshal(value)
	if err != nil {
		return map[string]interface{}{}
	}
	var out map[string]interface{}
	if json.Unmarshal(data, &out) != nil {
		return map[string]interface{}{}
	}
	return out
}
