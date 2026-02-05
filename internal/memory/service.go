package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	tmodel "trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/yourusername/kaggen/internal/embedding"
)

var _ memory.Service = (*FileMemoryService)(nil)

// FileMemoryService implements memory.Service backed by SQLite,
// sqlite-vec vector search, FTS5 keyword search, and Markdown export.
type FileMemoryService struct {
	db               *sql.DB
	vecIndex         *VectorIndex
	embedder         embedding.Embedder
	workspace        string
	logger           *slog.Logger
	opts             serviceOpts
	precomputedTools []tool.Tool
	autoWorker       *autoMemoryWorker
	synthesizer      *Synthesizer
}

// --- Constants mirroring memory/internal/memory defaults ---

const defaultMemoryLimit = 1000

var allToolCreators = map[string]memory.ToolCreator{
	memory.AddToolName:    func() tool.Tool { return memorytool.NewAddTool() },
	memory.UpdateToolName: func() tool.Tool { return memorytool.NewUpdateTool() },
	memory.SearchToolName: func() tool.Tool { return memorytool.NewSearchTool() },
	memory.LoadToolName:   func() tool.Tool { return memorytool.NewLoadTool() },
	memory.DeleteToolName: func() tool.Tool { return memorytool.NewDeleteTool() },
	memory.ClearToolName:  func() tool.Tool { return memorytool.NewClearTool() },
}

var defaultEnabledTools = map[string]bool{
	memory.AddToolName:    true,
	memory.UpdateToolName: true,
	memory.SearchToolName: true,
	memory.LoadToolName:   true,
}

var validToolNames = map[string]bool{
	memory.AddToolName: true, memory.UpdateToolName: true,
	memory.DeleteToolName: true, memory.ClearToolName: true,
	memory.SearchToolName: true, memory.LoadToolName: true,
}

// autoModeExposedTools — in auto mode only these are shown to the agent.
var autoModeExposedTools = map[string]bool{
	memory.SearchToolName: true,
	memory.LoadToolName:   true,
}

var autoModeDefaultEnabledTools = map[string]bool{
	memory.AddToolName:    true,
	memory.UpdateToolName: true,
	memory.DeleteToolName: true,
	memory.ClearToolName:  false,
	memory.SearchToolName: true,
	memory.LoadToolName:   false,
}

// --- Options ---

type serviceOpts struct {
	memoryLimit       int
	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]bool
	userExplicitlySet map[string]bool
	extractor         extractor.MemoryExtractor
	asyncMemoryNum    int
	memoryQueueSize   int
	memoryJobTimeout  time.Duration
	enqueueRetryWait  time.Duration // How long to wait when queue is full before dropping (0 = no retry)
	model             tmodel.Model
	synthesisInterval time.Duration
}

var defaultServiceOpts = serviceOpts{
	memoryLimit:    defaultMemoryLimit,
	toolCreators:   allToolCreators,
	enabledTools:   defaultEnabledTools,
	asyncMemoryNum: defaultAsyncMemoryNum,
}

func (o serviceOpts) clone() serviceOpts {
	opts := o
	opts.toolCreators = make(map[string]memory.ToolCreator, len(o.toolCreators))
	for k, v := range o.toolCreators {
		opts.toolCreators[k] = v
	}
	opts.enabledTools = make(map[string]bool, len(o.enabledTools))
	for k, v := range o.enabledTools {
		opts.enabledTools[k] = v
	}
	opts.userExplicitlySet = make(map[string]bool)
	return opts
}

// ServiceOpt configures the FileMemoryService.
type ServiceOpt func(*serviceOpts)

// WithExtractor enables auto-extraction mode.
func WithExtractor(e extractor.MemoryExtractor) ServiceOpt {
	return func(o *serviceOpts) { o.extractor = e }
}

// WithAsyncMemoryNum sets the number of async extraction workers.
func WithAsyncMemoryNum(n int) ServiceOpt {
	return func(o *serviceOpts) {
		if n >= 1 {
			o.asyncMemoryNum = n
		}
	}
}

// WithMemoryQueueSize sets the extraction job queue size.
func WithMemoryQueueSize(n int) ServiceOpt {
	return func(o *serviceOpts) {
		if n >= 1 {
			o.memoryQueueSize = n
		}
	}
}

// WithMemoryJobTimeout sets the timeout for each extraction job.
func WithMemoryJobTimeout(d time.Duration) ServiceOpt {
	return func(o *serviceOpts) { o.memoryJobTimeout = d }
}

