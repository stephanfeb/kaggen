package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	defaultHTTPTimeout = 30 * time.Second
	maxHTTPTimeout     = 5 * time.Minute
	maxResponseBody    = 100 * 1024 // 100KB max response body
)

// HttpRequestArgs defines the input arguments for the http_request tool.
type HttpRequestArgs struct {
	URL           string            `json:"url" jsonschema:"required,description=The URL to send the request to."`
	Method        string            `json:"method" jsonschema:"required,description=HTTP method: GET POST PUT PATCH DELETE."`
	Headers       map[string]string `json:"headers,omitempty" jsonschema:"description=Additional HTTP headers to include."`
	Body          string            `json:"body,omitempty" jsonschema:"description=Request body (typically JSON for POST/PUT/PATCH)."`
	AuthSecret    string            `json:"auth_secret,omitempty" jsonschema:"description=Name of the secret to use for authentication. The secret value is injected automatically."`
	AuthHeader    string            `json:"auth_header,omitempty" jsonschema:"description=Custom header name for auth (default: Authorization)."`
	AuthScheme    string            `json:"auth_scheme,omitempty" jsonschema:"description=Auth scheme: bearer (default) or api-key."`
	TimeoutSecs   int               `json:"timeout_seconds,omitempty" jsonschema:"description=Request timeout in seconds (default 30 max 300)."`
	ContentType   string            `json:"content_type,omitempty" jsonschema:"description=Content-Type header (default: application/json for requests with body)."`
}

// HttpRequestResult defines the output of the http_request tool.
type HttpRequestResult struct {
	StatusCode int               `json:"status_code"`
	Status     string            `json:"status"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body"`
	Message    string            `json:"message"`
}

// NewHttpRequestTool creates a new http_request tool with the given secrets map.
// The secrets map contains secret_name -> secret_value mappings for this skill.
// Secret values are never exposed to the LLM - only secret names are used in args.
func NewHttpRequestTool(secrets map[string]string) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args HttpRequestArgs) (*HttpRequestResult, error) {
			return executeHttpRequest(ctx, args, secrets)
		},
		function.WithName("http_request"),
		function.WithDescription("Make HTTP requests to external APIs. Use auth_secret to reference a secret by name for authentication - the actual secret value is injected automatically and never visible."),
	)
}

// executeHttpRequest performs the actual HTTP request.
func executeHttpRequest(ctx context.Context, args HttpRequestArgs, secrets map[string]string) (*HttpRequestResult, error) {
	result := &HttpRequestResult{}

	// Validate required fields
	if args.URL == "" {
		result.Message = "Error: url is required"
		return result, fmt.Errorf("url is required")
	}
	if args.Method == "" {
		result.Message = "Error: method is required"
		return result, fmt.Errorf("method is required")
	}

	// Normalize method
	method := strings.ToUpper(args.Method)
	validMethods := map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true}
	if !validMethods[method] {
		result.Message = fmt.Sprintf("Error: invalid method %q", args.Method)
		return result, fmt.Errorf("invalid method: %s", args.Method)
	}

	// Initialize headers
	headers := make(map[string]string)
	for k, v := range args.Headers {
		headers[k] = v
	}

	// Handle authentication via secret injection
	if args.AuthSecret != "" {
		secretValue, ok := secrets[args.AuthSecret]
		if !ok {
			result.Message = fmt.Sprintf("Error: secret %q not available to this skill", args.AuthSecret)
			return result, fmt.Errorf("secret %q not available to this skill", args.AuthSecret)
		}

		// Determine auth header and scheme
		authHeader := args.AuthHeader
		if authHeader == "" {
			authHeader = "Authorization"
		}
		authScheme := strings.ToLower(args.AuthScheme)
		if authScheme == "" {
			authScheme = "bearer"
		}

		// Inject the auth header
		switch authScheme {
		case "bearer":
			headers[authHeader] = "Bearer " + secretValue
		case "api-key", "apikey":
			headers[authHeader] = secretValue
		case "basic":
			headers[authHeader] = "Basic " + secretValue
		default:
			headers[authHeader] = secretValue
		}
	}

	// Set Content-Type for requests with body
	if args.Body != "" {
		contentType := args.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		if _, exists := headers["Content-Type"]; !exists {
			headers["Content-Type"] = contentType
		}
	}

	// Set timeout
	timeout := defaultHTTPTimeout
	if args.TimeoutSecs > 0 {
		timeout = time.Duration(args.TimeoutSecs) * time.Second
		if timeout > maxHTTPTimeout {
			timeout = maxHTTPTimeout
		}
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create request
	var bodyReader io.Reader
	if args.Body != "" {
		bodyReader = bytes.NewBufferString(args.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, args.URL, bodyReader)
	if err != nil {
		result.Message = fmt.Sprintf("Error creating request: %v", err)
		return result, fmt.Errorf("create request: %w", err)
	}

	// Set headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// Execute request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Message = "Error: request timed out"
			return result, nil
		}
		result.Message = fmt.Sprintf("Error: %v", err)
		return result, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body (limited)
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		result.Message = fmt.Sprintf("Error reading response: %v", err)
		return result, fmt.Errorf("read response: %w", err)
	}

	// Build response headers map
	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}

	result.StatusCode = resp.StatusCode
	result.Status = resp.Status
	result.Headers = respHeaders
	result.Body = string(body)
	result.Message = fmt.Sprintf("HTTP %s %s -> %s", method, args.URL, resp.Status)

	return result, nil
}
