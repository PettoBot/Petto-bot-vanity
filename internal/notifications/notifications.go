package notifications

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/database"
	"github.com/PettoBot/vanity-tag-bot/internal/embeds"
	"github.com/PettoBot/vanity-tag-bot/internal/identity"
	"github.com/bwmarrin/discordgo"
)

// Sender is the small Discord surface needed for user notifications.
type Sender interface {
	ChannelMessageSendComplex(string, *discordgo.MessageSend, ...discordgo.RequestOption) (*discordgo.Message, error)
}

type Service struct {
	Store  *database.Store
	Sender Sender
}

func DefaultEmbedName(source identity.Source) string {
	if source == identity.SourceGuildTag {
		return "guildtag_notify"
	}
	return "vanity_notify"
}

// EmitAction sends successful thank-you notifications for matching add-role
// rules. The identity engine may call it even when the role was already
// present; action audit logs remain in the separate logs service and include
// both additions and removals.
func (s *Service) EmitAction(ctx context.Context, action identity.ActionEvent) error {
	if s == nil || s.Store == nil || s.Sender == nil || action.Action != identity.ActionAddRole || action.Error != nil || action.Result != "completed" {
		return nil
	}
	if action.Source != identity.SourceVanity && action.Source != identity.SourceGuildTag {
		return nil
	}
	config, err := s.Store.GetNotificationConfig(ctx, action.GuildID, action.Source)
	if err != nil || config.ChannelID == "" {
		return err
	}

	template := DefaultTemplate(action.Source)
	if config.EmbedID != "" {
		if item, loadErr := s.Store.GetEmbedTemplateByID(ctx, action.GuildID, config.EmbedID); loadErr == nil {
			var custom embeds.Template
			if json.Unmarshal(item.Payload, &custom) == nil {
				template = custom
			}
		}
	}
	payload, err := embeds.Build(template, variables(action))
	if err != nil {
		payload, err = embeds.Build(DefaultTemplate(action.Source), variables(action))
		if err != nil {
			return err
		}
	}
	payload.AllowedMentions = allowedMentions(config.Ping, action.UserID)
	_, err = s.Sender.ChannelMessageSendComplex(config.ChannelID, &discordgo.MessageSend{
		Content: payload.Content, Embeds: payload.Embeds, Components: payload.Components,
		AllowedMentions: payload.AllowedMentions,
	})
	return err
}

func DefaultTemplate(source identity.Source) embeds.Template {
	if source == identity.SourceGuildTag {
		return embeds.Template{
			Description: "gracias por usar el tag {tag}, {user.mention} ♡",
			Color:       0xF0A9C4,
		}
	}
	return embeds.Template{
		Description: "gracias por usar el vanity {vanity.word}, {user.mention} ♡",
		Color:       0xF0A9C4,
	}
}

func variables(action identity.ActionEvent) embeds.Variables {
	ruleName := action.RuleName
	if ruleName == "" {
		ruleName = action.RuleID
	}
	value := action.MatchedValue
	if value == "" {
		value = action.Value
	}
	result := action.Result
	if result == "" {
		result = "completed"
	}
	vars := embeds.Variables{
		UserID: action.UserID, UserMention: mention(action.UserID), RoleID: action.RoleID,
		RuleName: ruleName, RuleSource: string(action.Source), RuleValue: action.Value,
		RuleCondition: action.RuleCondition, RuleReason: action.Reason,
		Action: string(action.Action), ActionText: "add", Result: result,
		EventName: string(action.Source) + "_notify", EventTitle: title(action.Source),
		MatchedValue: value, Timestamp: time.Now().UTC(),
	}
	if action.Source == identity.SourceVanity {
		vars.VanityRule, vars.VanityWord, vars.VanitySource, vars.VanityValue, vars.VanityRoleID = ruleName, action.Value, action.MatchField, value, action.RoleID
	} else {
		vars.TagRule, vars.TagCondition, vars.Tag, vars.TagRuleValue, vars.TagRoleID = ruleName, action.RuleCondition, nonEmpty(action.Tag, value), action.Value, action.RoleID
		vars.TagGuildID, vars.TagEnabled, vars.TagBadge = action.TagGuildID, action.TagEnabled, action.TagBadge
	}
	return vars
}

func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func allowedMentions(ping, userID string) *discordgo.MessageAllowedMentions {
	if strings.EqualFold(strings.TrimSpace(ping), "none") || userID == "" {
		return &discordgo.MessageAllowedMentions{}
	}
	return &discordgo.MessageAllowedMentions{Users: []string{userID}}
}

func mention(id string) string {
	if id == "" {
		return ""
	}
	return "<@" + id + ">"
}

func title(source identity.Source) string {
	if source == identity.SourceGuildTag {
		return "Guild Tag notification"
	}
	return "Vanity notification"
}
