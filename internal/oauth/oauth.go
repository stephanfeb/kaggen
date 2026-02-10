// Package oauth provides OAuth 2.0 authentication flows for kaggen skills.
//
// The oauth package enables skills to securely access OAuth-protected APIs
// (Google, GitHub, Microsoft, etc.) without exposing tokens to the LLM.
// Skills declare their OAuth provider requirements, and the bot handles
// the full authentication lifecycle:
//
//   - Authorization code flow with PKCE support
//   - Secure token storage with encryption at rest
//   - Automatic token refresh before expiration
//   - Multi-user token isolation
//
// Skills reference OAuth providers by name in their SKILL.md frontmatter:
//
//	---
//	name: gmail
//	oauth_providers: [google]
//	---
//
// When making HTTP requests, skills use the oauth_provider field:
//
//	http_request:
//	  url: https://gmail.googleapis.com/gmail/v1/users/me/messages
//	  method: GET
//	  oauth_provider: google
//
// The http_request tool automatically injects the Authorization header
// using the stored OAuth token for the current user.
package oauth
