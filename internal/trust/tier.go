// Package trust implements trust-tier security for message routing and access control.
package trust

import (
	"strings"

	"github.com/yourusername/kaggen/internal/config"
)

// TrustTier represents the trust level of a session/user.
type TrustTier int

const (
	// TrustTierOwner has full access: all tools, shell, file system, send messages.
	TrustTierOwner TrustTier = iota

	// TrustTierAuthorized is allowlisted with configurable access (current behavior).
	TrustTierAuthorized

	// TrustTierThirdParty is sandboxed: conversation only, can request message relay.
	TrustTierThirdParty
)

// String returns a human-readable name for the trust tier.
func (t TrustTier) String() string {
	switch t {
	case TrustTierOwner:
		return "owner"
	case TrustTierAuthorized:
		return "authorized"
	case TrustTierThirdParty:
		return "third_party"
	default:
		return "unknown"
	}
}

// Classify determines the trust tier for a sender based on their identifiers.
// It checks owner status first, then authorized allowlists, defaulting to third-party.
func Classify(phone string, telegramID int64, cfg *config.TrustConfig) TrustTier {
	if cfg == nil {
		// No trust config means everyone is authorized (backwards compatible)
		return TrustTierAuthorized
	}

	// Check owner first - highest privilege
	if isOwner(phone, telegramID, cfg) {
		return TrustTierOwner
	}

	// Check authorized allowlists
	// Note: The actual allowlist check happens in the channel layer (telegram/whatsapp)
	// If we get here with a message, the sender passed channel-level authorization.
	// We distinguish owner from authorized by checking the owner lists above.
	// The third-party tier is for senders who aren't in ANY allowlist.

	return TrustTierAuthorized
}

// ClassifyWithAllowlist determines trust tier considering both owner status and allowlist membership.
// Use this when you need to explicitly check if a sender is in the allowlist.
func ClassifyWithAllowlist(phone string, telegramID int64, cfg *config.TrustConfig, isInAllowlist bool) TrustTier {
	if cfg == nil {
		// No trust config means everyone is authorized (backwards compatible)
		return TrustTierAuthorized
	}

	// Check owner first
	if isOwner(phone, telegramID, cfg) {
		return TrustTierOwner
	}

	// If in allowlist, they're authorized
	if isInAllowlist {
		return TrustTierAuthorized
	}

	// Otherwise third-party
	return TrustTierThirdParty
}

// isOwner checks if the sender is in the owner lists.
func isOwner(phone string, telegramID int64, cfg *config.TrustConfig) bool {
	// Check phone
	if phone != "" {
		normalizedPhone := normalizePhone(phone)
		for _, ownerPhone := range cfg.OwnerPhones {
			if normalizePhone(ownerPhone) == normalizedPhone {
				return true
			}
		}
	}

	// Check Telegram ID
	if telegramID != 0 {
		for _, ownerID := range cfg.OwnerTelegram {
			if ownerID == telegramID {
				return true
			}
		}
	}

	return false
}

// normalizePhone strips the leading + from a phone number for comparison.
func normalizePhone(phone string) string {
	return strings.TrimPrefix(phone, "+")
}

// IsOwner returns true if the tier is Owner.
func (t TrustTier) IsOwner() bool {
	return t == TrustTierOwner
}

// IsAuthorized returns true if the tier is Owner or Authorized.
func (t TrustTier) IsAuthorized() bool {
	return t == TrustTierOwner || t == TrustTierAuthorized
}

// IsThirdParty returns true if the tier is ThirdParty.
func (t TrustTier) IsThirdParty() bool {
	return t == TrustTierThirdParty
}

// CanUseTools returns true if this tier can use tools (owner and authorized only).
func (t TrustTier) CanUseTools() bool {
	return t == TrustTierOwner || t == TrustTierAuthorized
}

// CanSendToOthers returns true if this tier can use send_* tools (owner only).
func (t TrustTier) CanSendToOthers() bool {
	return t == TrustTierOwner
}
