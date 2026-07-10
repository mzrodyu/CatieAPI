package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestChatGPTWebProofSolves verifies the PoW loop actually finds a solution at
// a modest difficulty and that the returned token has the required prefix.
func TestChatGPTWebProofSolves(t *testing.T) {
	config := newChatGPTWebConfig()
	token, solved := solveChatGPTWebProof("test-seed", "0fff", config)
	if !solved {
		t.Fatalf("proof-of-work exhausted retry budget without finding a solution")
	}
	if !strings.HasPrefix(token, chatGPTWebProofPrefix) {
		t.Fatalf("token missing proof prefix: %s", token)
	}
}

// TestChatGPTWebRequirementsToken checks that requirements token generation
// produces the right prefix and encodes a valid base64 config array.
func TestChatGPTWebRequirementsToken(t *testing.T) {
	config := newChatGPTWebConfig()
	token := generateChatGPTWebRequirementsToken(config)
	if !strings.HasPrefix(token, chatGPTWebRequirementsPrefix) {
		t.Fatalf("requirements token missing prefix: %s", token)
	}
}

// TestChatGPTWebConfigShape asserts the config array keeps its 18 slots.
func TestChatGPTWebConfigShape(t *testing.T) {
	config := newChatGPTWebConfig()
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("config marshal failed: %v", err)
	}
	if len(encoded) < 100 {
		t.Fatalf("config unexpectedly small: %s", encoded)
	}
	if len(config) != 18 {
		t.Fatalf("expected 18 slots, got %d", len(config))
	}
}

func TestChatGPTWebImageStreamExtractsAssetPointer(t *testing.T) {
	stream := "data: \"v1\"\n\n" +
		"data: {\"p\":\"\",\"o\":\"add\",\"v\":{\"conversation_id\":\"conv-1\",\"message\":{\"author\":{\"role\":\"tool\"},\"content\":{\"content_type\":\"multimodal_text\",\"parts\":[{\"content_type\":\"image_asset_pointer\",\"asset_pointer\":\"sediment://file_1\"}]},\"metadata\":{\"async_task_type\":\"image_gen\"}}}}\n\n" +
		"data: [DONE]\n\n"
	conversationID, assetPointer, providerErr := parseChatGPTWebImageStream(bytes.NewBufferString(stream))
	if providerErr != nil {
		t.Fatalf("image stream parse failed: %v", providerErr)
	}
	if conversationID != "conv-1" || assetPointer != "sediment://file_1" {
		t.Fatalf("unexpected image stream fields: conversation=%q asset=%q", conversationID, assetPointer)
	}
}

func TestChatGPTWebImageStreamIgnoresInputAssetPointer(t *testing.T) {
	stream := "data: {\"v\":{\"conversation_id\":\"conv-edit\",\"message\":{\"author\":{\"role\":\"user\"},\"content\":{\"parts\":[{\"asset_pointer\":\"file-service://input-file\"}]}}}}\n\n" +
		"data: {\"v\":{\"message\":{\"author\":{\"role\":\"tool\"},\"content\":{\"parts\":[{\"asset_pointer\":\"sediment://output-file\"}]},\"metadata\":{\"async_task_type\":\"image_gen\"}}}}\n\n" +
		"data: [DONE]\n\n"
	conversationID, assetPointer, providerErr := parseChatGPTWebImageStream(bytes.NewBufferString(stream))
	if providerErr != nil {
		t.Fatalf("image edit stream parse failed: %v", providerErr)
	}
	if conversationID != "conv-edit" || assetPointer != "sediment://output-file" {
		t.Fatalf("input asset was not ignored: conversation=%q asset=%q", conversationID, assetPointer)
	}
}

func TestChatGPTWebImageStreamAcceptsFileServicePointer(t *testing.T) {
	stream := "data: {\"v\":{\"conversation_id\":\"conv-2\",\"message\":{\"author\":{\"role\":\"tool\"},\"content\":{\"parts\":[{\"asset_pointer\":\"file-service://file_2\"}]},\"metadata\":{\"async_task_type\":\"image_gen\"}}}}\n\n" +
		"data: [DONE]\n\n"
	conversationID, assetPointer, providerErr := parseChatGPTWebImageStream(bytes.NewBufferString(stream))
	if providerErr != nil {
		t.Fatalf("file-service image stream parse failed: %v", providerErr)
	}
	if conversationID != "conv-2" || assetPointer != "file-service://file_2" {
		t.Fatalf("unexpected file-service fields: conversation=%q asset=%q", conversationID, assetPointer)
	}
}

func TestChatGPTWebImageBytesValid(t *testing.T) {
	if !chatGPTWebImageBytesValid([]byte("\x89PNG\r\n\x1a\nrest")) {
		t.Fatal("PNG signature was not recognized")
	}
	if chatGPTWebImageBytesValid([]byte(`{"download_url":"https://chatgpt.com/file"}`)) {
		t.Fatal("download JSON was incorrectly accepted as image bytes")
	}
	if chatGPTWebImageBytesValid(nil) {
		t.Fatal("empty image was incorrectly accepted")
	}
}

