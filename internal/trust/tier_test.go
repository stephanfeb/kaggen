package trust

import (
	"testing"

	"github.com/yourusername/kaggen/internal/config"
)

func TestClassify_NilConfig(t *testing.T) {
	tier := Classify("1234567890", 12345, nil)
	if tier != TrustTierAuthorized {
		t.Errorf("expected Authorized for nil config, got %s", tier)
	}
}

func TestClassify_OwnerByPhone(t *testing.T) {
	cfg := &config.TrustConfig{
		OwnerPhones: []string{"+1234567890"},
	}
	tier := Classify("1234567890", 0, cfg)
	if tier != TrustTierOwner {
		t.Errorf("expected Owner, got %s", tier)
	}
}

func TestClassify_OwnerByPhoneWithPlus(t *testing.T) {
	cfg := &config.TrustConfig{
		OwnerPhones: []string{"+1234567890"},
	}
	tier := Classify("+1234567890", 0, cfg)
	if tier != TrustTierOwner {
		t.Errorf("expected Owner, got %s", tier)
	}
}

func TestClassify_OwnerByTelegramID(t *testing.T) {
	cfg := &config.TrustConfig{
		OwnerTelegram: []int64{12345},
	}
	tier := Classify("", 12345, cfg)
	if tier != TrustTierOwner {
		t.Errorf("expected Owner, got %s", tier)
	}
}

func TestClassify_NotOwner(t *testing.T) {
	cfg := &config.TrustConfig{
		OwnerPhones:   []string{"+1234567890"},
		OwnerTelegram: []int64{12345},
	}
	tier := Classify("9999999999", 99999, cfg)
	if tier != TrustTierAuthorized {
		t.Errorf("expected Authorized (not owner), got %s", tier)
	}
}

func TestClassifyWithAllowlist_Owner(t *testing.T) {
	cfg := &config.TrustConfig{
		OwnerPhones: []string{"+1234567890"},
	}
	tier := ClassifyWithAllowlist("1234567890", 0, cfg, false)
	if tier != TrustTierOwner {
		t.Errorf("expected Owner, got %s", tier)
	}
}

func TestClassifyWithAllowlist_Authorized(t *testing.T) {
	cfg := &config.TrustConfig{
		OwnerPhones: []string{"+1234567890"},
	}
	tier := ClassifyWithAllowlist("9999999999", 0, cfg, true)
	if tier != TrustTierAuthorized {
		t.Errorf("expected Authorized, got %s", tier)
	}
}

func TestClassifyWithAllowlist_ThirdParty(t *testing.T) {
	cfg := &config.TrustConfig{
		OwnerPhones: []string{"+1234567890"},
	}
	tier := ClassifyWithAllowlist("9999999999", 0, cfg, false)
	if tier != TrustTierThirdParty {
		t.Errorf("expected ThirdParty, got %s", tier)
	}
}

func TestTrustTier_String(t *testing.T) {
	tests := []struct {
		tier TrustTier
		want string
	}{
		{TrustTierOwner, "owner"},
		{TrustTierAuthorized, "authorized"},
		{TrustTierThirdParty, "third_party"},
		{TrustTier(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.tier.String(); got != tt.want {
			t.Errorf("TrustTier(%d).String() = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

func TestTrustTier_Helpers(t *testing.T) {
	if !TrustTierOwner.IsOwner() {
		t.Error("expected Owner.IsOwner() = true")
	}
	if TrustTierAuthorized.IsOwner() {
		t.Error("expected Authorized.IsOwner() = false")
	}

	if !TrustTierOwner.IsAuthorized() {
		t.Error("expected Owner.IsAuthorized() = true")
	}
	if !TrustTierAuthorized.IsAuthorized() {
		t.Error("expected Authorized.IsAuthorized() = true")
	}
	if TrustTierThirdParty.IsAuthorized() {
		t.Error("expected ThirdParty.IsAuthorized() = false")
	}

	if TrustTierOwner.IsThirdParty() {
		t.Error("expected Owner.IsThirdParty() = false")
	}
	if !TrustTierThirdParty.IsThirdParty() {
		t.Error("expected ThirdParty.IsThirdParty() = true")
	}
}

func TestTrustTier_CanUseTools(t *testing.T) {
	if !TrustTierOwner.CanUseTools() {
		t.Error("expected Owner.CanUseTools() = true")
	}
	if !TrustTierAuthorized.CanUseTools() {
		t.Error("expected Authorized.CanUseTools() = true")
	}
	if TrustTierThirdParty.CanUseTools() {
		t.Error("expected ThirdParty.CanUseTools() = false")
	}
}

func TestTrustTier_CanSendToOthers(t *testing.T) {
	if !TrustTierOwner.CanSendToOthers() {
		t.Error("expected Owner.CanSendToOthers() = true")
	}
	if TrustTierAuthorized.CanSendToOthers() {
		t.Error("expected Authorized.CanSendToOthers() = false")
	}
	if TrustTierThirdParty.CanSendToOthers() {
		t.Error("expected ThirdParty.CanSendToOthers() = false")
	}
}
