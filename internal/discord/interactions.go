package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/database"
	"github.com/PettoBot/vanity-tag-bot/internal/embeds"
	"github.com/PettoBot/vanity-tag-bot/internal/identity"
	"github.com/PettoBot/vanity-tag-bot/internal/logs"
	"github.com/PettoBot/vanity-tag-bot/internal/notifications"
	"github.com/PettoBot/vanity-tag-bot/internal/profile"
	"github.com/bwmarrin/discordgo"
	"github.com/jackc/pgx/v5"
)

var sessionRegistry sync.Map

func (b *Bot) handleInteraction(event *discordgo.InteractionCreate) {
	defer func() {
		if recovered := recover(); recovered != nil {
			b.logger.Error("interaction handler panic", "panic", recovered, "stack", string(debug.Stack()))
			if event != nil && event.Interaction != nil {
				respond(event, "An internal error occurred while processing that interaction.", true)
			}
		}
	}()
	if event == nil || event.Interaction == nil {
		return
	}
	switch event.Type {
	case discordgo.InteractionApplicationCommand:
		b.handleCommand(event)
	case discordgo.InteractionMessageComponent:
		b.handleComponent(event)
	case discordgo.InteractionModalSubmit:
		b.handleModal(event)
	}
}

func (b *Bot) handleCommand(event *discordgo.InteractionCreate) {
	data := event.ApplicationCommandData()
	if data.Name != "cmds" && data.Name != "set" && !b.hasGuildAdmin(event) {
		respond(event, "You need Manage Server and Manage Roles to use this command.", true)
		return
	}
	if data.Name == "set" && !b.hasManageGuild(event) {
		respond(event, "You need Manage Server to customize the bot profile.", true)
		return
	}
	switch data.Name {
	case "cmds":
		b.handleCmds(event, data)
	case "setup":
		b.handleSetup(event)
	case "config":
		b.handleConfig(event, data)
	case "set":
		b.handleSet(event, data)
	case "vanity":
		b.handleVanity(event, data)
	case "guildtag":
		b.handleGuildTag(event, data)
	case "identity":
		b.handleIdentity(event, data)
	case "logs":
		b.handleLogs(event, data)
	case "embed":
		b.handleEmbed(event, data)
	default:
		respond(event, "Unknown command.", true)
	}
}

func (b *Bot) handleSet(event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	path, options := commandPath(data.Options)
	action := strings.Join(path, " ")
	ctx, cancel := b.operationContext()
	defer cancel()
	guildID := event.GuildID
	if err := b.store.EnsureGuild(ctx, guildID); err != nil {
		respond(event, "Could not initialize this server: "+err.Error(), true)
		return
	}
	current, err := b.store.GetBotProfile(ctx, guildID)
	if err != nil {
		respond(event, err.Error(), true)
		return
	}

	switch action {
	case "view":
		respond(event, fmt.Sprintf("**Bot profile for this server**\nNickname: `%s`\nAvatar: `%s`\nBanner: `%s`\nBio: `%s`\nStatus: `%s`", pointerValue(current.Nickname), pointerValue(current.AvatarRef), pointerValue(current.BannerRef), pointerValue(current.Bio), nonEmpty(current.SyncStatus, "not_configured")), true)
	case "nickname":
		value := optionString(options, "value")
		if optionBool(options, "clear") {
			value = ""
			current.Nickname = nil
		} else {
			if strings.TrimSpace(value) == "" {
				respond(event, "Provide a nickname or select `clear`.", true)
				return
			}
			current.Nickname = &value
		}
		if err := b.updateProfile(ctx, guildID, profile.MemberUpdate{Nickname: &value}, current); err != nil {
			respond(event, profileUpdateError("Discord rejected the nickname; the previous profile was preserved.", err), true)
			return
		}
		respond(event, "Nickname synchronized for this server.", true)
	case "avatar", "banner":
		kind := action
		if optionBool(options, "clear") {
			value := ""
			if kind == "avatar" {
				current.AvatarRef = nil
			} else {
				current.BannerRef = nil
			}
			if err := b.updateProfile(ctx, guildID, profileAssetUpdate(kind, value), current); err != nil {
				respond(event, profileUpdateError("Discord rejected the reset; the previous profile was preserved.", err), true)
				return
			}
			respond(event, kind+" reset to the global bot profile.", true)
			return
		}
		urlValue := optionString(options, "url")
		if attachment := attachmentOption(data, options, "file"); attachment != nil {
			if urlValue != "" {
				respond(event, "Choose either a URL or an uploaded file, not both.", true)
				return
			}
			urlValue = attachment.URL
		}
		if strings.TrimSpace(urlValue) == "" {
			respond(event, "Attach an image or provide an HTTPS URL.", true)
			return
		}
		if err := b.updateProfileAsset(ctx, guildID, kind, urlValue, current); err != nil {
			respond(event, profileUpdateError("Asset rejected or Discord update failed; the previous profile was preserved.", err), true)
			return
		}
		respond(event, kind+" synchronized for this server.", true)
	case "bio":
		value := optionString(options, "value")
		if optionBool(options, "clear") {
			value = ""
			current.Bio = nil
		} else {
			if strings.TrimSpace(value) == "" {
				respond(event, "Provide bio text or select `clear`.", true)
				return
			}
			current.Bio = &value
		}
		if err := b.updateProfile(ctx, guildID, profile.MemberUpdate{Bio: &value}, current); err != nil {
			respond(event, profileUpdateError("Discord rejected the bio; the previous profile was preserved.", err), true)
			return
		}
		respond(event, "Bio synchronized for this server.", true)
	case "reset":
		current.Nickname, current.AvatarRef, current.BannerRef, current.Bio = nil, nil, nil, nil
		empty := ""
		if err := b.updateProfile(ctx, guildID, profile.MemberUpdate{Nickname: &empty, Avatar: &empty, Banner: &empty, Bio: &empty}, current); err != nil {
			respond(event, profileUpdateError("Discord rejected the reset; the previous profile was preserved.", err), true)
			return
		}
		respond(event, "Profile reset to the global bot profile for this server.", true)
	default:
		respond(event, "Choose `/set view`, `/set nickname`, `/set avatar`, `/set banner`, `/set bio`, or `/set reset`.", true)
	}
}

func (b *Bot) handleCmds(event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	query := ""
	if option := data.GetOption("command"); option != nil {
		query = option.StringValue()
	}
	response, err := b.helpPanel(actorID(event), query, 0)
	if err != nil {
		respond(event, err.Error(), true)
		return
	}
	_ = eventInteractionRespond(b.session, event, response)
}

func (b *Bot) handleSetup(event *discordgo.InteractionCreate) {
	content := "**Vanity Tag Bot · Guided setup**\n\nFollow these steps once. Every completed form is saved immediately.\n\n**1 · Action logs**\nThe private staff log for role additions, removals, reasons, and errors. Run `/logs setup` in the channel you want to use.\n\n**2 · Matching rules**\nChoose **Custom Status** to match the text shown on a member's Discord profile, or choose **Server Tag** to match Discord's identity guild data.\n\n**3 · Bot profile**\nSet this server's nickname and bio. Use `/set avatar` and `/set banner` for a Discord upload or HTTPS image.\n\n**4 · Thank-you messages**\nChoose where Vanity and Server Tag members receive their notification, which embed to use, and whether to mention them.\n\n**Before testing:** give the bot Manage Roles, move its role above the roles it must manage, and enable Server Members and Presence intents in the Developer Portal. Use `/config view` to see what is configured." + editorLinks(b.config.WebsiteURL, b.config.DocsURL)
	data := &discordgo.InteractionResponseData{Content: content, Components: []discordgo.MessageComponent{
		&discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.Button{Style: discordgo.PrimaryButton, Label: "1 · Choose log channel", CustomID: "setup:logs"}, &discordgo.Button{Style: discordgo.PrimaryButton, Label: "2 · Custom Status rule", CustomID: "setup:vanity"}, &discordgo.Button{Style: discordgo.PrimaryButton, Label: "2 · Server Tag rule", CustomID: "setup:guildtag"}}},
		&discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.Button{Style: discordgo.SecondaryButton, Label: "3 · Bot profile", CustomID: "setup:profile"}, &discordgo.Button{Style: discordgo.SecondaryButton, Label: "4 · Thank-you messages", CustomID: "setup:embeds"}, &discordgo.Button{Style: discordgo.SuccessButton, Label: "Close setup", CustomID: "setup:cancel"}}},
	}}
	_ = eventInteractionRespond(b.session, event, data)
}

