package discord

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

type helpCategory struct {
	Name, Summary string
	Commands      []string
}

var helpCategories = []helpCategory{
	{Name: "Setup", Summary: "Start here: guided setup, status overview, and this server's bot profile.", Commands: []string{"/setup · guided setup", "/config view · see what is ready", "/config reset · remove saved server settings", "/set view · inspect profile", "/set nickname · change server name", "/set avatar · upload or use HTTPS", "/set banner · upload or use HTTPS", "/set bio · change profile text", "/set reset · use global profile"}},
	{Name: "Identity", Summary: "Inspect and reconcile a member's identity data.", Commands: []string{"/identity status", "/identity sync", "/identity audit"}},
	{Name: "Vanity", Summary: "Give or remove a role when a member's Custom Status or profile name matches text.", Commands: []string{"/vanity add · create a rule", "/vanity edit · change a rule", "/vanity remove · disable a rule", "/vanity list · view active rules", "/vanity test · preview without roles", "/vanity sync · check members manually", "/vanity notify · configure thank-you messages"}},
	{Name: "Server Tags", Summary: "Give or remove a role from Discord identity guild and visible Server Tag data.", Commands: []string{"/guildtag add · create a rule", "/guildtag edit · change a rule", "/guildtag remove · disable a rule", "/guildtag list · view active rules", "/guildtag test · preview without roles", "/guildtag sync · check members manually", "/guildtag notify · configure thank-you messages"}},
	{Name: "Logs", Summary: "Petto-style action audit cards for role changes and errors.", Commands: []string{"/logs setup", "/logs set", "/logs view", "/logs test"}},
	{Name: "Embeds", Summary: "Safe reusable notification embeds with bounded variables.", Commands: []string{"/embed create", "/embed edit", "/embed delete", "/embed list", "/embed preview", "/embed send", "/embed variables"}},
	{Name: "Utility", Summary: "Slash-first commands, dry-runs, and bounded synchronization.", Commands: []string{"/cmds [command]"}},
}

func (b *Bot) helpPanel(ownerID, query string, category int) (*discordgo.InteractionResponseData, error) {
	if category < 0 {
		category = len(helpCategories) - 1
	}
	if category >= len(helpCategories) {
		category = 0
	}
	if query != "" {
		query = strings.ToLower(strings.TrimSpace(query))
		for index, item := range helpCategories {
			for _, command := range item.Commands {
				if strings.Contains(strings.ToLower(command), query) {
					category = index
					goto found
				}
			}
		}
	}
found:
	item := helpCategories[category]
	b.helpMu.Lock()
	b.helpSessions[ownerID] = helpSession{OwnerID: ownerID, Category: category, Expires: time.Now().Add(10 * time.Minute)}
	b.helpMu.Unlock()
	content := fmt.Sprintf("**%s**\n%s\n\n**Commands**\n%s\n\nUse the menu or buttons to navigate. This private panel expires in 10 minutes.%s", item.Name, item.Summary, strings.Join(item.Commands, "\n"), editorLinks(b.config.WebsiteURL, b.config.DocsURL))
	return &discordgo.InteractionResponseData{Content: content, Flags: discordgo.MessageFlagsEphemeral, Components: helpComponents(ownerID, category, b.config.Emojis)}, nil
}

func helpComponents(ownerID string, category int, emojis map[string]string) []discordgo.MessageComponent {
	previous := category - 1
	next := category + 1
	if previous < 0 {
		previous = len(helpCategories) - 1
	}
	if next >= len(helpCategories) {
		next = 0
	}
	previousEmoji := emojiOr(emojis["PREV"], "◀️")
	nextEmoji := emojiOr(emojis["NEXT"], "▶️")
	closeEmoji := emojiOr(emojis["CLOSE"], "✕")
	return []discordgo.MessageComponent{
		&discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: "help:select:" + ownerID, Placeholder: "Choose a category", Options: helpSelectOptions(category)}}},
		&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			&discordgo.Button{Style: discordgo.SecondaryButton, Label: "Previous", CustomID: fmt.Sprintf("help:page:%s:%d", ownerID, previous), Emoji: previousEmoji},
			&discordgo.Button{Style: discordgo.SecondaryButton, Label: "Next", CustomID: fmt.Sprintf("help:page:%s:%d", ownerID, next), Emoji: nextEmoji},
			&discordgo.Button{Style: discordgo.DangerButton, Label: "Close", CustomID: "help:close:" + ownerID, Emoji: closeEmoji},
		}},
	}
}

func helpSelectOptions(selected int) []discordgo.SelectMenuOption {
	result := make([]discordgo.SelectMenuOption, 0, len(helpCategories))
	for index, category := range helpCategories {
		result = append(result, discordgo.SelectMenuOption{Label: category.Name, Value: fmt.Sprintf("%d", index), Description: category.Summary, Default: index == selected})
	}
	return result
}

func componentEmoji(raw string) *discordgo.ComponentEmoji {
	if raw == "" {
		return nil
	}
	if match := regexp.MustCompile(`^<(a?):([^:>]+):(\d+)>$`).FindStringSubmatch(raw); len(match) == 4 {
		return &discordgo.ComponentEmoji{Name: match[2], ID: match[3], Animated: match[1] == "a"}
	}
	if strings.HasPrefix(raw, ":") && strings.HasSuffix(raw, ":") {
		return nil
	}
	return &discordgo.ComponentEmoji{Name: raw}
}

func emojiOr(raw, fallback string) *discordgo.ComponentEmoji {
	if emoji := componentEmoji(raw); emoji != nil {
		return emoji
	}
	return &discordgo.ComponentEmoji{Name: fallback}
}

func (b *Bot) helpAllowed(userID string) bool {
	b.helpMu.Lock()
	defer b.helpMu.Unlock()
	session, ok := b.helpSessions[userID]
	return ok && session.OwnerID == userID && time.Now().Before(session.Expires)
}