// TestChatGPTWebModelIDMapsToFamily verifies model ids collapse to a web app
// family slug and that unknown or retired ids fall back to "auto".
func TestChatGPTWebModelIDMapsToFamily(t *testing.T) {
	cases := map[string]string{
		"":                    "auto",
		"gpt-5.6-sol":         "gpt-5",
		"gpt-5.6-terra":       "gpt-5",
		"gpt-5.5":             "gpt-5",
		"gpt-5.4":             "gpt-5",
		"gpt-5.4-mini":        "gpt-5",
		"GPT-5.6-Luna":        "gpt-5",
		"gpt-4o-mini":         "gpt-4o",
		"gpt-4.1":             "gpt-4-1",
		"gpt-4.1-mini":        "gpt-4-1",
		"gpt-4-turbo":         "gpt-4",
		"o1-preview":          "o1-preview",
		"o3-mini":             "o3-mini",
		"o4-mini":             "o4-mini",
		"claude-sonnet-4":     "auto",
		"some-unknown-model":  "auto",
	}
	for input, want := range cases {
		if got := chatGPTWebModelID(input); got != want {
			t.Fatalf("chatGPTWebModelID(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestBuildChatGPTWebConversationPayload verifies role normalization, the
// history/training flag, array-content text extraction, and model mapping.
func TestBuildChatGPTWebConversationPayload(t *testing.T) {
	call := GatewayCall{
		Model: Model{ID: "gpt-5.5"},
		Body: ChatRequest{
			Messages: []ChatMessage{
				{Role: "system", Content: "be brief"},
				{Role: "assistant", Content: "prior reply"},
				{Role: "tool", Content: "tool noise"},
				{Role: "user", Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "part one"},
					map[string]interface{}{"type": "text", "text": "part two"},
				}},
			},
		},
	}
	encoded, providerErr := buildChatGPTWebConversationPayload(call, false)
	if providerErr != nil {
		t.Fatalf("payload build failed: %#v", providerErr)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload["model"] != "gpt-5" {
		t.Fatalf("payload model = %v, want gpt-5", payload["model"])
	}
	if payload["history_and_training_disabled"] != true {
		t.Fatalf("history_and_training_disabled = %v, want true", payload["history_and_training_disabled"])
	}
	messages, ok := payload["messages"].([]interface{})
	if !ok || len(messages) != 4 {
		t.Fatalf("messages shape = %#v", payload["messages"])
	}
	roles := make([]string, 0, len(messages))
	for _, raw := range messages {
		message := raw.(map[string]interface{})
		author := message["author"].(map[string]interface{})
		roles = append(roles, author["role"].(string))
	}
	if roles[0] != "system" || roles[1] != "assistant" || roles[2] != "user" || roles[3] != "user" {
		t.Fatalf("role normalization = %#v, want [system assistant user user]", roles)
	}
	last := messages[3].(map[string]interface{})["content"].(map[string]interface{})
	parts := last["parts"].([]interface{})
	if len(parts) != 1 || parts[0].(string) != "part onepart two" {
		t.Fatalf("array content was not flattened: %#v", parts)
	}

	// modelOverride replaces the mapped slug for the auto-downgrade retry.
	overridden, providerErr := buildChatGPTWebConversationPayload(call, false, "auto")
	if providerErr != nil {
		t.Fatalf("override payload build failed: %#v", providerErr)
	}
	var overriddenPayload map[string]interface{}
	if err := json.Unmarshal(overridden, &overriddenPayload); err != nil {
		t.Fatalf("override payload not valid JSON: %v", err)
	}
	if overriddenPayload["model"] != "auto" {
		t.Fatalf("override model = %v, want auto", overriddenPayload["model"])
	}
}

// TestParseChatGPTWebStreamAccumulates checks incremental delta emission, that
// only assistant text is surfaced, and that the full text is returned.
func TestParseChatGPTWebStreamAccumulates(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"message":{"author":{"role":"assistant"},"content":{"content_type":"text","parts":["Hello"]}}}`,
		"",
		`data: {"message":{"author":{"role":"tool"},"content":{"content_type":"text","parts":["ignored tool output"]}}}`,
		"",
		`data: {"message":{"author":{"role":"assistant"},"content":{"content_type":"text","parts":["Hello world"]}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	deltas := []string{}
	full, providerErr := parseChatGPTWebStream(strings.NewReader(stream), func(delta string) {
		deltas = append(deltas, delta)
	})
	if providerErr != nil {
		t.Fatalf("stream parse failed: %#v", providerErr)
	}
	if full != "Hello world" {
		t.Fatalf("assembled text = %q, want %q", full, "Hello world")
	}
	if strings.Join(deltas, "|") != "Hello| world" {
		t.Fatalf("incremental deltas = %#v, want [Hello, \" world\"]", deltas)
	}
}

// TestChatCompletionFromText verifies the OpenAI chat.completion envelope.
func TestChatCompletionFromText(t *testing.T) {
	result := chatCompletionFromText(GatewayCall{Model: Model{ID: "gpt-5.5"}}, "final answer")
	if result["object"] != "chat.completion" {
		t.Fatalf("object = %v, want chat.completion", result["object"])
	}
	if result["model"] != "gpt-5.5" {
		t.Fatalf("model = %v, want gpt-5.5", result["model"])
	}
	choices, ok := result["choices"].([]gin.H)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices shape = %#v", result["choices"])
	}
	if choices[0]["finish_reason"] != "stop" {
		t.Fatalf("finish_reason = %v, want stop", choices[0]["finish_reason"])
	}
	message := choices[0]["message"].(gin.H)
	if message["role"] != "assistant" || message["content"] != "final answer" {
		t.Fatalf("message = %#v", message)
	}
}
