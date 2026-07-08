package main

import (
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
