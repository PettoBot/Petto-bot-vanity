package identity

import "testing"

func TestVanityComparisonsAndUnicodeNormalization(t *testing.T) {
	member := MemberIdentity{Username: "  Cinnamochi  ", GlobalName: "Cinna Mochi", GuildNickname: "🌸 Cinnamochi"}
	base := VanityRule{Name: "test", Source: VanityUsername, Word: "cinnamochi", Normalization: Normalization{CaseFold: true, TrimSpace: true, CollapseSpace: true}}
	cases := []struct {
		comparison Comparison
		word       string
		want       bool
	}{
		{ComparisonEquals, "cinnamochi", true},
		{ComparisonContains, "innam", true},
		{ComparisonStartsWith, "cinn", true},
		{ComparisonEndsWith, "mochi", true},
		{ComparisonEquals, "other", false},
	}
	for _, test := range cases {
		rule := base
		rule.Comparison, rule.Word = test.comparison, test.word
		got, err := MatchVanity(rule, member)
		if err != nil || got != test.want {
			t.Fatalf("%s %q: got %t, err %v; want %t", test.comparison, test.word, got, err, test.want)
		}
	}
	unicodeRule := VanityRule{Name: "unicode", Source: VanityGuildNickname, Comparison: ComparisonContains, Word: "🌸", Normalization: Normalization{CaseFold: true, TrimSpace: true, CollapseSpace: true}}
	if got, err := MatchVanity(unicodeRule, member); err != nil || !got {
		t.Fatalf("unicode match got %t, err %v", got, err)
	}
}

func TestCustomStatusVanityValue(t *testing.T) {
	rule := VanityRule{
		Name:          "custom-status",
		Source:        VanityCustomStatus,
		Word:          "cinnamochi",
		Comparison:    ComparisonEquals,
		Normalization: Normalization{CaseFold: true, TrimSpace: true},
	}

	member := MemberIdentity{CustomStatus: "  CinnaMochi  "}
	if got, err := MatchVanity(rule, member); err != nil || !got {
		t.Fatalf("custom status match got %t, err %v", got, err)
	}

	member.CustomStatus = "another status"
	if got, err := MatchVanity(rule, member); err != nil || got {
		t.Fatalf("different custom status match got %t, err %v", got, err)
	}
}

func TestGuildTagConditions(t *testing.T) {
	enabled := true
	disabled := false
	primary := &PrimaryGuild{IdentityGuildID: "707", IdentityEnabled: &enabled, Tag: "CINN"}
	cases := []struct {
		condition GuildTagCondition
		value     string
		want      bool
	}{
		{ConditionIsGuildID, "707", true}, {ConditionIsGuildID, "other", false},
		{ConditionIsNotGuildID, "other", true}, {ConditionIsNotGuildID, "707", false},
		{ConditionIdentityEnabled, "", true}, {ConditionIdentityDisabled, "", false},
		{ConditionTagEquals, "cinn", true}, {ConditionTagNotEquals, "other", true},
	}
	for _, test := range cases {
		got := MatchGuildTag(GuildTagRule{Condition: test.condition, Value: test.value}, primary)
		if got != test.want {
			t.Fatalf("%s %q: got %t, want %t", test.condition, test.value, got, test.want)
		}
	}
	if MatchGuildTag(GuildTagRule{Condition: ConditionIdentityEnabled}, &PrimaryGuild{IdentityEnabled: &disabled}) {
		t.Fatal("disabled identity matched identity_enabled")
	}
	if !MatchGuildTag(GuildTagRule{Condition: ConditionIdentityDisabled}, nil) {
		t.Fatal("nil primary guild must be identity_disabled")
	}
	if !MatchGuildTag(GuildTagRule{Condition: ConditionIsGuildID, Value: "707"}, &PrimaryGuild{IdentityGuildID: "707"}) {
		t.Fatal("is_guild_id must match when Discord omits identity_enabled")
	}
	if !MatchGuildTag(GuildTagRule{Condition: ConditionTagEquals, Value: "cinn"}, &PrimaryGuild{Tag: "CINN"}) {
		t.Fatal("tag_equals must match when Discord omits identity_enabled")
	}
}

func TestResolvedDisplayNamePriority(t *testing.T) {
	member := MemberIdentity{Username: "username", GlobalName: "global", GuildNickname: "nickname", DisplayName: "display"}
	if got := ResolveDisplayName(member); got != "nickname" {
		t.Fatalf("got %q, want nickname", got)
	}
	member.GuildNickname = ""
	if got := ResolveDisplayName(member); got != "global" {
		t.Fatalf("got %q, want global", got)
	}
}
