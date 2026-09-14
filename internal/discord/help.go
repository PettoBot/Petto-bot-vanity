package discord

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/identity"
	"github.com/bwmarrin/discordgo"
)

const helpSessionLifetime = 10 * time.Minute
const helpColor = 0x4B4F59

type helpCategory struct {
	Name, Summary, Emoji string
	Commands             []string
}

type helpCommand struct {
	Name        string
	Description string
	Usage       string
	Example     string
	Permission  string
	Category    int
}

var helpCategories = []helpCategory{
	{Name: "Setup", Emoji: "⚙️", Summary: "Guided setup, server configuration, and this server's Petto profile.", Commands: []string{"setup", "config", "set"}},
	{Name: "Identity", Emoji: "🪪", Summary: "Inspect and reconcile a member's identity data.", Commands: []string{"identity"}},
	{Name: "Vanity", Emoji: "✨", Summary: "Manage Custom Status and profile-text role rules.", Commands: []string{"vanity"}},
	{Name: "Server Tags", Emoji: "🏷️", Summary: "Manage Discord primary guild and visible Server Tag role rules.", Commands: []string{"guildtag"}},
	{Name: "Logs", Emoji: "📋", Summary: "Petto-style audit cards for role changes and errors.", Commands: []string{"logs"}},
	{Name: "Embeds", Emoji: "🖼️", Summary: "Create safe reusable notification embeds and previews.", Commands: []string{"embed"}},
	{Name: "Utility", Emoji: "🔎", Summary: "Command lookup and bounded synchronization helpers.", Commands: []string{"cmds"}},
}

func (b *Bot) helpPanel(ownerID, query string, category int) (*discordgo.InteractionResponseData, error) {
	category = normalizeHelpCategory(category)
	commands := buildHelpCommands()
	view := helpCategoryView(commands, category, time.Now().Add(helpSessionLifetime))

	query = normalizeHelpQuery(query)
	if query != "" {
		matches := searchHelpCommands(commands, query)
		if matchedCategory := helpCategoryForTopLevel(query); matchedCategory >= 0 && len(matches) != 1 {
			category = matchedCategory
			view = helpCategoryView(commands, category, time.Now().Add(helpSessionLifetime))
		} else if len(matches) == 1 {
			category = matches[0].Category
			view = helpCommandView(matches[0], time.Now().Add(helpSessionLifetime))
		} else if len(matches) > 1 {
			category = matches[0].Category
			view = helpSearchView(query, matches, time.Now().Add(helpSessionLifetime))
		} else {
			view = helpNotFoundView(query, time.Now().Add(helpSessionLifetime))
		}
	}

	sessionID, expires := b.createHelpSession(ownerID, category)
	view.Footer = &discordgo.MessageEmbedFooter{Text: "Petto help panel · expires " + expires.Format("15:04:05 MST")}
	return &discordgo.InteractionResponseData{
		Embeds:     []*discordgo.MessageEmbed{view},
		Components: helpComponents(sessionID, category, b.config.Emojis),
	}, nil
}

func (b *Bot) helpPanelForSession(sessionID string, category int) (*discordgo.InteractionResponseData, error) {
	category = normalizeHelpCategory(category)
	b.helpMu.Lock()
	session, ok := b.helpSessions[sessionID]
	if ok && time.Now().Before(session.Expires) {
		session.Category = category
		b.helpSessions[sessionID] = session
	}
	b.helpMu.Unlock()
	if !ok || !time.Now().Before(session.Expires) {
		return nil, fmt.Errorf("this help panel has expired; run `/cmds` again")
	}
	embed := helpCategoryView(buildHelpCommands(), category, session.Expires)
	embed.Footer = &discordgo.MessageEmbedFooter{Text: "Petto help panel · expires " + session.Expires.Format("15:04:05 MST")}
	return &discordgo.InteractionResponseData{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: helpComponents(sessionID, category, b.config.Emojis),
	}, nil
}

func (b *Bot) createHelpSession(ownerID string, category int) (string, time.Time) {
	sessionID := identity.NewID()
	if len(sessionID) > 16 {
		sessionID = sessionID[:16]
	}
	expires := time.Now().Add(helpSessionLifetime)
	b.helpMu.Lock()
	for id, session := range b.helpSessions {
		if time.Now().After(session.Expires) {
			delete(b.helpSessions, id)
		}
	}
	b.helpSessions[sessionID] = helpSession{OwnerID: ownerID, Category: category, Expires: expires}
	b.helpMu.Unlock()
	return sessionID, expires
}