func (b *Bot) handleConfig(event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	path, _ := commandPath(data.Options)
	ctx, cancel := b.operationContext()
	defer cancel()
	guildID := event.GuildID
	switch strings.Join(path, " ") {
	case "view":
		botProfile, err := b.store.GetBotProfile(ctx, guildID)
		if err != nil {
			respond(event, err.Error(), true)
			return
		}
		config, err := b.store.GetLogConfig(ctx, guildID)
		if err != nil {
			respond(event, err.Error(), true)
			return
		}
		vanityNotify, vanityErr := b.store.GetNotificationConfig(ctx, guildID, identity.SourceVanity)
		if vanityErr != nil {
			respond(event, vanityErr.Error(), true)
			return
		}
		tagNotify, tagErr := b.store.GetNotificationConfig(ctx, guildID, identity.SourceGuildTag)
		if tagErr != nil {
			respond(event, tagErr.Error(), true)
			return
		}
		logStatus := nonEmpty(config.ChannelID, "Not configured · run `/logs setup`")
		vanityStatus := nonEmpty(vanityNotify.ChannelID, "Not configured · run `/vanity notify`")
		tagStatus := nonEmpty(tagNotify.ChannelID, "Not configured · run `/guildtag notify`")
		respond(event, fmt.Sprintf("**Configuration overview**\n\n**1 · Staff action logs**\n%s\n\n**2 · Vanity thank-you message**\n%s\n\n**3 · Server Tag thank-you message**\n%s\n\n**4 · Bot profile**\nStatus: `%s`\nLast error: `%s`\n\nRun `/setup` for the guided flow. Use `/config reset` only if you want to remove this server's saved configuration.", logStatus, vanityStatus, tagStatus, nonEmpty(botProfile.SyncStatus, "not configured"), nonEmpty(botProfile.LastError, "none")), true)
	case "reset":
		if err := b.store.ResetGuild(ctx, guildID); err != nil {
			respond(event, err.Error(), true)
			return
		}
		b.queueCurrentMemberEvaluation(event)
		respond(event, "Server configuration reset. Active grants were invalidated and your member was queued for safe role reconciliation; audit history is retained.", true)
	default:
		respond(event, "Choose a valid configuration action.", true)
	}
}

func (b *Bot) handleVanity(event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	path, options := commandPath(data.Options)
	action := strings.Join(path, " ")
	ctx, cancel := b.operationContext()
	defer cancel()
	guildID := event.GuildID
	switch action {
	case "add":
		rule := identity.VanityRule{ID: identity.NewID(), GuildID: guildID, Name: strings.TrimSpace(optionString(options, "name")), Word: strings.TrimSpace(optionString(options, "word")), Source: identity.VanitySource(optionString(options, "source")), Comparison: identity.Comparison(optionString(options, "comparison")), RoleID: optionString(options, "role"), Action: identity.Action(optionString(options, "action")), Enabled: true, CreatedBy: actorID(event), Normalization: identity.Normalization{CaseFold: optionBoolDefault(options, "case_fold", true), TrimSpace: optionBoolDefault(options, "trim_space", true), CollapseSpace: optionBoolDefault(options, "collapse_space", true)}}
		if err := validateVanityRuleInput(rule); err != nil {
			respond(event, err.Error(), true)
			return
		}
		if _, err := identity.MatchVanity(rule, identity.MemberIdentity{Username: rule.Word}); err != nil {
			respond(event, err.Error(), true)
			return
		}
		if err := b.store.EnsureGuild(ctx, guildID); err != nil {
			respond(event, err.Error(), true)
			return
		}
		if err := b.store.CreateVanityRule(ctx, rule); err != nil {
			respond(event, "Could not create Vanity rule: "+err.Error(), true)
			return
		}
		b.queueCurrentMemberEvaluation(event)
		respond(event, fmt.Sprintf("Vanity rule created. It checks **%s** with **%s** `%s` and will **%s** <@&%s>. Your member was queued for an immediate check; use `/vanity sync` for other members.", vanitySourceLabel(rule.Source), comparisonLabel(rule.Comparison), rule.Word, strings.ToLower(roleActionLabel(rule.Action)), rule.RoleID), true)
	case "edit":
		name := optionString(options, "name")
		var word *string
		if optionString(options, "word") != "" {
			value := optionString(options, "word")
			word = &value
		}
		var comparison *identity.Comparison
		if optionString(options, "comparison") != "" {
			value := identity.Comparison(optionString(options, "comparison"))
			comparison = &value
		}
		var source *identity.VanitySource
		if value := optionString(options, "source"); value != "" {
			parsed := identity.VanitySource(value)
			source = &parsed
		}
		var role *string
		if optionString(options, "role") != "" {
			value := optionString(options, "role")
			role = &value
		}
		var enabled *bool
		if option(options, "enabled") != nil {
			value := optionBool(options, "enabled")
			enabled = &value
		}
		if err := b.store.UpdateVanityRule(ctx, guildID, name, word, source, comparison, role, enabled); err != nil {
			respond(event, err.Error(), true)
			return
		}
		b.queueCurrentMemberEvaluation(event)
		respond(event, "Vanity rule updated. Previous grants were invalidated and your member was queued for an immediate check.", true)
	case "remove":
		name := optionString(options, "name")
		if err := b.store.DeleteRuleByName(ctx, identity.SourceVanity, guildID, name); err != nil {
			respond(event, err.Error(), true)
			return
		}
		b.queueCurrentMemberEvaluation(event)
		respond(event, "Vanity rule removed. Previous grants were invalidated and your member was queued for an immediate check.", true)
	case "list":
		rules, err := b.store.ListVanityRules(ctx, guildID)
		if err != nil {
			respond(event, err.Error(), true)
			return
		}
		lines := make([]string, 0, len(rules))
		for _, rule := range rules {
			lines = append(lines, fmt.Sprintf("• `%s` · %s %s `%s` → <@&%s> · %s", rule.Name, vanitySourceLabel(rule.Source), comparisonLabel(rule.Comparison), rule.Word, rule.RoleID, roleActionLabel(rule.Action)))
		}
		respond(event, "**Vanity rules**\n"+nonEmpty(strings.Join(lines, "\n"), "No active rules."), true)
	case "test", "sync":
		b.handleMemberEvaluation(event, identity.SourceVanity, action == "test")
	case "notify":
		b.handleNotificationConfig(event, identity.SourceVanity, options)
	default:
		respond(event, "Choose a valid Vanity action.", true)
	}
}

func (b *Bot) handleGuildTag(event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	path, options := commandPath(data.Options)
	action := strings.Join(path, " ")
	ctx, cancel := b.operationContext()
	defer cancel()
	guildID := event.GuildID
	switch action {
	case "add":
		condition := identity.GuildTagCondition(optionString(options, "condition"))
		value := strings.TrimSpace(optionString(options, "value"))
		rule := identity.GuildTagRule{ID: identity.NewID(), GuildID: guildID, Name: strings.TrimSpace(optionString(options, "name")), Condition: condition, Value: value, RoleID: optionString(options, "role"), Action: identity.Action(optionString(options, "action")), Enabled: true, CreatedBy: actorID(event)}
		if err := validateGuildTagRuleInput(rule); err != nil {
			respond(event, err.Error(), true)
			return
		}
		if err := b.store.EnsureGuild(ctx, guildID); err != nil {
			respond(event, err.Error(), true)
			return
		}
		if err := b.store.CreateGuildTagRule(ctx, rule); err != nil {
			respond(event, "Could not create Server Tag rule: "+err.Error(), true)
			return
		}
		b.queueCurrentMemberEvaluation(event)
		respond(event, fmt.Sprintf("Server Tag rule created. It checks **%s** and will **%s** <@&%s>. Your member was queued for an immediate check. Use `/guildtag sync user:<member>` for another member.", guildTagConditionLabel(rule.Condition), strings.ToLower(roleActionLabel(rule.Action)), rule.RoleID), true)
	case "edit":
		name := optionString(options, "name")
		var condition *identity.GuildTagCondition
		if value := optionString(options, "condition"); value != "" {
			parsed := identity.GuildTagCondition(value)
			condition = &parsed
		}
		var value *string
		if optionString(options, "value") != "" {
			v := optionString(options, "value")
			value = &v
		}
		var role *string
		if optionString(options, "role") != "" {
			v := optionString(options, "role")
			role = &v
		}
		var enabled *bool
		if option(options, "enabled") != nil {
			v := optionBool(options, "enabled")
			enabled = &v
		}
		if err := b.store.UpdateGuildTagRule(ctx, guildID, name, condition, value, role, enabled); err != nil {
			respond(event, err.Error(), true)
			return
		}
		b.queueCurrentMemberEvaluation(event)
		respond(event, "Server Tag rule updated. Previous grants were invalidated and your member was queued for an immediate primary_guild check.", true)
	case "remove":
		name := optionString(options, "name")
		if err := b.store.DeleteRuleByName(ctx, identity.SourceGuildTag, guildID, name); err != nil {
			respond(event, err.Error(), true)
			return
		}
		b.queueCurrentMemberEvaluation(event)
		respond(event, "Server Tag rule removed. Previous grants were invalidated and your member was queued so any role no longer justified can be reconciled safely.", true)
	case "list":
		rules, err := b.store.ListGuildTagRules(ctx, guildID)
		if err != nil {
			respond(event, err.Error(), true)
			return
		}
		lines := make([]string, 0, len(rules))
		for _, rule := range rules {
			value := rule.Value
			if value == "" {
				value = "no value needed"
			}
			lines = append(lines, fmt.Sprintf("• `%s` · %s · `%s` → <@&%s> · %s", rule.Name, guildTagConditionLabel(rule.Condition), value, rule.RoleID, roleActionLabel(rule.Action)))
		}
		respond(event, "**Server Tag rules**\n"+nonEmpty(strings.Join(lines, "\n"), "No active rules."), true)
	case "test", "sync":
		b.handleMemberEvaluation(event, identity.SourceGuildTag, action == "test")
	case "notify":
		b.handleNotificationConfig(event, identity.SourceGuildTag, options)
	default:
		respond(event, "Choose a valid Server Tag action.", true)
	}
}

