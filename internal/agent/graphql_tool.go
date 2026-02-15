package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/oauth"
)

const (
	graphqlDefaultTimeout  = 30 * time.Second
	graphqlMaxTimeout      = 5 * time.Minute
	graphqlMaxResponseBody = 500 * 1024 // 500KB max response
)

// GraphQLToolArgs defines the input arguments for the graphql tool.
type GraphQLToolArgs struct {
	// Action selection
	Action string `json:"action" jsonschema:"required,description=Action to perform: query mutation introspect,enum=query,enum=mutation,enum=introspect"`

	// Endpoint configuration
	Endpoint string            `json:"endpoint" jsonschema:"required,description=GraphQL endpoint URL (e.g. https://api.github.com/graphql)"`
	Headers  map[string]string `json:"headers,omitempty" jsonschema:"description=Additional HTTP headers to include"`

	// Authentication (same as http_request)
	AuthSecret    string `json:"auth_secret,omitempty" jsonschema:"description=Name of secret for authentication. Injected as Authorization header."`
	AuthScheme    string `json:"auth_scheme,omitempty" jsonschema:"description=Auth scheme: bearer (default) or api-key."`
	OAuthProvider string `json:"oauth_provider,omitempty" jsonschema:"description=OAuth provider name. Uses stored token for authentication."`

	// GraphQL request
	Query         string         `json:"query,omitempty" jsonschema:"description=GraphQL query or mutation string. Required for query/mutation actions."`
	Variables     map[string]any `json:"variables,omitempty" jsonschema:"description=Variables for the GraphQL operation."`
	OperationName string         `json:"operation_name,omitempty" jsonschema:"description=Name of the operation to execute (if query contains multiple)."`

	// Options
	TimeoutSecs     int  `json:"timeout_seconds,omitempty" jsonschema:"description=Request timeout in seconds (default: 30 max: 300)."`
	InsecureSkipTLS bool `json:"insecure_skip_tls,omitempty" jsonschema:"description=Skip TLS verification (only for localhost)."`
}

// GraphQLToolResult is the result of a GraphQL operation.
type GraphQLToolResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`

	// Response data
	Data   any              `json:"data,omitempty"`   // GraphQL response data
	Errors []GraphQLError   `json:"errors,omitempty"` // GraphQL errors
	Schema *GraphQLSchema   `json:"schema,omitempty"` // For introspect action
}

// GraphQLError represents a GraphQL error.
type GraphQLError struct {
	Message    string         `json:"message"`
	Locations  []ErrorLocation `json:"locations,omitempty"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// ErrorLocation represents a location in the GraphQL query.
type ErrorLocation struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// GraphQLSchema represents introspection schema info.
type GraphQLSchema struct {
	QueryType        *SchemaType   `json:"queryType,omitempty"`
	MutationType     *SchemaType   `json:"mutationType,omitempty"`
	SubscriptionType *SchemaType   `json:"subscriptionType,omitempty"`
	Types            []SchemaType  `json:"types,omitempty"`
}

// SchemaType represents a GraphQL type from introspection.
type SchemaType struct {
	Name        string        `json:"name"`
	Kind        string        `json:"kind,omitempty"`
	Description string        `json:"description,omitempty"`
	Fields      []SchemaField `json:"fields,omitempty"`
}

// SchemaField represents a field in a GraphQL type.
type SchemaField struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
}

// graphqlRequest is the request body sent to GraphQL endpoints.
type graphqlRequest struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables,omitempty"`
	OperationName string         `json:"operationName,omitempty"`
}

// graphqlResponse is the response from GraphQL endpoints.
type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []GraphQLError  `json:"errors,omitempty"`
}

// NewGraphQLTool creates a GraphQL tool with OAuth and secrets support.
func NewGraphQLTool(
	userID string,
	allowedProviders []string,
	secrets map[string]string,
	tokenGetter OAuthTokenGetter,
) tool.CallableTool {
	allowed := make(map[string]bool)
	for _, p := range allowedProviders {
		allowed[p] = true
	}

	return function.NewFunctionTool(
		func(ctx context.Context, args GraphQLToolArgs) (*GraphQLToolResult, error) {
			return executeGraphQLTool(ctx, args, userID, allowed, secrets, tokenGetter)
		},
		function.WithName("graphql"),
		function.WithDescription("Execute GraphQL queries and mutations against any GraphQL endpoint. Actions: query (read data), mutation (modify data), introspect (get schema info). Use auth_secret or oauth_provider for authentication."),
	)
}

