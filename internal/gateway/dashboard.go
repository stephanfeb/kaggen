package gateway

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/auth"
	"github.com/yourusername/kaggen/internal/backlog"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/p2p"
	"github.com/yourusername/kaggen/internal/secrets"
	kaggenSession "github.com/yourusername/kaggen/internal/session"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

//go:embed web/dashboard.html
var dashboardHTML []byte

// DashboardAPI serves the dashboard UI and REST API endpoints.
type DashboardAPI struct {
	agentProvider  *agent.AgentProvider
	backlogStore   *backlog.Store
	sessionService session.Service
	config         *config.Config
	logStreamer    *LogStreamer
	tokenStore     *auth.TokenStore
	dashboardAuth  *DashboardAuth // dashboard login/session management
	startTime      time.Time
	wsClientCount  func() int
	taskBroadcast  func(data []byte) // broadcasts to all WS clients
	handler        *Handler          // for approval InjectCompletion; set via SetHandler
	p2pNodeFunc    func() *p2p.Node  // returns P2P node if available
}

// NewDashboardAPI creates a new dashboard API.
func NewDashboardAPI(
	provider *agent.AgentProvider,
	store *backlog.Store,
	ss session.Service,
	cfg *config.Config,
	ls *LogStreamer,
	clientCount func() int,
) *DashboardAPI {
	// Initialize token store for settings UI
	tokenFile := cfg.Security.Auth.TokenFile
	if tokenFile == "" {
		tokenFile = config.ExpandPath("~/.kaggen/tokens.json")
	}
	tokenStore, _ := auth.NewTokenStore(tokenFile)

	return &DashboardAPI{
		agentProvider:  provider,
		backlogStore:   store,
		sessionService: ss,
		config:         cfg,
		logStreamer:    ls,
		tokenStore:     tokenStore,
		dashboardAuth:  NewDashboardAuth(secrets.DefaultStore()),
		startTime:      time.Now(),
		wsClientCount:  clientCount,
	}
}

// SetClientCountFunc sets the function to retrieve connected client count.
func (d *DashboardAPI) SetClientCountFunc(fn func() int) {
	d.wsClientCount = fn
}

// SetBroadcastFunc sets the function to broadcast data to all WS clients.
func (d *DashboardAPI) SetBroadcastFunc(fn func(data []byte)) {
	d.taskBroadcast = fn
}

// SetP2PNodeFunc sets the function to retrieve the P2P node.
func (d *DashboardAPI) SetP2PNodeFunc(fn func() *p2p.Node) {
	d.p2pNodeFunc = fn
}

// WireTaskEvents registers a callback on the InFlightStore that broadcasts
// task events to all WebSocket clients for real-time dashboard updates.
func (d *DashboardAPI) WireTaskEvents() {
	store := d.agentProvider.InFlightStore()
	store.SetEventCallback(func(taskID string, evt *agent.TaskEvent) {
		if d.taskBroadcast == nil {
			return
		}
		msg := map[string]any{
			"type":    "task_event",
			"task_id": taskID,
			"event":   evt,
		}
		data, err := json.Marshal(msg)
		if err != nil {
			return
		}
		d.taskBroadcast(data)
	})
}

func (d *DashboardAPI) clientCount() int {
	if d.wsClientCount == nil {
		return 0
	}
	return d.wsClientCount()
}

// ServeHTML serves the embedded dashboard HTML.
func (d *DashboardAPI) ServeHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}

// HandleOverview returns system overview information.
func (d *DashboardAPI) HandleOverview(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(d.startTime)

	// Count in-flight tasks and aggregate metrics
	store := d.agentProvider.InFlightStore()
	allTasks := store.List("")
	running, completed, failed, cancelled := 0, 0, 0, 0
	var totalTokens agent.TokenUsage
	for _, t := range allTasks {
		switch t.Status {
		case agent.TaskRunning:
			running++
		case agent.TaskCompleted:
			completed++
		case agent.TaskFailed:
			failed++
		case agent.TaskCancelled:
			cancelled++
		}
		if t.TotalTokens != nil {
			totalTokens.Input += t.TotalTokens.Input
			totalTokens.Output += t.TotalTokens.Output
			totalTokens.Total += t.TotalTokens.Total
		}
	}

	// Count backlog pending
	var backlogPending int
	if d.backlogStore != nil {
		items, err := d.backlogStore.List(backlog.Filter{Status: "pending", Limit: 500})
		if err == nil {
			backlogPending = len(items)
		}
	}

	// Count skills
	skillCount := len(d.agentProvider.SubAgents())

	// Count pending approvals
	pendingApprovals := len(store.List(agent.TaskPendingApproval))

	resp := map[string]any{
		"status":              "healthy",
		"uptime_seconds":      int64(uptime.Seconds()),
		"uptime":              formatDuration(uptime),
		"model":               d.config.Agent.Model,
		"connected_clients":   d.clientCount(),
		"inflight_tasks":      running,
		"total_tasks":         len(allTasks),
		"tasks_completed":     completed,
		"tasks_failed":        failed,
		"tasks_cancelled":     cancelled,
		"backlog_pending":     backlogPending,
		"pending_approvals":   pendingApprovals,
		"skills_loaded":       skillCount,
		"memory_enabled":      d.config.Memory.Search.Enabled,
		"telegram_enabled":    d.config.Channels.Telegram.Enabled,
		"total_tokens_input":  totalTokens.Input,
		"total_tokens_output": totalTokens.Output,
		"total_tokens":        totalTokens.Total,
	}
	writeJSON(w, resp)
}

// HandleTasks returns in-flight and completed async tasks.
func (d *DashboardAPI) HandleTasks(w http.ResponseWriter, r *http.Request) {
	statusFilter := agent.TaskStatus(r.URL.Query().Get("status"))
	tasks := d.agentProvider.InFlightStore().List(statusFilter)

	// Sort by start time descending
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].StartedAt.After(tasks[j].StartedAt)
	})

	writeJSON(w, tasks)
}

// HandleBacklog returns backlog items.
func (d *DashboardAPI) HandleBacklog(w http.ResponseWriter, r *http.Request) {
	if d.backlogStore == nil {
		writeJSON(w, []any{})
		return
	}

	f := backlog.Filter{
		Status:   r.URL.Query().Get("status"),
		Priority: r.URL.Query().Get("priority"),
		ParentID: r.URL.Query().Get("parent_id"),
		Limit:    200,
	}
	// Default to top-level items unless a parent_id filter or "all=true" is specified.
	if f.ParentID == "" && r.URL.Query().Get("all") != "true" {
		f.TopLevel = true
	}

	items, err := d.backlogStore.List(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []*backlog.Item{}
	}
	writeJSON(w, items)
}

// HandleSkills returns loaded skills information.
func (d *DashboardAPI) HandleSkills(w http.ResponseWriter, r *http.Request) {
	subAgents := d.agentProvider.SubAgents()

	type skillInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	skills := make([]skillInfo, 0, len(subAgents))
	for _, sa := range subAgents {
		info := sa.Info()
		skills = append(skills, skillInfo{
			Name:        info.Name,
			Description: info.Description,
		})
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})

	writeJSON(w, skills)
}

