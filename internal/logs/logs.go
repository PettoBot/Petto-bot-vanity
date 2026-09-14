package logs

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/database"
	"github.com/PettoBot/vanity-tag-bot/internal/embeds"
	"github.com/PettoBot/vanity-tag-bot/internal/identity"
	"github.com/bwmarrin/discordgo"
)

const (
	EventVanityAdd      = "vanity_add"
	EventVanityRemove   = "vanity_remove"
	EventTagAdd         = "tag_add"
	EventTagRemove      = "tag_remove"
	EventError          = "error"
	DefaultApproveEmoji = "<:petto_approve:1527894552277549066>"
	DefaultDenyEmoji    = "<:petto_deny:1527894509579665458>"
)

var eventKeys = []string{EventVanityAdd, EventVanityRemove, EventTagAdd, EventTagRemove, EventError}

type Event struct {
	GuildID       string
	UserID        string
	RoleID        string
	RuleID        string
	RuleName      string
	RuleCondition string
	MatchField    string
	MatchedValue  string
	Tag           string
	TagGuildID    string
	TagEnabled    string
	TagBadge      string
	Source        identity.Source
	Action        identity.Action
	Value         string
	Reason        string
	Result        string
	Error         error
	Timestamp     time.Time
}

type Sender interface {
	ChannelMessageSendComplex(string, *discordgo.MessageSend, ...discordgo.RequestOption) (*discordgo.Message, error)
}

type Logger struct {
	Store        *database.Store
	Sender       Sender
	ApproveEmoji string
	DenyEmoji    string
	mu           sync.Mutex
	dedupe       map[string]struct{}
}

func EventKeys() []string {
	return append([]string(nil), eventKeys...)
}

func DefaultTemplateName(eventKey string) string {
	return "notify_" + eventKey
}

func (l *Logger) EmitAction(ctx context.Context, action identity.ActionEvent) error {
	key := fmt.Sprintf("%s:%s:%s:%s:%s:%s", action.GuildID, action.UserID, action.RoleID, action.Action, action.Result, errorText(action.Error))
	l.mu.Lock()
	if l.dedupe == nil {
		l.dedupe = make(map[string]struct{})
	}
	if _, exists := l.dedupe[key]; exists {
		l.mu.Unlock()
		return nil
	}
	l.dedupe[key] = struct{}{}
	l.mu.Unlock()
	return l.Emit(ctx, Event{
		GuildID: action.GuildID, UserID: action.UserID, RoleID: action.RoleID, RuleID: action.RuleID,
		RuleName: action.RuleName, RuleCondition: action.RuleCondition, MatchField: action.MatchField, MatchedValue: action.MatchedValue,
		Tag: action.Tag, TagGuildID: action.TagGuildID, TagEnabled: action.TagEnabled, TagBadge: action.TagBadge,
		Source: action.Source, Action: action.Action, Value: action.Value, Reason: action.Reason,
		Result: action.Result, Error: action.Error,
	})
}

func (l *Logger) Emit(ctx context.Context, event Event) error {
	if l == nil || l.Store == nil || l.Sender == nil {
		return nil
	}
	config, err := l.Store.GetLogConfig(ctx, event.GuildID)
	if err != nil {
		return err
	}
	key := EventKey(event)
	if config.ChannelID == "" || !eventEnabled(config.Events, key) {
		return nil
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	// Audit logs intentionally use the built-in Petto-style action card. User
	// notifications have their own /vanity notify and /guildtag notify config.
	return l.emitLegacy(config.ChannelID, event)
}

func (l *Logger) emitLegacy(channelID string, event Event) error {
	color := 0xA5EA7A
	icon := emojiText(l.ApproveEmoji, DefaultApproveEmoji)
	if event.Action == identity.ActionRemoveRole || event.Error != nil {
		color = 0xFE6465
		icon = emojiText(l.DenyEmoji, DefaultDenyEmoji)
	}
	preposition := "to"
	if event.Action == identity.ActionRemoveRole {
		preposition = "from"
	}
	description := fmt.Sprintf("### %s\n%s %s role <@&%s> %s <@%s>", titleFor(event.Source), icon, actionWord(event.Action), event.RoleID, preposition, event.UserID)
	if event.Error != nil {
		description = fmt.Sprintf("### %s\n%s Could not %s role <@&%s> %s <@%s>\nError: %s", titleFor(event.Source), icon, strings.ToLower(actionWord(event.Action)), event.RoleID, preposition, event.UserID, event.Error.Error())
	} else if event.Source == identity.SourceGuildTag {
		description += fmt.Sprintf("\nReason: Matched `%s` condition for value `%s`", nonEmpty(event.RuleCondition, "tag"), nonEmpty(event.Value, event.MatchedValue))
	} else {
		description += fmt.Sprintf("\nWord: `%s`", nonEmpty(event.Value, event.MatchedValue))
	}
	embed := &discordgo.MessageEmbed{
		Description: description, Color: color,
	}
	_, err := l.Sender.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed}, AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	return err
}

func emojiText(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || (strings.HasPrefix(value, ":") && strings.HasSuffix(value, ":")) {
		return fallback
	}
	return value
}

