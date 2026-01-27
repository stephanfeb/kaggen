# Hindsight-Inspired Epistemic Memory for Kaggen

## Goal

Evolve Kaggen's flat memory system into a structured epistemic memory architecture inspired by the Hindsight paper (Latimer 2025). Introduce memory type classification (facts, experiences, opinions, observations), an entity graph, temporal awareness, opinion evolution, 4-way retrieval, and background observation synthesis — all within the existing `memory.Service` interface.

## Design Principles

- **Epistemic clarity**: structurally separate facts from beliefs from inferences
- **Interface compatibility**: `memory.Service` takes `(content string, topics []string)` — metadata is encoded in structured prefixes and `_`-prefixed topics, then parsed into extended SQLite columns
- **Incremental**: each phase builds on the last, system works at every stage
- **Local-first**: SQLite + Ollama, no cloud dependencies

## Metadata Transport Convention

The `memory.Service` interface passes `content string` and `topics []string`. We encode structured metadata in a prefix on the content field:

```
[type:opinion|conf:0.7|when:2025-06~2025-08|ent:Go,Rust] User prefers Go over Rust for CLI tools
```

`AddMemory`/`UpdateMemory` parse this prefix, extract metadata into extended SQLite columns, and store only the clean text in `content`. The `topics` array carries metadata as `_`-prefixed entries (`_type:opinion`, `_conf:0.7`) alongside semantic topics.

This means the custom extractor prompt (Phase 2) instructs the LLM to emit the prefix convention in its `memory` field output.

---

## Phase 1: Epistemic Schema Extension

### Schema migration

Add columns to `memories` table (idempotent via `PRAGMA table_info` check):

```sql
ALTER TABLE memories ADD COLUMN memory_type TEXT DEFAULT 'fact';
  -- 'fact' | 'experience' | 'opinion' | 'observation'
ALTER TABLE memories ADD COLUMN confidence REAL DEFAULT 1.0;
  -- [0.0, 1.0], meaningful for opinions
ALTER TABLE memories ADD COLUMN occurred_start TEXT;
  -- ISO8601 nullable: when the event/fact started
ALTER TABLE memories ADD COLUMN occurred_end TEXT;
  -- ISO8601 nullable: when it ended (null = ongoing/point-in-time)
ALTER TABLE memories ADD COLUMN entities_json TEXT DEFAULT '[]';
  -- JSON array of entity name strings
```

### New file: `internal/memory/metadata.go`

- `type MemoryMetadata struct` — type, confidence, occurred start/end, entities
- `ParseStructuredContent(raw string) (cleanContent string, meta MemoryMetadata)` — parse `[type:...|conf:...|when:...|ent:...]` prefix
- `FormatStructuredContent(content string, meta MemoryMetadata) string` — serialize back (for extractor output)
- `ParseMetaTopics(topics []string) (semanticTopics []string, meta MemoryMetadata)` — extract `_`-prefixed metadata from topics
- `EvolveConfidence(oldConf, newConf float64, reinforces bool) float64` — exponential moving average for opinion evolution

### Changes to `service.go`

- `initMemoriesTable` runs ALTER TABLE migrations idempotently
- `AddMemory`: call `ParseStructuredContent` + `ParseMetaTopics`, store metadata in extended columns, store clean content in `content`
- `UpdateMemory`: same parsing; if memory_type=opinion, call `EvolveConfidence`
- `ReadMemories`/`SearchMemories`: populate extended fields when scanning rows
- New helper: `scanExtendedEntry` that reads the additional columns

---

## Phase 2: Custom Extractor Prompt

### New file: `internal/memory/extractor_prompt.go`

Custom prompt replacing the default trpc-agent-go extraction prompt. Instructs the LLM to:

1. **Classify** each memory as fact, experience, opinion, or observation
2. **Extract entities** — people, places, technologies, organizations mentioned
3. **Identify temporal range** — when the fact/experience occurred (not when it was said)
4. **Assign confidence** — [0.0-1.0] for opinions based on strength of evidence
5. **Format output** using the structured prefix convention in the `memory` field

Example LLM output for an add operation:
```
memory: "[type:experience|conf:1.0|when:2025-01|ent:Berlin,Germany] User relocated to Berlin in January 2025"
topics: ["relocation", "living"]
```

### Changes to `cmd/kaggen/cmd/agent.go` and `gateway.go`

Pass `extractor.WithPrompt(memory.ExtractorPrompt)` when creating the extractor:

```go
memExtractor := extractor.NewExtractor(modelAdapter, extractor.WithPrompt(memory.ExtractorPrompt))
```

---

## Phase 3: Entity Graph

### New tables (created in `initMemoriesTable`):