func (b *Bot) handleNotificationConfig(event *discordgo.InteractionCreate, source identity.Source, options map[string]*discordgo.ApplicationCommandInteractionDataOption) {
	ctx, cancel := b.operationContext()
	defer cancel()
	guildID := event.GuildID
	if err := b.store.EnsureGuild(ctx, guildID); err != nil {
		respond(event, err.Error(), true)
		return
	}
	current, err := b.store.GetNotificationConfig(ctx, guildID, source)
	if err != nil {
		respond(event, "Could not load notification configuration: "+err.Error(), true)
		return
	}
	channel := optionString(options, "channel")
	embedName := strings.TrimSpace(optionString(options, "embed"))
	ping := strings.TrimSpace(optionString(options, "ping"))
	if channel == "" && embedName == "" && ping == "" {
		respond(event, notificationConfigView(source, current), true)
		return
	}
	if channel == "" {
		channel = current.ChannelID
	}
	if channel == "" {
		respond(event, "Choose a channel for this notification.", true)
		return
	}
	if ping == "" {
		ping = current.Ping
		if ping == "" {
			ping = "user"
		}
	}
	embedID, selectedName := current.EmbedID, current.EmbedName
	if embedName != "" || embedID == "" {
		embedID, selectedName, err = b.notificationEmbed(ctx, guildID, source, embedName, actorID(event))
		if err != nil {
			respond(event, err.Error(), true)
			return
		}
	}
	if err := b.store.SetNotificationConfig(ctx, database.NotificationConfig{GuildID: guildID, Source: source, ChannelID: channel, EmbedID: embedID, EmbedName: selectedName, Ping: ping}); err != nil {
		respond(event, "Could not save notification configuration: "+err.Error(), true)
		return
	}
	respond(event, fmt.Sprintf("**%s thank-you message configured**\nChannel: <#%s>\nEmbed: `%s`\nMention: `%s`\n\nThe message is sent when this source starts matching. It also sends when the member already has the role, and it will not repeat for every presence or member event. Edit the embed with `/embed edit name:%s` or preview it with `/embed preview name:%s`.", notificationSourceName(source), channel, selectedName, pingLabel(ping), notifications.DefaultEmbedName(source), notifications.DefaultEmbedName(source)), true)
}

func (b *Bot) notificationEmbed(ctx context.Context, guildID string, source identity.Source, requested, createdBy string) (string, string, error) {
	name := requested
	if name == "" || strings.EqualFold(name, "default") {
		name = notifications.DefaultEmbedName(source)
	}
	item, err := b.store.GetEmbedTemplate(ctx, guildID, name)
	if errors.Is(err, pgx.ErrNoRows) && name == notifications.DefaultEmbedName(source) {
		payload, marshalErr := json.Marshal(notifications.DefaultTemplate(source))
		if marshalErr != nil {
			return "", "", marshalErr
		}
		item = databaseEmbedTemplate(identity.NewID(), guildID, name, payload, createdBy)
		if saveErr := b.store.SaveEmbedTemplate(ctx, item); saveErr != nil {
			return "", "", saveErr
		}
	} else if err != nil {
		return "", "", fmt.Errorf("embed `%s` was not found", name)
	}
	return item.ID, item.Name, nil
}

func notificationConfigView(source identity.Source, config database.NotificationConfig) string {
	if config.ChannelID == "" {
		return fmt.Sprintf("**%s thank-you message**\nNot configured yet. Use `/%s notify channel:#channel` to choose where members should receive it.\n\nThe default embed is created automatically when you save the channel.", notificationSourceName(source), notificationCommandName(source))
	}
	embed := config.EmbedName
	if embed == "" {
		embed = notifications.DefaultEmbedName(source)
	}
	ping := config.Ping
	if ping == "" {
		ping = "user"
	}
	return fmt.Sprintf("**%s thank-you message**\nChannel: <#%s>\nEmbed: `%s`\nMention: `%s`\n\nIt sends when a matching rule starts applying, including when the member already has the role.", notificationSourceName(source), config.ChannelID, embed, pingLabel(ping))
}

func pingLabel(value string) string {
	if strings.EqualFold(value, "none") {
		return "No mention"
	}
	return "Mention member"
}

func notificationSourceName(source identity.Source) string {
	if source == identity.SourceGuildTag {
		return "Guild Tag"
	}
	return "Vanity"
}

func notificationCommandName(source identity.Source) string {
	if source == identity.SourceGuildTag {
		return "guildtag"
	}
	return "vanity"
}

func (b *Bot) queueCurrentMemberEvaluation(event *discordgo.InteractionCreate) {
	if event == nil || event.Member == nil || event.Member.User == nil || event.GuildID == "" {
		return
	}
	go b.evaluateMember(event.GuildID, b.cachedMemberIdentity(event.GuildID, event.Member, nil))
}

func (b *Bot) handleIdentity(event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	path, commandOptions := commandPath(data.Options)
	ctx, cancel := b.operationContext()
	defer cancel()
	userID := event.Member.User.ID
	if option := commandOptions["user"]; option != nil {
		userID = optionString(commandOptions, "user")
	}
	member, err := b.loadMemberIdentity(ctx, event.GuildID, userID)
	if err != nil {
		respond(event, "Could not load member: "+err.Error(), true)
		return
	}
	switch strings.Join(path, " ") {
	case "status":
		primary := "none"
		enabled := "unknown"
		tag := ""
		if member.PrimaryGuild != nil {
			primary = member.PrimaryGuild.IdentityGuildID
			if member.PrimaryGuild.IdentityEnabled != nil {
				enabled = strconv.FormatBool(*member.PrimaryGuild.IdentityEnabled)
			}
			tag = member.PrimaryGuild.Tag
		}
		respond(event, fmt.Sprintf("**Identity status**\nPrimary guild: `%s`\nEnabled: `%s`\nVisible tag: `%s`\nCustom Status: `%s`", primary, enabled, nonEmpty(tag, "none"), nonEmpty(member.CustomStatus, "none")), true)
	case "sync":
		results, err := b.identity.EvaluateMember(ctx, member)
		if err != nil {
			respond(event, err.Error(), true)
			return
		}
		respond(event, fmt.Sprintf("Identity sync complete for <@%s>: %d role decisions.", userID, len(results)), true)
	case "audit":
		respond(event, "Audit events are stored per guild in PostgreSQL and deduplicated by transition key. Use `/logs view` for the delivery channel.", true)
	default:
		respond(event, "Choose a valid identity action.", true)
	}
}

func (b *Bot) handleLogs(event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	path, options := commandPath(data.Options)
	ctx, cancel := b.operationContext()
	defer cancel()
	guildID := event.GuildID
	if err := b.store.EnsureGuild(ctx, guildID); err != nil {
		respond(event, err.Error(), true)
		return
	}
	switch strings.Join(path, " ") {
	case "setup":
		config := databaseLogConfig(guildID, event.ChannelID, strings.Join(logs.EventKeys(), ","))
		if err := b.store.SetLogConfig(ctx, config); err != nil {
			respond(event, err.Error(), true)
			return
		}
		respond(event, "Action-log channel configured. This channel receives role additions, removals, reasons, and errors. User notifications are configured separately with `/vanity notify` and `/guildtag notify`.", true)
	case "set":
		channel := optionString(options, "channel")
		events := map[string]bool{}
		for _, name := range strings.Split(optionString(options, "events"), ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				if !validLogEvent(name) {
					respond(event, "Unknown log event `"+name+"`. Choose from: `"+strings.Join(logs.EventKeys(), "`, `")+"`.", true)
					return
				}
				events[name] = true
			}
		}
		if len(events) == 0 {
			respond(event, "Choose at least one log event, separated by commas.", true)
			return
		}
		if err := b.store.SetLogConfig(ctx, databaseLogConfig(guildID, channelMapValue(channel), strings.Join(mapKeys(events), ","))); err != nil {
			respond(event, err.Error(), true)
			return
		}
		respond(event, "Log configuration saved.", true)
	case "view":
		config, err := b.store.GetLogConfig(ctx, guildID)
		if err != nil {
			respond(event, err.Error(), true)
			return
		}
		respond(event, fmt.Sprintf("Log channel: `%s`\nEvents: `%s`", config.ChannelID, strings.Join(mapKeys(config.Events), ", ")), true)
	case "test":
		eventKey := optionString(options, "event")
		if eventKey == "" {
			eventKey = logs.EventVanityAdd
		}
		if !validLogEvent(eventKey) {
			respond(event, "Unknown notification event. Use vanity_add, vanity_remove, tag_add, tag_remove, or error.", true)
			return
		}
		if err := b.logSink.Emit(ctx, logs.SampleEvent(eventKey, guildID, actorID(event), sampleRoleID(event))); err != nil {
			respond(event, err.Error(), true)
			return
		}
		respond(event, "Action-log test sent for `"+eventKey+"`. Check the configured log channel.", true)
	default:
		respond(event, "Choose a valid logs action.", true)
	}
}

