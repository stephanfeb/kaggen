package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tmodel "trpc.group/trpc-go/trpc-agent-go/model"
	tmemory "trpc.group/trpc-go/trpc-agent-go/memory"
)

// Synthesizer periodically generates entity summaries from linked memories
// and stores them as observation-type memory entries.
type Synthesizer struct {
	db       *sql.DB
	model    tmodel.Model
	svc      *FileMemoryService
	logger   *slog.Logger
	interval time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// NewSynthesizer creates a new Synthesizer.
func NewSynthesizer(db *sql.DB, m tmodel.Model, svc *FileMemoryService, logger *slog.Logger, interval time.Duration) *Synthesizer {
	return &Synthesizer{
		db:       db,
		model:    m,
		svc:      svc,
		logger:   logger,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start begins the background synthesis loop.
func (s *Synthesizer) Start() {
	go s.loop()
}

// Stop halts the synthesis loop and waits for it to finish.
func (s *Synthesizer) Stop() {
	close(s.stopCh)
	<-s.doneCh
}

func (s *Synthesizer) loop() {
	defer close(s.doneCh)

	timer := time.NewTimer(s.interval)
	defer timer.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-timer.C:
			if err := s.synthesize(); err != nil {
				s.logger.Warn("synthesis cycle failed", "error", err)
			}
			timer.Reset(s.interval)
		}
	}
}

func (s *Synthesizer) synthesize() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.name FROM entities e
		JOIN memory_entities me ON e.id = me.entity_id
		JOIN memories m ON me.memory_id = m.id
		WHERE e.summary_updated_at IS NULL
		   OR e.summary_updated_at < m.updated_at
		GROUP BY e.id
		HAVING COUNT(me.memory_id) >= 3
		LIMIT 10
	`)
	if err != nil {
		return fmt.Errorf("query entities for synthesis: %w", err)
	}
	defer rows.Close()

	type entityInfo struct {
		id   int64
		name string
	}
	var entities []entityInfo
	for rows.Next() {
		var e entityInfo
		if err := rows.Scan(&e.id, &e.name); err != nil {
			return err
		}
		entities = append(entities, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, entity := range entities {
		if err := s.synthesizeEntity(ctx, entity.id, entity.name); err != nil {
			s.logger.Warn("entity synthesis failed", "entity", entity.name, "error", err)
		}
	}
	return nil
}

func (s *Synthesizer) synthesizeEntity(ctx context.Context, entityID int64, entityName string) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.content, m.memory_type, m.confidence
		FROM memories m
		JOIN memory_entities me ON m.id = me.memory_id
		WHERE me.entity_id = ?
		ORDER BY m.updated_at DESC LIMIT 20
	`, entityID)
	if err != nil {
		return fmt.Errorf("gather memories for %q: %w", entityName, err)
	}
	defer rows.Close()

	var memories []string
	for rows.Next() {
		var content, memType string
		var confidence float64
		if err := rows.Scan(&content, &memType, &confidence); err != nil {
			return err
		}
		memories = append(memories, fmt.Sprintf("- [%s, conf:%.1f] %s", memType, confidence, content))
	}
	if len(memories) == 0 {
		return nil
	}

	prompt := fmt.Sprintf(
		"Summarize what is known about %q based on these memories. "+
			"Write a neutral, factual summary paragraph. Do not speculate.\n\n%s",
		entityName, strings.Join(memories, "\n"),
	)

	req := &tmodel.Request{
		Messages: []tmodel.Message{
			{Role: tmodel.RoleUser, Content: prompt},
		},
	}

	respCh, err := s.model.GenerateContent(ctx, req)
	if err != nil {
		return fmt.Errorf("LLM synthesis for %q: %w", entityName, err)
	}

	var summary string
	for resp := range respCh {
		if resp.Error != nil {
			return fmt.Errorf("LLM error for %q: %s", entityName, resp.Error.Message)
		}
		for _, choice := range resp.Choices {
			summary += choice.Message.Content
		}
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`UPDATE entities SET summary = ?, summary_updated_at = ?, updated_at = ? WHERE id = ?`,
		summary, now, now, entityID,
	)
	if err != nil {
		return fmt.Errorf("update entity summary: %w", err)
	}

	obsContent := fmt.Sprintf("[type:observation|ent:%s] %s", entityName, summary)
	if err := s.svc.AddMemory(ctx,
		defaultSynthesisUserKey,
		obsContent,
		[]string{"_type:observation", "entity_summary", strings.ToLower(entityName)},
	); err != nil {
		return fmt.Errorf("store observation for %q: %w", entityName, err)
	}

	s.logger.Info("synthesized entity summary", "entity", entityName)
	return nil
}

var defaultSynthesisUserKey = tmemory.UserKey{AppName: "kaggen", UserID: "system"}
