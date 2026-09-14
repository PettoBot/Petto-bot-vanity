package discord

import (
	"strings"
	"testing"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/identity"
)

func TestSyncProgressBarEndpoints(t *testing.T) {
	empty := syncProgressBar(0, 10)
	if !strings.HasPrefix(empty, syncStartEmpty) || !strings.HasSuffix(empty, syncEndEmpty) {
		t.Fatalf("empty progress bar has wrong endpoints: %s", empty)
	}
	if strings.Count(empty, syncMiddleEmpty) != syncProgressSegments-2 {
		t.Fatalf("empty progress bar has wrong middle segments: %s", empty)
	}

	full := syncProgressBar(10, 10)
	if !strings.HasPrefix(full, syncStartFull) || !strings.HasSuffix(full, syncEndFull) {
		t.Fatalf("full progress bar has wrong endpoints: %s", full)
	}
	if strings.Count(full, syncMiddleFull) != syncProgressSegments-2 {
		t.Fatalf("full progress bar has wrong middle segments: %s", full)
	}
}

func TestSyncProgressBarUsesHalfSegment(t *testing.T) {
	bar := syncProgressBar(1, 16)
	if !strings.HasPrefix(bar, syncStartHalf) {
		t.Fatalf("expected half-filled start segment, got %s", bar)
	}
}

func TestSyncPercentIsBounded(t *testing.T) {
	cases := []struct {
		done, total, want int
	}{
		{0, 0, 0},
		{0, 10, 0},
		{5, 10, 50},
		{10, 10, 100},
		{20, 10, 100},
	}
	for _, test := range cases {
		if got := syncPercent(test.done, test.total); got != test.want {
			t.Fatalf("syncPercent(%d, %d) = %d, want %d", test.done, test.total, got, test.want)
		}
	}
}

func TestManualSyncStatsUnknownIdentityIsWarning(t *testing.T) {
	stats := manualSyncStats{StartedAt: time.Now()}
	stats.apply(syncMemberResult{unknownIdentity: true, warnings: 1, warning: "primary_guild unknown"})
	if stats.Processed != 1 || stats.UnknownIdentity != 1 || stats.Warnings != 1 || stats.Errors != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if stage := finalSyncStage(stats); stage != "Completed with warnings" {
		t.Fatalf("unexpected final stage: %s", stage)
	}
}

func TestSanitizeSyncMessage(t *testing.T) {
	value := sanitizeSyncMessage("  hello\n`world`  ")
	if value != "hello 'world'" {
		t.Fatalf("unexpected sanitized value: %q", value)
	}
}

func TestPrimaryLookupCircuitOpensAndRecovers(t *testing.T) {
	circuit := &primaryLookupCircuit{}
	now := time.Now()
	for index := 0; index < primaryCircuitThreshold; index++ {
		circuit.failure(now)
	}
	if circuit.allow(now.Add(time.Second)) {
		t.Fatal("circuit remained open to requests after repeated failures")
	}
	if !circuit.allow(now.Add(primaryCircuitOpenFor + time.Second)) {
		t.Fatal("circuit did not allow a probe after the open interval")
	}
	circuit.success()
	if !circuit.allow(now.Add(time.Second)) {
		t.Fatal("successful probe did not reset circuit")
	}
}

func TestHasVanitySourceOnlyCountsEnabledRules(t *testing.T) {
	rules := []identity.VanityRule{
		{Source: identity.VanityCustomStatus, Enabled: false},
		{Source: identity.VanityUsername, Enabled: true},
	}
	if hasVanitySource(rules, identity.VanityCustomStatus) {
		t.Fatal("disabled Custom Status rule was treated as active")
	}
	if !hasVanitySource(rules, identity.VanityUsername) {
		t.Fatal("enabled username rule was not detected")
	}
}

func TestManualSyncClaimPreventsDuplicateSourceRun(t *testing.T) {
	bot := &Bot{}
	if !bot.claimManualSync("guild", identity.SourceVanity) {
		t.Fatal("first sync claim was rejected")
	}
	if bot.claimManualSync("guild", identity.SourceVanity) {
		t.Fatal("duplicate Vanity sync claim was accepted")
	}
	if !bot.claimManualSync("guild", identity.SourceGuildTag) {
		t.Fatal("Guild Tag sync should use a separate source key")
	}
	bot.releaseManualSync("guild", identity.SourceVanity)
	if !bot.claimManualSync("guild", identity.SourceVanity) {
		t.Fatal("released Vanity sync could not be claimed again")
	}
}
