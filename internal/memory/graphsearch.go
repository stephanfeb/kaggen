package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// rankedMemory holds a memory ID with an activation score from graph search.
type rankedMemory struct {
	MemoryID string
	Score    float64
}

// graphSearch performs spreading-activation search through the entity graph.
// It matches query words against entity names, then walks co-occurrence edges
// up to 2 hops, collecting linked memories weighted by hop distance.
func graphSearch(ctx context.Context, db *sql.DB, query string, limit int) ([]rankedMemory, error) {
	// Step 1: Find entities matching query words (case-insensitive substring match)
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil, nil
	}

	seedIDs, err := matchEntities(ctx, db, words)
	if err != nil {
		return nil, err
	}
	if len(seedIDs) == 0 {
		return nil, nil
	}

	// Step 2: BFS 2 hops through entity_relations
	// activation[entityID] = score
	activation := make(map[int64]float64)
	for _, id := range seedIDs {
		activation[id] = 1.0
	}

	// 1-hop neighbors
	hop1, err := spreadActivation(ctx, db, seedIDs)
	if err != nil {
		return nil, err
	}
	for eid, weight := range hop1 {
		if _, exists := activation[eid]; !exists {
			activation[eid] = 0.5 * weight
		}
	}

	// 2-hop neighbors (from hop1 entities)
	hop1IDs := make([]int64, 0, len(hop1))
	for eid := range hop1 {
		hop1IDs = append(hop1IDs, eid)
	}
	if len(hop1IDs) > 0 {
		hop2, err := spreadActivation(ctx, db, hop1IDs)
		if err != nil {
			return nil, err
		}
		for eid, weight := range hop2 {
			if _, exists := activation[eid]; !exists {
				activation[eid] = 0.25 * weight
			}
		}
	}

	// Step 3: Collect memories linked to activated entities
	allEntityIDs := make([]int64, 0, len(activation))
	for eid := range activation {
		allEntityIDs = append(allEntityIDs, eid)
	}

	memScores, err := collectLinkedMemories(ctx, db, allEntityIDs, activation)
	if err != nil {
		return nil, err
	}

	// Sort by score descending
	results := make([]rankedMemory, 0, len(memScores))
	for mid, score := range memScores {
		results = append(results, rankedMemory{MemoryID: mid, Score: score})
	}
	// Simple selection sort for small result sets
	for i := 0; i < len(results) && i < limit; i++ {
		maxIdx := i
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[maxIdx].Score {
				maxIdx = j
			}
		}
		results[i], results[maxIdx] = results[maxIdx], results[i]
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// matchEntities finds entity IDs whose names contain any of the query words.
func matchEntities(ctx context.Context, db *sql.DB, words []string) ([]int64, error) {
	conditions := make([]string, len(words))
	args := make([]any, len(words))
	for i, w := range words {
		conditions[i] = "LOWER(name) LIKE ?"
		args[i] = "%" + w + "%"
	}
	query := `SELECT id FROM entities WHERE ` + strings.Join(conditions, " OR ")
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("match entities: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// spreadActivation returns neighbors of the given entity IDs with their normalized weight.
func spreadActivation(ctx context.Context, db *sql.DB, entityIDs []int64) (map[int64]float64, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(entityIDs))
	args := make([]any, len(entityIDs)*2)
	for i, id := range entityIDs {
		placeholders[i] = "?"
		args[i] = id
		args[len(entityIDs)+i] = id
	}
	ph := strings.Join(placeholders, ",")
	query := fmt.Sprintf(
		`SELECT CASE WHEN entity_a IN (%s) THEN entity_b ELSE entity_a END AS neighbor,
		        weight
		 FROM entity_relations
		 WHERE entity_a IN (%s) OR entity_b IN (%s)`, ph, ph, ph,
	)
	// We need 3 sets of args for the 3 IN clauses
	allArgs := make([]any, 0, len(entityIDs)*3)
	for i := 0; i < 3; i++ {
		for _, id := range entityIDs {
			allArgs = append(allArgs, id)
		}
	}

	rows, err := db.QueryContext(ctx, query, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("spread activation: %w", err)
	}
	defer rows.Close()

	neighbors := make(map[int64]float64)
	for rows.Next() {
		var neighbor int64
		var weight float64
		if err := rows.Scan(&neighbor, &weight); err != nil {
			return nil, err
		}
		// Normalize weight: cap at 1.0 for scoring
		if weight > 10 {
			weight = 10
		}
		w := weight / 10.0
		if w < 0.1 {
			w = 0.1
		}
		if existing, ok := neighbors[neighbor]; ok {
			if w > existing {
				neighbors[neighbor] = w
			}
		} else {
			neighbors[neighbor] = w
		}
	}
	return neighbors, rows.Err()
}

// collectLinkedMemories finds memories linked to the given entities and sums activation scores.
func collectLinkedMemories(ctx context.Context, db *sql.DB, entityIDs []int64, activation map[int64]float64) (map[string]float64, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(entityIDs))
	args := make([]any, len(entityIDs))
	for i, id := range entityIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `SELECT memory_id, entity_id FROM memory_entities WHERE entity_id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("collect linked memories: %w", err)
	}
	defer rows.Close()

	memScores := make(map[string]float64)
	for rows.Next() {
		var memID string
		var entityID int64
		if err := rows.Scan(&memID, &entityID); err != nil {
			return nil, err
		}
		memScores[memID] += activation[entityID]
	}
	return memScores, rows.Err()
}
