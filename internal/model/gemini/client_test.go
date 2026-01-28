package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yourusername/kaggen/pkg/protocol"
)

func TestClient_Generate(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check URL
		if r.URL.Path != "/models/gemini-3-pro-preview:generateContent" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("Mxissing or invalid API key")
		}

		// Return success response
		resp := apiResponse{
			Candidates: []apiCandidate{
				{
					Content: apiContent{
						Parts: []apiPart{
							{Text: "Hello from Gemini!"},
						},
					},
					FinishReason: "STOP",
				},
			},
			UsageMetadata: &apiUsageMetadata{
				TotalTokenCount: 10,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create client
	client := NewClient("test-key", "gemini-3-pro-preview")
	client.baseURL = server.URL + "/models" // Hack to override base URL for testing

	// Test Generate
	messages := []protocol.Message{
		{Role: "user", Content: "Hello"},
	}

	resp, err := client.Generate(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if resp.Content != "Hello from Gemini!" {
		t.Errorf("Unexpected content: %s", resp.Content)
	}
	if resp.StopReason != "STOP" {
		t.Errorf("Unexpected stop reason: %s", resp.StopReason)
	}
}
