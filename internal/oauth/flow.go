package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yourusername/kaggen/internal/config"
)

const (
	// flowExpiry is how long a pending OAuth flow remains valid.
	flowExpiry = 10 * time.Minute
)

// FlowManager handles OAuth 2.0 authorization flows.
type FlowManager struct {
	config      *config.Config
	tokenStore  TokenStore
	httpClient  *http.Client
	callbackURL string
	logger      *slog.Logger

	// activeFlows maps state parameter to pending flow
	activeFlows sync.Map
}

// PendingFlow represents an in-progress OAuth authorization.
type PendingFlow struct {
	UserID       string
	Provider     string
	State        string
	CodeVerifier string // PKCE code verifier (empty if PKCE not used)
	RedirectURI  string
	CreatedAt    time.Time
}

// NewFlowManager creates a new OAuth flow manager.
func NewFlowManager(cfg *config.Config, tokenStore TokenStore, callbackURL string, logger *slog.Logger) *FlowManager {
	if logger == nil {
		logger = slog.Default()
	}

	fm := &FlowManager{
		config:      cfg,
		tokenStore:  tokenStore,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		callbackURL: callbackURL,
		logger:      logger,
	}

	// Set up token refresh
	if refresher, ok := tokenStore.(Refresher); ok {
		refresher.SetRefresher(fm.refreshToken)
	}

	return fm
}

// StartAuth initiates an OAuth authorization flow.
// Returns the authorization URL that the user should be redirected to.
func (fm *FlowManager) StartAuth(userID, providerName string) (string, error) {
	provider, ok := fm.config.GetOAuthProvider(providerName)
	if !ok {
		return "", ErrProviderNotConfigured
	}

	// Generate state
	state, err := GenerateState()
	if err != nil {
		return "", fmt.Errorf("oauth: failed to generate state: %w", err)
	}

	// Generate PKCE code verifier if required
	var codeVerifier, codeChallenge string
	if provider.PKCE {
		codeVerifier, err = GenerateCodeVerifier()
		if err != nil {
			return "", fmt.Errorf("oauth: failed to generate code verifier: %w", err)
		}
		codeChallenge = ComputeCodeChallenge(codeVerifier)
	}

	// Build redirect URI
	redirectURI := fm.callbackURL
	if provider.RedirectURI != "" {
		redirectURI = provider.RedirectURI
	}

	// Build authorization URL
	params := url.Values{
		"client_id":     {config.ResolveSecret(provider.ClientID)},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"state":         {state},
	}

	if len(provider.Scopes) > 0 {
		params.Set("scope", strings.Join(provider.Scopes, " "))
	}

	if provider.PKCE {
		params.Set("code_challenge", codeChallenge)
		params.Set("code_challenge_method", "S256")
	}

	// Add provider-specific auth params (e.g., access_type=offline for Google)
	for k, v := range provider.AuthParams {
		params.Set(k, v)
	}

	// Store pending flow
	pending := &PendingFlow{
		UserID:       userID,
		Provider:     providerName,
		State:        state,
		CodeVerifier: codeVerifier,
		RedirectURI:  redirectURI,
		CreatedAt:    time.Now(),
	}
	fm.activeFlows.Store(state, pending)

	// Clean up old flows periodically
	go fm.cleanupOldFlows()

	authURL := provider.AuthURL + "?" + params.Encode()
	fm.logger.Info("oauth flow started",
		"provider", providerName,
		"user_id", userID,
		"state", state[:8]+"...",
	)

	return authURL, nil
}

// HandleCallback processes an OAuth callback with authorization code.
// Returns the user ID associated with the flow.
func (fm *FlowManager) HandleCallback(ctx context.Context, code, state string) (string, error) {
	// Look up pending flow
	val, ok := fm.activeFlows.LoadAndDelete(state)
	if !ok {
		return "", ErrInvalidState
	}

	pending := val.(*PendingFlow)

	// Check if flow expired
	if time.Since(pending.CreatedAt) > flowExpiry {
		return "", ErrInvalidState
	}

	// Get provider config
	provider, ok := fm.config.GetOAuthProvider(pending.Provider)
	if !ok {
		return "", ErrProviderNotConfigured
	}

	// Exchange code for token
	token, err := fm.exchangeCode(ctx, provider, pending, code)
	if err != nil {
		fm.logger.Error("oauth token exchange failed",
			"provider", pending.Provider,
			"user_id", pending.UserID,
			"error", err,
		)
		return "", err
	}

	// Store token
	if err := fm.tokenStore.Store(pending.UserID, pending.Provider, token); err != nil {
		fm.logger.Error("oauth token storage failed",
			"provider", pending.Provider,
			"user_id", pending.UserID,
			"error", err,
		)
		return "", fmt.Errorf("oauth: failed to store token: %w", err)
	}

	fm.logger.Info("oauth flow completed",
		"provider", pending.Provider,
		"user_id", pending.UserID,
		"expires_at", token.ExpiresAt,
	)

	return pending.UserID, nil
}

