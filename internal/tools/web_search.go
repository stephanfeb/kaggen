package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/config"
)

// WebSearchArgs defines the input arguments for the web_search tool.
type WebSearchArgs struct {
	Query      string `json:"query" jsonschema:"required,description=The search query to execute"`
	NumResults int    `json:"num_results,omitempty" jsonschema:"description=Maximum number of results to return (default 5)"`
	Domain     string `json:"domain,omitempty" jsonschema:"description=Limit search to a specific domain (e.g. 'github.com')"`
}

// WebSearchResult defines the output of the web_search tool.
type WebSearchResult struct {
	Results []SearchResult `json:"results"`
	Message string         `json:"message"`
}

// SearchResult represents a single search result.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

const (
	defaultSearchTimeout    = 15 * time.Second
	defaultNumResults       = 5
	maxNumResults           = 20
)

// WebSearchTool creates a web search tool if search is configured, nil otherwise.
func WebSearchTool(cfg config.WebSearchConfig) tool.Tool {
	if cfg.Provider == "" {
		return nil
	}
	return newWebSearchTool(cfg)
}

func newWebSearchTool(cfg config.WebSearchConfig) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args WebSearchArgs) (*WebSearchResult, error) {
			return executeWebSearch(ctx, cfg, args)
		},
		function.WithName("web_search"),
		function.WithDescription("Search the web for information. Returns titles, URLs, and snippets. Use this to discover documentation, find solutions, or research topics."),
	)
}

func executeWebSearch(ctx context.Context, cfg config.WebSearchConfig, args WebSearchArgs) (*WebSearchResult, error) {
	if args.Query == "" {
		return &WebSearchResult{Message: "query is required"}, nil
	}

	numResults := args.NumResults
	if numResults <= 0 {
		numResults = defaultNumResults
	}
	if cfg.NumResults > 0 && numResults > cfg.NumResults {
		numResults = cfg.NumResults
	}
	if numResults > maxNumResults {
		numResults = maxNumResults
	}

	// Add domain filter to query if specified
	query := args.Query
	if args.Domain != "" {
		query = fmt.Sprintf("site:%s %s", args.Domain, query)
	}

	var results []SearchResult
	var err error

	switch strings.ToLower(cfg.Provider) {
	case "searxng":
		results, err = searchSearXNG(ctx, cfg.BaseURL, query, numResults)
	case "brave":
		results, err = searchBrave(ctx, cfg.APIKey, query, numResults)
	case "google":
		results, err = searchGoogle(ctx, cfg.APIKey, query, numResults)
	default:
		return &WebSearchResult{Message: fmt.Sprintf("unknown search provider: %s", cfg.Provider)}, nil
	}

	if err != nil {
		return &WebSearchResult{Message: fmt.Sprintf("search failed: %v", err)}, nil
	}

	return &WebSearchResult{
		Results: results,
		Message: fmt.Sprintf("Found %d results", len(results)),
	}, nil
}

// searchSearXNG queries a SearXNG instance.
func searchSearXNG(ctx context.Context, baseURL, query string, numResults int) ([]SearchResult, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("searxng base_url not configured")
	}

	// Build URL
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid searxng base_url: %w", err)
	}
	u.Path = "/search"
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	// Make request
	ctx, cancel := context.WithTimeout(ctx, defaultSearchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("searxng returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var searxResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&searxResp); err != nil {
		return nil, fmt.Errorf("failed to parse searxng response: %w", err)
	}

	// Convert to our format
	results := make([]SearchResult, 0, numResults)
	for i, r := range searxResp.Results {
		if i >= numResults {
			break
		}
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}

	return results, nil
}

// searchBrave queries the Brave Search API.
func searchBrave(ctx context.Context, apiKey, query string, numResults int) ([]SearchResult, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("brave api_key not configured")
	}

	// Build URL
	u := "https://api.search.brave.com/res/v1/web/search"
	params := url.Values{}
	params.Set("q", query)
	params.Set("count", fmt.Sprintf("%d", numResults))

	// Make request
	ctx, cancel := context.WithTimeout(ctx, defaultSearchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("brave returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&braveResp); err != nil {
		return nil, fmt.Errorf("failed to parse brave response: %w", err)
	}

	// Convert to our format
	results := make([]SearchResult, 0, numResults)
	for i, r := range braveResp.Web.Results {
		if i >= numResults {
			break
		}
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
		})
	}

	return results, nil
}

// searchGoogle queries the Google Custom Search JSON API.
func searchGoogle(ctx context.Context, apiKey, query string, numResults int) ([]SearchResult, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("google api_key not configured (format: 'api_key:cx_id')")
	}

	// API key format: "api_key:cx_id"
	parts := strings.SplitN(apiKey, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("google api_key must be in format 'api_key:cx_id'")
	}
	key, cxID := parts[0], parts[1]

	// Google limits to 10 results per request
	if numResults > 10 {
		numResults = 10
	}

	// Build URL
	u := "https://www.googleapis.com/customsearch/v1"
	params := url.Values{}
	params.Set("key", key)
	params.Set("cx", cxID)
	params.Set("q", query)
	params.Set("num", fmt.Sprintf("%d", numResults))

	// Make request
	ctx, cancel := context.WithTimeout(ctx, defaultSearchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("google returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var googleResp struct {
		Items []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&googleResp); err != nil {
		return nil, fmt.Errorf("failed to parse google response: %w", err)
	}

	// Convert to our format
	results := make([]SearchResult, 0, numResults)
	for i, r := range googleResp.Items {
		if i >= numResults {
			break
		}
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.Link,
			Snippet: r.Snippet,
		})
	}

	return results, nil
}