func (b *Bot) helpAllowed(sessionID, userID string) (helpSession, bool) {
	b.helpMu.Lock()
	defer b.helpMu.Unlock()
	session, ok := b.helpSessions[sessionID]
	if !ok {
		return helpSession{}, false
	}
	if time.Now().After(session.Expires) {
		delete(b.helpSessions, sessionID)
		return helpSession{}, false
	}
	return session, session.OwnerID == userID && userID != ""
}

func (b *Bot) closeHelpSession(sessionID string) {
	b.helpMu.Lock()
	delete(b.helpSessions, sessionID)
	b.helpMu.Unlock()
}

func helpComponents(sessionID string, category int, emojis map[string]string) []discordgo.MessageComponent {
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
		&discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: "help:select:" + sessionID, Placeholder: "Select a command category", Options: helpSelectOptions(category)}}},
		&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			&discordgo.Button{Style: discordgo.SecondaryButton, Label: "Previous", CustomID: fmt.Sprintf("help:page:%s:%d", sessionID, previous), Emoji: previousEmoji},
			&discordgo.Button{Style: discordgo.SecondaryButton, Label: "Next", CustomID: fmt.Sprintf("help:page:%s:%d", sessionID, next), Emoji: nextEmoji},
			&discordgo.Button{Style: discordgo.DangerButton, Label: "Close", CustomID: "help:close:" + sessionID, Emoji: closeEmoji},
		}},
	}
}

func helpSelectOptions(selected int) []discordgo.SelectMenuOption {
	result := make([]discordgo.SelectMenuOption, 0, len(helpCategories))
	for index, category := range helpCategories {
		result = append(result, discordgo.SelectMenuOption{
			Label:       category.Name,
			Value:       fmt.Sprintf("%d", index),
			Description: truncateHelpText(category.Summary, 100),
			Default:     index == selected,
			Emoji:       &discordgo.ComponentEmoji{Name: category.Emoji},
		})
	}
	return result
}

func helpCategoryView(commands []helpCommand, category int, _ time.Time) *discordgo.MessageEmbed {
	category = normalizeHelpCategory(category)
	meta := helpCategories[category]
	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("%s Petto Vanity · %s", meta.Emoji, meta.Name),
		Description: meta.Summary + "\n\nSelect a category below, or search directly with `/cmds command:<command>`.",
		Color:       helpColor,
	}
	for _, command := range commands {
		if command.Category != category {
			continue
		}
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  command.Name,
			Value: truncateHelpText(fmt.Sprintf("%s\n**Example:** `%s`\n**Permission:** %s", command.Description, command.Example, command.Permission), 1024),
		})
	}
	return embed
}

func helpCommandView(command helpCommand, _ time.Time) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🔎 Command: " + command.Name,
		Description: command.Description,
		Color:       helpColor,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Usage", Value: "`" + command.Usage + "`"},
			{Name: "Example", Value: "`" + command.Example + "`"},
			{Name: "Required permission", Value: command.Permission},
			{Name: "Category", Value: helpCategories[command.Category].Name},
		},
	}
}

func helpSearchView(query string, matches []helpCommand, _ time.Time) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Title:       "🔎 Petto Vanity · Search results",
		Description: fmt.Sprintf("I found %d commands matching `%s`. Search a full command path to open its detail card.", len(matches), query),
		Color:       helpColor,
	}
	limit := len(matches)
	if limit > 15 {
		limit = 15
	}
	for _, command := range matches[:limit] {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: command.Name, Value: truncateHelpText(command.Description, 1024)})
	}
	return embed
}

func helpNotFoundView(query string, _ time.Time) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🔎 Petto Vanity · No match",
		Description: fmt.Sprintf("No command matched `%s`. Try a full path such as `/vanity add`, `/guildtag sync`, or choose a category below.", query),
		Color:       helpColor,
	}
}

func buildHelpCommands() []helpCommand {
	catalog := CommandCatalog()
	result := make([]helpCommand, 0, 48)
	for _, command := range catalog {
		category := helpCategoryForCommand(command.Name)
		if category < 0 {
			continue
		}
		leaves := flattenHelpOptions(command.Options, nil)
		if len(leaves) == 0 {
			name := "/" + command.Name
			usage, example := helpSyntax(name, command.Options)
			result = append(result, helpCommand{
				Name: name, Description: command.Description,
				Usage: usage, Example: helpExample(name, example),
				Permission: helpPermission(command.DefaultMemberPermissions), Category: category,
			})
			continue
		}
		for _, leaf := range leaves {
			name := "/" + command.Name + " " + strings.Join(leaf.path, " ")
			usage, example := helpSyntax(name, leaf.options)
			result = append(result, helpCommand{
				Name: name, Description: nonEmpty(leaf.description, command.Description),
				Usage: usage, Example: helpExample(name, example),
				Permission: helpPermission(command.DefaultMemberPermissions), Category: category,
			})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Category != result[j].Category {
			return result[i].Category < result[j].Category
		}
		return result[i].Name < result[j].Name
	})
	return result
}

