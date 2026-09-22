package logs

import (
	"strings"
	"testing"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/embeds"
	"github.com/PettoBot/vanity-tag-bot/internal/identity"
)

type fakeSender struct{ sent int }

func (f *fakeSender) ChannelMessageSendComplex(string, interface{}, ...interface{}) (interface{}, error) {
	return nil, nil
}

func TestActionDedupeOnlySuppressesImmediateDuplicate(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	logger := &Logger{now: func() time.Time { return now }}
	action := identity.ActionEvent{
		GuildID: "g", UserID: "u", RoleID: "r", RuleID: "rule",
		Source: identity.SourceVanity, Action: identity.ActionAddRole, Result: "completed",
	}
	if !logger.shouldEmitAction(action) {
		t.Fatal("first transition should be delivered")
	}
	if logger.shouldEmitAction(action) {
		t.Fatal("immediate duplicate should be suppressed")
	}
	now = now.Add(actionDedupeWindow + time.Millisecond)
	if !logger.shouldEmitAction(action) {
		t.Fatal("same legitimate transition must be allowed after the short dedupe window")
	}
}

func TestActionDedupeSeparatesSourceAndRule(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	logger := &Logger{now: func() time.Time { return now }}
	base := identity.ActionEvent{
		GuildID: "g", UserID: "u", RoleID: "r", RuleID: "vanity-rule",
		Source: identity.SourceVanity, Action: identity.ActionAddRole, Result: "completed",
	}
	if !logger.shouldEmitAction(base) {
		t.Fatal("first vanity action should be delivered")
	}
	tag := base
	tag.Source = identity.SourceGuildTag
	tag.RuleID = "tag-rule"
	if !logger.shouldEmitAction(tag) {
		t.Fatal("guild tag action must not be deduped against vanity")
	}
	otherRule := base
	otherRule.RuleID = "other-vanity-rule"
	if !logger.shouldEmitAction(otherRule) {
		t.Fatal("different vanity rule must not share the same dedupe key")
	}
}

func TestDefaultNotificationTemplatesRenderEveryEvent(t *testing.T) {
	for _, eventKey := range EventKeys() {
		event := SampleEvent(eventKey, "guild", "user", "role")
		payload, err := embeds.Build(DefaultTemplate(eventKey), eventVariables(event))
		if err != nil {
			t.Fatalf("%s default template failed: %v", eventKey, err)
		}
		if len(payload.Embeds) != 1 || strings.Contains(payload.Embeds[0].Description, "{") {
			t.Fatalf("%s did not resolve its notification template: %#v", eventKey, payload)
		}
	}
}

func TestEventKeySeparatesVanityAndTag(t *testing.T) {
	if got := EventKey(Event{Source: identity.SourceVanity, Action: identity.ActionAddRole}); got != EventVanityAdd {
		t.Fatalf("got %q for vanity add", got)
	}
	if got := EventKey(Event{Source: identity.SourceGuildTag, Action: identity.ActionRemoveRole}); got != EventTagRemove {
		t.Fatalf("got %q for tag remove", got)
	}
}