// HandleSessions returns session listing with metadata.
func (d *DashboardAPI) HandleSessions(w http.ResponseWriter, r *http.Request) {
	sessionsDir := d.config.SessionsPath()
	appDir := filepath.Join(sessionsDir, AppName)

	includeThreads := r.URL.Query().Get("include_threads") == "true"

	type sessionInfo struct {
		ID              string        `json:"id"`
		Name            string        `json:"name,omitempty"`
		UserID          string        `json:"user_id"`
		Channel         string        `json:"channel,omitempty"`
		CreatedAt       string        `json:"created_at,omitempty"`
		UpdatedAt       string        `json:"updated_at"`
		MessageCount    int           `json:"message_count"`
		IsThread        bool          `json:"is_thread,omitempty"`
		ParentSessionID string        `json:"parent_session_id,omitempty"`
		Threads         []sessionInfo `json:"threads,omitempty"`
	}

	var allSessions []sessionInfo

	userDirs, err := os.ReadDir(appDir)
	if err != nil {
		writeJSON(w, []any{})
		return
	}

	fs := kaggenSession.NewFileService(sessionsDir)

	for _, userDir := range userDirs {
		if !userDir.IsDir() {
			continue
		}
		userPath := filepath.Join(appDir, userDir.Name())
		sessDirs, err := os.ReadDir(userPath)
		if err != nil {
			continue
		}
		for _, sessDir := range sessDirs {
			if !sessDir.IsDir() {
				continue
			}
			dirInfo, _ := sessDir.Info()

			eventsPath := filepath.Join(userPath, sessDir.Name(), "events.jsonl")
			msgCount := countLines(eventsPath)

			si := sessionInfo{
				ID:           sessDir.Name(),
				UserID:       userDir.Name(),
				MessageCount: msgCount,
			}

			// Try to read metadata.json for richer info.
			key := session.Key{AppName: AppName, UserID: userDir.Name(), SessionID: sessDir.Name()}
			if meta, err := fs.ReadMetadata(key); err == nil && meta != nil {
				// Skip archived sessions.
				if !meta.ArchivedAt.IsZero() {
					continue
				}
				si.Name = meta.Name
				si.Channel = meta.Channel
				si.IsThread = meta.IsThread
				si.ParentSessionID = meta.ParentSessionID
				if !meta.CreatedAt.IsZero() {
					si.CreatedAt = meta.CreatedAt.Format(time.RFC3339)
				}
				if !meta.UpdatedAt.IsZero() {
					si.UpdatedAt = meta.UpdatedAt.Format(time.RFC3339)
				}
			}

			// Fallback UpdatedAt from directory mod time.
			if si.UpdatedAt == "" && dirInfo != nil {
				si.UpdatedAt = dirInfo.ModTime().Format(time.RFC3339)
			}

			allSessions = append(allSessions, si)
		}
	}

	sort.Slice(allSessions, func(i, j int) bool {
		return allSessions[i].UpdatedAt > allSessions[j].UpdatedAt
	})

	// Nest threads under their parent sessions unless flat listing requested.
	if !includeThreads {
		parentMap := make(map[string]*sessionInfo)
		var topLevel []sessionInfo
		// First pass: collect top-level sessions.
		for i := range allSessions {
			if !allSessions[i].IsThread {
				topLevel = append(topLevel, allSessions[i])
				parentMap[allSessions[i].ID] = &topLevel[len(topLevel)-1]
			}
		}
		// Second pass: attach threads to parents.
		for _, s := range allSessions {
			if s.IsThread {
				if parent, ok := parentMap[s.ParentSessionID]; ok {
					parent.Threads = append(parent.Threads, s)
				} else {
					// Orphaned thread — include at top level.
					topLevel = append(topLevel, s)
				}
			}
		}
		allSessions = topLevel
	}

	if len(allSessions) > 100 {
		allSessions = allSessions[:100]
	}

	writeJSON(w, allSessions)
}

// HandleSessionMessages returns chat history for a specific session.
// Query params: user_id, session_id (required), detail=chat|full, limit, offset.
func (d *DashboardAPI) HandleSessionMessages(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	userID := r.URL.Query().Get("user_id")
	sessionID := r.URL.Query().Get("session_id")
	if userID == "" || sessionID == "" {
		http.Error(w, `{"error":"user_id and session_id are required"}`, http.StatusBadRequest)
		return
	}

	detail := r.URL.Query().Get("detail")
	if detail == "" {
		detail = "chat"
	}

	sessionsDir := d.config.SessionsPath()
	eventsPath := filepath.Join(sessionsDir, AppName, userID, sessionID, "events.jsonl")

	events, _, err := kaggenSession.ReadEventJSONL(eventsPath)
	if err != nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	type msgOut struct {
		EventID   string   `json:"event_id"`
		Role      string   `json:"role"`
		Content   string   `json:"content"`
		Timestamp string   `json:"timestamp,omitempty"`
		SendFile  string   `json:"send_file,omitempty"`
		ToolCalls []string `json:"tool_calls,omitempty"` // only if detail=full
		ToolID    string   `json:"tool_id,omitempty"`    // only if detail=full
	}

	var messages []msgOut
	for _, evt := range events {
		if evt.Response == nil {
			continue
		}
		ts := ""
		if !evt.Timestamp.IsZero() {
			ts = evt.Timestamp.Format(time.RFC3339)
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message

			if detail == "chat" {
				// Chat mode: only user/assistant text messages.
				if msg.Role == "tool" || msg.ToolID != "" {
					continue
				}
				if msg.Content == "" && len(msg.ContentParts) == 0 {
					continue
				}
				if len(msg.ToolCalls) > 0 && msg.Content == "" {
					continue
				}
				content := msg.Content
				var sendFile string
				meta := map[string]any{}
				content, meta = extractSendFiles(content, meta)
				if sf, ok := meta["send_file"].(string); ok {
					sendFile = sf
				}
				messages = append(messages, msgOut{
					EventID:   evt.ID,
					Role:      string(msg.Role),
					Content:   content,
					Timestamp: ts,
					SendFile:  sendFile,
				})
			} else {
				// Full mode: include everything.
				m := msgOut{
					EventID:   evt.ID,
					Role:      string(msg.Role),
					Content:   msg.Content,
					Timestamp: ts,
					ToolID:    msg.ToolID,
				}
				for _, tc := range msg.ToolCalls {
					m.ToolCalls = append(m.ToolCalls, tc.Function.Name)
				}
				messages = append(messages, m)
			}
		}
	}

	// Pagination.
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		fmt.Sscanf(v, "%d", &offset)
	}
	if offset > len(messages) {
		offset = len(messages)
	}
	end := offset + limit
	if end > len(messages) {
		end = len(messages)
	}
	page := messages[offset:end]

	// Read metadata and summary.
	fs := kaggenSession.NewFileService(sessionsDir)
	key := session.Key{AppName: AppName, UserID: userID, SessionID: sessionID}

	var name string
	if meta, err := fs.ReadMetadata(key); err == nil && meta != nil {
		name = meta.Name
	}

	var summaryText string
	summaryPath := filepath.Join(sessionsDir, AppName, userID, sessionID, "summary.json")
	var summaryObj struct {
		Summary string `json:"summary"`
	}
	if data, err := os.ReadFile(summaryPath); err == nil {
		json.Unmarshal(data, &summaryObj)
		summaryText = summaryObj.Summary
	}

	// Context cache info.
	type contextInfo struct {
		TokenCount int    `json:"token_count"`
		UpdatedAt  string `json:"updated_at"`
	}
	var ctxInfo *contextInfo
	if cache, err := fs.ReadContextCache(key); err == nil && cache != nil {
		ctxInfo = &contextInfo{
			TokenCount: cache.TokenCount,
			UpdatedAt:  cache.UpdatedAt.Format(time.RFC3339),
		}
	}

	resp := struct {
		SessionID    string       `json:"session_id"`
		Name         string       `json:"name,omitempty"`
		Messages     []msgOut     `json:"messages"`
		Summary      string       `json:"summary,omitempty"`
		Total        int          `json:"total"`
		ContextCache *contextInfo `json:"context_cache,omitempty"`
	}{
		SessionID:    sessionID,
		Name:         name,
		Messages:     page,
		Summary:      summaryText,
		Total:        len(messages),
		ContextCache: ctxInfo,
	}

	writeJSON(w, resp)
}

