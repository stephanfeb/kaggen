package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

const (
	// codeVerifierLength is the length of the PKCE code verifier in bytes.
	// RFC 7636 recommends 32-96 bytes, we use 32 for 43 base64url characters.
	codeVerifierLength = 32
)

// GenerateCodeVerifier generates a cryptographically random PKCE code verifier.
// The verifier is a 43-character base64url-encoded string.
func GenerateCodeVerifier() (string, error) {
	bytes := make([]byte, codeVerifierLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// ComputeCodeChallenge computes the PKCE code challenge from a code verifier.
// Uses the S256 method: BASE64URL(SHA256(code_verifier))
func ComputeCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// GenerateState generates a cryptographically random state parameter.
// The state is used to prevent CSRF attacks during OAuth flows.
func GenerateState() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
