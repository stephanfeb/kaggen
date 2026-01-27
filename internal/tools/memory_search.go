package tools

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/embedding"
	"github.com/yourusername/kaggen/internal/memory"
)

// MemorySearchArgs defines the input for the memory_search tool.
type MemorySearchArgs struct {
	Query string `json:"query" jsonschema:"required,description=Natural language search query to find relevant memories"`
	Limit *int   `json:"limit,omitempty" jsonschema:"description=Maximum number of results to return (default 5)"`
}

// MemorySearchResult defines the output of the memory_search tool.
type MemorySearchResult struct {
	Results []MemorySearchHit `json:"results"`
	Message string            `json:"message"`
}

// MemorySearchHit represents a single search result.
type MemorySearchHit struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
	Lines    string `json:"lines"`
}

func newMemorySearchTool(embedder embedding.Embedder, index *memory.VectorIndex) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args MemorySearchArgs) (*MemorySearchResult, error) {
			return executeMemorySearch(ctx, embedder, index, args)
		},
		function.WithName("memory_search"),
		function.WithDescription("Search through stored memories and past conversations using semantic similarity. Returns the most relevant memory chunks matching the query."),
	)
}

func executeMemorySearch(ctx context.Context, embedder embedding.Embedder, index *memory.VectorIndex, args MemorySearchArgs) (*MemorySearchResult, error) {
	result := &MemorySearchResult{}

	if args.Query == "" {
		result.Message = "Error: query is required"
		return result, fmt.Errorf("query is required")
	}

	limit := 5
	if args.Limit != nil && *args.Limit > 0 {
		limit = *args.Limit
	}

	// Embed the query
	emb, err := embedder.Embed(ctx, args.Query)
	if err != nil {
		result.Message = fmt.Sprintf("Error: embedding failed: %v", err)
		return result, fmt.Errorf("embed query: %w", err)
	}

	// Hybrid search
	searchResults, err := index.HybridSearch(emb, args.Query, limit)
	if err != nil {
		result.Message = fmt.Sprintf("Error: search failed: %v", err)
		return result, fmt.Errorf("search: %w", err)
	}

	if len(searchResults) == 0 {
		result.Message = "No memories found matching the query."
		return result, nil
	}

	hits := make([]MemorySearchHit, len(searchResults))
	for i, sr := range searchResults {
		hits[i] = MemorySearchHit{
			FilePath: sr.FilePath,
			Content:  sr.Content,
			Lines:    fmt.Sprintf("%d-%d", sr.LineStart, sr.LineEnd),
		}
	}

	result.Results = hits
	result.Message = fmt.Sprintf("Found %d memory results for query: %s", len(hits), args.Query)
	return result, nil
}