// HandleSessionRename renames a session (updates metadata.json name field).
// Expects POST with JSON body: {"user_id": "...", "session_id": "...", "name": "..."}.
func (d *DashboardAPI) HandleSessionRename(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)
	if r.Method != http.MethodPost && r.Method != http.MethodPatch {
		http.Error(w, `{"error":"POST or PATCH required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		UserID    string `json:"user_id"`
		SessionID string `json:"session_id"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.UserID == "" || req.SessionID == "" || req.Name == "" {
		http.Error(w, `{"error":"user_id, session_id, and name are required"}`, http.StatusBadRequest)
		return
	}

	sessionsDir := d.config.SessionsPath()
	fs := kaggenSession.NewFileService(sessionsDir)
	key := session.Key{AppName: AppName, UserID: req.UserID, SessionID: req.SessionID}

	meta, err := fs.ReadMetadata(key)
	if err != nil || meta == nil {
		// Create metadata if it doesn't exist yet.
		meta = &kaggenSession.SessionMetadata{
			ID:     req.SessionID,
			UserID: req.UserID,
		}
	}
	meta.Name = req.Name
	meta.UpdatedAt = time.Now().UTC()

	if err := fs.WriteMetadata(key, meta); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to write metadata: %v"}`, err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "ok", "name": meta.Name})
}

// HandleSessionDelete deletes a session permanently.
// Expects POST with JSON body: {"user_id": "...", "session_id": "..."}.
func (d *DashboardAPI) HandleSessionDelete(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, `{"error":"POST or DELETE required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		UserID    string `json:"user_id"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.UserID == "" || req.SessionID == "" {
		http.Error(w, `{"error":"user_id and session_id are required"}`, http.StatusBadRequest)
		return
	}

	sessionsDir := d.config.SessionsPath()
	fs := kaggenSession.NewFileService(sessionsDir)
	key := session.Key{AppName: AppName, UserID: req.UserID, SessionID: req.SessionID}

	if err := fs.DeleteSession(r.Context(), key); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to delete session: %v"}`, err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "ok"})
}

// HandleSessionArchive marks a session as archived by setting archived_at in metadata.
// Expects POST with JSON body: {"user_id": "...", "session_id": "..."}.
func (d *DashboardAPI) HandleSessionArchive(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		UserID    string `json:"user_id"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.UserID == "" || req.SessionID == "" {
		http.Error(w, `{"error":"user_id and session_id are required"}`, http.StatusBadRequest)
		return
	}

	sessionsDir := d.config.SessionsPath()
	fs := kaggenSession.NewFileService(sessionsDir)
	key := session.Key{AppName: AppName, UserID: req.UserID, SessionID: req.SessionID}

	meta, err := fs.ReadMetadata(key)
	if err != nil || meta == nil {
		meta = &kaggenSession.SessionMetadata{
			ID:     req.SessionID,
			UserID: req.UserID,
		}
	}
	meta.ArchivedAt = time.Now().UTC()
	meta.UpdatedAt = time.Now().UTC()

	if err := fs.WriteMetadata(key, meta); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to archive session: %v"}`, err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "ok"})
}

// HandleConfig returns sanitized configuration.
func (d *DashboardAPI) HandleConfig(w http.ResponseWriter, r *http.Request) {
	// Marshal then unmarshal to get a mutable map
	data, _ := json.Marshal(d.config)
	var m map[string]any
	json.Unmarshal(data, &m)

	// Redact sensitive fields
	redactNestedKey(m, "channels", "telegram", "bot_token")
	redactNestedKey(m, "session", "redis", "password")
	redactNestedKey(m, "session", "postgres", "password")
	redactNestedKey(m, "proactive", "webhooks") // may contain secrets
	redactNestedKey(m, "web_search", "api_key")
	// Redact sensitive fields in maps (databases, mqtt, ssh, oauth)
	redactMapPasswords(m, "databases", "connections", "password")
	redactMapPasswords(m, "mqtt", "brokers", "password")
	redactMapPasswords(m, "ssh", "hosts", "password")
	redactMapPasswords(m, "ssh", "hosts", "passphrase")
	redactMapPasswords(m, "oauth", "providers", "client_secret")

	writeJSON(w, m)
}

// HandleLogsSSE streams log entries via Server-Sent Events.
func (d *DashboardAPI) HandleLogsSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	d.setCORSHeaders(w, r)

	ctx := r.Context()
	d.logStreamer.ServeSSE(ctx, func(data []byte) error {
		_, err := w.Write(data)
		return err
	}, flusher.Flush)
}

// HandlePipelines returns pipeline definitions with current progress.
func (d *DashboardAPI) HandlePipelines(w http.ResponseWriter, r *http.Request) {
	pipelines := d.agentProvider.Pipelines()
	store := d.agentProvider.InFlightStore()
	progress := store.PipelineProgress()

	// Determine which agents currently have running tasks, keyed by sessionID+agentName
	// so that concurrent pipelines in different sessions don't bleed into each other.
	type agentSessionKey struct{ session, agent string }
	runningBySession := make(map[agentSessionKey]bool)
	runningGlobal := make(map[string]bool) // fallback for no-session display
	for _, t := range store.List(agent.TaskRunning) {
		runningBySession[agentSessionKey{t.SessionID, t.AgentName}] = true
		runningGlobal[t.AgentName] = true
	}

	type stageStatus struct {
		Agent       string `json:"agent"`
		Description string `json:"description"`
		Stage       int    `json:"stage"`
		Status      string `json:"status"` // pending, running, completed
	}
	type pipelineStatus struct {
		Name            string        `json:"name"`
		Description     string        `json:"description"`
		TaskDescription string        `json:"task_description,omitempty"` // what the user asked for
		SessionID       string        `json:"session_id,omitempty"`
		Stages          []stageStatus `json:"stages"`
	}

	var result []pipelineStatus

	for _, p := range pipelines {
		// Collect all sessions that have progress for this pipeline.
		sessionsWithProgress := make(map[string]map[int]bool)
		for sid, pmap := range progress {
			if stages, ok := pmap[p.Name]; ok {
				sessionsWithProgress[sid] = stages
			}
		}

		if len(sessionsWithProgress) == 0 {
			// No active progress — show pipeline definition with all pending.
			stages := make([]stageStatus, len(p.Stages))
			for i, s := range p.Stages {
				status := "pending"
				if runningGlobal[s.Agent] {
					status = "running"
				}
				stages[i] = stageStatus{Agent: s.Agent, Description: s.Description, Stage: i + 1, Status: status}
			}
			result = append(result, pipelineStatus{Name: p.Name, Description: p.Description, Stages: stages})
		} else {
			// Show one entry per session with progress.
			for sid, completedStages := range sessionsWithProgress {
				stages := make([]stageStatus, len(p.Stages))
				for i, s := range p.Stages {
					status := "pending"
					if completedStages[i+1] {
						status = "completed"
					} else if runningBySession[agentSessionKey{sid, s.Agent}] {
						status = "running"
					}
					stages[i] = stageStatus{Agent: s.Agent, Description: s.Description, Stage: i + 1, Status: status}
				}
				taskDesc := store.PipelineDescription(sid, p.Name)
				result = append(result, pipelineStatus{Name: p.Name, Description: p.Description, TaskDescription: taskDesc, SessionID: sid, Stages: stages})
			}
		}
	}

	writeJSON(w, result)
}

