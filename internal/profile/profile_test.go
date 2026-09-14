package profile

import (
	"net/url"
	"strings"
	"testing"
)

func TestValidateURLRejectsSSRFTargets(t *testing.T) {
	for _, raw := range []string{"http://localhost/image.png", "https://127.0.0.1/image.png", "https://metadata.google.internal/image.png"} {
		parsed, _ := url.Parse(raw)
		if err := ValidateURL(parsed, map[string]struct{}{"example.com": {}}); err == nil {
			t.Fatalf("accepted SSRF target %s", raw)
		}
	}
}

func TestProfileTextLimits(t *testing.T) {
	long := strings.Repeat("x", 33)
	if err := validateMemberUpdate(MemberUpdate{Nickname: &long}); err == nil {
		t.Fatal("accepted an oversized nickname")
	}
	long = strings.Repeat("x", 191)
	if err := validateMemberUpdate(MemberUpdate{Bio: &long}); err == nil {
		t.Fatal("accepted an oversized bio")
	}
}

func TestValidateURLRequiresExplicitAllowlist(t *testing.T) {
	parsed, _ := url.Parse("https://example.com/image.png")
	if err := ValidateURL(parsed, map[string]struct{}{}); err == nil {
		t.Fatal("accepted an empty allowlist")
	}
	if err := ValidateURL(parsed, map[string]struct{}{"example.com": {}}); err != nil {
		t.Fatal(err)
	}
}