type helpLeaf struct {
	path        []string
	description string
	options     []*discordgo.ApplicationCommandOption
}

func flattenHelpOptions(options []*discordgo.ApplicationCommandOption, prefix []string) []helpLeaf {
	result := make([]helpLeaf, 0)
	for _, option := range options {
		if option == nil {
			continue
		}
		switch option.Type {
		case discordgo.ApplicationCommandOptionSubCommand:
			path := append(append([]string{}, prefix...), option.Name)
			result = append(result, helpLeaf{path: path, description: option.Description, options: option.Options})
		case discordgo.ApplicationCommandOptionSubCommandGroup:
			path := append(append([]string{}, prefix...), option.Name)
			result = append(result, flattenHelpOptions(option.Options, path)...)
		}
	}
	return result
}

func helpSyntax(name string, options []*discordgo.ApplicationCommandOption) (string, string) {
	usage := name
	example := name
	for _, option := range options {
		if option == nil {
			continue
		}
		if option.Required {
			usage += " <" + option.Name + ">"
			example += " " + option.Name + ":" + helpOptionExample(option)
		} else {
			usage += " [" + option.Name + "]"
		}
	}
	return usage, example
}

func helpExample(name, fallback string) string {
	overrides := map[string]string{
		"/cmds":            "/cmds command:vanity add",
		"/set nickname":    "/set nickname value:Petto",
		"/set avatar":      "/set avatar file:image.png",
		"/set banner":      "/set banner file:banner.png",
		"/set bio":         "/set bio value:Vanity & Server Tags",
		"/vanity notify":   "/vanity notify channel:#vanity-notify embed:default ping:user",
		"/guildtag notify": "/guildtag notify channel:#tag-notify embed:default ping:user",
	}
	if example, ok := overrides[name]; ok {
		return example
	}
	return fallback
}

func helpOptionExample(option *discordgo.ApplicationCommandOption) string {
	if len(option.Choices) > 0 {
		return fmt.Sprint(option.Choices[0].Value)
	}
	switch option.Type {
	case discordgo.ApplicationCommandOptionUser:
		return "@member"
	case discordgo.ApplicationCommandOptionRole:
		return "@role"
	case discordgo.ApplicationCommandOptionChannel:
		return "#channel"
	case discordgo.ApplicationCommandOptionAttachment:
		return "image.png"
	case discordgo.ApplicationCommandOptionBoolean:
		return "true"
	case discordgo.ApplicationCommandOptionInteger, discordgo.ApplicationCommandOptionNumber:
		return "1"
	}
	switch option.Name {
	case "name":
		return "example"
	case "word", "value":
		return "petto"
	case "url":
		return "https://cdn.discordapp.com/image.png"
	case "events":
		return "tag_add"
	default:
		return option.Name
	}
}

func helpPermission(value *int64) string {
	if value == nil {
		return "Everyone"
	}
	permissions := *value
	labels := make([]string, 0, 2)
	if permissions&discordgo.PermissionManageGuild != 0 {
		labels = append(labels, "Manage Server")
	}
	if permissions&discordgo.PermissionManageRoles != 0 {
		labels = append(labels, "Manage Roles")
	}
	if len(labels) == 0 {
		return "Server permissions required"
	}
	return strings.Join(labels, " + ")
}

func searchHelpCommands(commands []helpCommand, query string) []helpCommand {
	query = normalizeHelpQuery(query)
	if query == "" {
		return nil
	}
	exact := make([]helpCommand, 0, 1)
	partial := make([]helpCommand, 0)
	for _, command := range commands {
		name := normalizeHelpQuery(command.Name)
		if name == query {
			exact = append(exact, command)
			continue
		}
		if strings.Contains(name, query) || strings.Contains(strings.ToLower(command.Description), query) {
			partial = append(partial, command)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}

func normalizeHelpQuery(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "/")
	return strings.Join(strings.Fields(value), " ")
}

func helpCategoryForTopLevel(query string) int {
	query = normalizeHelpQuery(query)
	if strings.Contains(query, " ") {
		return -1
	}
	return helpCategoryForCommand(query)
}

func helpCategoryForCommand(command string) int {
	for index, category := range helpCategories {
		for _, name := range category.Commands {
			if command == name {
				return index
			}
		}
	}
	return -1
}

func normalizeHelpCategory(category int) int {
	if category < 0 {
		return len(helpCategories) - 1
	}
	if category >= len(helpCategories) {
		return 0
	}
	return category
}

func truncateHelpText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
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