// HandleCancelTask cancels a running async task.
// Expects POST with JSON body: {"task_id": "..."}
func (d *DashboardAPI) HandleCancelTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}
	store := d.agentProvider.InFlightStore()
	if store.Cancel(req.TaskID) {
		writeJSON(w, map[string]any{"cancelled": true, "task_id": req.TaskID})
	} else {
		writeJSON(w, map[string]any{"cancelled": false, "message": "task not found or not running"})
	}
}

// HandleGroupedTasks returns tasks grouped by their pipeline context.
// Active pipelines are returned with their stages and associated tasks.
// Tasks not belonging to any pipeline are returned as standalone tasks.
func (d *DashboardAPI) HandleGroupedTasks(w http.ResponseWriter, r *http.Request) {
	statusFilter := agent.TaskStatus(r.URL.Query().Get("status"))
	store := d.agentProvider.InFlightStore()
	pipelines := d.agentProvider.Pipelines()
	progress := store.PipelineProgress()

	// Build agent → (pipeline, stage) lookup from pipeline definitions.
	type pipelineAgent struct {
		pipeline string
		stage    int // 1-based
	}
	agentToPipeline := make(map[string]pipelineAgent)
	for _, p := range pipelines {
		for i, s := range p.Stages {
			agentToPipeline[s.Agent] = pipelineAgent{pipeline: p.Name, stage: i + 1}
		}
	}

	// Determine which agents currently have running tasks, keyed by sessionID+agentName.
	type agentSessionKey struct{ session, agent string }
	runningBySession := make(map[agentSessionKey]bool)
	for _, t := range store.List(agent.TaskRunning) {
		runningBySession[agentSessionKey{t.SessionID, t.AgentName}] = true
	}

	// Get all tasks (filtered by status if provided).
	allTasks := store.List(statusFilter)
	sort.Slice(allTasks, func(i, j int) bool {
		return allTasks[i].StartedAt.After(allTasks[j].StartedAt)
	})

	// Group tasks by (sessionID, pipelineName).
	type groupKey struct{ sessionID, pipeline string }
	type stageStatus struct {
		Agent       string `json:"agent"`
		Description string `json:"description"`
		Stage       int    `json:"stage"`
		Status      string `json:"status"` // pending, running, completed
	}
	type pipelineGroup struct {
		Name            string              `json:"name"`
		Description     string              `json:"description"`
		TaskDescription string              `json:"task_description,omitempty"`
		SessionID       string              `json:"session_id"`
		Stages          []stageStatus       `json:"stages"`
		Tasks           []*agent.TaskState  `json:"tasks"`
		StartedAt       *time.Time          `json:"started_at,omitempty"`
	}

	groups := make(map[groupKey]*pipelineGroup)
	var standaloneTasks []*agent.TaskState

	for _, t := range allTasks {
		// Check if task has explicit pipeline context.
		if t.PipelineName != "" {
			key := groupKey{sessionID: t.SessionID, pipeline: t.PipelineName}
			if groups[key] == nil {
				// Find pipeline definition for stage info.
				var pDef struct {
					Name        string
					Description string
					Stages      []struct {
						Agent       string
						Description string
					}
				}
				for _, p := range pipelines {
					if p.Name == t.PipelineName {
						pDef.Name = p.Name
						pDef.Description = p.Description
						for _, s := range p.Stages {
							pDef.Stages = append(pDef.Stages, struct {
								Agent       string
								Description string
							}{Agent: s.Agent, Description: s.Description})
						}
						break
					}
				}

				// Build stage statuses.
				completedStages := make(map[int]bool)
				if sp, ok := progress[t.SessionID]; ok {
					if stg, ok := sp[t.PipelineName]; ok {
						completedStages = stg
					}
				}
				stages := make([]stageStatus, len(pDef.Stages))
				for i, s := range pDef.Stages {
					status := "pending"
					if completedStages[i+1] {
						status = "completed"
					} else if runningBySession[agentSessionKey{t.SessionID, s.Agent}] {
						status = "running"
					}
					stages[i] = stageStatus{Agent: s.Agent, Description: s.Description, Stage: i + 1, Status: status}
				}

				taskDesc := store.PipelineDescription(t.SessionID, t.PipelineName)
				startedAt := store.PipelineStartedAt(t.SessionID, t.PipelineName)
				groups[key] = &pipelineGroup{
					Name:            pDef.Name,
					Description:     pDef.Description,
					TaskDescription: taskDesc,
					SessionID:       t.SessionID,
					Stages:          stages,
					Tasks:           []*agent.TaskState{},
					StartedAt:       startedAt,
				}
			}
			groups[key].Tasks = append(groups[key].Tasks, t)
		} else {
			standaloneTasks = append(standaloneTasks, t)
		}
	}

	// Also check for pipelines with progress but no matching tasks (in case filter excludes them).
	for sid, pmap := range progress {
		for pname := range pmap {
			key := groupKey{sessionID: sid, pipeline: pname}
			if groups[key] == nil {
				// Find pipeline definition.
				var pDef struct {
					Name        string
					Description string
					Stages      []struct {
						Agent       string
						Description string
					}
				}
				for _, p := range pipelines {
					if p.Name == pname {
						pDef.Name = p.Name
						pDef.Description = p.Description
						for _, s := range p.Stages {
							pDef.Stages = append(pDef.Stages, struct {
								Agent       string
								Description string
							}{Agent: s.Agent, Description: s.Description})
						}
						break
					}
				}
				if pDef.Name == "" {
					continue // Pipeline definition not found, skip.
				}

				// Build stage statuses.
				completedStages := pmap[pname]
				stages := make([]stageStatus, len(pDef.Stages))
				for i, s := range pDef.Stages {
					status := "pending"
					if completedStages[i+1] {
						status = "completed"
					} else if runningBySession[agentSessionKey{sid, s.Agent}] {
						status = "running"
					}
					stages[i] = stageStatus{Agent: s.Agent, Description: s.Description, Stage: i + 1, Status: status}
				}

				taskDesc := store.PipelineDescription(sid, pname)
				startedAt := store.PipelineStartedAt(sid, pname)
				groups[key] = &pipelineGroup{
					Name:            pDef.Name,
					Description:     pDef.Description,
					TaskDescription: taskDesc,
					SessionID:       sid,
					Stages:          stages,
					Tasks:           []*agent.TaskState{},
					StartedAt:       startedAt,
				}
			}
		}
	}

	// Convert map to slice and sort by start time.
	pipelineList := make([]pipelineGroup, 0, len(groups))
	for _, g := range groups {
		pipelineList = append(pipelineList, *g)
	}
	sort.Slice(pipelineList, func(i, j int) bool {
		if pipelineList[i].StartedAt == nil && pipelineList[j].StartedAt == nil {
			return pipelineList[i].Name < pipelineList[j].Name
		}
		if pipelineList[i].StartedAt == nil {
			return false
		}
		if pipelineList[j].StartedAt == nil {
			return true
		}
		return pipelineList[i].StartedAt.After(*pipelineList[j].StartedAt)
	})

	writeJSON(w, map[string]any{
		"pipelines":        pipelineList,
		"standalone_tasks": standaloneTasks,
	})
}