```sql
CREATE TABLE IF NOT EXISTS entities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    aliases TEXT DEFAULT '[]',
    summary TEXT DEFAULT '',
    summary_updated_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS memory_entities (
    memory_id TEXT NOT NULL,
    entity_id INTEGER NOT NULL,
    PRIMARY KEY (memory_id, entity_id),
    FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE,
    FOREIGN KEY (entity_id) REFERENCES entities(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS entity_relations (
    entity_a INTEGER NOT NULL,
    entity_b INTEGER NOT NULL,
    relation_type TEXT NOT NULL DEFAULT 'cooccurs',
    weight REAL DEFAULT 1.0,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (entity_a, entity_b, relation_type)
);
```

### New file: `internal/memory/entities.go`

- `resolveEntity(ctx, db, name string) (int64, error)` — find by name or alias (case-insensitive), or create
- `resolveEntities(ctx, db, names []string) ([]int64, error)` — batch resolve
- `linkMemoryEntities(ctx, db, memoryID string, entityIDs []int64) error` — insert into junction
- `updateCooccurrences(ctx, db, entityIDs []int64) error` — for each pair in the set, upsert into `entity_relations` with `weight += 1.0`
- `getEntityIDsByName(ctx, db, names []string) ([]int64, error)` — lookup for search

### Integration into `AddMemory`/`UpdateMemory` in `service.go`

After inserting the memory row:
1. Parse entities from metadata
2. `resolveEntities` → get entity IDs
3. `linkMemoryEntities` → create junction rows
4. `updateCooccurrences` → strengthen co-occurrence edges

On `DeleteMemory`: cascade deletes handle junction cleanup automatically.

---

## Phase 4: Enhanced 4-Way Retrieval

### New file: `internal/memory/search.go`

The core search pipeline:

```go
func (s *FileMemoryService) fourWaySearch(ctx context.Context, query string, limit int) ([]*memory.Entry, error)
```

**Channel 1 — Vector search** (existing): Embed query → `vecIndex.Search(emb, fetchLimit)`

**Channel 2 — Keyword search** (existing): `vecIndex.KeywordSearch(query, fetchLimit)`

**Channel 3 — Graph search**: Extract entity names from query by matching against `entities` table (case-insensitive LIKE), then spreading activation through `memory_entities` → `entity_relations` → `memory_entities` (2-hop BFS). Score by hop distance + relation weight.

**Channel 4 — Temporal search**: Parse temporal expressions from query → date range → `SELECT FROM memories WHERE occurred_start <= range_end AND (occurred_end >= range_start OR occurred_end IS NULL)`.

All four channels run concurrently via goroutines. Results merged via **Reciprocal Rank Fusion** (k=60):

```go
RRF(f) = Σ 1/(k + rank_i(f))   for each channel i where f appears
```

### New file: `internal/memory/temporal.go`

- `ParseTemporalQuery(query string) (start, end time.Time, found bool)` — regex-based parser
- Patterns: "last week", "last month", "last N days/weeks/months", "last summer/winter/spring/fall", "in YYYY", "in YYYY-MM", "YYYY-MM-DD", "yesterday", "today"
- Returns zero times if no temporal expression found (channel 4 returns empty)

### New file: `internal/memory/graphsearch.go`

- `graphSearch(ctx, db, query string, limit int) ([]rankedMemory, error)`
- Match query words against `entities.name` (case-insensitive)
- From matching entities, BFS 2 hops through `entity_relations` to related entities
- Collect memories linked to all discovered entities via `memory_entities`
- Score by activation: direct entity match = 1.0, 1-hop = 0.5 * relation_weight, 2-hop = 0.25 * weight

### Changes to `service.go`

`SearchMemories` calls `fourWaySearch` instead of the current vector+keyword path.

---

## Phase 5: Opinion Evolution

Already partially covered in Phase 1 (`EvolveConfidence`). This phase integrates it fully.

### Logic in `UpdateMemory`

When updating a memory with `memory_type = 'opinion'`:

1. Read old confidence from DB
2. Parse new confidence from structured prefix
3. Determine direction: if new content semantically aligns with old → reinforces; if contradicts → weakens
4. Apply: `new_conf = clamp(old_conf + alpha * delta, 0.0, 1.0)` where alpha=0.15
   - Reinforce: delta = +(1.0 - old_conf) * 0.3
   - Weaken: delta = -old_conf * 0.3
   - If new confidence explicitly provided by extractor, weighted average instead
5. Store updated confidence

### Simplification

For v1, trust the extractor's confidence assignment directly — the LLM prompt already sees existing memories with their confidence and can make the judgment. Only apply EMA smoothing to prevent wild swings:

```go
func evolveConfidence(old, new float64) float64 {
    const alpha = 0.3
    return alpha*new + (1-alpha)*old
}
```

---

## Phase 6: Observation Synthesis

### New file: `internal/memory/synthesizer.go`

Background goroutine that periodically creates entity summaries.

```go
type Synthesizer struct {
    db       *sql.DB
    model    model.Model
    embedder embedding.Embedder
    vecIndex *VectorIndex
    logger   *slog.Logger
    interval time.Duration  // default 1 hour
    stopCh   chan struct{}
}
```

**Cycle** (runs every `interval`):

