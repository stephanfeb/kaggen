package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/yourusername/kaggen/internal/oauth"
)

// oauthManager is set by the server when OAuth is configured.
var oauthManager *oauth.FlowManager

// SetOAuthManager sets the OAuth flow manager for the dashboard.
func SetOAuthManager(fm *oauth.FlowManager) {
	oauthManager = fm
}

// OAuthProviderStatus represents a provider's connection status.
type OAuthProviderStatus struct {
	Name      string   `json:"name"`
	Connected bool     `json:"connected"`
	Scopes    []string `json:"scopes,omitempty"`
}

// HandleOAuthProviders lists configured OAuth providers and their connection status.
// GET /api/oauth/providers?user_id=<user_id>
func (d *DashboardAPI) HandleOAuthProviders(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	if oauthManager == nil {
		writeJSON(w, map[string]any{
			"available": false,
			"providers": []OAuthProviderStatus{},
			"error":     "OAuth not configured",
		})
		return
	}

	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = "default"
	}

	// Get configured providers
	providers := make([]OAuthProviderStatus, 0)
	for name := range d.config.OAuth.Providers {
		status := OAuthProviderStatus{
			Name:      name,
			Connected: false,
		}

		// Check if user has a valid token
		token, err := oauthManager.GetToken(userID, name)
		if err == nil && token != nil && !token.IsExpired() {
			status.Connected = true
			status.Scopes = token.Scopes
		}

		providers = append(providers, status)
	}

	writeJSON(w, map[string]any{
		"available": true,
		"providers": providers,
		"user_id":   userID,
	})
}

// HandleOAuthAuthorize starts an OAuth authorization flow.
// POST /api/oauth/authorize
// Body: {"provider": "google", "user_id": "optional"}
func (d *DashboardAPI) HandleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	if oauthManager == nil {
		http.Error(w, `{"error":"OAuth not configured"}`, http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Provider string `json:"provider"`
		UserID   string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.Provider == "" {
		http.Error(w, `{"error":"provider is required"}`, http.StatusBadRequest)
		return
	}

	if req.UserID == "" {
		req.UserID = "default"
	}

	authURL, err := oauthManager.StartAuth(req.UserID, req.Provider)
	if err != nil {
		slog.Error("oauth authorize failed", "provider", req.Provider, "error", err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]string{
		"auth_url": authURL,
		"provider": req.Provider,
		"user_id":  req.UserID,
	})
}

// HandleOAuthCallback handles the OAuth redirect callback.
// GET /api/oauth/callback?code=...&state=...
func (d *DashboardAPI) HandleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	errorParam := r.URL.Query().Get("error")

	// Handle OAuth error response
	if errorParam != "" {
		errorDesc := r.URL.Query().Get("error_description")
		slog.Warn("oauth callback error", "error", errorParam, "description", errorDesc)
		d.renderOAuthResult(w, false, fmt.Sprintf("Authorization failed: %s", errorDesc))
		return
	}

	if code == "" || state == "" {
		d.renderOAuthResult(w, false, "Missing code or state parameter")
		return
	}

	if oauthManager == nil {
		d.renderOAuthResult(w, false, "OAuth not configured")
		return
	}

	userID, err := oauthManager.HandleCallback(r.Context(), code, state)
	if err != nil {
		slog.Error("oauth callback failed", "error", err)
		d.renderOAuthResult(w, false, fmt.Sprintf("Authorization failed: %s", err.Error()))
		return
	}

	slog.Info("oauth callback success", "user_id", userID)
	d.renderOAuthResult(w, true, "Authorization successful! You can close this window.")
}

// renderOAuthResult renders an HTML page with the OAuth result.
func (d *DashboardAPI) renderOAuthResult(w http.ResponseWriter, success bool, message string) {
	status := "error"
	if success {
		status = "success"
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <title>OAuth %s</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            height: 100vh;
            margin: 0;
            background: %s;
        }
        .container {
            text-align: center;
            padding: 40px;
            background: white;
            border-radius: 12px;
            box-shadow: 0 4px 20px rgba(0,0,0,0.1);
        }
        h1 { color: %s; margin-bottom: 16px; }
        p { color: #666; }
        .icon { font-size: 48px; margin-bottom: 16px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="icon">%s</div>
        <h1>%s</h1>
        <p>%s</p>
        <script>
            // Notify parent window and close after delay
            if (window.opener) {
                window.opener.postMessage({type: 'oauth_complete', success: %t}, '*');
            }
            setTimeout(function() { window.close(); }, 3000);
        </script>
    </div>
</body>
</html>`,
		status,
		map[bool]string{true: "#f0fdf4", false: "#fef2f2"}[success],
		map[bool]string{true: "#16a34a", false: "#dc2626"}[success],
		map[bool]string{true: "\u2705", false: "\u274c"}[success],
		map[bool]string{true: "Connected!", false: "Connection Failed"}[success],
		message,
		success,
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

// HandleOAuthRevoke revokes an OAuth token for a provider.
// POST /api/oauth/revoke
// Body: {"provider": "google", "user_id": "optional"}
func (d *DashboardAPI) HandleOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	if oauthManager == nil {
		http.Error(w, `{"error":"OAuth not configured"}`, http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Provider string `json:"provider"`
		UserID   string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.Provider == "" {
		http.Error(w, `{"error":"provider is required"}`, http.StatusBadRequest)
		return
	}

	if req.UserID == "" {
		req.UserID = "default"
	}

	if err := oauthManager.RevokeToken(req.UserID, req.Provider); err != nil {
		slog.Error("oauth revoke failed", "provider", req.Provider, "error", err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{
		"status":   "revoked",
		"provider": req.Provider,
		"user_id":  req.UserID,
	})
}

// HandleOAuthStatus checks the OAuth status for a user/provider.
// GET /api/oauth/status?provider=google&user_id=optional
func (d *DashboardAPI) HandleOAuthStatus(w http.ResponseWriter, r *http.Request) {
	d.setCORSHeaders(w, r)

	if oauthManager == nil {
		writeJSON(w, map[string]any{
			"available": false,
			"error":     "OAuth not configured",
		})
		return
	}

	provider := r.URL.Query().Get("provider")
	userID := r.URL.Query().Get("user_id")

	if provider == "" {
		http.Error(w, `{"error":"provider query param is required"}`, http.StatusBadRequest)
		return
	}

	if userID == "" {
		userID = "default"
	}

	token, err := oauthManager.GetToken(userID, provider)
	if err != nil {
		writeJSON(w, map[string]any{
			"provider":  provider,
			"user_id":   userID,
			"connected": false,
			"error":     err.Error(),
		})
		return
	}

	writeJSON(w, map[string]any{
		"provider":   provider,
		"user_id":    userID,
		"connected":  true,
		"expires_at": token.ExpiresAt,
		"scopes":     token.Scopes,
	})
}
