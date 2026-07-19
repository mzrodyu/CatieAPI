package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicMessagesNonStreamUsesChatFlow(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	body := `{"model":"ds","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`
	response := perform(router, http.MethodPost, "/v1/messages", body, map[string]string{"X-API-Key": "cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("anthropic messages status = %d body = %s", response.Code, response.Body.String())
	}

	var message struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &message); err != nil {
		t.Fatalf("decode anthropic message: %v", err)
	}
	if message.Type != "message" || message.Role != "assistant" {
		t.Fatalf("unexpected anthropic envelope: %#v", message)
	}
	if message.Model != "deepseek-v4" {
		t.Fatalf("alias was not resolved to deepseek-v4: %s", message.Model)
	}
	if len(message.Content) != 1 || message.Content[0].Type != "text" || strings.TrimSpace(message.Content[0].Text) == "" {
		t.Fatalf("unexpected anthropic content: %#v", message.Content)
	}
	if message.StopReason != "end_turn" {
		t.Fatalf("stop_reason = %s", message.StopReason)
	}
}

func TestAnthropicMessagesStreamEmitsAnthropicEvents(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	body := `{"model":"ds","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"hello"}]}`
	response := perform(router, http.MethodPost, "/v1/messages", body, map[string]string{"X-API-Key": "cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("anthropic stream status = %d body = %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("anthropic stream content-type = %s", contentType)
	}
	for _, want := range []string{
		"event: message_start",
		"event: content_block_start",
		"event: content_block_delta",
		`"type":"text_delta"`,
		"event: content_block_stop",
		"event: message_delta",
		"event: message_stop",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("anthropic stream missing %q: %s", want, response.Body.String())
		}
	}
}

func TestAnthropicMessagesWorkWithoutV1Prefix(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	body := `{"model":"ds","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`
	response := perform(router, http.MethodPost, "/messages", body, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("anthropic messages without v1 status = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"type":"message"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"model":"deepseek-v4"`)) {
		t.Fatalf("anthropic messages without v1 was not converted: %s", response.Body.String())
	}
}

func TestAnthropicMessagesForwardSystemToolsAndConvertToolUse(t *testing.T) {
	var upstreamPayload map[string]interface{}
	var upstreamPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_tool","object":"chat.completion","model":"deepseek-v4","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "compatible",
		"UPSTREAM_API_KEY": "upstream-secret",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.findChannel("chn_1002").BaseURL = upstream.URL + "/v1"
	server.mu.Unlock()

	body := `{
		"model":"ds",
		"max_tokens":256,
		"system":"Be brief",
		"messages":[{"role":"user","content":[{"type":"text","text":"weather?"}]}],
		"tools":[{"name":"get_weather","description":"Get weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}],
		"tool_choice":{"type":"auto"}
	}`
	response := perform(router, http.MethodPost, "/v1/messages", body, map[string]string{"X-API-Key": "cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("anthropic tool status = %d body = %s", response.Code, response.Body.String())
	}
	if upstreamPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %s", upstreamPath)
	}

	messages, ok := upstreamPayload["messages"].([]interface{})
	if !ok || len(messages) != 2 {
		t.Fatalf("system prompt was not forwarded as a message: %#v", upstreamPayload["messages"])
	}
	first, _ := messages[0].(map[string]interface{})
	if first["role"] != "system" || first["content"] != "Be brief" {
		t.Fatalf("system message = %#v", messages[0])
	}
	tools, ok := upstreamPayload["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("tools were not forwarded: %#v", upstreamPayload["tools"])
	}
	tool, _ := tools[0].(map[string]interface{})
	function, _ := tool["function"].(map[string]interface{})
	if tool["type"] != "function" || function["name"] != "get_weather" {
		t.Fatalf("tool was not converted to an OpenAI function: %#v", tools[0])
	}
	if upstreamPayload["tool_choice"] != "auto" {
		t.Fatalf("tool_choice was not converted: %#v", upstreamPayload["tool_choice"])
	}

	var message struct {
		Content []struct {
			Type  string                 `json:"type"`
			Name  string                 `json:"name"`
			Input map[string]interface{} `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &message); err != nil {
		t.Fatalf("decode anthropic tool message: %v", err)
	}
	if len(message.Content) != 1 || message.Content[0].Type != "tool_use" || message.Content[0].Name != "get_weather" {
		t.Fatalf("tool_calls were not converted to tool_use: %#v", message.Content)
	}
	if message.Content[0].Input["city"] != "SF" {
		t.Fatalf("tool_use input was not decoded: %#v", message.Content[0].Input)
	}
	if message.StopReason != "tool_use" {
		t.Fatalf("stop_reason = %s", message.StopReason)
	}
}

func TestAnthropicMessagesRejectInvalidKeyWithAnthropicError(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	body := `{"model":"ds","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`
	response := perform(router, http.MethodPost, "/v1/messages", body, map[string]string{"X-API-Key": "cat_wrong"})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid key status = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"type":"error"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"type":"authentication_error"`)) {
		t.Fatalf("invalid key did not return an anthropic error: %s", response.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	last := server.state.Logs[len(server.state.Logs)-1]
	if last.ErrorCode != "invalid_api_key" {
		t.Fatalf("invalid key log error = %#v", last)
	}
}
