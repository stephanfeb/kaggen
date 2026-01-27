package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	sqlite_vec.Auto()
}

// MemoryChunk represents a chunk of text to be indexed.
type MemoryChunk struct {
	FilePath  string
	LineStart int
	LineEnd   int
	Content   string
	Embedding []float32
}

// SearchResult represents a search hit from the vector index.
type SearchResult struct {
	FilePath  string  `json:"file_path"`
	LineStart int     `json:"line_start"`
	LineEnd   int     `json:"line_end"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
}

// VectorIndex manages a sqlite-vec backed vector store for memory chunks.
type VectorIndex struct {
	db        *sql.DB
	dimension int
}

// NewVectorIndex opens or creates a sqlite-vec vector index at dbPath.
func NewVectorIndex(dbPath string, dimension int) (*VectorIndex, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	idx := &VectorIndex{db: db, dimension: dimension}
	if err := idx.initTables(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init tables: %w", err)
	}
	return idx, nil
}

func (v *VectorIndex) initTables() error {
	// Metadata table
	_, err := v.db.Exec(`CREATE TABLE IF NOT EXISTS memory_meta (
		rowid INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path TEXT,
		line_start INTEGER,
		line_end INTEGER,
		content TEXT,
		updated_at TEXT DEFAULT (datetime('now'))
	)`)
	if err != nil {
		return fmt.Errorf("create memory_meta: %w", err)
	}

	// Vector table
	vecSQL := fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS memory_chunks USING vec0(
		embedding float[%d]
	)`, v.dimension)
	if _, err := v.db.Exec(vecSQL); err != nil {
		return fmt.Errorf("create memory_chunks: %w", err)
	}

	// FTS5 table
	_, err = v.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
		content, file_path
	)`)
	if err != nil {
		return fmt.Errorf("create memory_fts: %w", err)
	}

	return nil
}

// Insert adds a memory chunk to all three tables with matching rowids.
func (v *VectorIndex) Insert(chunk MemoryChunk) error {
	tx, err := v.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Insert metadata
	res, err := tx.Exec(
		`INSERT INTO memory_meta (file_path, line_start, line_end, content) VALUES (?, ?, ?, ?)`,
		chunk.FilePath, chunk.LineStart, chunk.LineEnd, chunk.Content,
	)
	if err != nil {
		return fmt.Errorf("insert meta: %w", err)
	}

	rowid, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}

	// Serialize embedding as JSON for sqlite-vec
	embJSON, err := json.Marshal(chunk.Embedding)
	if err != nil {
		return fmt.Errorf("marshal embedding: %w", err)
	}

	// Insert vector
	if _, err := tx.Exec(
		`INSERT INTO memory_chunks (rowid, embedding) VALUES (?, ?)`,
		rowid, string(embJSON),
	); err != nil {
		return fmt.Errorf("insert vector: %w", err)
	}

	// Insert FTS
	if _, err := tx.Exec(
		`INSERT INTO memory_fts (rowid, content, file_path) VALUES (?, ?, ?)`,
		rowid, chunk.Content, chunk.FilePath,
	); err != nil {
		return fmt.Errorf("insert fts: %w", err)
	}

	return tx.Commit()
}

// Search performs a KNN vector similarity search.
func (v *VectorIndex) Search(embedding []float32, limit int) ([]SearchResult, error) {
	embJSON, err := json.Marshal(embedding)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding: %w", err)
	}

	rows, err := v.db.Query(`
		SELECT mc.rowid, mc.distance, mm.file_path, mm.line_start, mm.line_end, mm.content
		FROM memory_chunks mc
		JOIN memory_meta mm ON mc.rowid = mm.rowid
		WHERE mc.embedding MATCH ?
		  AND k = ?
		ORDER BY mc.distance
	`, string(embJSON), limit)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var rowid int64
		var distance float64
		var r SearchResult
		if err := rows.Scan(&rowid, &distance, &r.FilePath, &r.LineStart, &r.LineEnd, &r.Content); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		// Convert distance to similarity score (1 / (1 + distance))
		r.Score = 1.0 / (1.0 + distance)
		results = append(results, r)
	}
	return results, rows.Err()
}

// KeywordSearch performs a full-text search using FTS5.
func (v *VectorIndex) KeywordSearch(query string, limit int) ([]SearchResult, error) {
	rows, err := v.db.Query(`
		SELECT mm.rowid, mm.file_path, mm.line_start, mm.line_end, mm.content,
		       rank
		FROM memory_fts
		JOIN memory_meta mm ON memory_fts.rowid = mm.rowid
		WHERE memory_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var rowid int64
		var rank float64
		var r SearchResult
		if err := rows.Scan(&rowid, &r.FilePath, &r.LineStart, &r.LineEnd, &r.Content, &rank); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		// FTS5 rank is negative (lower is better), normalize
		r.Score = 1.0 / (1.0 + math.Abs(rank))
		results = append(results, r)
	}
	return results, rows.Err()
}