func validLogEvent(value string) bool {
	for _, eventKey := range logs.EventKeys() {
		if value == eventKey {
			return true
		}
	}
	return false
}

func sampleRoleID(event *discordgo.InteractionCreate) string {
	if event != nil && event.Member != nil {
		for _, roleID := range event.Member.Roles {
			if roleID != "" {
				return roleID
			}
		}
	}
	return "0"
}

func (b *Bot) handleEmbed(event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	path, options := commandPath(data.Options)
	guildID := event.GuildID
	command := strings.Join(path, " ")
	deferred := command == "create" || command == "edit" || command == "preview" || command == "send"
	if deferred {
		ephemeral := command != "send"
		if err := interactionRespond(b.session, event, &discordgo.InteractionResponseData{Flags: messageFlags(ephemeral)}, discordgo.InteractionResponseDeferredChannelMessageWithSource); err != nil {
			b.logger.Error("defer embed command failed", "command", command, "error", err)
			return
		}
	}
	ctx, cancel := b.operationContext()
	defer cancel()
	reply := func(content string, ephemeral bool) {
		b.embedReply(event, deferred, &discordgo.InteractionResponseData{Content: content, Flags: messageFlags(ephemeral)})
	}
	replyData := func(response *discordgo.InteractionResponseData) {
		b.embedReply(event, deferred, response)
	}
	switch command {
	case "variables":
		respond(event, "**Safe embed variables**\nMember: `{user}`, `{user.mention}`, `{user.id}`, `{user.name}`, `{user.avatar}`, `{user.display_name}`\nServer: `{guild.name}`, `{guild.id}`, `{guild.icon}`, `{server_name}`, `{server_icon}`\nRule: `{rule.name}`, `{rule.source}`, `{rule.value}`, `{rule.condition}`, `{rule.reason}`, `{role}`, `{role.id}`\nVanity: `{vanity.rule}`, `{vanity.word}`, `{vanity.source}`, `{vanity.value}`, `{vanity.role}`. For `custom_status`, `{vanity.value}` is the member's Custom Status text.\nServer Tag: `{tag.rule}`, `{tag.condition}`, `{tag}`, `{tag.rule_value}`, `{tag.guild_id}`, `{tag.enabled}`, `{tag.badge}`, `{tag.role}`\nEvent: `{action}`, `{action.text}`, `{result}`, `{event}`, `{event.title}`, `{event.error}`, `{event.matched_value}`, `{identity.source}`, `{identity.value}`, `{timestamp}`, `{newline}`, `{separator}`, `{date.utc_timestamp}`\n\nPreview and send resolve data from the current member. Notification embeds use the same variables.", true)
	case "list":
		items, err := b.store.ListEmbedTemplates(ctx, guildID)
		if err != nil {
			respond(event, err.Error(), true)
			return
		}
		lines := make([]string, 0, len(items))
		for _, item := range items {
			lines = append(lines, "• `"+item.Name+"`")
		}
		respond(event, "**Embed templates**\n"+nonEmpty(strings.Join(lines, "\n"), "No templates."), true)
	case "create":
		name := strings.TrimSpace(optionString(options, "name"))
		if name == "" {
			reply("Provide an embed name.", true)
			return
		}
		template := embeds.Template{Title: optionString(options, "title"), Description: optionString(options, "description")}
		if color := optionString(options, "color"); color != "" {
			parsed, err := parseColorStrict(color)
			if err != nil {
				reply(err.Error(), true)
				return
			}
			template.Color = parsed
		}
		if err := embeds.ValidateTemplate(template); err != nil {
			reply("Template is invalid: "+err.Error(), true)
			return
		}
		if err := b.store.EnsureGuild(ctx, guildID); err != nil {
			reply(err.Error(), true)
			return
		}
		if existing, err := b.store.GetEmbedTemplate(ctx, guildID, name); err == nil {
			reply(fmt.Sprintf("An embed named `%s` already exists. Use `/embed edit name:%s` or choose another name.", existing.Name, name), true)
			return
		} else if !errors.Is(err, pgx.ErrNoRows) {
			reply("Could not check the embed name: "+err.Error(), true)
			return
		}
		item := databaseEmbedTemplate(identity.NewID(), guildID, name, nil, actorID(event))
		payload, err := json.Marshal(template)
		if err != nil {
			reply("Could not encode the embed: "+err.Error(), true)
			return
		}
		item.Payload = payload
		if err := b.store.SaveEmbedTemplate(ctx, item); err != nil {
			reply("Could not create the embed: "+err.Error(), true)
			return
		}
		response, err := b.embedPanelResponse(ctx, item, actorID(event), b.baseEmbedVariables(event))
		if err != nil {
			reply(err.Error(), true)
			return
		}
		replyData(response)
	case "delete":
		if err := b.store.DeleteEmbedTemplate(ctx, guildID, optionString(options, "name")); err != nil {
			reply(err.Error(), true)
			return
		}
		reply("Embed template removed.", true)
	case "preview", "send":
		item, err := b.store.GetEmbedTemplate(ctx, guildID, optionString(options, "name"))
		if err != nil {
			reply("Template not found: "+err.Error(), true)
			return
		}
		var template embeds.Template
		if err := json.Unmarshal(item.Payload, &template); err != nil {
			reply("Template payload is invalid.", true)
			return
		}
		payload, err := embeds.Build(template, b.embedVariables(ctx, event))
		if err != nil {
			reply("Template cannot be sent: "+err.Error(), true)
			return
		}
		if command == "preview" {
			replyData(&discordgo.InteractionResponseData{Content: payload.Content, Embeds: payload.Embeds, Components: payload.Components, AllowedMentions: payload.AllowedMentions})
			return
		}
		replyData(&discordgo.InteractionResponseData{Content: payload.Content, Embeds: payload.Embeds, Components: payload.Components, AllowedMentions: payload.AllowedMentions})
	case "edit":
		item, err := b.store.GetEmbedTemplate(ctx, guildID, optionString(options, "name"))
		if err != nil {
			reply("Template not found: "+err.Error(), true)
			return
		}
		var template embeds.Template
		if err := json.Unmarshal(item.Payload, &template); err != nil {
			reply("Template payload is invalid.", true)
			return
		}
		changed := false
		if optionString(options, "title") != "" {
			template.Title = optionString(options, "title")
			changed = true
		}
		if optionString(options, "description") != "" {
			template.Description = optionString(options, "description")
			changed = true
		}
		if !changed {
			response, err := b.embedPanelResponse(ctx, item, actorID(event), b.baseEmbedVariables(event))
			if err != nil {
				reply(err.Error(), true)
				return
			}
			replyData(response)
			return
		}
		if err := embeds.ValidateTemplate(template); err != nil {
			reply("Template is invalid: "+err.Error(), true)
			return
		}
		payload, err := json.Marshal(template)
		if err != nil {
			reply("Could not encode the embed: "+err.Error(), true)
			return
		}
		item.Payload = payload
		if err := b.store.SaveEmbedTemplate(ctx, item); err != nil {
			reply("Could not update the embed: "+err.Error(), true)
			return
		}
		updated, err := b.store.GetEmbedTemplateByID(ctx, guildID, item.ID)
		if err != nil {
			reply("Embed template updated, but the editor could not be reloaded: "+err.Error(), true)
			return
		}
		response, err := b.embedPanelResponse(ctx, updated, actorID(event), b.baseEmbedVariables(event))
		if err != nil {
			reply(err.Error(), true)
			return
		}
		replyData(response)
	default:
		reply("Choose a valid embed action.", true)
	}
}

func messageFlags(ephemeral bool) discordgo.MessageFlags {
	_ = ephemeral
	return 0
}

func (b *Bot) embedReply(event *discordgo.InteractionCreate, deferred bool, response *discordgo.InteractionResponseData) {
	if deferred {
		content := response.Content
		embedsValue := response.Embeds
		components := response.Components
		if _, err := b.session.InteractionResponseEdit(event.Interaction, &discordgo.WebhookEdit{Content: &content, Embeds: &embedsValue, Components: &components, AllowedMentions: response.AllowedMentions}); err != nil {
			b.logger.Error("edit embed command response failed", "error", err)
		}
		return
	}
	if err := eventInteractionRespond(b.session, event, response); err != nil {
		b.logger.Error("send embed command response failed", "error", err)
	}
}

