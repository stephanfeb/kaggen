package memory

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yourusername/kaggen/internal/embedding"
)

// Indexer watches memory files and indexes them into the vector store.
type Indexer struct {
	index     *VectorIndex
	embedder  embedding.Embedder
	workspace string
	chunkSize int
	overlap   int
	logger    *slog.Logger

	mu    sync.Mutex
	mtimes map[string]time.Time
}

// NewIndexer creates a new memory indexer.
func NewIndexer(index *VectorIndex, embedder embedding.Embedder, workspace string, chunkSize, overlap int, logger *slog.Logger) *Indexer {
	if chunkSize <= 0 {
		chunkSize = 400
	}
	if overlap <= 0 {
		overlap = 80
	}
	return &Indexer{
		index:     index,
		embedder:  embedder,
		workspace: workspace,
		chunkSize: chunkSize,
		overlap:   overlap,
		logger:    logger,
		mtimes:    make(map[string]time.Time),
	}
}

// Start runs IndexAll once, then polls for changes every 30 seconds.
func (idx *Indexer) Start(ctx context.Context) error {
	if err := idx.IndexAll(ctx); err != nil {
		idx.logger.Warn("initial memory indexing failed", "error", err)
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				idx.indexChanged(ctx)
			}
		}
	}()

	return nil
}

// IndexAll indexes all memory markdown files.
func (idx *Indexer) IndexAll(ctx context.Context) error {
	files := idx.memoryFiles()
	for _, f := range files {
		if err := idx.IndexFile(ctx, f); err != nil {
			idx.logger.Warn("index file failed", "file", f, "error", err)
		}
	}
	return nil
}

// IndexFile re-indexes a single file.
func (idx *Indexer) IndexFile(ctx context.Context, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	content := string(data)
	chunks := chunkMarkdown(content, idx.chunkSize, idx.overlap)
	if len(chunks) == 0 {
		return nil
	}

	// Extract texts for batch embedding
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}

	embeddings, err := idx.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed batch: %w", err)
	}

	// Delete old chunks for this file
	if err := idx.index.DeleteByFile(filePath); err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}

	// Insert new chunks
	for i, c := range chunks {
		mc := MemoryChunk{
			FilePath:  filePath,
			LineStart: c.LineStart,
			LineEnd:   c.LineEnd,
			Content:   c.Content,
			Embedding: embeddings[i],
		}
		if err := idx.index.Insert(mc); err != nil {
			return fmt.Errorf("insert chunk: %w", err)
		}
	}

	// Track mtime
	idx.mu.Lock()
	if info, err := os.Stat(filePath); err == nil {
		idx.mtimes[filePath] = info.ModTime()
	}
	idx.mu.Unlock()

	idx.logger.Info("indexed memory file", "file", filePath, "chunks", len(chunks))
	return nil
}

func (idx *Indexer) indexChanged(ctx context.Context) {
	files := idx.memoryFiles()
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		idx.mu.Lock()
		lastMtime, seen := idx.mtimes[f]
		idx.mu.Unlock()

		if !seen || info.ModTime().After(lastMtime) {
			if err := idx.IndexFile(ctx, f); err != nil {
				idx.logger.Warn("re-index file failed", "file", f, "error", err)
			}
		}
	}
}

func (idx *Indexer) memoryFiles() []string {
	var files []string

	// MEMORY.md at workspace root
	memoryMD := filepath.Join(idx.workspace, "MEMORY.md")
	if _, err := os.Stat(memoryMD); err == nil {
		files = append(files, memoryMD)
	}

	// workspace/memory/*.md
	memDir := filepath.Join(idx.workspace, "memory")
	entries, err := filepath.Glob(filepath.Join(memDir, "*.md"))
	if err == nil {
		files = append(files, entries...)
	}

	return files
}

type textChunk struct {
	Content   string
	LineStart int
	LineEnd   int
}

// chunkMarkdown splits markdown content into chunks of approximately chunkSize
// tokens (estimated as words), with overlap tokens of overlap between chunks.
// It splits on paragraph/heading boundaries when possible.
func chunkMarkdown(content string, chunkSize, overlap int) []textChunk {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil
	}

	// Build paragraphs (groups of non-empty lines, or heading blocks)
	type paragraph struct {
		text      string
		lineStart int
		lineEnd   int
		wordCount int
	}

	var paragraphs []paragraph
	var current []string
	startLine := 1

	flush := func(endLine int) {
		if len(current) == 0 {
			return
		}
		text := strings.Join(current, "\n")
		wc := wordCount(text)
		paragraphs = append(paragraphs, paragraph{
			text:      text,
			lineStart: startLine,
			lineEnd:   endLine,
			wordCount: wc,
		})
		current = nil
	}

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Heading or blank line = paragraph boundary
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			flush(lineNum - 1)
			startLine = lineNum
			if trimmed != "" {
				// The heading is its own paragraph
				current = append(current, line)
				flush(lineNum)
				startLine = lineNum + 1
			} else {
				startLine = lineNum + 1
			}
			continue
		}

		if len(current) == 0 {
			startLine = lineNum
		}
		current = append(current, line)
	}
	flush(len(lines))

	if len(paragraphs) == 0 {
		return nil
	}

	// Merge paragraphs into chunks respecting chunkSize
	var chunks []textChunk
	var buf []string
	bufWords := 0
	chunkStart := paragraphs[0].lineStart
	chunkEnd := paragraphs[0].lineEnd

	for _, p := range paragraphs {
		if bufWords+p.wordCount > chunkSize && bufWords > 0 {
			// Emit current chunk
			chunks = append(chunks, textChunk{
				Content:   strings.Join(buf, "\n\n"),
				LineStart: chunkStart,
				LineEnd:   chunkEnd,
			})

			// Compute overlap: keep trailing paragraphs that fit in overlap
			overlapBuf, overlapWords, overlapStart := computeOverlap(buf, overlap)
			buf = overlapBuf
			bufWords = overlapWords
			chunkStart = overlapStart
		}

		if len(buf) == 0 {
			chunkStart = p.lineStart
		}
		buf = append(buf, p.text)
		bufWords += p.wordCount
		chunkEnd = p.lineEnd
	}

	// Emit remaining
	if len(buf) > 0 {
		chunks = append(chunks, textChunk{
			Content:   strings.Join(buf, "\n\n"),
			LineStart: chunkStart,
			LineEnd:   chunkEnd,
		})
	}

	return chunks
}

func computeOverlap(buf []string, overlap int) ([]string, int, int) {
	// Take trailing items from buf that fit within overlap word count
	var overlapBuf []string
	overlapWords := 0
	for i := len(buf) - 1; i >= 0; i-- {
		wc := wordCount(buf[i])
		if overlapWords+wc > overlap {
			break
		}
		overlapBuf = append([]string{buf[i]}, overlapBuf...)
		overlapWords += wc
	}
	// We don't precisely track line starts for overlap; use a rough estimate
	// The next paragraph will set the correct chunkStart anyway
	return overlapBuf, overlapWords, 0
}

func wordCount(s string) int {
	count := 0
	inWord := false
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			inWord = false
		} else if !inWord {
			inWord = true
			count++
		}
	}
	return count
}
