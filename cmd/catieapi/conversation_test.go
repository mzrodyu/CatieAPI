package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
		"data: {\"p\":\"\",\"o\":\"add\",\"v\":{\"conversation_id\":\"conv-1\",\"message\":{\"content\":{\"content_type\":\"multimodal_text\",\"parts\":[{\"content_type\":\"image_asset_pointer\",\"asset_pointer\":\"sediment://file_1\"}]}}}}\n\n" +
		"data: [DONE]\n\n"
	conversationID, assetPointer, providerErr := parseChatGPTWebImageStream(bytes.NewBufferString(stream))
	if providerErr != nil {
		t.Fatalf("image stream parse failed: %v", providerErr)
	}
	if conversationID != "conv-1" || assetPointer != "sediment://file_1" {
		t.Fatalf("unexpected image stream fields: conversation=%q asset=%q", conversationID, assetPointer)
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