// HybridSearch combines vector and keyword search using Reciprocal Rank Fusion.
func (v *VectorIndex) HybridSearch(embedding []float32, query string, limit int) ([]SearchResult, error) {
	// Fetch more candidates from each source for better fusion
	fetchLimit := limit * 3

	vecResults, err := v.Search(embedding, fetchLimit)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	kwResults, err := v.KeywordSearch(query, fetchLimit)
	if err != nil {
		// FTS may fail on syntax; fall back to vector only
		return vecResults[:min(len(vecResults), limit)], nil
	}

	// Reciprocal Rank Fusion (k=60)
	const k = 60.0
	type scored struct {
		result SearchResult
		rrf    float64
	}

	scoreMap := make(map[string]*scored) // key: file_path:line_start

	addResults := func(results []SearchResult) {
		for rank, r := range results {
			key := fmt.Sprintf("%s:%d", r.FilePath, r.LineStart)
			if s, ok := scoreMap[key]; ok {
				s.rrf += 1.0 / (k + float64(rank+1))
			} else {
				scoreMap[key] = &scored{
					result: r,
					rrf:    1.0 / (k + float64(rank+1)),
				}
			}
		}
	}

	addResults(vecResults)
	addResults(kwResults)

	merged := make([]SearchResult, 0, len(scoreMap))
	for _, s := range scoreMap {
		s.result.Score = s.rrf
		merged = append(merged, s.result)
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}

// DeleteByFile removes all chunks for a given file path.
func (v *VectorIndex) DeleteByFile(filePath string) error {
	tx, err := v.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Get rowids for this file
	rows, err := tx.Query(`SELECT rowid FROM memory_meta WHERE file_path = ?`, filePath)
	if err != nil {
		return fmt.Errorf("select rowids: %w", err)
	}

	var rowids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan rowid: %w", err)
		}
		rowids = append(rowids, id)
	}
	rows.Close()

	for _, id := range rowids {
		if _, err := tx.Exec(`DELETE FROM memory_chunks WHERE rowid = ?`, id); err != nil {
			return fmt.Errorf("delete vector: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM memory_fts WHERE rowid = ?`, id); err != nil {
			return fmt.Errorf("delete fts: %w", err)
		}
	}

	if _, err := tx.Exec(`DELETE FROM memory_meta WHERE file_path = ?`, filePath); err != nil {
		return fmt.Errorf("delete meta: %w", err)
	}

	return tx.Commit()
}

// DB returns the underlying database handle.
func (v *VectorIndex) DB() *sql.DB {
	return v.db
}

// InsertEntry adds an entry with an explicit ID to all three tables.
// The file_path is set to "memory:" + id for entries managed by the memory service.
func (v *VectorIndex) InsertEntry(id, content string, emb []float32) error {
	filePath := "memory:" + id
	chunk := MemoryChunk{
		FilePath:  filePath,
		Content:   content,
		Embedding: emb,
	}
	return v.Insert(chunk)
}

// DeleteEntry removes a single entry by its memory ID from all tables.
func (v *VectorIndex) DeleteEntry(id string) error {
	return v.DeleteByFile("memory:" + id)
}

// Close closes the database connection.
func (v *VectorIndex) Close() error {
	return v.db.Close()
}