// exchangeCode exchanges an authorization code for tokens.
func (fm *FlowManager) exchangeCode(ctx context.Context, provider config.OAuthProvider, pending *PendingFlow, code string) (*Token, error) {
	data := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {pending.RedirectURI},
		"client_id":    {config.ResolveSecret(provider.ClientID)},
	}

	// Add client secret (not required for PKCE-only flows, but most providers still want it)
	if provider.ClientSecret != "" {
		data.Set("client_secret", config.ResolveSecret(provider.ClientSecret))
	}

	// Add PKCE code verifier
	if pending.CodeVerifier != "" {
		data.Set("code_verifier", pending.CodeVerifier)
	}

	return fm.tokenRequest(ctx, provider.TokenURL, data, pending.Provider)
}

// refreshToken refreshes an expired token.
func (fm *FlowManager) refreshToken(userID, providerName string, current *Token) (*Token, error) {
	if current.RefreshToken == "" {
		return nil, ErrNoRefreshToken
	}

	provider, ok := fm.config.GetOAuthProvider(providerName)
	if !ok {
		return nil, ErrProviderNotConfigured
	}

	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {current.RefreshToken},
		"client_id":     {config.ResolveSecret(provider.ClientID)},
	}

	if provider.ClientSecret != "" {
		data.Set("client_secret", config.ResolveSecret(provider.ClientSecret))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	newToken, err := fm.tokenRequest(ctx, provider.TokenURL, data, providerName)
	if err != nil {
		fm.logger.Warn("oauth token refresh failed",
			"provider", providerName,
			"user_id", userID,
			"error", err,
		)
		return nil, err
	}

	// Some providers don't return a new refresh token - preserve the old one
	if newToken.RefreshToken == "" {
		newToken.RefreshToken = current.RefreshToken
	}

	fm.logger.Info("oauth token refreshed",
		"provider", providerName,
		"user_id", userID,
		"expires_at", newToken.ExpiresAt,
	)

	return newToken, nil
}

// tokenRequest makes a token endpoint request and parses the response.
func (fm *FlowManager) tokenRequest(ctx context.Context, tokenURL string, data url.Values, provider string) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := fm.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("oauth: failed to read response: %w", err)
	}

	// GitHub returns tokens as application/x-www-form-urlencoded by default
	// Check Content-Type and handle accordingly
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		return fm.parseFormToken(string(body), provider)
	}

	// Parse JSON response
	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("oauth: failed to parse token response: %w", err)
	}

	if tokenResp.Error != "" {
		return nil, fmt.Errorf("oauth: %s: %s", tokenResp.Error, tokenResp.ErrorDesc)
	}

	if tokenResp.AccessToken == "" {
		return nil, ErrAuthorizationFailed
	}

	token := tokenResp.ToToken()

	// Parse scopes if returned
	if tokenResp.Scope != "" {
		token.Scopes = strings.Split(tokenResp.Scope, " ")
	}

	return token, nil
}

// parseFormToken parses a token from URL-encoded form data (GitHub style).
func (fm *FlowManager) parseFormToken(body, provider string) (*Token, error) {
	values, err := url.ParseQuery(body)
	if err != nil {
		return nil, fmt.Errorf("oauth: failed to parse form token: %w", err)
	}

	if errVal := values.Get("error"); errVal != "" {
		return nil, fmt.Errorf("oauth: %s: %s", errVal, values.Get("error_description"))
	}

	accessToken := values.Get("access_token")
	if accessToken == "" {
		return nil, ErrAuthorizationFailed
	}

	// GitHub tokens don't expire by default, set a long expiry
	expiresIn := 0
	if exp := values.Get("expires_in"); exp != "" {
		fmt.Sscanf(exp, "%d", &expiresIn)
	}
	if expiresIn == 0 {
		expiresIn = 365 * 24 * 60 * 60 // 1 year for GitHub
	}

	token := &Token{
		AccessToken:  accessToken,
		RefreshToken: values.Get("refresh_token"),
		TokenType:    values.Get("token_type"),
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
	}

	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}

	if scope := values.Get("scope"); scope != "" {
		token.Scopes = strings.Split(scope, ",") // GitHub uses comma-separated
	}

	return token, nil
}

// cleanupOldFlows removes expired pending flows.
func (fm *FlowManager) cleanupOldFlows() {
	now := time.Now()
	fm.activeFlows.Range(func(key, value any) bool {
		pending := value.(*PendingFlow)
		if now.Sub(pending.CreatedAt) > flowExpiry {
			fm.activeFlows.Delete(key)
		}
		return true
	})
}

// GetToken retrieves a token for a user/provider, returning an error if authorization is needed.
func (fm *FlowManager) GetToken(userID, provider string) (*Token, error) {
	return fm.tokenStore.Get(userID, provider)
}

// RevokeToken removes a user's token for a provider.
func (fm *FlowManager) RevokeToken(userID, provider string) error {
	return fm.tokenStore.Delete(userID, provider)
}

// ListConnections returns providers with valid tokens for a user.
func (fm *FlowManager) ListConnections(userID string) ([]string, error) {
	return fm.tokenStore.List(userID)
}

// NeedsAuth returns providers that require authorization.
func (fm *FlowManager) NeedsAuth(userID string, providers []string) []string {
	return fm.tokenStore.NeedsAuth(userID, providers)
}
