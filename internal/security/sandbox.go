// Package security provides security-related utilities for kaggen.
package security

import (
	"fmt"
	"regexp"
	"strings"
)

// CommandSandbox validates shell commands against security policies.
type CommandSandbox struct {
	blockedPatterns []*regexp.Regexp
	enabled         bool
}

// DefaultBlockedPatterns returns the default list of dangerous command patterns.
func DefaultBlockedPatterns() []string {
	return []string{
		// Destructive file system commands
		`(?i)rm\s+(-[a-z]*\s+)*(-r|--recursive).*(/|~|\*)`,      // rm -rf / or ~
		`(?i)rm\s+(-[a-z]*\s+)*--no-preserve-root`,              // rm --no-preserve-root
		`(?i)mkfs\s+`,                                           // format disk
		`(?i)dd\s+.*if=/dev/(zero|random|urandom).*of=/dev/`,    // overwrite disk
		`(?i):\(\)\{\s*:\|:&\s*\};:`,                            // fork bomb
		`(?i)>\s*/dev/sd[a-z]`,                                  // write to disk device

		// Privilege escalation
		`(?i)^sudo\s+`,                                           // sudo commands
		`(?i)\|\s*sudo\s+`,                                       // piped to sudo
		`(?i)^su\s+`,                                             // switch user
		`(?i)chmod\s+.*777`,                                      // world-writable permissions
		`(?i)chown\s+.*root`,                                     // change owner to root

		// Remote code execution / exfiltration
		`(?i)curl\s+.*\|\s*(ba)?sh`,                              // curl pipe to shell
		`(?i)wget\s+.*\|\s*(ba)?sh`,                              // wget pipe to shell
		`(?i)curl\s+.*-o\s*/`,                                    // curl download to root
		`(?i)nc\s+(-[a-z]*\s+)*-e`,                               // netcat reverse shell
		`(?i)bash\s+(-[a-z]*\s+)*-i`,                             // interactive bash (often in reverse shells)

		// Credential/sensitive data access
		`(?i)cat\s+.*(\.ssh/|\.gnupg/|\.aws/|\.kube/)`,          // read SSH/GPG/AWS/K8s credentials
		`(?i)cat\s+.*(/etc/passwd|/etc/shadow)`,                  // read system passwords

		// System modification
		`(?i)>\s*/etc/`,                                          // write to /etc
		`(?i)echo\s+.*>\s*/etc/`,                                 // echo to /etc
		`(?i)systemctl\s+(stop|disable|mask)`,                    // disable system services
		`(?i)shutdown|reboot|halt|poweroff`,                      // system shutdown
	}
}

// NewCommandSandbox creates a new command sandbox with the given blocked patterns.
// If patterns is nil or empty, uses DefaultBlockedPatterns.
func NewCommandSandbox(patterns []string, enabled bool) (*CommandSandbox, error) {
	if len(patterns) == 0 {
		patterns = DefaultBlockedPatterns()
	}

	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", p, err)
		}
		compiled = append(compiled, re)
	}

	return &CommandSandbox{
		blockedPatterns: compiled,
		enabled:         enabled,
	}, nil
}

// ValidationResult contains the result of command validation.
type ValidationResult struct {
	Allowed bool
	Reason  string
	Pattern string // The pattern that matched (if blocked)
}

// Validate checks if a command is allowed to execute.
// Returns a ValidationResult indicating whether the command is allowed.
func (s *CommandSandbox) Validate(command string) ValidationResult {
	if !s.enabled {
		return ValidationResult{Allowed: true}
	}

	// Normalize the command for matching
	normalized := strings.TrimSpace(command)

	for _, pattern := range s.blockedPatterns {
		if pattern.MatchString(normalized) {
			return ValidationResult{
				Allowed: false,
				Reason:  "command matches blocked security pattern",
				Pattern: pattern.String(),
			}
		}
	}

	return ValidationResult{Allowed: true}
}

// IsEnabled returns whether the sandbox is enabled.
func (s *CommandSandbox) IsEnabled() bool {
	return s.enabled
}

// PathValidator validates file paths against security policies.
type PathValidator struct {
	blockedPaths      []*regexp.Regexp
	restrictWorkspace bool
}

// DefaultBlockedPaths returns the default list of blocked path patterns.
func DefaultBlockedPaths() []string {
	return []string{
		// System credentials and secrets
		`(?i)/etc/passwd`,
		`(?i)/etc/shadow`,
		`(?i)/etc/sudoers`,
		`(?i)\.ssh/`,
		`(?i)\.gnupg/`,
		`(?i)\.aws/credentials`,
		`(?i)\.kube/config`,
		`(?i)\.docker/config\.json`,

		// Environment files with secrets
		`(?i)\.env($|\.local|\.production|\.secret)`,

		// Private keys
		`(?i)\.pem$`,
		`(?i)_rsa$`,
		`(?i)\.key$`,
		`(?i)id_(rsa|dsa|ecdsa|ed25519)$`,
	}
}

// NewPathValidator creates a new path validator.
func NewPathValidator(blockedPaths []string, restrictWorkspace bool) (*PathValidator, error) {
	if len(blockedPaths) == 0 {
		blockedPaths = DefaultBlockedPaths()
	}

	compiled := make([]*regexp.Regexp, 0, len(blockedPaths))
	for _, p := range blockedPaths {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", p, err)
		}
		compiled = append(compiled, re)
	}

	return &PathValidator{
		blockedPaths:      compiled,
		restrictWorkspace: restrictWorkspace,
	}, nil
}

// PathValidationResult contains the result of path validation.
type PathValidationResult struct {
	Allowed bool
	Reason  string
	Pattern string
}

// ValidatePath checks if a path is allowed to be accessed.
func (v *PathValidator) ValidatePath(workspace, path string) PathValidationResult {
	// Normalize the path
	normalizedPath := path
	if !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "~") {
		normalizedPath = "/" + path
	}

	// Check blocked patterns
	for _, pattern := range v.blockedPaths {
		if pattern.MatchString(normalizedPath) {
			return PathValidationResult{
				Allowed: false,
				Reason:  "path matches blocked security pattern",
				Pattern: pattern.String(),
			}
		}
	}

	// Check workspace restriction (path traversal prevention)
	if v.restrictWorkspace && workspace != "" {
		// Resolve the actual path
		var absPath string
		if strings.HasPrefix(path, "/") {
			absPath = path
		} else if strings.HasPrefix(path, "~/") {
			// Allow ~ expansion
			return PathValidationResult{Allowed: true}
		} else {
			absPath = path
		}

		// Check for path traversal attempts
		cleanPath := strings.Replace(absPath, "\\", "/", -1)
		if strings.Contains(cleanPath, "../") || strings.Contains(cleanPath, "/..") {
			return PathValidationResult{
				Allowed: false,
				Reason:  "path traversal detected",
				Pattern: "../",
			}
		}
	}

	return PathValidationResult{Allowed: true}
}