// HandlePlanDetail returns a plan and its children.
func (d *DashboardAPI) HandlePlanDetail(w http.ResponseWriter, r *http.Request) {
	if d.backlogStore == nil {
		http.Error(w, "backlog not configured", http.StatusNotFound)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}
	parent, children, err := d.backlogStore.GetWithChildren(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if children == nil {
		children = []*backlog.Item{}
	}
	writeJSON(w, map[string]any{
		"plan":     parent,
		"subtasks": children,
	})
}

// RegisterRoutes registers all dashboard routes on the given handler registration func.
func (d *DashboardAPI) RegisterRoutes(handleFunc func(pattern string, handler http.HandlerFunc)) {
	handleFunc("/", d.ServeHTML)
	handleFunc("/dashboard", d.ServeHTML)
	handleFunc("/api/overview", d.HandleOverview)
	handleFunc("/api/tasks", d.HandleTasks)
	handleFunc("/api/tasks/grouped", d.HandleGroupedTasks)
	handleFunc("/api/backlog", d.HandleBacklog)
	handleFunc("/api/backlog/plan", d.HandlePlanDetail)
	handleFunc("/api/skills", d.HandleSkills)
	handleFunc("/api/sessions", d.HandleSessions)
	handleFunc("/api/sessions/messages", d.HandleSessionMessages)
	handleFunc("/api/sessions/rename", d.HandleSessionRename)
	handleFunc("/api/sessions/delete", d.HandleSessionDelete)
	handleFunc("/api/sessions/archive", d.HandleSessionArchive)
	handleFunc("/api/config", d.HandleConfig)
	handleFunc("/api/config/update", d.dashboardAuth.RequireAuth(d.HandleConfigUpdate))
	handleFunc("/api/pipelines", d.HandlePipelines)
	handleFunc("/api/tasks/cancel", d.HandleCancelTask)
	handleFunc("/api/logs", d.HandleLogsSSE)
	handleFunc("/api/approvals", d.HandleApprovals)
	handleFunc("/api/approvals/approve", d.HandleApprovalAction)
	handleFunc("/api/approvals/reject", d.HandleApprovalAction)
	handleFunc("/api/files/", d.HandleFiles)
	// Settings & Token management
	handleFunc("/api/settings", d.HandleSettings)
	handleFunc("/api/tokens", d.HandleTokens)
	handleFunc("/api/tokens/generate", d.HandleTokenGenerate)
	handleFunc("/api/tokens/revoke", d.HandleTokenRevoke)
	// Dashboard authentication
	handleFunc("/api/auth/status", d.HandleAuthStatus)
	handleFunc("/api/auth/setup", d.HandleAuthSetup)
	handleFunc("/api/auth/login", d.HandleAuthLogin)
	handleFunc("/api/auth/logout", d.HandleAuthLogout)
	// Secrets management (requires auth)
	handleFunc("/api/secrets", d.dashboardAuth.RequireAuth(d.HandleSecrets))
	handleFunc("/api/secrets/set", d.dashboardAuth.RequireAuth(d.HandleSecretsSet))
	handleFunc("/api/secrets/delete", d.dashboardAuth.RequireAuth(d.HandleSecretsDelete))
	// OAuth management
	handleFunc("/api/oauth/providers", d.dashboardAuth.RequireAuth(d.HandleOAuthProviders))
	handleFunc("/api/oauth/authorize", d.dashboardAuth.RequireAuth(d.HandleOAuthAuthorize))
	handleFunc("/api/oauth/callback", d.HandleOAuthCallback) // No auth - callback from OAuth provider
	handleFunc("/api/oauth/revoke", d.dashboardAuth.RequireAuth(d.HandleOAuthRevoke))
	handleFunc("/api/oauth/status", d.dashboardAuth.RequireAuth(d.HandleOAuthStatus))
}

// SetHandler stores the message handler for approval completion injection.
func (d *DashboardAPI) SetHandler(h *Handler) {
	d.handler = h
}

// HandleApprovals returns pending approval requests.
func (d *DashboardAPI) HandleApprovals(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	store := d.agentProvider.InFlightStore()
	all := store.List(agent.TaskPendingApproval)

	type approvalOut struct {
		ID           string              `json:"id"`
		ToolName     string              `json:"tool_name"`
		SkillName    string              `json:"skill_name"`
		Description  string              `json:"description"`
		Arguments    string              `json:"arguments"`
		SessionID    string              `json:"session_id"`
		UserID       string              `json:"user_id"`
		RequestedAt  string              `json:"requested_at"`
		TimeoutAt    string              `json:"timeout_at"`
		EmailPreview *agent.EmailPreview `json:"email_preview,omitempty"`
	}

	out := make([]approvalOut, 0, len(all))
	for _, t := range all {
		if t.ApprovalRequest == nil {
			continue
		}
		out = append(out, approvalOut{
			ID:           t.ID,
			ToolName:     t.ApprovalRequest.ToolName,
			SkillName:    t.ApprovalRequest.SkillName,
			Description:  t.ApprovalRequest.Description,
			Arguments:    t.ApprovalRequest.Arguments,
			SessionID:    t.SessionID,
			UserID:       t.UserID,
			RequestedAt:  t.ApprovalRequest.RequestedAt.Format(time.RFC3339),
			TimeoutAt:    t.TimeoutAt.Format(time.RFC3339),
			EmailPreview: t.ApprovalRequest.EmailPreview,
		})
	}
	writeJSON(w, out)
}

// HandleApprovalAction handles POST /api/approvals/approve and /api/approvals/reject.
func (d *DashboardAPI) HandleApprovalAction(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID     string `json:"id"`
		Reason string `json:"reason,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, `{"error":"id is required"}`, http.StatusBadRequest)
		return
	}

	store := d.agentProvider.InFlightStore()
	task, ok := store.Get(req.ID)
	if !ok || task.Status != agent.TaskPendingApproval {
		http.Error(w, `{"error":"approval not found or already resolved"}`, http.StatusNotFound)
		return
	}

	isApprove := strings.HasSuffix(r.URL.Path, "/approve")
	var action, result string
	if isApprove {
		action = "approved"
		store.Complete(req.ID, "approved")
		result = fmt.Sprintf("Tool %s was APPROVED by user. You may now retry the action.", task.ApprovalRequest.ToolName)
	} else {
		action = "rejected"
		reason := req.Reason
		if reason == "" {
			reason = "rejected by user"
		}
		store.Fail(req.ID, reason)
		result = fmt.Sprintf("Tool %s was REJECTED by user. Reason: %s. Find an alternative approach.", task.ApprovalRequest.ToolName, reason)
	}

	// Try graph-based SignalDecision first (for blocking goroutine pattern).
	guardedRunner := d.agentProvider.GuardedRunner()
	if guardedRunner != nil {
		if signaled := guardedRunner.ExecutionStore().SignalDecision(req.ID, isApprove); signaled {
			// Graph execution will resume and deliver results via the waiting goroutine.
			writeJSON(w, map[string]string{"status": action, "id": req.ID})
			return
		}
	}

	// Fall back to legacy InjectCompletion for non-graph flows.
	if d.handler != nil {
		_ = d.handler.InjectCompletion(r.Context(), task.SessionID, task.UserID, req.ID, task.AgentName, result)
	}

	writeJSON(w, map[string]string{"status": action, "id": req.ID})
}

// HandleFiles serves published files from ~/.kaggen/public/.
// Only files that have been explicitly published via extractSendFiles are accessible.
// No server filesystem paths are exposed — clients request by filename only.
func (d *DashboardAPI) HandleFiles(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	// Extract filename from /api/files/<name>
	name := strings.TrimPrefix(r.URL.Path, "/api/files/")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	pubDir := config.ExpandPath("~/.kaggen/public")
	filePath := filepath.Join(pubDir, filepath.Base(name))

	// Verify the resolved path is still inside the public directory.
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	absPubDir, _ := filepath.Abs(pubDir)
	if !strings.HasPrefix(absPath, absPubDir) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	http.ServeFile(w, r, absPath)
}

// HandleTokens returns the list of configured tokens (metadata only, not secrets).
func (d *DashboardAPI) HandleTokens(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)
	if d.tokenStore == nil {
		writeJSON(w, []any{})
		return
	}
	tokens := d.tokenStore.ListTokens()
	writeJSON(w, tokens)
}

// HandleTokenGenerate generates a new authentication token.
// Expects POST with JSON body: {"name": "...", "expires_in": "24h"}
func (d *DashboardAPI) HandleTokenGenerate(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}
	if d.tokenStore == nil {
		http.Error(w, `{"error":"token store not configured"}`, http.StatusInternalServerError)
		return
	}

	var req struct {
		Name      string `json:"name"`
		ExpiresIn string `json:"expires_in"` // e.g., "24h", "7d", ""
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	// Parse expiration duration
	var expiresIn time.Duration
	if req.ExpiresIn != "" {
		var err error
		expiresIn, err = time.ParseDuration(req.ExpiresIn)
		if err != nil {
			// Try parsing as days (e.g., "7d")
			if strings.HasSuffix(req.ExpiresIn, "d") {
				days := strings.TrimSuffix(req.ExpiresIn, "d")
				var n int
				fmt.Sscanf(days, "%d", &n)
				expiresIn = time.Duration(n) * 24 * time.Hour
			}
		}
	}

	plaintext, id, err := d.tokenStore.GenerateToken(req.Name, expiresIn)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to generate token: %v"}`, err), http.StatusInternalServerError)
		return
	}

	// Determine WebSocket protocol based on TLS config
	wsProto := "ws"
	if d.config.Gateway.TLS.Enabled {
		wsProto = "wss"
	}

	// Build WebSocket URLs for QR code
	var wsURLs []InterfaceURL
	if d.config.Gateway.Bind == "0.0.0.0" || d.config.Gateway.Bind == "" {
		// Get URLs for all non-loopback interfaces
		wsURLs = getInterfaceURLs(d.config.Gateway.Port, plaintext, wsProto)
	} else {
		// Single bound address
		wsURLs = []InterfaceURL{{
			Name: "bound",
			IP:   d.config.Gateway.Bind,
			URL:  fmt.Sprintf("%s://%s:%d/ws?token=%s", wsProto, d.config.Gateway.Bind, d.config.Gateway.Port, plaintext),
		}}
	}

	// Build P2P multiaddrs with token for mobile app QR code
	var p2pAddrs []P2PMultiaddr
	if d.config.P2P.Enabled && d.p2pNodeFunc != nil {
		if node := d.p2pNodeFunc(); node != nil {
			peerID := node.PeerID().String()
			p2pAddrs = getP2PMultiaddrs(d.config.P2P.Port, peerID, d.config.P2P.Transports, plaintext)
		}
	}

	writeJSON(w, map[string]any{
		"id":        id,
		"token":     plaintext,
		"name":      req.Name,
		"ws_urls":   wsURLs,
		"p2p_addrs": p2pAddrs,
		"message":   "Save this token now - it cannot be retrieved again!",
	})
}