// WithEnqueueRetryWait sets how long to wait when the extraction queue is full
// before dropping the job. Default is 0 (no retry, immediate drop).
// Setting this to a small value like 500ms allows brief queue pressure to clear.
func WithEnqueueRetryWait(d time.Duration) ServiceOpt {
	return func(o *serviceOpts) { o.enqueueRetryWait = d }
}

// WithModel provides a model for background synthesis.
func WithModel(m tmodel.Model) ServiceOpt {
	return func(o *serviceOpts) { o.model = m }
}

// WithSynthesisInterval sets how often entity summaries are synthesized.
func WithSynthesisInterval(d time.Duration) ServiceOpt {
	return func(o *serviceOpts) { o.synthesisInterval = d }
}

// WithMemoryLimit sets the maximum number of memories per user.
func WithMemoryLimit(n int) ServiceOpt {
	return func(o *serviceOpts) { o.memoryLimit = n }
}

// WithToolEnabled enables or disables a specific memory tool.
func WithToolEnabled(name string, enabled bool) ServiceOpt {
	return func(o *serviceOpts) {
		if validToolNames[name] {
			o.enabledTools[name] = enabled
			o.userExplicitlySet[name] = true
		}
	}
}

// --- Constructor ---

// NewFileMemoryService creates a memory service backed by SQLite + sqlite-vec + Markdown.
func NewFileMemoryService(
	db *sql.DB,
	vecIndex *VectorIndex,
	embedder embedding.Embedder,
	workspace string,
	logger *slog.Logger,
	opts ...ServiceOpt,
) (*FileMemoryService, error) {
	sopts := defaultServiceOpts.clone()
	for _, o := range opts {
		o(&sopts)
	}

	if sopts.extractor != nil {
		applyAutoModeDefaults(sopts.enabledTools, sopts.userExplicitlySet)
	}

	if err := initMemoriesTable(db); err != nil {
		return nil, fmt.Errorf("init memories table: %w", err)
	}
	if err := initEntityTables(db); err != nil {
		return nil, fmt.Errorf("init entity tables: %w", err)
	}

	svc := &FileMemoryService{
		db:        db,
		vecIndex:  vecIndex,
		embedder:  embedder,
		workspace: workspace,
		logger:    logger,
		opts:      sopts,
	}

	svc.precomputedTools = buildToolsList(sopts.extractor, sopts.toolCreators, sopts.enabledTools)

	// Start background entity synthesis if model is provided
	if sopts.model != nil {
		interval := sopts.synthesisInterval
		if interval == 0 {
			interval = 1 * time.Hour
		}
		svc.synthesizer = NewSynthesizer(db, sopts.model, svc, logger, interval)
		svc.synthesizer.Start()
	}

	if sopts.extractor != nil {
		cfg := autoMemoryConfig{
			Extractor:        sopts.extractor,
			AsyncMemoryNum:   sopts.asyncMemoryNum,
			MemoryQueueSize:  sopts.memoryQueueSize,
			MemoryJobTimeout: sopts.memoryJobTimeout,
			EnqueueRetryWait: sopts.enqueueRetryWait,
		}
		svc.autoWorker = newAutoMemoryWorker(cfg, svc, logger)
		svc.autoWorker.Start()
	}

	return svc, nil
}

func initMemoriesTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS memories (
		id TEXT PRIMARY KEY,
		app_name TEXT NOT NULL,
		user_id TEXT NOT NULL,
		content TEXT NOT NULL,
		topics TEXT DEFAULT '[]',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		return err
	}

	// Idempotent schema migration: add epistemic columns if missing.
	migrate := []string{
		"ALTER TABLE memories ADD COLUMN memory_type TEXT DEFAULT 'fact'",
		"ALTER TABLE memories ADD COLUMN confidence REAL DEFAULT 1.0",
		"ALTER TABLE memories ADD COLUMN occurred_start TEXT",
		"ALTER TABLE memories ADD COLUMN occurred_end TEXT",
		"ALTER TABLE memories ADD COLUMN entities_json TEXT DEFAULT '[]'",
	}
	for _, stmt := range migrate {
		// SQLite returns "duplicate column name" if already exists — ignore that.
		if _, err := db.Exec(stmt); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				return fmt.Errorf("migrate: %w", err)
			}
		}
	}
	return nil
}

// --- memory.Service methods ---

