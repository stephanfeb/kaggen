// Package tools provides tool implementations for the agent.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// MemorySearcher is the interface for searching memories.
// This matches the SearchMemories method from memory.Service.
type MemorySearcher interface {
	SearchMemories(ctx context.Context, userKey memory.UserKey, query string) ([]*memory.Entry, error)
}

// analogySearchArgs is the input schema for the find_analogies tool.
type analogySearchArgs struct {
	Problem      string   `json:"problem" jsonschema:"required,description=Current problem to find analogies for"`
	Domain       string   `json:"domain,omitempty" jsonschema:"description=Domain or area to focus search (e.g. caching; authentication; performance)"`
	Keywords     []string `json:"keywords,omitempty" jsonschema:"description=Additional keywords to include in search"`
	MaxAnalogies int      `json:"max_analogies,omitempty" jsonschema:"description=Max analogies to return (default 5)"`
}

// Analogy represents a past problem that may be relevant.
type Analogy struct {
	MemoryID    string   `json:"memory_id"`
	Content     string   `json:"content"`
	Topics      []string `json:"topics"`
	Similarity  float64  `json:"similarity"`
	ProblemType string   `json:"problem_type"` // Extracted from analysis
	Solution    string   `json:"solution"`     // Extracted from analysis
}

// analogySearchResult is the output schema for the find_analogies tool.
type analogySearchResult struct {
	Analogies   []Analogy `json:"analogies"`
	Adaptations string    `json:"adaptations"` // Suggestions for adapting past solutions
}

// AnalogyTool handles searching for and analyzing similar past problems.
type AnalogyTool struct {
	memService    MemorySearcher
	tier2Model    model.Model
	resolvedModel string
	maxAnalogies  int
	logger        *slog.Logger
}

// NewAnalogyTool creates the find_analogies tool.
// Returns nil if memService or tier2Model is nil.
func NewAnalogyTool(
	memService MemorySearcher,
	tier2Model model.Model,
	resolvedModel string,
	maxAnalogies int,
	logger *slog.Logger,
) tool.Tool {
	if memService == nil || tier2Model == nil {
		return nil
	}

	if maxAnalogies <= 0 {
		maxAnalogies = 5
	}

	t := &AnalogyTool{
		memService:    memService,
		tier2Model:    tier2Model,
		resolvedModel: resolvedModel,
		maxAnalogies:  maxAnalogies,
		logger:        logger,
	}

	return function.NewFunctionTool(
		t.execute,
		function.WithName("find_analogies"),
		function.WithDescription(`Search memory for similar past problems and get adaptation suggestions.

Use this tool when:
- The current problem seems familiar or reminds you of past work
- You want to learn from how similar problems were solved before
- You're looking for patterns or solutions that worked in the past
- You want to avoid reinventing the wheel

Returns relevant memories with extracted problem/solution patterns and suggestions for adapting them to the current situation.`),
	)
}

func (t *AnalogyTool) execute(ctx context.Context, args analogySearchArgs) (*analogySearchResult, error) {
	t.logger.Info("analogy search invoked",
		"problem_preview", truncate(args.Problem, 100),
		"domain", args.Domain,
	)

	// Build search query from problem + domain + keywords
	query := t.buildSearchQuery(args)

	// Determine max analogies
	maxAnalogies := args.MaxAnalogies
	if maxAnalogies <= 0 {
		maxAnalogies = t.maxAnalogies
	}
	if maxAnalogies > 10 {
		maxAnalogies = 10
	}

	// Search memories using 4-way hybrid search
	// Use a default UserKey since this is coordinator-level search
	userKey := memory.UserKey{AppName: "kaggen", UserID: "coordinator"}
	entries, err := t.memService.SearchMemories(ctx, userKey, query)
	if err != nil {
		t.logger.Warn("memory search failed", "error", err)
		return &analogySearchResult{
			Adaptations: "Memory search unavailable: " + err.Error(),
		}, nil
	}

	if len(entries) == 0 {
		t.logger.Info("no memories found for analogy search")
		return &analogySearchResult{
			Adaptations: "No similar past problems found in memory. This may be a novel situation.",
		}, nil
	}

	// Limit entries
	if len(entries) > maxAnalogies {
		entries = entries[:maxAnalogies]
	}

	// Build analogies from memory entries
	analogies := make([]Analogy, 0, len(entries))
	for _, e := range entries {
		content := ""
		var topics []string
		if e.Memory != nil {
			content = e.Memory.Memory
			topics = e.Memory.Topics
		}
		analogies = append(analogies, Analogy{
			MemoryID:   e.ID,
			Content:    content,
			Topics:     topics,
			Similarity: 0.0, // Will be filled by analysis
		})
	}

	// Use Tier 2 model to analyze memories and extract problem/solution patterns
	analysisResult, err := t.analyzeMemories(ctx, args.Problem, analogies)
	if err != nil {
		t.logger.Warn("memory analysis failed, returning raw results", "error", err)
		return &analogySearchResult{
			Analogies:   analogies,
			Adaptations: "Analysis unavailable, but relevant memories were found. Review them manually.",
		}, nil
	}

	t.logger.Info("analogy search complete",
		"analogies", len(analysisResult.Analogies),
	)

	return analysisResult, nil
}