// HandleTokenRevoke revokes a token by ID.
// Expects POST with JSON body: {"id": "..."}
func (d *DashboardAPI) HandleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, `{"error":"POST or DELETE required"}`, http.StatusMethodNotAllowed)
		return
	}
	if d.tokenStore == nil {
		http.Error(w, `{"error":"token store not configured"}`, http.StatusInternalServerError)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, `{"error":"id is required"}`, http.StatusBadRequest)
		return
	}

	if err := d.tokenStore.RevokeToken(req.ID); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to revoke token: %v"}`, err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "revoked", "id": req.ID})
}

// HandleSettings returns current security settings.
func (d *DashboardAPI) HandleSettings(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	hasTokens := d.tokenStore != nil && d.tokenStore.HasTokens()

	// Check secrets store availability
	secretStore := secrets.DefaultStore()
	secretsAvailable := secretStore != nil && secretStore.Available()
	secretsBackend := ""
	if secretStore != nil {
		secretsBackend = secretStore.Name()
	}

	// Build P2P info
	p2pInfo := map[string]any{
		"enabled": d.config.P2P.Enabled,
	}
	if d.p2pNodeFunc != nil {
		if node := d.p2pNodeFunc(); node != nil {
			peerID := node.PeerID().String()
			p2pInfo["peer_id"] = peerID
			p2pInfo["topics"] = d.config.P2P.Topics

			// Build multiaddrs for all network interfaces (like WebSocket URLs)
			// No auth token here - this is just for display; tokens are added when generating QR codes
			p2pInfo["multiaddrs"] = getP2PMultiaddrs(d.config.P2P.Port, peerID, d.config.P2P.Transports, "")
		}
	}

	writeJSON(w, map[string]any{
		"auth_enabled":      d.config.Security.Auth.Enabled,
		"has_tokens":        hasTokens,
		"gateway_bind":      d.config.Gateway.Bind,
		"gateway_port":      d.config.Gateway.Port,
		"allowed_origins":   d.config.Gateway.GetAllowedOrigins(),
		"sandbox_enabled":   d.config.Security.CommandSandbox.Enabled,
		"secrets_available": secretsAvailable,
		"secrets_backend":   secretsBackend,
		"p2p":               p2pInfo,
	})
}

// HandleAuthStatus returns authentication status for the dashboard.
func (d *DashboardAPI) HandleAuthStatus(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	// Check if password is set
	needsSetup := !d.dashboardAuth.IsPasswordSet()

	// Check if current request has valid session
	authenticated := false
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		authenticated = d.dashboardAuth.ValidateSession(cookie.Value)
	}

	writeJSON(w, map[string]any{
		"needs_setup":   needsSetup,
		"authenticated": authenticated,
	})
}

