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

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"

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

	svc := &FileMemoryService{
		db:        db,
		vecIndex:  vecIndex,
		embedder:  embedder,
		workspace: workspace,
		logger:    logger,
		opts:      sopts,
	}

	svc.precomputedTools = buildToolsList(sopts.extractor, sopts.toolCreators, sopts.enabledTools)

	if sopts.extractor != nil {
		cfg := autoMemoryConfig{
			Extractor:        sopts.extractor,
			AsyncMemoryNum:   sopts.asyncMemoryNum,
			MemoryQueueSize:  sopts.memoryQueueSize,
			MemoryJobTimeout: sopts.memoryJobTimeout,
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
	return err
}

// --- memory.Service methods ---

func (s *FileMemoryService) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string, topics []string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	now := time.Now()
	id := generateMemoryID(memoryStr, topics, userKey.AppName, userKey.UserID)
	topicsJSON, _ := json.Marshal(topics)

	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO memories (id, app_name, user_id, content, topics, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, userKey.AppName, userKey.UserID, memoryStr, string(topicsJSON), now, now,
	)
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}

	if s.embedder != nil {
		emb, err := s.embedder.Embed(ctx, memoryStr)
		if err != nil {
			s.logger.Warn("failed to embed memory", "error", err)
		} else {
			s.vecIndex.DeleteEntry(id)
			if err := s.vecIndex.InsertEntry(id, memoryStr, emb); err != nil {
				s.logger.Warn("failed to index memory", "error", err)
			}
		}
	}

	s.appendToMemoryFile(memoryStr, topics)
	return nil
}

func (s *FileMemoryService) UpdateMemory(ctx context.Context, memoryKey memory.Key, memoryStr string, topics []string) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	now := time.Now()
	topicsJSON, _ := json.Marshal(topics)

	res, err := s.db.ExecContext(ctx,
		`UPDATE memories SET content = ?, topics = ?, updated_at = ? WHERE id = ?`,
		memoryStr, string(topicsJSON), now, memoryKey.MemoryID,
	)
	if err != nil {
		return fmt.Errorf("update memory: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}

	if s.embedder != nil {
		emb, err := s.embedder.Embed(ctx, memoryStr)
		if err != nil {
			s.logger.Warn("failed to embed updated memory", "error", err)
		} else {
			s.vecIndex.DeleteEntry(memoryKey.MemoryID)
			if err := s.vecIndex.InsertEntry(memoryKey.MemoryID, memoryStr, emb); err != nil {
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

	if s.embedder != nil {
		emb, err := s.embedder.Embed(ctx, queryStr)
		if err != nil {
			s.logger.Warn("embed query failed, falling back to keyword", "error", err)
		} else {
			results, err := s.vecIndex.HybridSearch(emb, queryStr, 25)
			if err != nil {
				s.logger.Warn("hybrid search failed", "error", err)
			} else {
				return s.searchResultsToEntries(ctx, results)
			}
		}
	}

	results, err := s.vecIndex.KeywordSearch(queryStr, 25)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	return s.searchResultsToEntries(ctx, results)
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

func (s *FileMemoryService) searchResultsToEntries(ctx context.Context, results []SearchResult) ([]*memory.Entry, error) {
	var entries []*memory.Entry
	for _, r := range results {
		if strings.HasPrefix(r.FilePath, "memory:") {
			id := strings.TrimPrefix(r.FilePath, "memory:")
			entry, err := s.getEntryByID(ctx, id)
			if err != nil {
				continue
			}
			entries = append(entries, entry)
			continue
		}
		now := time.Now()
		entries = append(entries, &memory.Entry{
			ID:      fmt.Sprintf("file:%s:%d", r.FilePath, r.LineStart),
			AppName: "kaggen",
			UserID:  "default",
			Memory: &memory.Memory{
				Memory:      r.Content,
				LastUpdated: &now,
			},
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	return entries, nil
}

func (s *FileMemoryService) getEntryByID(ctx context.Context, id string) (*memory.Entry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, app_name, user_id, content, topics, created_at, updated_at
		 FROM memories WHERE id = ?`, id)
	return scanEntryRow(row)
}

func (s *FileMemoryService) appendToMemoryFile(content string, topics []string) {
	path := filepath.Join(s.workspace, "MEMORY.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