func (b *Bot) handleMemberEvaluation(event *discordgo.InteractionCreate, source identity.Source, dryRun bool) {
	_, commandOptions := commandPath(event.ApplicationCommandData().Options)
	option := commandOptions["user"]

	if !dryRun {
		targetUserID := ""
		if option != nil {
			targetUserID = optionString(commandOptions, "user")
		}
		b.startManualSync(event, source, targetUserID)
		return
	}

	userID := actorID(event)
	if option != nil {
		userID = optionString(commandOptions, "user")
	}
	if userID == "" {
		respond(event, "Could not determine which member to test.", true)
		return
	}

	member, primaryKnown, err := b.loadMemberForSource(event.GuildID, userID, source)
	if err != nil {
		respond(event, "Could not load member data: "+sanitizeSyncMessage(err.Error()), true)
		return
	}
	if member.IsBot {
		respond(event, "This bot only evaluates human members; bot accounts are ignored.", true)
		return
	}
	vanity, tags, err := b.loadManualSyncRules(event.GuildID, source)
	if err != nil {
		respond(event, "Could not load rules: "+sanitizeSyncMessage(err.Error()), true)
		return
	}

	lines := []string{"**Dry run (no role mutations)**"}
	if source == identity.SourceGuildTag {
		lines = append(lines, "Primary guild: "+primaryGuildText(member.PrimaryGuild))
		if !primaryKnown {
			lines = append(lines, "⚠️ Discord did not provide authoritative `primary_guild` data, so Server Tag rules were **not evaluated** and existing grants would be preserved.")
			respond(event, strings.Join(lines, "\n"), true)
			return
		}
	}
	if source == identity.SourceVanity {
		for _, rule := range vanity {
			if !identity.VanitySourceKnown(member, rule.Source) {
				lines = append(lines, fmt.Sprintf("Vanity `%s` · %s: unknown (Discord did not provide this value; grant preserved)", rule.Name, vanitySourceLabel(rule.Source)))
				continue
			}
			matched, matchErr := identity.MatchVanity(rule, member)
			if matchErr != nil {
				lines = append(lines, fmt.Sprintf("Vanity `%s` · evaluation error: `%s`", rule.Name, sanitizeSyncMessage(matchErr.Error())))
				continue
			}
			lines = append(lines, fmt.Sprintf("Vanity `%s` · %s %s `%s`: %t", rule.Name, vanitySourceLabel(rule.Source), comparisonLabel(rule.Comparison), rule.Word, matched))
		}
	}
	if source == identity.SourceGuildTag {
		for _, rule := range tags {
			lines = append(lines, fmt.Sprintf("Server Tag `%s` · %s: %t", rule.Name, guildTagConditionLabel(rule.Condition), identity.MatchGuildTag(rule, member.PrimaryGuild)))
		}
	}
	respond(event, strings.Join(lines, "\n"), true)
}

func primaryGuildText(primary *identity.PrimaryGuild) string {
	if primary == nil {
		return "not returned by Discord"
	}
	enabled := "unknown"
	if primary.IdentityEnabled != nil {
		enabled = strconv.FormatBool(*primary.IdentityEnabled)
	}
	return fmt.Sprintf("id=`%s`, enabled=`%s`, tag=`%s`", nonEmpty(primary.IdentityGuildID, "none"), enabled, nonEmpty(primary.Tag, "none"))
}

func guildTagConditionNeedsValue(condition identity.GuildTagCondition) bool {
	switch condition {
	case identity.ConditionIsGuildID, identity.ConditionIsNotGuildID, identity.ConditionTagEquals, identity.ConditionTagNotEquals:
		return true
	default:
		return false
	}
}

func validateVanityRuleInput(rule identity.VanityRule) error {
	if rule.Name == "" {
		return fmt.Errorf("give the rule a name, for example `cinnamochi`")
	}
	if rule.Word == "" {
		return fmt.Errorf("give the text to match in the selected source")
	}
	if rule.RoleID == "" {
		return fmt.Errorf("choose the role that should be managed")
	}
	switch rule.Source {
	case identity.VanityCustomStatus, identity.VanityUsername, identity.VanityGlobalName, identity.VanityGuildNickname, identity.VanityDisplayName:
	default:
		return fmt.Errorf("choose a valid Vanity source, such as `Custom Status (profile text)`")
	}
	switch rule.Action {
	case identity.ActionAddRole, identity.ActionRemoveRole:
	default:
		return fmt.Errorf("choose whether the rule adds or removes the role")
	}
	if _, err := identity.MatchVanity(rule, identity.MemberIdentity{}); err != nil {
		return fmt.Errorf("invalid Vanity rule: %w", err)
	}
	return nil
}

func validateGuildTagRuleInput(rule identity.GuildTagRule) error {
	if strings.TrimSpace(rule.Name) == "" {
		return fmt.Errorf("give the rule a name, for example `partner-server`")
	}
	if rule.RoleID == "" {
		return fmt.Errorf("choose the role that should be managed")
	}
	switch rule.Condition {
	case identity.ConditionIsGuildID, identity.ConditionIsNotGuildID, identity.ConditionIdentityEnabled, identity.ConditionIdentityDisabled, identity.ConditionTagEquals, identity.ConditionTagNotEquals:
	default:
		return fmt.Errorf("choose a valid Server Tag condition")
	}
	if guildTagConditionNeedsValue(rule.Condition) && strings.TrimSpace(rule.Value) == "" {
		return fmt.Errorf("this condition needs a guild ID or visible Server Tag value")
	}
	switch rule.Action {
	case identity.ActionAddRole, identity.ActionRemoveRole:
	default:
		return fmt.Errorf("choose whether the rule adds or removes the role")
	}
	return nil
}

func (b *Bot) handleComponent(event *discordgo.InteractionCreate) {
	data := event.MessageComponentData()
	id := data.CustomID
	if strings.HasPrefix(id, "et:") {
		b.handleEmbedPanelButton(event)
		return
	}
	if strings.HasPrefix(id, "help:") {
		b.handleHelpComponent(event, id, data)
		return
	}
	if strings.HasPrefix(id, "setup:") {
		b.handleSetupComponent(event, id, data)
		return
	}
	respond(event, "This panel has expired. Run the slash command again.", true)
}

func (b *Bot) handleHelpComponent(event *discordgo.InteractionCreate, id string, data discordgo.MessageComponentInteractionData) {
	parts := strings.Split(id, ":")
	if len(parts) < 3 {
		respond(event, "This help panel is invalid or has expired. Run `/cmds` again.", true)
		return
	}
	sessionID := parts[2]
	actor := actorID(event)
	session, allowed := b.helpAllowed(sessionID, actor)
	if !allowed {
		respond(event, "This help panel belongs to another user or has expired.", true)
		return
	}
	if parts[1] == "close" {
		b.closeHelpSession(sessionID)
		response := &discordgo.InteractionResponseData{Content: "Help panel closed. Run `/cmds` whenever you need it again.", Components: []discordgo.MessageComponent{}, Embeds: []*discordgo.MessageEmbed{}}
		_ = interactionRespond(b.session, event, response, discordgo.InteractionResponseUpdateMessage)
		return
	}
	category := session.Category
	if parts[1] == "page" && len(parts) > 3 {
		category, _ = strconv.Atoi(parts[3])
	}
	if parts[1] == "select" && len(data.Values) > 0 {
		category, _ = strconv.Atoi(data.Values[0])
	}
	response, err := b.helpPanelForSession(sessionID, category)
	if err != nil {
		respond(event, err.Error(), true)
		return
	}
	_ = interactionRespond(b.session, event, response, discordgo.InteractionResponseUpdateMessage)
}