// HandleAuthSetup sets up the initial dashboard password.
func (d *DashboardAPI) HandleAuthSetup(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Don't allow setup if password already exists
	if d.dashboardAuth.IsPasswordSet() {
		http.Error(w, `{"error":"password already set"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if len(req.Password) < 8 {
		http.Error(w, `{"error":"password must be at least 8 characters"}`, http.StatusBadRequest)
		return
	}

	if err := d.dashboardAuth.SetPassword(req.Password); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to set password: %v"}`, err), http.StatusInternalServerError)
		return
	}

	// Create session for the user
	token, err := d.dashboardAuth.CreateSession()
	if err != nil {
		http.Error(w, `{"error":"failed to create session"}`, http.StatusInternalServerError)
		return
	}

	d.dashboardAuth.SetSessionCookie(w, r, token)
	writeJSON(w, map[string]any{"success": true})
}

// HandleAuthLogin handles dashboard login.
func (d *DashboardAPI) HandleAuthLogin(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if !d.dashboardAuth.ValidatePassword(req.Password) {
		http.Error(w, `{"error":"invalid password"}`, http.StatusUnauthorized)
		return
	}

	// Create session
	token, err := d.dashboardAuth.CreateSession()
	if err != nil {
		http.Error(w, `{"error":"failed to create session"}`, http.StatusInternalServerError)
		return
	}

	d.dashboardAuth.SetSessionCookie(w, r, token)
	writeJSON(w, map[string]any{"success": true})
}

// HandleAuthLogout handles dashboard logout.
func (d *DashboardAPI) HandleAuthLogout(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Destroy session if exists
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		d.dashboardAuth.DestroySession(cookie.Value)
	}

	d.dashboardAuth.ClearSessionCookie(w)
	writeJSON(w, map[string]any{"success": true})
}

// HandleSecrets returns the list of stored secret keys (not values).
func (d *DashboardAPI) HandleSecrets(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	store := secrets.DefaultStore()
	if store == nil || !store.Available() {
		writeJSON(w, map[string]any{
			"available": false,
			"keys":      []string{},
			"error":     "Secret store not available. Set KAGGEN_MASTER_KEY for encrypted file storage.",
		})
		return
	}

	keys, err := store.List()
	if err != nil {
		writeJSON(w, map[string]any{
			"available": true,
			"keys":      []string{},
			"error":     err.Error(),
		})
		return
	}

	writeJSON(w, map[string]any{
		"available": true,
		"backend":   store.Name(),
		"keys":      keys,
	})
}

// HandleSecretsSet stores a new secret.
// Expects POST with JSON body: {"key": "...", "value": "..."}
func (d *DashboardAPI) HandleSecretsSet(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	store := secrets.DefaultStore()
	if store == nil || !store.Available() {
		http.Error(w, `{"error":"Secret store not available"}`, http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Key == "" || req.Value == "" {
		http.Error(w, `{"error":"key and value are required"}`, http.StatusBadRequest)
		return
	}

	// Validate key name (alphanumeric, dashes, underscores only)
	for _, c := range req.Key {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			http.Error(w, `{"error":"key must be alphanumeric with dashes/underscores only"}`, http.StatusBadRequest)
			return
		}
	}

	if err := store.Set(req.Key, req.Value); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to store secret: %v"}`, err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "stored", "key": req.Key})
}

// HandleSecretsDelete deletes a secret by key.
// Expects POST with JSON body: {"key": "..."}
func (d *DashboardAPI) HandleSecretsDelete(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, `{"error":"POST or DELETE required"}`, http.StatusMethodNotAllowed)
		return
	}

	store := secrets.DefaultStore()
	if store == nil || !store.Available() {
		http.Error(w, `{"error":"Secret store not available"}`, http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		http.Error(w, `{"error":"key is required"}`, http.StatusBadRequest)
		return
	}

	if err := store.Delete(req.Key); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to delete secret: %v"}`, err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "deleted", "key": req.Key})
}

// HandleConfigUpdate handles PUT requests to update configuration.
// Expects PUT with JSON body containing partial config updates.
func (d *DashboardAPI) HandleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "PUT, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, `{"error":"PUT or POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	// Decode the incoming updates
	var updates map[string]any
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	// Marshal current config to map for merging
	currentData, _ := json.Marshal(d.config)
	var current map[string]any
	json.Unmarshal(currentData, &current)

	// Merge updates into current config, preserving sensitive fields if unchanged
	mergeConfigMaps(current, updates)

	// Validate critical fields
	if errs := validateConfigMap(current); len(errs) > 0 {
		writeJSON(w, map[string]any{"error": "validation failed", "errors": errs})
		return
	}

	// Marshal merged map back to JSON and unmarshal into Config struct
	mergedData, _ := json.Marshal(current)
	var newConfig config.Config
	if err := json.Unmarshal(mergedData, &newConfig); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to parse config: %v"}`, err), http.StatusBadRequest)
		return
	}

	// Update the in-memory config
	*d.config = newConfig

	// Save to disk
	if err := d.config.Save(); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to save config: %v"}`, err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "saved"})
}

// mergeConfigMaps recursively merges src into dst, preserving sensitive fields if src has "***" or empty.
func mergeConfigMaps(dst, src map[string]any) {
	sensitiveKeys := map[string]bool{
		"bot_token": true, "password": true, "passphrase": true,
		"api_key": true, "client_secret": true, "secret": true,
	}

	for k, v := range src {
		if v == nil {
			continue
		}
		// Check if this is a sensitive field that should be preserved
		if sensitiveKeys[k] {
			if str, ok := v.(string); ok && (str == "" || str == "***") {
				continue // preserve existing value
			}
		}
		// If both are maps, merge recursively
		if srcMap, srcOK := v.(map[string]any); srcOK {
			if dstMap, dstOK := dst[k].(map[string]any); dstOK {
				mergeConfigMaps(dstMap, srcMap)
				continue
			}
		}
		dst[k] = v
	}
}

// validateConfigMap performs basic validation on the config map.
func validateConfigMap(m map[string]any) []string {
	var errs []string

	// Validate gateway port
	if gateway, ok := m["gateway"].(map[string]any); ok {
		if port, ok := gateway["port"].(float64); ok {
			if port < 1 || port > 65535 {
				errs = append(errs, "gateway.port must be 1-65535")
			}
		}
	}

	// Validate P2P port if enabled
	if p2p, ok := m["p2p"].(map[string]any); ok {
		if enabled, ok := p2p["enabled"].(bool); ok && enabled {
			if port, ok := p2p["port"].(float64); ok && (port < 1 || port > 65535) {
				errs = append(errs, "p2p.port must be 1-65535")
			}
		}
	}

	// Validate reasoning threshold
	if reasoning, ok := m["reasoning"].(map[string]any); ok {
		if thresh, ok := reasoning["escalation_threshold"].(float64); ok {
			if thresh < 0 || thresh > 1 {
				errs = append(errs, "reasoning.escalation_threshold must be 0-1")
			}
		}
	}

	// Validate JSON fields by attempting to unmarshal into target types
	errs = append(errs, validateJSONFields(m)...)

	return errs
}

