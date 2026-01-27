package memory

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tmemory "trpc.group/trpc-go/trpc-agent-go/memory"
)

const rrfK = 60 // Reciprocal Rank Fusion constant

// fourWaySearch runs vector, keyword, graph, and temporal search channels
// concurrently and merges results using Reciprocal Rank Fusion.
func (s *FileMemoryService) fourWaySearch(ctx context.Context, userKey tmemory.UserKey, query string, limit int) ([]*tmemory.Entry, error) {
	fetchLimit := limit * 3 // over-fetch per channel for better fusion

	type channelResult struct {
		ids []string // ordered by channel-specific ranking
	}

	var (
		mu       sync.Mutex
		channels []channelResult
		wg       sync.WaitGroup
	)

	// Channel 1: Vector search
	if s.embedder != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			emb, err := s.embedder.Embed(ctx, query)
			if err != nil {
				s.logger.Warn("vector search embed failed", "error", err)
				return
			}
			results, err := s.vecIndex.Search(emb, fetchLimit)
			if err != nil {
				s.logger.Warn("vector search failed", "error", err)
				return
			}
			ids := searchResultIDs(results)
			mu.Lock()
			channels = append(channels, channelResult{ids: ids})
			mu.Unlock()
		}()
	}

	// Channel 2: Keyword search
	wg.Add(1)
	go func() {
		defer wg.Done()
		results, err := s.vecIndex.KeywordSearch(query, fetchLimit)
		if err != nil {
			s.logger.Warn("keyword search failed", "error", err)
			return
		}
		ids := searchResultIDs(results)
		mu.Lock()
		channels = append(channels, channelResult{ids: ids})
		mu.Unlock()
	}()

	// Channel 3: Graph search
	wg.Add(1)
	go func() {
		defer wg.Done()
		ranked, err := graphSearch(ctx, s.db, query, fetchLimit)
		if err != nil {
			s.logger.Warn("graph search failed", "error", err)
			return
		}
		ids := make([]string, len(ranked))
		for i, r := range ranked {
			ids[i] = r.MemoryID
		}
		mu.Lock()
		channels = append(channels, channelResult{ids: ids})
		mu.Unlock()
	}()

	// Channel 4: Temporal search
	wg.Add(1)
	go func() {
		defer wg.Done()
		start, end, found := ParseTemporalQuery(query, time.Now())
		if !found {
			return
		}
		ids, err := temporalSearch(ctx, s.db, userKey, start, end, fetchLimit)
		if err != nil {
			s.logger.Warn("temporal search failed", "error", err)
			return
		}
		mu.Lock()
		channels = append(channels, channelResult{ids: ids})
		mu.Unlock()
	}()

	wg.Wait()

	// Reciprocal Rank Fusion
	scores := make(map[string]float64)
	for _, ch := range channels {
		for rank, id := range ch.ids {
			scores[id] += 1.0 / float64(rrfK+rank+1)
		}
	}

	// Sort by RRF score
	type scoredID struct {
		id    string
		score float64
	}
	sorted := make([]scoredID, 0, len(scores))
	for id, score := range scores {
		sorted = append(sorted, scoredID{id, score})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].score > sorted[j].score
	})
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}

	// Fetch full entries
	entries := make([]*tmemory.Entry, 0, len(sorted))
	for _, item := range sorted {
		entry, err := s.lookupEntry(ctx, item.id)
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// lookupEntry is a method alias to avoid collision with the scored struct.
func (svc *FileMemoryService) lookupEntry(ctx context.Context, id string) (*tmemory.Entry, error) {
	// Memory entries have plain IDs; file entries have "file:" prefix
	if strings.HasPrefix(id, "file:") {
		return nil, fmt.Errorf("file entries not supported in 4-way search")
	}
	return svc.getEntryByID(ctx, id)
}

// searchResultIDs extracts memory IDs from search results.
func searchResultIDs(results []SearchResult) []string {
	ids := make([]string, 0, len(results))
	for _, r := range results {
		if strings.HasPrefix(r.FilePath, "memory:") {
			ids = append(ids, strings.TrimPrefix(r.FilePath, "memory:"))
		}
	}
	return ids
}

// temporalSearch finds memories whose occurred_start/occurred_end overlap the given range.
func temporalSearch(ctx context.Context, db *sql.DB, userKey tmemory.UserKey, start, end time.Time, limit int) ([]string, error) {
	startStr := start.Format("2006-01-02")
	endStr := end.Format("2006-01-02")

	rows, err := db.QueryContext(ctx,
		`SELECT id FROM memories
		 WHERE app_name = ? AND user_id = ?
		   AND occurred_start IS NOT NULL
		   AND occurred_start <= ?
		   AND (occurred_end IS NULL OR occurred_end >= ?)
		 ORDER BY occurred_start DESC
		 LIMIT ?`,
		userKey.AppName, userKey.UserID, endStr, startStr, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("temporal search: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