func (s *FileMemoryService) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string, topics []string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	// Parse structured metadata from content prefix and topics
	cleanContent, contentMeta := ParseStructuredContent(memoryStr)
	semanticTopics, topicsMeta := ParseMetaTopics(topics)
	meta := MergeMetadata(contentMeta, topicsMeta)

	now := time.Now()
	id := generateMemoryID(cleanContent, semanticTopics, userKey.AppName, userKey.UserID)
	topicsJSON, _ := json.Marshal(semanticTopics)
	entitiesJSON, _ := json.Marshal(meta.Entities)

	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO memories (id, app_name, user_id, content, topics, created_at, updated_at,
		 memory_type, confidence, occurred_start, occurred_end, entities_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userKey.AppName, userKey.UserID, cleanContent, string(topicsJSON), now, now,
		string(meta.Type), meta.Confidence, nullStr(meta.OccurredStart), nullStr(meta.OccurredEnd), string(entitiesJSON),
	)
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}

	// Link entities to memory and update co-occurrence graph
	if len(meta.Entities) > 0 {
		if entityIDs, err := resolveEntities(ctx, s.db, meta.Entities); err != nil {
			s.logger.Warn("entity resolution failed", "error", err)
		} else {
			if err := linkMemoryEntities(ctx, s.db, id, entityIDs); err != nil {
				s.logger.Warn("entity linking failed", "error", err)
			}
			if err := updateCooccurrences(ctx, s.db, entityIDs); err != nil {
				s.logger.Warn("co-occurrence update failed", "error", err)
			}
		}
	}

	if s.embedder != nil {
		emb, err := s.embedder.Embed(ctx, cleanContent)
		if err != nil {
			s.logger.Warn("failed to embed memory", "error", err)
		} else {
			s.vecIndex.DeleteEntry(id)
			if err := s.vecIndex.InsertEntry(id, cleanContent, emb); err != nil {
				s.logger.Warn("failed to index memory", "error", err)
			}
		}
	}

	s.appendToMemoryFile(cleanContent, semanticTopics)
	return nil
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func (s *FileMemoryService) UpdateMemory(ctx context.Context, memoryKey memory.Key, memoryStr string, topics []string) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	cleanContent, contentMeta := ParseStructuredContent(memoryStr)
	semanticTopics, topicsMeta := ParseMetaTopics(topics)
	meta := MergeMetadata(contentMeta, topicsMeta)

	// For opinions, evolve confidence smoothly against old value
	if meta.Type == MemoryTypeOpinion {
		var oldConf float64
		err := s.db.QueryRowContext(ctx,
			`SELECT confidence FROM memories WHERE id = ?`, memoryKey.MemoryID,
		).Scan(&oldConf)
		if err == nil {
			meta.Confidence = EvolveConfidence(oldConf, meta.Confidence)
		}
	}

	now := time.Now()
	topicsJSON, _ := json.Marshal(semanticTopics)
	entitiesJSON, _ := json.Marshal(meta.Entities)

	res, err := s.db.ExecContext(ctx,
		`UPDATE memories SET content = ?, topics = ?, updated_at = ?,
		 memory_type = ?, confidence = ?, occurred_start = ?, occurred_end = ?, entities_json = ?
		 WHERE id = ?`,
		cleanContent, string(topicsJSON), now,
		string(meta.Type), meta.Confidence, nullStr(meta.OccurredStart), nullStr(meta.OccurredEnd), string(entitiesJSON),
		memoryKey.MemoryID,
	)
	if err != nil {
		return fmt.Errorf("update memory: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}

	// Re-link entities
	if len(meta.Entities) > 0 {
		if entityIDs, err := resolveEntities(ctx, s.db, meta.Entities); err != nil {
			s.logger.Warn("entity resolution failed", "error", err)
		} else {
			if err := linkMemoryEntities(ctx, s.db, memoryKey.MemoryID, entityIDs); err != nil {
				s.logger.Warn("entity linking failed", "error", err)
			}
			if err := updateCooccurrences(ctx, s.db, entityIDs); err != nil {
				s.logger.Warn("co-occurrence update failed", "error", err)
			}
		}
	}

	if s.embedder != nil {
		emb, err := s.embedder.Embed(ctx, cleanContent)
		if err != nil {
			s.logger.Warn("failed to embed updated memory", "error", err)
		} else {
			s.vecIndex.DeleteEntry(memoryKey.MemoryID)
			if err := s.vecIndex.InsertEntry(memoryKey.MemoryID, cleanContent, emb); err != nil {
				s.logger.Warn("failed to re-index memory", "error", err)
			}
		}
	}
	return nil
}

func (s *FileMemoryService) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, memoryKey.MemoryID)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}

	s.vecIndex.DeleteEntry(memoryKey.MemoryID)
	return nil
}