func eventVariables(event Event) embeds.Variables {
	key := EventKey(event)
	ruleName := nonEmpty(event.RuleName, event.RuleID)
	actionText := "add"
	if event.Action == identity.ActionRemoveRole {
		actionText = "remove"
	}
	vars := embeds.Variables{
		UserID: event.UserID, UserMention: mentionUser(event.UserID), RoleID: event.RoleID,
		RuleName: ruleName, RuleSource: string(event.Source), RuleValue: event.Value,
		RuleCondition: event.RuleCondition, RuleReason: event.Reason,
		Action: string(event.Action), ActionText: actionText, Result: nonEmpty(event.Result, "completed"),
		EventName: key, EventTitle: titleFor(event.Source), EventError: errorText(event.Error), MatchedValue: event.MatchedValue,
		Timestamp: event.Timestamp,
	}
	if event.Source == identity.SourceVanity {
		vars.VanityRule, vars.VanityWord, vars.VanitySource, vars.VanityValue, vars.VanityRoleID = ruleName, event.Value, event.MatchField, event.MatchedValue, event.RoleID
	} else if event.Source == identity.SourceGuildTag {
		vars.TagRule, vars.TagCondition, vars.Tag, vars.TagRuleValue, vars.TagRoleID = ruleName, event.RuleCondition, nonEmpty(event.Tag, event.MatchedValue), event.Value, event.RoleID
		vars.TagGuildID, vars.TagEnabled, vars.TagBadge = event.TagGuildID, event.TagEnabled, event.TagBadge
	}
	return vars
}

func DefaultTemplate(eventKey string) embeds.Template {
	remove := eventKey == EventVanityRemove || eventKey == EventTagRemove
	color := 0xB7F27A
	icon, verb, preposition := DefaultApproveEmoji, "Added", "to"
	if remove {
		color, icon, verb, preposition = 0xFE6465, DefaultDenyEmoji, "Removed", "from"
	}
	if eventKey == EventVanityAdd || eventKey == EventVanityRemove {
		return embeds.Template{
			Title: "Vanity Action", Description: fmt.Sprintf("%s %s role {role} %s {user}", icon, verb, preposition), Color: color,
			Fields: []embeds.TemplateField{{Name: "Word", Value: "{vanity.word}", Inline: true}},
		}
	}
	if eventKey == EventTagAdd || eventKey == EventTagRemove {
		return embeds.Template{
			Title: "Server Tag Action", Description: fmt.Sprintf("%s %s role {role} %s {user}", icon, verb, preposition), Color: color,
			Fields: []embeds.TemplateField{{Name: "Reason", Value: "{rule.reason}", Inline: false}},
		}
	}
	return embeds.Template{
		Title: "{event.title}", Description: fmt.Sprintf("%s Could not {action.text} role {role} for {user}", DefaultDenyEmoji), Color: 0xFE6465,
		Fields: []embeds.TemplateField{{Name: "Rule", Value: "{rule.name}", Inline: true}, {Name: "Error", Value: "{event.error}", Inline: false}},
	}
}

func SampleEvent(eventKey, guildID, userID, roleID string) Event {
	event := Event{GuildID: guildID, UserID: userID, RoleID: roleID, RuleName: "notification-test", Result: "completed", Value: "cinnamochi", MatchedValue: "cinnamochi"}
	switch eventKey {
	case EventVanityRemove:
		event.Source, event.Action, event.MatchField, event.RuleCondition, event.Reason = identity.SourceVanity, identity.ActionRemoveRole, "username", "equals", "Matched username condition for value cinnamochi"
	case EventTagAdd:
		event.Source, event.Action, event.RuleCondition, event.Value, event.MatchedValue = identity.SourceGuildTag, identity.ActionAddRole, "is_guild_id", "707307527846625280", "707307527846625280"
		event.Tag, event.TagGuildID, event.TagEnabled = "CINN", "707307527846625280", "true"
		event.Reason = "Matched is_guild_id condition for value 707307527846625280"
	case EventTagRemove:
		event.Source, event.Action, event.RuleCondition, event.Value, event.MatchedValue = identity.SourceGuildTag, identity.ActionRemoveRole, "is_not_guild_id", "707307527846625280", "707307527846625280"
		event.Tag, event.TagGuildID, event.TagEnabled = "CINN", "707307527846625280", "true"
		event.Reason = "Matched is_not_guild_id condition for value 707307527846625280"
	case EventError:
		event.Source, event.Action, event.Error, event.Result = identity.SourceGuildTag, identity.ActionAddRole, fmt.Errorf("test notification error"), "error"
	default:
		event.Source, event.Action, event.MatchField, event.RuleCondition, event.Reason = identity.SourceVanity, identity.ActionAddRole, "username", "equals", "Matched username condition for value cinnamochi"
	}
	return event
}

func EventKey(event Event) string {
	if event.Error != nil {
		return EventError
	}
	if event.Source == identity.SourceGuildTag {
		if event.Action == identity.ActionRemoveRole {
			return EventTagRemove
		}
		return EventTagAdd
	}
	if event.Action == identity.ActionRemoveRole {
		return EventVanityRemove
	}
	return EventVanityAdd
}

func eventEnabled(events map[string]bool, key string) bool {
	if len(events) == 0 || events[key] || events["all"] {
		return true
	}
	if key == EventError {
		return events["identity_error"]
	}
	return events["identity_role_add"] && (key == EventVanityAdd || key == EventTagAdd) ||
		events["identity_role_remove"] && (key == EventVanityRemove || key == EventTagRemove)
}

func titleFor(source identity.Source) string {
	if source == identity.SourceGuildTag {
		return "Server Tag Action"
	}
	return "Vanity Action"
}

func actionWord(action identity.Action) string {
	if action == identity.ActionRemoveRole {
		return "Removed"
	}
	return "Added"
}

func mentionUser(id string) string {
	if id == "" {
		return ""
	}
	return "<@" + id + ">"
}

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
