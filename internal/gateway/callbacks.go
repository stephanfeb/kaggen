package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/yourusername/kaggen/internal/agent"
)

// CallbackHandler handles HTTP callbacks from external systems, correlating
// them with registered external tasks via the InFlightStore.
type CallbackHandler struct {
	store   *agent.InFlightStore
	handler *Handler
	logger  *slog.Logger
}

// NewCallbackHandler creates a new callback handler.
func NewCallbackHandler(store *agent.InFlightStore, handler *Handler, logger *slog.Logger) *CallbackHandler {
	return &CallbackHandler{
		store:   store,
		handler: handler,
		logger:  logger,
	}
}

// ServeHTTP handles POST /callbacks/{taskID} and GET /callbacks/{taskID}/status.
func (ch *CallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract taskID from path: /callbacks/{taskID}[/status]
	path := strings.TrimPrefix(r.URL.Path, "/callbacks/")
	if path == "" {
		http.Error(w, `{"error":"missing task_id"}`, http.StatusBadRequest)
		return
	}

	// Check for /status suffix.
	if strings.HasSuffix(path, "/status") {
		taskID := strings.TrimSuffix(path, "/status")
		ch.handleStatus(w, r, taskID)
		return
	}

	taskID := path
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	ch.handleCallback(w, r, taskID)
}

func (ch *CallbackHandler) handleCallback(w http.ResponseWriter, r *http.Request, taskID string) {
	state, ok := ch.store.Get(taskID)
	if !ok {
		ch.logger.Warn("callback for unknown task", "task_id", taskID)
		http.Error(w, `{"error":"unknown task"}`, http.StatusNotFound)
		return
	}
	if !state.External {
		ch.logger.Warn("callback for non-external task", "task_id", taskID)
		http.Error(w, `{"error":"task is not external"}`, http.StatusBadRequest)
		return
	}
	if state.Status != agent.TaskRunning {
		ch.logger.Warn("callback for non-running task", "task_id", taskID, "status", state.Status)
		http.Error(w, fmt.Sprintf(`{"error":"task already %s"}`, state.Status), http.StatusConflict)
		return
	}

	// Read body.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
		return
	}

	// Verify HMAC signature if secret is configured.
	if state.CallbackSecret != "" {
		sig := r.Header.Get("X-Callback-Signature")
		if sig == "" {
			ch.logger.Warn("callback missing signature", "task_id", taskID)
			http.Error(w, `{"error":"missing X-Callback-Signature header"}`, http.StatusUnauthorized)
			return
		}
		mac := hmac.New(sha256.New, []byte(state.CallbackSecret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(sig), []byte(expected)) {
			ch.logger.Warn("callback signature mismatch", "task_id", taskID)
			http.Error(w, `{"error":"invalid signature"}`, http.StatusForbidden)
			return
		}
	}

	// Parse payload to check for error indication.
	var payload struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)

	result := string(body)
	if payload.Status == "error" || payload.Status == "failed" {
		ch.store.Fail(taskID, payload.Error)
		ch.logger.Info("external task failed via callback", "task_id", taskID, "error", payload.Error)
	} else {
		ch.store.Complete(taskID, result)
		ch.logger.Info("external task completed via callback", "task_id", taskID, "result_len", len(result))
	}

	// Inject completion into the originating session.
	agentName := state.AgentName
	if agentName == "" {
		agentName = "external"
	}
	if err := ch.handler.InjectCompletion(
		r.Context(), state.SessionID, state.UserID, taskID, agentName, result,
	); err != nil {
		ch.logger.Warn("failed to inject callback completion", "task_id", taskID, "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"accepted","task_id":"%s"}`, taskID)
}

func (ch *CallbackHandler) handleStatus(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	state, ok := ch.store.Get(taskID)
	if !ok {
		http.Error(w, `{"error":"unknown task"}`, http.StatusNotFound)
		return
	}

	resp := map[string]any{
		"task_id":    state.ID,
		"status":     state.Status,
		"task":       state.Task,
		"started_at": state.StartedAt,
	}
	if state.DoneAt != nil {
		resp["done_at"] = state.DoneAt
	}
	if state.Error != "" {
		resp["error"] = state.Error
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
