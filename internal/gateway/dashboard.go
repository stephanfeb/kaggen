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

// HandleSessions returns session listing.
func (d *DashboardAPI) HandleSessions(w http.ResponseWriter, r *http.Request) {
	sessionsDir := d.config.SessionsPath()
	appDir := filepath.Join(sessionsDir, AppName)

	type sessionInfo struct {
		UserID       string `json:"user_id"`
		SessionID    string `json:"session_id"`
		UpdatedAt    string `json:"updated_at"`
		MessageCount int    `json:"message_count"`
	}

	var sessions []sessionInfo

	// Walk sessions directory: <appName>/<userID>/<sessionID>/
	userDirs, err := os.ReadDir(appDir)
	if err != nil {
		writeJSON(w, []any{})
		return
	}

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
			info, err := sessDir.Info()
			if err != nil {
				continue
			}
			// Count lines in events.jsonl for message count
			eventsPath := filepath.Join(userPath, sessDir.Name(), "events.jsonl")
			msgCount := countLines(eventsPath)
			sessions = append(sessions, sessionInfo{
				UserID:       userDir.Name(),
				SessionID:    sessDir.Name(),
				UpdatedAt:    info.ModTime().Format(time.RFC3339),
				MessageCount: msgCount,
			})
		}
	}

	// Sort by most recently updated
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})

	// Limit
	if len(sessions) > 100 {
		sessions = sessions[:100]
	}

	writeJSON(w, sessions)
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
	handleFunc("/api/config", d.HandleConfig)
	handleFunc("/api/pipelines", d.HandlePipelines)
	handleFunc("/api/tasks/cancel", d.HandleCancelTask)
	handleFunc("/api/logs", d.HandleLogsSSE)
	handleFunc("/api/files/", d.HandleFiles)
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