func (b *Bot) handleSetupComponent(event *discordgo.InteractionCreate, id string, data discordgo.MessageComponentInteractionData) {
	switch id {
	case "setup:cancel":
		respond(event, "Setup closed. Completed forms were already saved. Run `/setup` again whenever you need this guide.", true)
	case "setup:logs":
		respond(event, "**Step 1 · Action logs**\nRun `/logs setup` in the channel where role additions, removals, reasons, and errors should appear. This channel is only for staff audit cards.\n\nVerify it with `/logs view` or `/logs test event:tag_add`. Member thank-you messages are configured separately in Step 4.", true)
	case "setup:embeds":
		respond(event, "**Step 4 · Thank-you messages**\nConfigure delivery with `/vanity notify` and `/guildtag notify`. These are messages sent to members when a rule starts matching. They still send when the member already has the role.\n\nThe default embeds are `vanity_notify` and `guildtag_notify`; edit either one with `/embed edit name:vanity_notify` or `/embed edit name:guildtag_notify`. Use `/embed preview name:vanity_notify` before a real notification. `/logs` is only for staff audit cards.", true)
	case "setup:vanity":
		b.updateSetupChoice(event, "**Step 2 · Custom Status rule**\nChoose which profile value should trigger the Vanity rule. Custom Status means the text a member writes in their Discord profile status. The setup uses a safe `contains` match and adds the selected role.", "setup:vanity_source", []discordgo.SelectMenuOption{{Label: "Custom Status (profile text)", Value: string(identity.VanityCustomStatus), Description: "Match text shown in a member's Discord profile"}, {Label: "Username", Value: string(identity.VanityUsername), Description: "Match the member's Discord username"}, {Label: "Global name", Value: string(identity.VanityGlobalName), Description: "Match the member's global display name"}, {Label: "Server nickname", Value: string(identity.VanityGuildNickname), Description: "Match the member's nickname in this server"}})
	case "setup:vanity_source":
		if len(data.Values) == 0 {
			respond(event, "Choose a Vanity source first.", true)
			return
		}
		source := identity.VanitySource(data.Values[0])
		b.updateSetupRoleChoice(event, "Choose the role that this rule should manage. Discord will show only roles from this server.", "setup:vanity_role:"+string(source))
	case "setup:vanity_role":
		if len(data.Values) == 0 {
			respond(event, "Choose a role first.", true)
			return
		}
		source := strings.TrimPrefix(id, "setup:vanity_role:")
		label := "the selected profile value"
		if source == string(identity.VanityCustomStatus) {
			label = "the member's Custom Status text"
		}
		showSetupModal(event, "setup:vanity_modal:"+source+":"+data.Values[0], "Create Vanity rule", []discordgo.TextInput{{CustomID: "name", Label: "Rule name", Placeholder: "For example: cinnamochi", Style: discordgo.TextInputShort, Required: true, MaxLength: 64}, {CustomID: "word", Label: "Text to find in " + label, Placeholder: "For example: cinnamochi", Style: discordgo.TextInputShort, Required: true, MaxLength: 256}})
	case "setup:guildtag":
		b.updateSetupChoice(event, "**Step 2 · Server Tag rule**\nChoose the Discord identity condition to match. The setup adds the selected role and checks the member's current identity data.", "setup:guildtag_condition", []discordgo.SelectMenuOption{{Label: "Identity guild is", Value: string(identity.ConditionIsGuildID), Description: "Match a specific identity guild ID"}, {Label: "Identity guild is not", Value: string(identity.ConditionIsNotGuildID), Description: "Match members outside a specific identity guild"}, {Label: "Server Tag is enabled", Value: string(identity.ConditionIdentityEnabled), Description: "Match members with Server Tag enabled"}, {Label: "Server Tag is disabled", Value: string(identity.ConditionIdentityDisabled), Description: "Match members without Server Tag enabled"}, {Label: "Visible tag equals", Value: string(identity.ConditionTagEquals), Description: "Match the visible Server Tag text"}, {Label: "Visible tag is not", Value: string(identity.ConditionTagNotEquals), Description: "Match a different visible Server Tag text"}})
	case "setup:guildtag_condition":
		if len(data.Values) == 0 {
			respond(event, "Choose a Server Tag condition first.", true)
			return
		}
		condition := identity.GuildTagCondition(data.Values[0])
		b.updateSetupRoleChoice(event, "Choose the role that this rule should manage. Discord will show only roles from this server.", "setup:guildtag_role:"+string(condition))
	case "setup:guildtag_role":
		if len(data.Values) == 0 {
			respond(event, "Choose a role first.", true)
			return
		}
		condition := strings.TrimPrefix(id, "setup:guildtag_role:")
		inputs := []discordgo.TextInput{{CustomID: "name", Label: "Rule name", Placeholder: "For example: partner-server", Style: discordgo.TextInputShort, Required: true, MaxLength: 64}}
		if guildTagConditionNeedsValue(identity.GuildTagCondition(condition)) {
			inputs = append(inputs, discordgo.TextInput{CustomID: "value", Label: "Expected guild ID or visible tag", Placeholder: "Paste the ID or type the tag", Style: discordgo.TextInputShort, Required: true, MaxLength: 32})
		}
		showSetupModal(event, "setup:guildtag_modal:"+condition+":"+data.Values[0], "Create Server Tag rule", inputs)
	case "setup:profile":
		showSetupModal(event, "setup:profile_modal", "Customize bot profile", []discordgo.TextInput{{CustomID: "nickname", Label: "Nickname (optional)", Placeholder: "Name shown in this server", Style: discordgo.TextInputShort, Required: false, MaxLength: 32}, {CustomID: "bio", Label: "Bio (optional)", Placeholder: "Short profile text", Style: discordgo.TextInputParagraph, Required: false, MaxLength: 190}})
	default:
		respond(event, "Unknown setup step.", true)
	}
}

func (b *Bot) updateSetupChoice(event *discordgo.InteractionCreate, content, customID string, options []discordgo.SelectMenuOption) {
	data := &discordgo.InteractionResponseData{Content: content, Components: []discordgo.MessageComponent{&discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: customID, Placeholder: "Choose one option", Options: options}}}, &discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.Button{Style: discordgo.SecondaryButton, Label: "Close", CustomID: "setup:cancel"}}}}}
	if err := interactionRespond(b.session, event, data, discordgo.InteractionResponseUpdateMessage); err != nil {
		b.logger.Warn("update setup choice failed", "error", err)
	}
}

func (b *Bot) updateSetupRoleChoice(event *discordgo.InteractionCreate, content, customID string) {
	components := []discordgo.MessageComponent{&discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.SelectMenu{MenuType: discordgo.RoleSelectMenu, CustomID: customID, Placeholder: "Choose a role"}}}, &discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.Button{Style: discordgo.SecondaryButton, Label: "Close", CustomID: "setup:cancel"}}}}
	data := &discordgo.InteractionResponseData{Content: content, Components: components}
	if err := interactionRespond(b.session, event, data, discordgo.InteractionResponseUpdateMessage); err != nil {
		b.logger.Warn("update setup role choice failed", "error", err)
	}
}

func (b *Bot) handleModal(event *discordgo.InteractionCreate) {
	data := event.ModalSubmitData()
	if strings.HasPrefix(data.CustomID, "etm:") {
		b.handleEmbedPanelModal(event)
		return
	}
	if strings.HasPrefix(data.CustomID, "setup:vanity_modal:") {
		parts := strings.SplitN(strings.TrimPrefix(data.CustomID, "setup:vanity_modal:"), ":", 2)
		if len(parts) != 2 {
			respond(event, "This setup form has expired. Run `/setup` again.", true)
			return
		}
		b.handleSetupVanityModal(event, parts[0], parts[1])
		return
	}
	if strings.HasPrefix(data.CustomID, "setup:guildtag_modal:") {
		parts := strings.SplitN(strings.TrimPrefix(data.CustomID, "setup:guildtag_modal:"), ":", 2)
		if len(parts) != 2 {
			respond(event, "This setup form has expired. Run `/setup` again.", true)
			return
		}
		b.handleSetupGuildTagModal(event, parts[0], parts[1])
		return
	}
	values := modalValues(data.Components)
	ctx, cancel := b.operationContext()
	defer cancel()
	switch data.CustomID {
	case "setup:vanity_modal":
		rule := identity.VanityRule{ID: identity.NewID(), GuildID: event.GuildID, Name: values["name"], Word: values["word"], Source: identity.VanityUsername, Comparison: identity.ComparisonContains, RoleID: values["role"], Action: identity.ActionAddRole, Enabled: true, CreatedBy: actorID(event), Normalization: identity.Normalization{CaseFold: true, TrimSpace: true, CollapseSpace: true}}
		if err := b.store.EnsureGuild(ctx, event.GuildID); err != nil {
			respond(event, err.Error(), true)
			return
		}
		if err := b.store.CreateVanityRule(ctx, rule); err != nil {
			respond(event, err.Error(), true)
			return
		}
		respond(event, "Vanity rule draft saved: username contains the supplied word and adds the supplied role. Review with `/vanity test`.", true)
	case "setup:guildtag_modal":
		rule := identity.GuildTagRule{ID: identity.NewID(), GuildID: event.GuildID, Name: values["name"], Condition: identity.GuildTagCondition(values["condition"]), Value: values["value"], RoleID: values["role"], Action: identity.ActionAddRole, Enabled: true, CreatedBy: actorID(event)}
		if err := b.store.EnsureGuild(ctx, event.GuildID); err != nil {
			respond(event, err.Error(), true)
			return
		}
		if err := b.store.CreateGuildTagRule(ctx, rule); err != nil {
			respond(event, err.Error(), true)
			return
		}
		respond(event, "Server Tag rule draft saved. It reads primary_guild fields and can be dry-run with `/guildtag test`.", true)
	case "setup:profile_modal":
		botProfile, err := b.store.GetBotProfile(ctx, event.GuildID)
		if err != nil {
			respond(event, err.Error(), true)
			return
		}
		nickname, bio := values["nickname"], values["bio"]
		update := profile.MemberUpdate{}
		if nickname != "" {
			botProfile.Nickname = &nickname
			update.Nickname = &nickname
		}
		if bio != "" {
			botProfile.Bio = &bio
			update.Bio = &bio
		}
		if err := b.updateProfile(ctx, event.GuildID, update, botProfile); err != nil {
			respond(event, "Discord rejected the profile change; the previous profile was preserved.", true)
			return
		}
		respond(event, "Profile draft saved and synchronized. Use `/set avatar` or `/set banner` with a Discord upload or HTTPS URL for images.", true)
	default:
		respond(event, "Unknown setup form.", true)
	}
}