func executeGraphQLTool(
	ctx context.Context,
	args GraphQLToolArgs,
	userID string,
	allowedProviders map[string]bool,
	secrets map[string]string,
	tokenGetter OAuthTokenGetter,
) (*GraphQLToolResult, error) {
	result := &GraphQLToolResult{}

	// Validate endpoint
	if args.Endpoint == "" {
		result.Message = "Error: 'endpoint' is required"
		return result, nil
	}

	// Validate mutually exclusive auth options
	if args.AuthSecret != "" && args.OAuthProvider != "" {
		result.Message = "Error: auth_secret and oauth_provider are mutually exclusive"
		return result, nil
	}

	switch args.Action {
	case "query", "mutation":
		return executeGraphQLOperation(ctx, args, userID, allowedProviders, secrets, tokenGetter)
	case "introspect":
		return executeGraphQLIntrospect(ctx, args, userID, allowedProviders, secrets, tokenGetter)
	default:
		result.Message = fmt.Sprintf("Unknown action %q. Use: query, mutation, introspect", args.Action)
		return result, nil
	}
}

func executeGraphQLOperation(
	ctx context.Context,
	args GraphQLToolArgs,
	userID string,
	allowedProviders map[string]bool,
	secrets map[string]string,
	tokenGetter OAuthTokenGetter,
) (*GraphQLToolResult, error) {
	result := &GraphQLToolResult{}

	if args.Query == "" {
		result.Message = fmt.Sprintf("Error: 'query' is required for %s action", args.Action)
		return result, nil
	}

	// Build request body
	reqBody := graphqlRequest{
		Query:         args.Query,
		Variables:     args.Variables,
		OperationName: args.OperationName,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		result.Message = fmt.Sprintf("Error encoding request: %v", err)
		return result, nil
	}

	// Execute HTTP request
	respBody, err := executeGraphQLHTTP(ctx, args, userID, allowedProviders, secrets, tokenGetter, bodyBytes)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Parse response
	var gqlResp graphqlResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		result.Message = fmt.Sprintf("Error parsing GraphQL response: %v", err)
		return result, nil
	}

	// Parse data field
	if len(gqlResp.Data) > 0 && string(gqlResp.Data) != "null" {
		var data any
		if err := json.Unmarshal(gqlResp.Data, &data); err != nil {
			result.Message = fmt.Sprintf("Error parsing data field: %v", err)
			return result, nil
		}
		result.Data = data
	}

	result.Errors = gqlResp.Errors
	result.Success = len(gqlResp.Errors) == 0

	if result.Success {
		result.Message = fmt.Sprintf("GraphQL %s executed successfully", args.Action)
	} else {
		// Summarize errors
		var errMsgs []string
		for _, e := range gqlResp.Errors {
			errMsgs = append(errMsgs, e.Message)
		}
		result.Message = fmt.Sprintf("GraphQL errors: %s", strings.Join(errMsgs, "; "))
	}

	return result, nil
}