// validateJSONFields validates complex JSON fields by attempting to unmarshal them
// into their target Go types.
func validateJSONFields(m map[string]any) []string {
	var errs []string

	// Validate proactive.jobs
	if proactive, ok := m["proactive"].(map[string]any); ok {
		if jobs, ok := proactive["jobs"]; ok && jobs != nil {
			data, _ := json.Marshal(jobs)
			var parsed []config.CronJobConfig
			if err := json.Unmarshal(data, &parsed); err != nil {
				errs = append(errs, "proactive.jobs: "+simplifyUnmarshalError(err))
			}
		}
		if webhooks, ok := proactive["webhooks"]; ok && webhooks != nil {
			data, _ := json.Marshal(webhooks)
			var parsed []config.WebhookConfig
			if err := json.Unmarshal(data, &parsed); err != nil {
				errs = append(errs, "proactive.webhooks: "+simplifyUnmarshalError(err))
			}
		}
		if heartbeats, ok := proactive["heartbeats"]; ok && heartbeats != nil {
			data, _ := json.Marshal(heartbeats)
			var parsed []config.HeartbeatConfig
			if err := json.Unmarshal(data, &parsed); err != nil {
				errs = append(errs, "proactive.heartbeats: "+simplifyUnmarshalError(err))
			}
		}
	}

	// Validate databases.connections
	if dbs, ok := m["databases"].(map[string]any); ok {
		if conns, ok := dbs["connections"]; ok && conns != nil {
			data, _ := json.Marshal(conns)
			var parsed map[string]config.DatabaseConnection
			if err := json.Unmarshal(data, &parsed); err != nil {
				errs = append(errs, "databases.connections: "+simplifyUnmarshalError(err))
			}
		}
	}

	// Validate mqtt.brokers
	if mqtt, ok := m["mqtt"].(map[string]any); ok {
		if brokers, ok := mqtt["brokers"]; ok && brokers != nil {
			data, _ := json.Marshal(brokers)
			var parsed map[string]config.MQTTBroker
			if err := json.Unmarshal(data, &parsed); err != nil {
				errs = append(errs, "mqtt.brokers: "+simplifyUnmarshalError(err))
			}
		}
	}

	// Validate ssh.hosts
	if ssh, ok := m["ssh"].(map[string]any); ok {
		if hosts, ok := ssh["hosts"]; ok && hosts != nil {
			data, _ := json.Marshal(hosts)
			var parsed map[string]config.SSHHost
			if err := json.Unmarshal(data, &parsed); err != nil {
				errs = append(errs, "ssh.hosts: "+simplifyUnmarshalError(err))
			}
		}
	}

	// Validate oauth.providers
	if oauth, ok := m["oauth"].(map[string]any); ok {
		if providers, ok := oauth["providers"]; ok && providers != nil {
			data, _ := json.Marshal(providers)
			var parsed map[string]config.OAuthProvider
			if err := json.Unmarshal(data, &parsed); err != nil {
				errs = append(errs, "oauth.providers: "+simplifyUnmarshalError(err))
			}
		}
	}

	// Validate approval.auto_approve
	if approval, ok := m["approval"].(map[string]any); ok {
		if rules, ok := approval["auto_approve"]; ok && rules != nil {
			data, _ := json.Marshal(rules)
			var parsed []config.AutoApproveRule
			if err := json.Unmarshal(data, &parsed); err != nil {
				errs = append(errs, "approval.auto_approve: "+simplifyUnmarshalError(err))
			}
		}
	}

	return errs
}

// simplifyUnmarshalError extracts a more readable error message from json unmarshal errors.
func simplifyUnmarshalError(err error) string {
	msg := err.Error()
	// Try to extract the field name and type mismatch from common errors
	if strings.Contains(msg, "cannot unmarshal") {
		return msg
	}
	return msg
}

// --- helpers ---

// setCORSHeaders sets the CORS headers based on the configured allowed origins.
// If no allowed origins are configured, defaults to localhost variants only.
func (d *DashboardAPI) setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return // Same-origin request, no CORS headers needed
	}

	allowedOrigins := d.config.Gateway.GetAllowedOrigins()
	for _, allowed := range allowedOrigins {
		if strings.HasPrefix(origin, allowed) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			return
		}
	}
	// Origin not allowed - don't set CORS headers
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	// Note: CORS headers should be set by the handler before calling writeJSON
	json.NewEncoder(w).Encode(v)
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	parts := []string{}
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	parts = append(parts, fmt.Sprintf("%dm", mins))
	return strings.Join(parts, " ")
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		count++
	}
	return count
}

func redactNestedKey(m map[string]any, keys ...string) {
	if len(keys) == 0 {
		return
	}
	if len(keys) == 1 {
		if _, ok := m[keys[0]]; ok {
			m[keys[0]] = "***"
		}
		return
	}
	if nested, ok := m[keys[0]].(map[string]any); ok {
		redactNestedKey(nested, keys[1:]...)
	}
}

// redactMapPasswords redacts a sensitive field within all entries of a map.
// e.g., redactMapPasswords(m, "databases", "connections", "password") redacts
// m["databases"]["connections"]["*"]["password"].
func redactMapPasswords(m map[string]any, mapKey, entriesKey, fieldKey string) {
	outer, ok := m[mapKey].(map[string]any)
	if !ok {
		return
	}
	entries, ok := outer[entriesKey].(map[string]any)
	if !ok {
		return
	}
	for _, v := range entries {
		if entry, ok := v.(map[string]any); ok {
			if _, exists := entry[fieldKey]; exists {
				entry[fieldKey] = "***"
			}
		}
	}
}

// InterfaceURL represents a WebSocket URL for a specific network interface.
type InterfaceURL struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
	URL  string `json:"url"`
}

// getInterfaceURLs returns WebSocket URLs for all non-loopback network interfaces.
// Used when the gateway is bound to 0.0.0.0 to provide routable addresses in QR codes.
// The proto parameter should be "ws" or "wss" depending on TLS configuration.
func getInterfaceURLs(port int, token, proto string) []InterfaceURL {
	var urls []InterfaceURL

	ifaces, err := net.Interfaces()
	if err != nil {
		return urls
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				urls = append(urls, InterfaceURL{
					Name: iface.Name,
					IP:   ipnet.IP.String(),
					URL:  fmt.Sprintf("%s://%s:%d/ws?token=%s", proto, ipnet.IP.String(), port, token),
				})
			}
		}
	}

	return urls
}

// P2PMultiaddr represents a P2P multiaddr for a specific network interface.
type P2PMultiaddr struct {
	Name      string `json:"name"`
	IP        string `json:"ip"`
	Multiaddr string `json:"multiaddr"`
}

// getP2PMultiaddrs returns P2P multiaddrs for all non-loopback network interfaces.
// Used when the P2P node is bound to 0.0.0.0 to provide routable addresses in QR codes.
// If authToken is provided, it's appended as a query parameter for mobile auth.
func getP2PMultiaddrs(port int, peerID string, transports []string, authToken string) []P2PMultiaddr {
	var addrs []P2PMultiaddr

	if port == 0 {
		port = 4001 // default P2P port
	}

	// Determine which transports to include
	if len(transports) == 0 {
		transports = []string{"udx"}
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return addrs
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range ifaceAddrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				ip := ipnet.IP.String()
				// Add multiaddr for each configured transport
				for _, transport := range transports {
					var multiaddr string
					switch transport {
					case "udx":
						multiaddr = fmt.Sprintf("/ip4/%s/udp/%d/udx/p2p/%s", ip, port, peerID)
					case "tcp":
						multiaddr = fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s", ip, port, peerID)
					default:
						continue
					}
					// Append auth token as query parameter if provided
					if authToken != "" {
						multiaddr = fmt.Sprintf("%s?token=%s", multiaddr, authToken)
					}
					addrs = append(addrs, P2PMultiaddr{
						Name:      fmt.Sprintf("%s (%s)", iface.Name, transport),
						IP:        ip,
						Multiaddr: multiaddr,
					})
				}
			}
		}
	}

	return addrs
}