func (b *Bot) handleSetupVanityModal(event *discordgo.InteractionCreate, sourceValue, roleID string) {
	ctx, cancel := b.operationContext()
	defer cancel()
	values := modalValues(event.ModalSubmitData().Components)
	source := identity.VanitySource(sourceValue)
	rule := identity.VanityRule{ID: identity.NewID(), GuildID: event.GuildID, Name: strings.TrimSpace(values["name"]), Word: strings.TrimSpace(values["word"]), Source: source, Comparison: identity.ComparisonContains, RoleID: roleID, Action: identity.ActionAddRole, Enabled: true, CreatedBy: actorID(event), Normalization: identity.Normalization{CaseFold: true, TrimSpace: true, CollapseSpace: true}}
	if err := validateVanityRuleInput(rule); err != nil {
		respond(event, err.Error(), true)
		return
	}
	if err := b.store.EnsureGuild(ctx, event.GuildID); err != nil {
		respond(event, err.Error(), true)
		return
	}
	if err := b.store.CreateVanityRule(ctx, rule); err != nil {
		respond(event, "Could not create the Vanity rule: "+err.Error(), true)
		return
	}
	if event.Member != nil {
		go b.evaluateMember(event.GuildID, b.cachedMemberIdentity(event.GuildID, event.Member, nil))
	}
	respond(event, fmt.Sprintf("Vanity rule created. It uses `%s contains %s` and adds <@&%s>. Test it with `/vanity test` or run `/vanity sync` for the server.", vanitySourceLabel(source), rule.Word, roleID), true)
}

func (b *Bot) handleSetupGuildTagModal(event *discordgo.InteractionCreate, conditionValue, roleID string) {
	ctx, cancel := b.operationContext()
	defer cancel()
	values := modalValues(event.ModalSubmitData().Components)
	rule := identity.GuildTagRule{ID: identity.NewID(), GuildID: event.GuildID, Name: strings.TrimSpace(values["name"]), Condition: identity.GuildTagCondition(conditionValue), Value: strings.TrimSpace(values["value"]), RoleID: roleID, Action: identity.ActionAddRole, Enabled: true, CreatedBy: actorID(event)}
	if err := validateGuildTagRuleInput(rule); err != nil {
		respond(event, err.Error(), true)
		return
	}
	if err := b.store.EnsureGuild(ctx, event.GuildID); err != nil {
		respond(event, err.Error(), true)
		return
	}
	if err := b.store.CreateGuildTagRule(ctx, rule); err != nil {
		respond(event, "Could not create the Server Tag rule: "+err.Error(), true)
		return
	}
	b.queueCurrentMemberEvaluation(event)
	respond(event, fmt.Sprintf("Server Tag rule created. It matches `%s` and adds <@&%s>. Test it with `/guildtag test` or run `/guildtag sync` for the server.", guildTagConditionLabel(rule.Condition), roleID), true)
}

func vanitySourceLabel(source identity.VanitySource) string {
	switch source {
	case identity.VanityCustomStatus:
		return "Custom Status"
	case identity.VanityUsername:
		return "Username"
	case identity.VanityGlobalName:
		return "Global name"
	case identity.VanityGuildNickname:
		return "Server nickname"
	case identity.VanityDisplayName:
		return "Display name"
	default:
		return string(source)
	}
}

func comparisonLabel(comparison identity.Comparison) string {
	switch comparison {
	case identity.ComparisonEquals:
		return "exactly matches"
	case identity.ComparisonContains:
		return "contains"
	case identity.ComparisonStartsWith:
		return "starts with"
	case identity.ComparisonEndsWith:
		return "ends with"
	case identity.ComparisonRegex:
		return "matches pattern"
	default:
		return string(comparison)
	}
}

func roleActionLabel(action identity.Action) string {
	if action == identity.ActionRemoveRole {
		return "Remove role"
	}
	return "Add role"
}

func guildTagConditionLabel(condition identity.GuildTagCondition) string {
	switch condition {
	case identity.ConditionIsGuildID:
		return "identity guild is the supplied ID"
	case identity.ConditionIsNotGuildID:
		return "identity guild is not the supplied ID"
	case identity.ConditionIdentityEnabled:
		return "Server Tag is enabled"
	case identity.ConditionIdentityDisabled:
		return "Server Tag is disabled"
	case identity.ConditionTagEquals:
		return "visible tag equals the supplied text"
	case identity.ConditionTagNotEquals:
		return "visible tag is not the supplied text"
	default:
		return string(condition)
	}
}

func showSetupModal(event *discordgo.InteractionCreate, customID, title string, inputs []discordgo.TextInput) {
	rows := make([]discordgo.MessageComponent, 0, len(inputs))
	for _, input := range inputs {
		rows = append(rows, &discordgo.ActionsRow{Components: []discordgo.MessageComponent{&input}})
	}
	_ = interactionRespond(nil, event, &discordgo.InteractionResponseData{CustomID: customID, Title: title, Components: rows}, discordgo.InteractionResponseModal)
}

func modalValues(components []discordgo.MessageComponent) map[string]string {
	values := make(map[string]string)
	for _, component := range components {
		row, ok := component.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, child := range row.Components {
			if input, ok := child.(*discordgo.TextInput); ok {
				values[input.CustomID] = input.Value
			}
		}
	}
	return values
}

func (b *Bot) hasGuildAdmin(event *discordgo.InteractionCreate) bool {
	if event.GuildID == "" || event.Member == nil {
		return false
	}
	permissions := event.Member.Permissions
	if permissions&discordgo.PermissionAdministrator != 0 {
		return true
	}
	return permissions&discordgo.PermissionManageGuild != 0 && permissions&discordgo.PermissionManageRoles != 0
}

func (b *Bot) hasManageGuild(event *discordgo.InteractionCreate) bool {
	if event.GuildID == "" || event.Member == nil {
		return false
	}
	permissions := event.Member.Permissions
	return permissions&discordgo.PermissionAdministrator != 0 || permissions&discordgo.PermissionManageGuild != 0
}

func profileAssetUpdate(kind, value string) profile.MemberUpdate {
	if kind == "banner" {
		return profile.MemberUpdate{Banner: &value}
	}
	return profile.MemberUpdate{Avatar: &value}
}

func attachmentOption(data discordgo.ApplicationCommandInteractionData, options map[string]*discordgo.ApplicationCommandInteractionDataOption, name string) *discordgo.MessageAttachment {
	item := options[name]
	if item == nil || item.Type != discordgo.ApplicationCommandOptionAttachment || data.Resolved == nil {
		return nil
	}
	id, ok := item.Value.(string)
	if !ok || id == "" {
		return nil
	}
	return data.Resolved.Attachments[id]
}

func actorID(event *discordgo.InteractionCreate) string {
	if event == nil || event.Interaction == nil {
		return ""
	}
	if event.Member != nil && event.Member.User != nil {
		return event.Member.User.ID
	}
	if event.User != nil {
		return event.User.ID
	}
	return ""
}

func (b *Bot) loadMemberIdentity(ctx context.Context, guildID, userID string) (identity.MemberIdentity, error) {
	endpoint := discordgo.EndpointGuildMember(guildID, userID)
	raw, err := b.session.RequestWithBucketID(http.MethodGet, endpoint, nil, discordgo.EndpointGuildMember(guildID, ""), discordgo.WithContext(ctx))
	if err != nil {
		return identity.MemberIdentity{}, err
	}
	var item rawSyncMember
	if err := json.Unmarshal(raw, &item); err != nil {
		return identity.MemberIdentity{}, fmt.Errorf("decode guild member %s: %w", userID, err)
	}
	candidate := syncCandidateFromRaw(guildID, item)
	if candidate.Member == nil {
		return identity.MemberIdentity{}, fmt.Errorf("member %s is unavailable", userID)
	}
	return b.cachedMemberIdentity(guildID, candidate.Member, candidate.PrimaryGuild), nil
}

func (b *Bot) loadPrimaryGuildForGuild(ctx context.Context, guildID, userID string) (*identity.PrimaryGuild, bool, error) {
	endpoint := discordgo.EndpointGuildMember(guildID, userID)
	raw, err := b.session.RequestWithBucketID(http.MethodGet, endpoint, nil, discordgo.EndpointGuildMember(guildID, ""), discordgo.WithContext(ctx))
	if err != nil {
		return nil, false, fmt.Errorf("load primary_guild for %s: %w", userID, err)
	}
	var item rawSyncMember
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, false, fmt.Errorf("decode primary_guild for %s: %w", userID, err)
	}
	candidate := syncCandidateFromRaw(guildID, item)
	return candidate.PrimaryGuild, candidate.PrimaryKnown, nil
}

func profileUpdateError(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	reason := strings.Join(strings.Fields(err.Error()), " ")
	reason = strings.ReplaceAll(reason, "`", "'")
	const maxRunes = 300
	runes := []rune(reason)
	if len(runes) > maxRunes {
		reason = string(runes[:maxRunes]) + "…"
	}
	if reason == "" {
		return prefix
	}
	return prefix + " Reason: `" + reason + "`"
}

func respond(event *discordgo.InteractionCreate, content string, ephemeral bool) {
	_ = ephemeral
	_ = eventInteractionRespond(nil, event, &discordgo.InteractionResponseData{Content: content})
}