func executeGraphQLIntrospect(
	ctx context.Context,
	args GraphQLToolArgs,
	userID string,
	allowedProviders map[string]bool,
	secrets map[string]string,
	tokenGetter OAuthTokenGetter,
) (*GraphQLToolResult, error) {
	result := &GraphQLToolResult{}

	// Standard introspection query
	introspectionQuery := `
		query IntrospectionQuery {
			__schema {
				queryType { name }
				mutationType { name }
				subscriptionType { name }
				types {
					name
					kind
					description
					fields(includeDeprecated: false) {
						name
						description
					}
				}
			}
		}
	`

	reqBody := graphqlRequest{
		Query: introspectionQuery,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		result.Message = fmt.Sprintf("Error encoding introspection request: %v", err)
		return result, nil
	}

	respBody, err := executeGraphQLHTTP(ctx, args, userID, allowedProviders, secrets, tokenGetter, bodyBytes)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}

	// Parse introspection response
	var gqlResp struct {
		Data struct {
			Schema struct {
				QueryType        *SchemaType  `json:"queryType"`
				MutationType     *SchemaType  `json:"mutationType"`
				SubscriptionType *SchemaType  `json:"subscriptionType"`
				Types            []SchemaType `json:"types"`
			} `json:"__schema"`
		} `json:"data"`
		Errors []GraphQLError `json:"errors"`
	}

	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		result.Message = fmt.Sprintf("Error parsing introspection response: %v", err)
		return result, nil
	}

	if len(gqlResp.Errors) > 0 {
		result.Errors = gqlResp.Errors
		var errMsgs []string
		for _, e := range gqlResp.Errors {
			errMsgs = append(errMsgs, e.Message)
		}
		result.Message = fmt.Sprintf("Introspection errors: %s", strings.Join(errMsgs, "; "))
		return result, nil
	}

	// Filter out built-in types (those starting with __)
	var userTypes []SchemaType
	for _, t := range gqlResp.Data.Schema.Types {
		if !strings.HasPrefix(t.Name, "__") {
			userTypes = append(userTypes, t)
		}
	}

	result.Success = true
	result.Message = fmt.Sprintf("Schema introspection successful: %d types found", len(userTypes))
	result.Schema = &GraphQLSchema{
		QueryType:        gqlResp.Data.Schema.QueryType,
		MutationType:     gqlResp.Data.Schema.MutationType,
		SubscriptionType: gqlResp.Data.Schema.SubscriptionType,
		Types:            userTypes,
	}

	return result, nil
}

func executeGraphQLHTTP(
	ctx context.Context,
	args GraphQLToolArgs,
	userID string,
	allowedProviders map[string]bool,
	secrets map[string]string,
	tokenGetter OAuthTokenGetter,
	body []byte,
) ([]byte, error) {
	// Build headers
	headers := make(map[string]string)
	for k, v := range args.Headers {
		headers[k] = v
	}
	headers["Content-Type"] = "application/json"

	// Handle OAuth authentication
	if args.OAuthProvider != "" {
		if len(allowedProviders) > 0 && !allowedProviders[args.OAuthProvider] {
			return nil, fmt.Errorf("OAuth provider %q not available to this skill", args.OAuthProvider)
		}

		if tokenGetter == nil {
			return nil, fmt.Errorf("OAuth not configured")
		}

		token, err := tokenGetter(userID, args.OAuthProvider)
		if err != nil {
			if errors.Is(err, oauth.ErrTokenNotFound) {
				return nil, fmt.Errorf("OAuth authorization required for %s. Please authorize via dashboard", args.OAuthProvider)
			}
			if errors.Is(err, oauth.ErrTokenExpired) {
				return nil, fmt.Errorf("OAuth token for %s has expired. Please re-authorize via dashboard", args.OAuthProvider)
			}
			return nil, fmt.Errorf("OAuth token retrieval failed: %w", err)
		}

		tokenType := token.TokenType
		if tokenType == "" {
			tokenType = "Bearer"
		}
		headers["Authorization"] = tokenType + " " + token.AccessToken
	}

	// Handle secret-based auth
	if args.AuthSecret != "" {
		secretValue, ok := secrets[args.AuthSecret]
		if !ok {
			return nil, fmt.Errorf("secret %q not available to this skill", args.AuthSecret)
		}

		authScheme := strings.ToLower(args.AuthScheme)
		if authScheme == "" {
			authScheme = "bearer"
		}

		switch authScheme {
		case "bearer":
			headers["Authorization"] = "Bearer " + secretValue
		case "api-key", "apikey":
			headers["Authorization"] = secretValue
		default:
			headers["Authorization"] = secretValue
		}
	}

	// Set timeout
	timeout := graphqlDefaultTimeout
	if args.TimeoutSecs > 0 {
		timeout = time.Duration(args.TimeoutSecs) * time.Second
		if timeout > graphqlMaxTimeout {
			timeout = graphqlMaxTimeout
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, args.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// Build HTTP client
	client := &http.Client{}
	if args.InsecureSkipTLS {
		// Validate localhost-only
		u, err := url.Parse(args.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("error parsing URL: %w", err)
		}
		host := strings.Split(u.Host, ":")[0]
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return nil, fmt.Errorf("insecure TLS only allowed for localhost")
		}
		client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	}

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("request timed out")
		}
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check HTTP status
	if resp.StatusCode != http.StatusOK {
		bodyPreview, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyPreview))
	}

	// Read response body
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, graphqlMaxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("error reading response: %w", err)
	}

	return respBody, nil
}
