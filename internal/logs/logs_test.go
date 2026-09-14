package logs

import (
	"context"
	"strings"
	"testing"

	"github.com/PettoBot/vanity-tag-bot/internal/embeds"
	"github.com/PettoBot/vanity-tag-bot/internal/identity"
)

type fakeSender struct{ sent int }

func (f *fakeSender) ChannelMessageSendComplex(string, interface{}, ...interface{}) (interface{}, error) {
	return nil, nil
}

func TestActionDedupeKeyIsStable(t *testing.T) {
	logger := &Logger{}
	ctx := context.Background()
	// EmitAction has a nil sink guard after dedupe bookkeeping; use it to assert
	// the same transition cannot create a second delivery attempt.
	if err := logger.EmitAction(ctx, identity.ActionEvent{GuildID: "g", UserID: "u", RoleID: "r", Action: identity.ActionAddRole}); err != nil {
		t.Fatal(err)
	}
	if err := logger.EmitAction(ctx, identity.ActionEvent{GuildID: "g", UserID: "u", RoleID: "r", Action: identity.ActionAddRole}); err != nil {
		t.Fatal(err)
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if len(logger.dedupe) != 1 {
		t.Fatalf("got %d dedupe keys", len(logger.dedupe))
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