1. Query entities needing summary refresh:
   ```sql
   SELECT e.id, e.name FROM entities e
   JOIN memory_entities me ON e.id = me.entity_id
   JOIN memories m ON me.memory_id = m.id
   WHERE e.summary_updated_at IS NULL
      OR e.summary_updated_at < m.updated_at
   GROUP BY e.id
   HAVING COUNT(me.memory_id) >= 3  -- only synthesize entities with 3+ memories
   ```

2. For each entity, gather linked memories:
   ```sql
   SELECT m.content, m.memory_type, m.confidence
   FROM memories m
   JOIN memory_entities me ON m.id = me.memory_id
   WHERE me.entity_id = ?
   ORDER BY m.updated_at DESC LIMIT 20
   ```

3. Call LLM with synthesis prompt:
   ```
   Summarize what is known about {entity} based on these memories.
   Write a neutral, factual summary paragraph. Do not speculate.
   ```

4. Store summary in `entities.summary`, update `summary_updated_at`

5. Upsert an `observation`-type memory entry so the summary participates in search:
   ```
   [type:observation|ent:{entity}] {summary text}
   ```

### Integration

- Started in `NewFileMemoryService` alongside the auto-worker
- Uses the same `model.Model` (passed as new constructor param or option)
- Stopped in `Close()`

### Changes to `service.go`

- Add `model model.Model` field to `FileMemoryService`
- New option: `WithModel(m model.Model)` — needed for synthesizer LLM calls
- New option: `WithSynthesisInterval(d time.Duration)` — default 1h
- Constructor starts synthesizer if model is provided

### Changes to `cmd/kaggen/cmd/agent.go` and `gateway.go`

Pass the model adapter to the memory service:
```go
memory.WithModel(modelAdapter),
memory.WithSynthesisInterval(1 * time.Hour),
```

---

## Files Summary

### New files (7)
| File | Phase | Purpose |
|------|-------|---------|
| `internal/memory/metadata.go` | 1 | Structured content prefix parsing, confidence evolution |
| `internal/memory/metadata_test.go` | 1 | Tests for metadata parsing |
| `internal/memory/extractor_prompt.go` | 2 | Custom LLM extraction prompt |
| `internal/memory/entities.go` | 3 | Entity resolution, linking, co-occurrence |
| `internal/memory/graphsearch.go` | 4 | Spreading activation graph search |
| `internal/memory/temporal.go` | 4 | Temporal expression parsing + temporal SQL search |
| `internal/memory/search.go` | 4 | 4-way parallel search pipeline with RRF fusion |
| `internal/memory/synthesizer.go` | 6 | Background entity summary synthesis |

### Modified files (4)
| File | Phases | Changes |
|------|--------|---------|
| `internal/memory/service.go` | 1,3,4,5,6 | Schema migration, metadata parsing in Add/Update, SearchMemories upgrade, synthesizer lifecycle |
| `internal/memory/service_test.go` | 1-6 | New tests for each phase |
| `cmd/kaggen/cmd/agent.go` | 2,6 | Custom extractor prompt, model option for synthesizer |
| `cmd/kaggen/cmd/gateway.go` | 2,6 | Same as agent.go |

### Unchanged files
| File | Reason |
|------|--------|
| `internal/memory/vectorindex.go` | Already has all methods needed |
| `internal/memory/autoworker.go` | Operations flow through unchanged AddMemory/UpdateMemory |
| `internal/memory/indexer.go` | Separate concern (file watching) |
| `internal/memory/file.go` | Bootstrap loading unchanged |
| `internal/gateway/handler.go` | No changes needed |
| `internal/gateway/server.go` | No changes needed |

## Implementation Order

1. `metadata.go` + `metadata_test.go` — parse/serialize structured prefixes
2. Schema migration in `service.go` — add columns idempotently
3. Update `AddMemory`/`UpdateMemory` in `service.go` — parse metadata, store in extended columns
4. `extractor_prompt.go` — custom prompt + wire in agent.go/gateway.go
5. `entities.go` — entity resolution + graph tables + linking in Add/Update
6. `temporal.go` — temporal expression parsing
7. `graphsearch.go` — spreading activation
8. `search.go` — 4-way pipeline + wire into `SearchMemories`
9. Opinion evolution in `UpdateMemory` — confidence EMA
10. `synthesizer.go` — background synthesis + wire into service lifecycle
11. Tests for each component
12. Build, vet, test

## Verification

- `go build -tags "fts5" ./...`
- `go vet -tags "fts5" ./...`
- `go test -tags "fts5" ./...`
- Unit tests for: metadata parsing round-trip, entity resolution, temporal parsing, graph search, 4-way RRF fusion, confidence evolution, synthesizer query
- Manual: start agent, converse, verify structured memories in DB (`sqlite3 ~/.kaggen/memory.db "SELECT id, memory_type, confidence, entities_json FROM memories"`)
- Manual: test temporal query ("what did I do last month?") returns temporally-relevant memories
- Manual: test entity-based query ("tell me about Go") returns graph-connected memories
- Manual: after enough memories accumulate, verify entity summaries are synthesized
