package notifications

import (
	"strings"
	"testing"

	"github.com/PettoBot/vanity-tag-bot/internal/embeds"
	"github.com/PettoBot/vanity-tag-bot/internal/identity"
)

func TestDefaultEmbedNames(t *testing.T) {
	if got := DefaultEmbedName(identity.SourceVanity); got != "vanity_notify" {
		t.Fatalf("vanity default embed = %q", got)
	}
	if got := DefaultEmbedName(identity.SourceGuildTag); got != "guildtag_notify" {
		t.Fatalf("guild tag default embed = %q", got)
	}
}

func TestDefaultNotificationTemplatesResolveVariables(t *testing.T) {
	for _, source := range []identity.Source{identity.SourceVanity, identity.SourceGuildTag} {
		vars := embeds.Variables{
			UserMention: "<@user>", VanityWord: "cinnamochi", Tag: "petto",
		}
		payload, err := embeds.Build(DefaultTemplate(source), vars)
		if err != nil {
			t.Fatalf("%s default notification failed: %v", source, err)
		}
		if len(payload.Embeds) != 1 || strings.Contains(payload.Embeds[0].Description, "{") {
			t.Fatalf("%s default notification has unresolved variables: %#v", source, payload)
		}
	}
}

func TestGuildTagNotificationUsesVisibleTagInsteadOfRuleID(t *testing.T) {
	vars := variables(identity.ActionEvent{
		UserID: "user", RoleID: "role", RuleName: "server-tag", RuleCondition: "is_guild_id",
		Value: "707307527846625280", MatchedValue: "707307527846625280", Tag: "CINN",
		TagGuildID: "707307527846625280", Source: identity.SourceGuildTag, Action: identity.ActionAddRole, Result: "completed",
	})
	if vars.Tag != "CINN" {
		t.Fatalf("notification tag = %q, want visible tag CINN", vars.Tag)
	}
	if vars.TagRuleValue != "707307527846625280" {
		t.Fatalf("notification rule value = %q, want guild ID", vars.TagRuleValue)
	}
}