func (s *FileMemoryService) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM memories WHERE app_name = ? AND user_id = ?`,
		userKey.AppName, userKey.UserID,
	)
	if err != nil {
		return fmt.Errorf("query memories: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()

	_, err = s.db.ExecContext(ctx,
		`DELETE FROM memories WHERE app_name = ? AND user_id = ?`,
		userKey.AppName, userKey.UserID,
	)
	if err != nil {
		return fmt.Errorf("clear memories: %w", err)
	}

	for _, id := range ids {
		s.vecIndex.DeleteEntry(id)
	}
	return nil
}

func (s *FileMemoryService) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	query := `SELECT id, app_name, user_id, content, topics, created_at, updated_at
		FROM memories WHERE app_name = ? AND user_id = ?
		ORDER BY updated_at DESC`
	args := []any{userKey.AppName, userKey.UserID}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read memories: %w", err)
	}
	defer rows.Close()

	var entries []*memory.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *FileMemoryService) SearchMemories(ctx context.Context, userKey memory.UserKey, queryStr string) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	queryStr = strings.TrimSpace(queryStr)
	if queryStr == "" {
		return nil, nil
	}

	return s.fourWaySearch(ctx, userKey, queryStr, 25)
}

func (s *FileMemoryService) Tools() []tool.Tool {
	return s.precomputedTools
}

func (s *FileMemoryService) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	if s.autoWorker == nil {
		return nil
	}
	return s.autoWorker.EnqueueJob(ctx, sess)
}

func (s *FileMemoryService) Close() error {
	if s.synthesizer != nil {
		s.synthesizer.Stop()
	}
	if s.autoWorker != nil {
		s.autoWorker.Stop()
	}
	return nil
}

// --- Helpers ---

// generateMemoryID produces a deterministic ID from content + topics + user context.
// Matches the algorithm in trpc-agent-go/memory/internal/memory.GenerateMemoryID.
func generateMemoryID(content string, topics []string, appName, userID string) string {
	var b strings.Builder
	b.WriteString("memory:")
	b.WriteString(content)
	if len(topics) > 0 {
		sorted := make([]string, len(topics))
		copy(sorted, topics)
		slices.Sort(sorted)
		b.WriteString("|topics:")
		b.WriteString(strings.Join(sorted, ","))
	}
	b.WriteString("|app:")
	b.WriteString(appName)
	b.WriteString("|user:")
	b.WriteString(userID)
	hash := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", hash)
}

// applyAutoModeDefaults sets auto mode tool defaults for tools not explicitly set.
func applyAutoModeDefaults(enabledTools, userExplicitlySet map[string]bool) {
	for name, defaultVal := range autoModeDefaultEnabledTools {
		if userExplicitlySet[name] {
			continue
		}
		enabledTools[name] = defaultVal
	}
}

// buildToolsList builds the pre-computed tool list based on mode and enabled tools.
func buildToolsList(ext extractor.MemoryExtractor, creators map[string]memory.ToolCreator, enabled map[string]bool) []tool.Tool {
	names := make([]string, 0, len(creators))
	for name := range creators {
		if ext != nil {
			// Auto mode: only expose search/load.
			if !autoModeExposedTools[name] || !enabled[name] {
				continue
			}
		} else {
			if !enabled[name] {
				continue
			}
		}
		names = append(names, name)
	}
	slices.Sort(names)
	tools := make([]tool.Tool, 0, len(names))
	for _, name := range names {
		tools = append(tools, creators[name]())
	}
	return tools
}

func (s *FileMemoryService) getEntryByID(ctx context.Context, id string) (*memory.Entry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, app_name, user_id, content, topics, created_at, updated_at
		 FROM memories WHERE id = ?`, id)
	return scanEntryRow(row)
}