func eventInteractionRespond(session *discordgo.Session, event *discordgo.InteractionCreate, data *discordgo.InteractionResponseData) error {
	return interactionRespond(session, event, data, discordgo.InteractionResponseChannelMessageWithSource)
}
func interactionRespond(session *discordgo.Session, event *discordgo.InteractionCreate, data *discordgo.InteractionResponseData, responseType discordgo.InteractionResponseType) error {
	if data == nil {
		data = &discordgo.InteractionResponseData{}
	}
	if session == nil && event != nil && event.Interaction != nil {
		if registered, ok := sessionRegistry.Load(event.Interaction.AppID); ok {
			session, _ = registered.(*discordgo.Session)
		}
	}
	if session == nil {
		return fmt.Errorf("Discord session is not initialized")
	}
	return session.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{Type: responseType, Data: data})
}

func commandPath(options []*discordgo.ApplicationCommandInteractionDataOption) ([]string, map[string]*discordgo.ApplicationCommandInteractionDataOption) {
	path := []string{}
	values := map[string]*discordgo.ApplicationCommandInteractionDataOption{}
	current := options
	for len(current) > 0 {
		item := current[0]
		if item.Type == discordgo.ApplicationCommandOptionSubCommand || item.Type == discordgo.ApplicationCommandOptionSubCommandGroup {
			path = append(path, item.Name)
			current = item.Options
			continue
		}
		for _, option := range current {
			values[option.Name] = option
		}
		break
	}
	return path, values
}
func option(options map[string]*discordgo.ApplicationCommandInteractionDataOption, name string) *discordgo.ApplicationCommandInteractionDataOption {
	return options[name]
}
func optionString(options map[string]*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	if item := options[name]; item != nil {
		switch item.Type {
		case discordgo.ApplicationCommandOptionString:
			return item.StringValue()
		case discordgo.ApplicationCommandOptionRole,
			discordgo.ApplicationCommandOptionUser,
			discordgo.ApplicationCommandOptionChannel,
			discordgo.ApplicationCommandOptionMentionable,
			discordgo.ApplicationCommandOptionAttachment:
			// Discord sends entity options as their snowflake string. Do not call
			// StringValue on them because discordgo intentionally panics for
			// every non-String option type.
			value, _ := item.Value.(string)
			return value
		}
	}
	return ""
}
func optionBool(options map[string]*discordgo.ApplicationCommandInteractionDataOption, name string) bool {
	if item := options[name]; item != nil {
		return item.BoolValue()
	}
	return false
}
func optionBoolDefault(options map[string]*discordgo.ApplicationCommandInteractionDataOption, name string, fallback bool) bool {
	if item := options[name]; item != nil {
		return item.BoolValue()
	}
	return fallback
}
func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
func pointerValue(value *string) string {
	if value == nil {
		return "global fallback"
	}
	return *value
}
func parseColor(value string) int {
	value = strings.TrimPrefix(strings.TrimSpace(value), "#")
	if value == "" {
		return 0x5865F2
	}
	parsed, err := strconv.ParseInt(value, 16, 32)
	if err != nil {
		return 0x5865F2
	}
	return int(parsed)
}

func parseColorStrict(value string) (int, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(value) != 6 {
		return 0, fmt.Errorf("color must be a six-digit hex value such as `#5865F2`")
	}
	parsed, err := strconv.ParseInt(value, 16, 32)
	if err != nil || parsed < 0 || parsed > 0xFFFFFF {
		return 0, fmt.Errorf("color must be a six-digit hex value such as `#5865F2`")
	}
	return int(parsed), nil
}

// baseEmbedVariables only uses data already present in the interaction and
// gateway cache. It is safe for the first response to an interaction, where
// waiting on Discord's primary_guild endpoint could exceed the 3-second
// acknowledgement window.
func (b *Bot) baseEmbedVariables(event *discordgo.InteractionCreate) embeds.Variables {
	userID := actorID(event)
	username := ""
	avatar := ""
	displayName := ""
	if event.Member != nil && event.Member.User != nil {
		username = event.Member.User.Username
		avatar = event.Member.AvatarURL("256")
		displayName = event.Member.DisplayName()
	} else if event.User != nil {
		username = event.User.Username
		avatar = event.User.AvatarURL("256")
		displayName = username
	}
	guildName, guildIcon := "", ""
	if b.session != nil && b.session.State != nil {
		if guild, err := b.session.State.Guild(event.GuildID); err == nil {
			guildName, guildIcon = guild.Name, guild.IconURL("256")
		}
	}
	channelName := ""
	if b.session != nil && b.session.State != nil {
		if channel, err := b.session.State.Channel(event.ChannelID); err == nil {
			channelName = channel.Name
		}
	}
	variables := embeds.Variables{
		UserID: userID, UserName: username, UserMention: "<@" + userID + ">", UserAvatar: avatar,
		UserDisplayName: displayName, GuildID: event.GuildID, GuildName: guildName, GuildIcon: guildIcon,
		ChannelID: event.ChannelID, ChannelName: channelName, Timestamp: time.Now().UTC(),
	}
	return variables
}

func (b *Bot) embedVariables(ctx context.Context, event *discordgo.InteractionCreate) embeds.Variables {
	variables := b.baseEmbedVariables(event)
	userID := variables.UserID
	if event.GuildID == "" || userID == "" {
		return variables
	}
	member, err := b.loadMemberIdentity(ctx, event.GuildID, userID)
	if err != nil {
		return variables
	}
	vanityRules, err := b.store.ListVanityRules(ctx, event.GuildID)
	if err == nil {
		for _, rule := range vanityRules {
			matched, matchErr := identity.MatchVanity(rule, member)
			if matchErr != nil || !matched || variables.VanityRule != "" {
				continue
			}
			variables.VanityRule = rule.Name
			variables.VanityWord = rule.Word
			variables.VanitySource = string(rule.Source)
			variables.VanityValue = identity.VanityValue(member, rule.Source)
			variables.VanityRoleID = rule.RoleID
			variables.MatchedValue = variables.VanityValue
			if variables.RuleName == "" {
				variables.RuleName, variables.RuleSource, variables.RuleValue, variables.RoleID = rule.Name, string(identity.SourceVanity), rule.Word, rule.RoleID
				variables.RuleCondition, variables.RuleReason = string(rule.Comparison), fmt.Sprintf("Matched %s condition for value %s", rule.Source, variables.VanityValue)
				variables.Action, variables.ActionText, variables.Result, variables.EventName, variables.EventTitle = string(rule.Action), actionText(rule.Action), "preview", vanityEventName(rule.Action), "Vanity Action"
			}
		}
	}
	tagRules, err := b.store.ListGuildTagRules(ctx, event.GuildID)
	if err == nil {
		for _, rule := range tagRules {
			if !identity.MatchGuildTag(rule, member.PrimaryGuild) || variables.TagRule != "" {
				continue
			}
			variables.TagRule = rule.Name
			variables.TagCondition = string(rule.Condition)
			variables.TagRuleValue = rule.Value
			variables.TagRoleID = rule.RoleID
			if member.PrimaryGuild != nil {
				variables.Tag, variables.TagGuildID, variables.TagBadge = member.PrimaryGuild.Tag, member.PrimaryGuild.IdentityGuildID, member.PrimaryGuild.Badge
				if member.PrimaryGuild.IdentityEnabled != nil {
					variables.TagEnabled = strconv.FormatBool(*member.PrimaryGuild.IdentityEnabled)
				}
			}
			if variables.RuleName == "" {
				variables.RuleName, variables.RuleSource, variables.RuleValue, variables.RoleID = rule.Name, string(identity.SourceGuildTag), rule.Value, rule.RoleID
				variables.RuleCondition, variables.RuleReason = string(rule.Condition), fmt.Sprintf("Matched %s condition for value %s", rule.Condition, rule.Value)
				variables.MatchedValue = rule.Value
				variables.Action, variables.ActionText, variables.Result, variables.EventName, variables.EventTitle = string(rule.Action), actionText(rule.Action), "preview", tagEventName(rule.Action), "Server Tag Action"
			}
		}
	}
	return variables
}
func mapKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
func channelMapValue(value string) string { return value }
func databaseLogConfig(guildID, channelID, events string) database.LogConfig {
	set := map[string]bool{}
	for _, item := range strings.Split(events, ",") {
		if strings.TrimSpace(item) != "" {
			set[strings.TrimSpace(item)] = true
		}
	}
	return database.LogConfig{GuildID: guildID, ChannelID: channelID, Events: set}
}

func actionText(action identity.Action) string {
	if action == identity.ActionRemoveRole {
		return "remove"
	}
	return "add"
}

func vanityEventName(action identity.Action) string {
	if action == identity.ActionRemoveRole {
		return logs.EventVanityRemove
	}
	return logs.EventVanityAdd
}

func tagEventName(action identity.Action) string {
	if action == identity.ActionRemoveRole {
		return logs.EventTagRemove
	}
	return logs.EventTagAdd
}

func databaseEmbedTemplate(id, guildID, name string, payload []byte, createdBy string) database.EmbedTemplate {
	return database.EmbedTemplate{ID: id, GuildID: guildID, Name: name, Payload: payload, Enabled: true, CreatedBy: createdBy}
}