func (t *AnalogyTool) buildSearchQuery(args analogySearchArgs) string {
	var parts []string

	// Add the main problem
	parts = append(parts, args.Problem)

	// Add domain if specified
	if args.Domain != "" {
		parts = append(parts, args.Domain)
	}

	// Add keywords
	for _, kw := range args.Keywords {
		parts = append(parts, kw)
	}

	return strings.Join(parts, " ")
}

func (t *AnalogyTool) analyzeMemories(ctx context.Context, problem string, analogies []Analogy) (*analogySearchResult, error) {
	prompt := t.buildAnalysisPrompt(problem, analogies)

	maxTokens := 4096
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: t.systemPrompt()},
			{Role: model.RoleUser, Content: prompt},
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: &maxTokens,
		},
	}

	respCh, err := t.tier2Model.GenerateContent(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("analysis model call failed: %w", err)
	}

	var responseContent string
	for resp := range respCh {
		if resp.Error != nil {
			return nil, fmt.Errorf("analysis model error: %s", resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			responseContent = resp.Choices[0].Message.Content
		}
	}

	return t.parseAnalysisResponse(responseContent)
}

func (t *AnalogyTool) systemPrompt() string {
	return `You are an expert at finding patterns in past work and adapting solutions to new problems.

Given memories from past work and a current problem, analyze each memory for:
1. What type of problem it represents
2. What solution or approach was used
3. How similar it is to the current problem (0-1 score)

Then provide adaptation suggestions for how past solutions might apply to the current problem.

Always respond in the following JSON format:
{
    "analogies": [
        {
            "memory_id": "id from input",
            "content": "original content",
            "topics": ["topics from input"],
            "similarity": 0.8,
            "problem_type": "What kind of problem this was",
            "solution": "How it was solved or approached"
        }
    ],
    "adaptations": "Concrete suggestions for how to adapt past solutions to the current problem"
}

Be specific about adaptations - don't just say "use a similar approach". Explain what specifically could be reused or modified.`
}

func (t *AnalogyTool) buildAnalysisPrompt(problem string, analogies []Analogy) string {
	var sb strings.Builder

	sb.WriteString("## Current Problem\n\n")
	sb.WriteString(problem)
	sb.WriteString("\n\n")

	sb.WriteString("## Past Memories to Analyze\n\n")
	for i, a := range analogies {
		sb.WriteString(fmt.Sprintf("### Memory %d (ID: %s)\n", i+1, a.MemoryID))
		sb.WriteString(fmt.Sprintf("Topics: %s\n", strings.Join(a.Topics, ", ")))
		sb.WriteString(fmt.Sprintf("Content:\n%s\n\n", a.Content))
	}

	sb.WriteString("Analyze each memory for problem type, solution patterns, and similarity to the current problem. Then provide adaptation suggestions.")

	return sb.String()
}

func (t *AnalogyTool) parseAnalysisResponse(content string) (*analogySearchResult, error) {
	if content == "" {
		return nil, fmt.Errorf("empty response from model")
	}

	// Try to extract JSON from the response
	jsonContent := content

	// Handle cases where JSON is wrapped in markdown code blocks
	if idx := strings.Index(content, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(content[start:], "```"); end >= 0 {
			jsonContent = content[start : start+end]
		}
	} else if idx := strings.Index(content, "```"); idx >= 0 {
		start := idx + 3
		if nlIdx := strings.Index(content[start:], "\n"); nlIdx >= 0 {
			start += nlIdx + 1
		}
		if end := strings.Index(content[start:], "```"); end >= 0 {
			jsonContent = content[start : start+end]
		}
	}

	jsonContent = strings.TrimSpace(jsonContent)

	var result analogySearchResult
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &result, nil
}
