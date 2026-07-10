package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// chatGPTWebConversationSSE builds a minimal assistant conversation event stream
// in the shape the web /backend-api/conversation endpoint emits.
func chatGPTWebConversationSSE(text string) string {
	event := map[string]interface{}{
		"message": map[string]interface{}{
			"author":  map[string]interface{}{"role": "assistant"},
			"content": map[string]interface{}{"content_type": "text", "parts": []string{text}},
		},
	}
	encoded, _ := json.Marshal(event)
	return "data: " + string(encoded) + "\n\ndata: [DONE]\n\n"
}

// webConversationUpstream is a test double for the ChatGPT web backend. It
// answers the sentinel chat-requirements handshake and the conversation POST,
// recording the model slug the gateway sent and optionally rejecting the first
// model to exercise the auto-downgrade path.
type webConversationUpstream struct {
	server        *httptest.Server
	seenModels    []string
	rejectModel   string
	requirements  chatGPTWebRequirements
	conversation  string
	arkoseGate    bool
	missingToken  bool
}

func newWebConversationUpstream(t *testing.T, reply string) *webConversationUpstream {
	t.Helper()
	up := &webConversationUpstream{conversation: reply}
	up.requirements = chatGPTWebRequirements{Token: "req-token"}
	up.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sentinel/chat-requirements"):
			if up.missingToken {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			payload := map[string]interface{}{"token": up.requirements.Token}
			if up.arkoseGate {
				payload["arkose"] = map[string]interface{}{"required": true}
			}
			encoded, _ := json.Marshal(payload)
			_, _ = w.Write(encoded)
		case strings.HasSuffix(r.URL.Path, "/conversation"):
			body, _ := readAllTestBody(r)
			model, _ := body["model"].(string)
			up.seenModels = append(up.seenModels, model)
			if up.rejectModel != "" && model == up.rejectModel {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"The requested model does not exist","type":"invalid_request_error"}}`))
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(chatGPTWebConversationSSE(up.conversation)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(up.server.Close)
	return up
}

func readAllTestBody(r *http.Request) (map[string]interface{}, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r.Body); err != nil {
		return nil, err
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// seedWebConversationChannel points chn_1001 at the web upstream, enables the
// web endpoint, and gives it one usable pool account.
func seedWebConversationChannel(server *Server, baseURL string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	channel := server.findChannel("chn_1001")
	channel.WebEndpoint = true
	channel.BaseURL = "https://api.openai.com/v1"
	channel.OpenAIAccounts = []OpenAIAccount{
		{ID: "oaiacc_web", Email: "web@example.com", AccessToken: "web-token", AccountID: "web-account", Status: "healthy"},
	}
}

func TestChatGPTWebConversationNonStreamRoutesToConversationEndpoint(t *testing.T) {
	upstream := newWebConversationUpstream(t, "hello from web")
	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.server.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	seedWebConversationChannel(server, upstream.server.URL)

	body := `{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`
	response := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("chat status = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"content":"hello from web"`)) {
		t.Fatalf("web conversation reply was not converted: %s", response.Body.String())
	}
	if len(upstream.seenModels) != 1 || upstream.seenModels[0] != "gpt-5" {
		t.Fatalf("web conversation model slug = %#v, want [gpt-5]", upstream.seenModels)
	}
}

func TestChatGPTWebConversationDowngradesToAutoOnModelUnavailable(t *testing.T) {
	upstream := newWebConversationUpstream(t, "hello after downgrade")
	upstream.rejectModel = "gpt-5"
	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.server.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	seedWebConversationChannel(server, upstream.server.URL)

	body := `{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`
	response := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("chat status = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"content":"hello after downgrade"`)) {
		t.Fatalf("web conversation did not recover via auto: %s", response.Body.String())
	}
	if len(upstream.seenModels) != 2 || upstream.seenModels[0] != "gpt-5" || upstream.seenModels[1] != "auto" {
		t.Fatalf("model downgrade sequence = %#v, want [gpt-5 auto]", upstream.seenModels)
	}
}

func TestChatGPTWebConversationStreamEmitsChunks(t *testing.T) {
	upstream := newWebConversationUpstream(t, "streamed web reply")
	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.server.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	seedWebConversationChannel(server, upstream.server.URL)

	body := `{"model":"gpt-5.5","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	response := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("stream status = %d body = %s", response.Code, response.Body.String())
	}
	raw := response.Body.String()
	if !strings.Contains(raw, `"chat.completion.chunk"`) || !strings.Contains(raw, `"role":"assistant"`) {
		t.Fatalf("stream missing chunk/role: %s", raw)
	}
	if !strings.Contains(raw, "streamed web reply") {
		t.Fatalf("stream missing content: %s", raw)
	}
	if !strings.Contains(raw, `"finish_reason":"stop"`) || !strings.Contains(raw, "data: [DONE]") {
		t.Fatalf("stream missing terminator: %s", raw)
	}
}

func TestChatGPTWebConversationArkoseReturnsChallengeRequired(t *testing.T) {
	upstream := newWebConversationUpstream(t, "unused")
	upstream.arkoseGate = true
	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.server.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	seedWebConversationChannel(server, upstream.server.URL)

	body := `{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`
	response := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code == http.StatusOK {
		t.Fatalf("arkose gated request unexpectedly succeeded: %s", response.Body.String())
	}
	if len(upstream.seenModels) != 0 {
		t.Fatalf("arkose gate should not send a conversation request, saw %#v", upstream.seenModels)
	}
	server.mu.Lock()
	account := server.findChannel("chn_1001").OpenAIAccounts[0]
	server.mu.Unlock()
	if account.Status == "invalid" {
		t.Fatal("arkose challenge must not invalidate the account")
	}
}

func TestChatGPTWebConversationMissingRequirementsToken(t *testing.T) {
	upstream := newWebConversationUpstream(t, "unused")
	upstream.missingToken = true
	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.server.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	seedWebConversationChannel(server, upstream.server.URL)

	body := `{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`
	response := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code == http.StatusOK {
		t.Fatalf("missing requirements token should fail, got: %s", response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("upstream_no_requirements")) {
		t.Fatalf("expected upstream_no_requirements, got: %s", response.Body.String())
	}
}

func TestChatGPTWebModelUnavailableErrorClassifies(t *testing.T) {
	positives := []*ProviderError{
		{Status: http.StatusNotFound, Code: "model_not_found", Message: "The model does not exist"},
		{Status: http.StatusBadRequest, Code: "upstream_error", Message: "unsupported model gpt-5"},
		{Status: http.StatusBadRequest, Code: "upstream_error", Message: "unknown model"},
		{Status: http.StatusNotFound, Code: "upstream_error", Message: "model route missing"},
	}
	for _, providerErr := range positives {
		if !chatGPTWebModelUnavailableError(providerErr) {
			t.Fatalf("expected model-unavailable classification for %#v", providerErr)
		}
	}
	negatives := []*ProviderError{
		nil,
		{Status: http.StatusTooManyRequests, Code: "usage_limit_reached", Message: "The usage limit has been reached"},
		{Status: http.StatusUnauthorized, Code: "upstream_token_invalidated", Message: "token invalidated"},
		{Status: http.StatusNotFound, Code: "upstream_error", Message: "conversation not found"},
	}
	for _, providerErr := range negatives {
		if chatGPTWebModelUnavailableError(providerErr) {
			t.Fatalf("unexpected model-unavailable classification for %#v", providerErr)
		}
	}
}
