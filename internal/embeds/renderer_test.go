package embeds

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderEscapesMentionsAndVariables(t *testing.T) {
	embed, err := Render(Template{Title: "Hello {user}", Description: "{role} @everyone", Fields: []TemplateField{{Name: "Rule", Value: "{rule.value}"}}}, Variables{UserName: "A", RoleID: "7", RuleValue: "cinn"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(embed.Description, "@everyone") {
		t.Fatal("everyone mention was not escaped")
	}
	if !strings.Contains(embed.Description, "<@&7>") {
		t.Fatal("role variable was not rendered")
	}
}

func TestEmbedLimits(t *testing.T) {
	tooLong := strings.Repeat("x", 6001)
	if _, err := Render(Template{Description: tooLong}, Variables{}); err == nil {
		t.Fatal("oversized embed was accepted")
	}
}

func TestPettoCompatibleNestedAuthorFooterAndPayload(t *testing.T) {
	var template Template
	if err := json.Unmarshal([]byte(`{
		"content":"Welcome {user.mention}",
		"title":"Hello {user.name}",
		"author":{"name":"Petto","icon":"https://example.com/author.png","url":"https://example.com"},
		"footer":{"text":"{guild.name}","icon_url":"https://example.com/footer.png"},
		"timestamp":true,
		"fields":[{"name":"Rule","value":"{rule.value}","inline":true}],
		"buttons":[[{"label":"Docs","url":"https://example.com/docs"}]]
	}`), &template); err != nil {
		t.Fatal(err)
	}
	if template.Author != "Petto" || template.AuthorIcon == "" || template.Footer != "{guild.name}" || !template.Timestamp {
		t.Fatalf("nested Petto shape was not decoded: %#v", template)
	}
	payload, err := Build(template, Variables{UserID: "7", UserName: "Julia", UserMention: "<@7>", GuildName: "Test", RuleValue: "cinn"})
	if err != nil {
		t.Fatal(err)
	}
	if payload.Content == "" || len(payload.Embeds) != 1 || len(payload.Components) != 1 {
		t.Fatalf("expected content, embed, and button row: %#v", payload)
	}
	if !strings.Contains(payload.Content, "<@7>") {
		t.Fatalf("user mention variable was not resolved: %q", payload.Content)
	}
}

func TestBuildRejectsInvalidRemoteURL(t *testing.T) {
	_, err := Build(Template{Image: "javascript:alert(1)"}, Variables{})
	if err == nil {
		t.Fatal("accepted a non-http image URL")
	}
}

func TestBuildContentOnlyDoesNotSendEmptyEmbed(t *testing.T) {
	payload, err := Build(Template{Content: "hello"}, Variables{})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Embeds) != 0 || payload.Content != "hello" {
		t.Fatalf("content-only template produced an empty embed: %#v", payload)
	}
}

func TestIdentityVariablesResolveForVanityAndGuildTag(t *testing.T) {
	payload, err := Build(Template{Description: "{vanity.word}|{vanity.value}|{tag}|{tag.guild_id}|{tag.enabled}|{tag.badge}"}, Variables{
		VanityWord: "cinnamochi", VanityValue: "Cinnamochi", Tag: "CINN", TagGuildID: "707", TagEnabled: "true", TagBadge: "badge",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "cinnamochi|Cinnamochi|CINN|707|true|badge"
	if payload.Embeds[0].Description != want {
		t.Fatalf("got %q, want %q", payload.Embeds[0].Description, want)
	}
}

func TestNotificationVariablesResolve(t *testing.T) {
	payload, err := Build(Template{Description: "{event.title}|{action.text}|{rule.name}|{rule.reason}|{event.error}"}, Variables{
		EventTitle: "Server Tag Action", ActionText: "add", RuleName: "primary", RuleReason: "Matched is_guild_id condition for value 707", EventError: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := payload.Embeds[0].Description, "Server Tag Action|add|primary|Matched is_guild_id condition for value 707|none"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