func (s *FileMemoryService) appendToMemoryFile(content string, topics []string) {
	path := filepath.Join(s.workspace, "MEMORY.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) // Secure: owner-only file
	if err != nil {
		s.logger.Warn("failed to open MEMORY.md", "error", err)
		return
	}
	defer f.Close()

	line := content
	if len(topics) > 0 {
		line = fmt.Sprintf("[%s] %s", strings.Join(topics, ", "), content)
	}
	timestamp := time.Now().Format("2006-01-02 15:04")
	fmt.Fprintf(f, "\n- %s: %s\n", timestamp, line)
}

func scanEntry(rows *sql.Rows) (*memory.Entry, error) {
	var id, appName, userID, content, topicsJSON string
	var createdAt, updatedAt time.Time
	if err := rows.Scan(&id, &appName, &userID, &content, &topicsJSON, &createdAt, &updatedAt); err != nil {
		return nil, fmt.Errorf("scan memory: %w", err)
	}
	return buildEntry(id, appName, userID, content, topicsJSON, createdAt, updatedAt), nil
}

func scanEntryRow(row *sql.Row) (*memory.Entry, error) {
	var id, appName, userID, content, topicsJSON string
	var createdAt, updatedAt time.Time
	if err := row.Scan(&id, &appName, &userID, &content, &topicsJSON, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	return buildEntry(id, appName, userID, content, topicsJSON, createdAt, updatedAt), nil
}

func buildEntry(id, appName, userID, content, topicsJSON string, createdAt, updatedAt time.Time) *memory.Entry {
	var topics []string
	json.Unmarshal([]byte(topicsJSON), &topics)
	return &memory.Entry{
		ID:      id,
		AppName: appName,
		UserID:  userID,
		Memory: &memory.Memory{
			Memory:      content,
			Topics:      topics,
			LastUpdated: &updatedAt,
		},
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
}

// BeforeCompaction implements session.CompactionHook.
// It performs synchronous memory extraction from events that are about to be
// removed during session compaction. This prevents memory loss when async
// extraction hasn't completed before compaction.
func (s *FileMemoryService) BeforeCompaction(ctx context.Context, sess *session.Session, eventsToRemove []event.Event) error {
	if s.opts.extractor == nil {
		// No extractor configured - nothing to do.
		return nil
	}

	userKey := memory.UserKey{AppName: sess.AppName, UserID: sess.UserID}
	if userKey.AppName == "" || userKey.UserID == "" {
		return nil
	}

	// Convert events to messages for extraction.
	messages := eventsToMessages(eventsToRemove)
	if len(messages) == 0 {
		s.logger.Debug("pre-compaction: no extractable messages in events to remove")
		return nil
	}

	// Check for at least one user message (extractor requirement).
	hasUser := false
	for _, m := range messages {
		if m.Role == tmodel.RoleUser {
			hasUser = true
			break
		}
	}
	if !hasUser {
		s.logger.Debug("pre-compaction: no user messages, skipping extraction")
		return nil
	}

	s.logger.Info("pre-compaction memory extraction starting",
		"session_id", sess.ID,
		"events", len(eventsToRemove),
		"messages", len(messages))

	// Read existing memories for duplicate detection.
	var existing []*memory.Entry
	if s.db != nil {
		var err error
		existing, err = s.ReadMemories(ctx, userKey, 0)
		if err != nil {
			s.logger.Warn("pre-compaction: failed to read existing memories", "error", err)
			existing = nil
		}
	}

	// Run synchronous extraction.
	ops, err := s.opts.extractor.Extract(ctx, messages, existing)
	if err != nil {
		return fmt.Errorf("pre-compaction extraction failed: %w", err)
	}

	// Execute operations.
	var addCount, updateCount int
	for _, op := range ops {
		switch op.Type {
		case extractor.OperationAdd:
			if err := s.AddMemory(ctx, userKey, op.Memory, op.Topics); err != nil {
				s.logger.Warn("pre-compaction: add failed", "error", err)
			} else {
				addCount++
			}
		case extractor.OperationUpdate:
			key := memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: op.MemoryID}
			if err := s.UpdateMemory(ctx, key, op.Memory, op.Topics); err != nil {
				s.logger.Warn("pre-compaction: update failed", "error", err)
			} else {
				updateCount++
			}
		case extractor.OperationDelete:
			key := memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: op.MemoryID}
			if err := s.DeleteMemory(ctx, key); err != nil {
				s.logger.Warn("pre-compaction: delete failed", "error", err)
			}
		case extractor.OperationClear:
			if err := s.ClearMemories(ctx, userKey); err != nil {
				s.logger.Warn("pre-compaction: clear failed", "error", err)
			}
		}
	}

	s.logger.Info("pre-compaction memory extraction complete",
		"session_id", sess.ID,
		"added", addCount,
		"updated", updateCount,
		"total_ops", len(ops))

	return nil
}

// eventsToMessages converts session events to model messages for extraction.
// Filters out tool calls and empty messages to match async extraction behavior.
func eventsToMessages(events []event.Event) []tmodel.Message {
	var messages []tmodel.Message
	for _, e := range events {
		if e.Response == nil {
			continue
		}
		for _, choice := range e.Response.Choices {
			msg := choice.Message
			// Skip tool-related messages.
			if msg.Role == tmodel.RoleTool || msg.ToolID != "" {
				continue
			}
			// Skip empty messages.
			if msg.Content == "" && len(msg.ContentParts) == 0 {
				continue
			}
			// Skip messages with tool calls (these are assistant messages initiating tool use).
			if len(msg.ToolCalls) > 0 {
				continue
			}
			messages = append(messages, msg)
		}
	}
	return messages
}
