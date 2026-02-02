package gateway

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/backlog"
	"github.com/yourusername/kaggen/internal/config"
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
	logStreamer     *LogStreamer
	startTime      time.Time
	wsClientCount  func() int
	taskBroadcast  func(data []byte) // broadcasts to all WS clients
	handler        *Handler           // for approval InjectCompletion; set via SetHandler
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
	return &DashboardAPI{
		agentProvider:  provider,
		backlogStore:   store,
		sessionService: ss,
		config:         cfg,
		logStreamer:     ls,
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
	w.Header().Set("Access-Control-Allow-Origin", "*")

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
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
	w.Header().Set("Access-Control-Allow-Origin", "*")

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
	handleFunc("/api/backlog", d.HandleBacklog)
	handleFunc("/api/backlog/plan", d.HandlePlanDetail)
	handleFunc("/api/skills", d.HandleSkills)
	handleFunc("/api/sessions", d.HandleSessions)
	handleFunc("/api/sessions/messages", d.HandleSessionMessages)
	handleFunc("/api/sessions/rename", d.HandleSessionRename)
	handleFunc("/api/sessions/delete", d.HandleSessionDelete)
	handleFunc("/api/sessions/archive", d.HandleSessionArchive)
	handleFunc("/api/config", d.HandleConfig)
	handleFunc("/api/pipelines", d.HandlePipelines)
	handleFunc("/api/tasks/cancel", d.HandleCancelTask)
	handleFunc("/api/logs", d.HandleLogsSSE)
	handleFunc("/api/approvals", d.HandleApprovals)
	handleFunc("/api/approvals/approve", d.HandleApprovalAction)
	handleFunc("/api/approvals/reject", d.HandleApprovalAction)
	handleFunc("/api/files/", d.HandleFiles)
}

// SetHandler stores the message handler for approval completion injection.
func (d *DashboardAPI) SetHandler(h *Handler) {
	d.handler = h
}

// HandleApprovals returns pending approval requests.
func (d *DashboardAPI) HandleApprovals(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	store := d.agentProvider.InFlightStore()
	all := store.List(agent.TaskPendingApproval)

	type approvalOut struct {
		ID          string                  `json:"id"`
		ToolName    string                  `json:"tool_name"`
		SkillName   string                  `json:"skill_name"`
		Description string                  `json:"description"`
		Arguments   string                  `json:"arguments"`
		SessionID   string                  `json:"session_id"`
		UserID      string                  `json:"user_id"`
		RequestedAt string                  `json:"requested_at"`
		TimeoutAt   string                  `json:"timeout_at"`
	}

	out := make([]approvalOut, 0, len(all))
	for _, t := range all {
		if t.ApprovalRequest == nil {
			continue
		}
		out = append(out, approvalOut{
			ID:          t.ID,
			ToolName:    t.ApprovalRequest.ToolName,
			SkillName:   t.ApprovalRequest.SkillName,
			Description: t.ApprovalRequest.Description,
			Arguments:   t.ApprovalRequest.Arguments,
			SessionID:   t.SessionID,
			UserID:      t.UserID,
			RequestedAt: t.ApprovalRequest.RequestedAt.Format(time.RFC3339),
			TimeoutAt:   t.TimeoutAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, out)
}

// HandleApprovalAction handles POST /api/approvals/approve and /api/approvals/reject.
func (d *DashboardAPI) HandleApprovalAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
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

	// Inject completion back to the coordinator.
	if d.handler != nil {
		_ = d.handler.InjectCompletion(r.Context(), task.SessionID, task.UserID, req.ID, task.AgentName, result)
	}

	writeJSON(w, map[string]string{"status": action, "id": req.ID})
}

// HandleFiles serves published files from ~/.kaggen/public/.
// Only files that have been explicitly published via extractSendFiles are accessible.
// No server filesystem paths are exposed — clients request by filename only.
func (d *DashboardAPI) HandleFiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

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

// --- helpers ---

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
