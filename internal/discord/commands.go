package discord

import (
	"strconv"

	"github.com/PettoBot/vanity-tag-bot/internal/logs"
	"github.com/bwmarrin/discordgo"
)

func CommandCatalog() []*discordgo.ApplicationCommand {
	admin := int64(discordgo.PermissionManageGuild | discordgo.PermissionManageRoles)
	adminPtr := &admin
	settingsAdmin := int64(discordgo.PermissionManageGuild)
	settingsAdminPtr := &settingsAdmin
	return []*discordgo.ApplicationCommand{
		{Name: "cmds", Description: "Open the private Vanity Tag Bot help panel", Options: []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionString, Name: "command", Description: "Search for a command", Required: false, Autocomplete: false, MaxLength: 64}}},
		{Name: "setup", Description: "Open the guided server setup", DefaultMemberPermissions: adminPtr},
		{Name: "config", Description: "View or reset this server's setup", DefaultMemberPermissions: adminPtr, Options: configOptions()},
		{Name: "set", Description: "Customize this server's bot profile", DefaultMemberPermissions: settingsAdminPtr, Options: setOptions()},
		{Name: "vanity", Description: "Manage Custom Status and profile rules", DefaultMemberPermissions: adminPtr, Options: vanityOptions()},
		{Name: "guildtag", Description: "Manage Discord Server Tag rules", DefaultMemberPermissions: adminPtr, Options: guildTagOptions()},
		{Name: "identity", Description: "Inspect or synchronize Discord identity data", DefaultMemberPermissions: adminPtr, Options: identityOptions()},
		{Name: "logs", Description: "Configure role-action audit logs", DefaultMemberPermissions: adminPtr, Options: logsOptions()},
		{Name: "embed", Description: "Create and edit notification embeds", DefaultMemberPermissions: adminPtr, Options: embedOptions()},
	}
}

func configOptions() []*discordgo.ApplicationCommandOption {
	return []*discordgo.ApplicationCommandOption{
		sub("view", "View active configuration", nil),
		sub("reset", "Reset server configuration", nil),
	}
}

func setOptions() []*discordgo.ApplicationCommandOption {
	return []*discordgo.ApplicationCommandOption{
		sub("view", "View this server's bot profile", nil),
		sub("nickname", "Set or clear the bot nickname", []*discordgo.ApplicationCommandOption{stringOption("value", "Nickname", false, 0, 32), boolOption("clear", "Use the global nickname", false)}),
		sub("avatar", "Set avatar from a Discord upload or HTTPS URL", []*discordgo.ApplicationCommandOption{
			stringOption("url", "HTTPS PNG, JPEG, or GIF URL", false, 0, 2048),
			{Type: discordgo.ApplicationCommandOptionAttachment, Name: "file", Description: "PNG, JPEG, or GIF upload", Required: false},
			boolOption("clear", "Use the global avatar", false),
		}),
		sub("banner", "Set banner from a Discord upload or HTTPS URL", []*discordgo.ApplicationCommandOption{
			stringOption("url", "HTTPS PNG, JPEG, or GIF URL", false, 0, 2048),
			{Type: discordgo.ApplicationCommandOptionAttachment, Name: "file", Description: "PNG, JPEG, or GIF upload", Required: false},
			boolOption("clear", "Use the global banner", false),
		}),
		sub("bio", "Set or clear the bot bio", []*discordgo.ApplicationCommandOption{stringOption("value", "Bio text", false, 0, 190), boolOption("clear", "Use the global bio", false)}),
		sub("reset", "Revert all profile fields to the global bot profile", nil),
	}
}

func vanityOptions() []*discordgo.ApplicationCommandOption {
	return []*discordgo.ApplicationCommandOption{
		sub("add", "Create a Vanity rule", []*discordgo.ApplicationCommandOption{
			stringOption("name", "Unique rule name", true, 1, 64),
			stringOption("word", "Word or bounded regular expression", true, 1, 256),
			vanitySourceOption(true),
			vanityComparisonOption(true),
			roleOption("role", "Role to manage", true),
			roleActionOption(true),
			boolOption("case_fold", "Lowercase without deleting Unicode", false),
			boolOption("trim_space", "Trim surrounding whitespace", false),
			boolOption("collapse_space", "Collapse Unicode whitespace", false),
		}),
		sub("edit", "Edit a Vanity rule", []*discordgo.ApplicationCommandOption{stringOption("name", "Rule name", true, 1, 64), stringOption("word", "New text or pattern", false, 0, 256), vanitySourceOption(false), vanityComparisonOption(false), roleOption("role", "New role", false), boolOption("enabled", "Enable rule", false)}),
		sub("remove", "Soft-delete a Vanity rule", []*discordgo.ApplicationCommandOption{stringOption("name", "Rule name", true, 1, 64)}),
		sub("list", "List Vanity rules", nil),
		sub("test", "Dry-run Vanity rules for a member", []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Member to evaluate", Required: false}}),
		sub("sync", "Synchronize Vanity rules with a bounded queue", []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Only this member", Required: false}}),
		sub("notify", "Configure the Vanity message sent to matched members", notifyOptions()),
	}
}

func guildTagOptions() []*discordgo.ApplicationCommandOption {
	return []*discordgo.ApplicationCommandOption{
		sub("add", "Create a Server Tag rule", []*discordgo.ApplicationCommandOption{stringOption("name", "Unique rule name", true, 1, 64), guildTagConditionOption(true), roleOption("role", "Role to manage", true), roleActionOption(true), stringOption("value", "Guild ID or visible tag text", false, 0, 32)}),
		sub("edit", "Edit a Server Tag rule", []*discordgo.ApplicationCommandOption{stringOption("name", "Rule name", true, 1, 64), guildTagConditionOption(false), stringOption("value", "New guild ID or tag text", false, 0, 32), roleOption("role", "New role", false), boolOption("enabled", "Enable rule", false)}),
		sub("remove", "Soft-delete a Server Tag rule", []*discordgo.ApplicationCommandOption{stringOption("name", "Rule name", true, 1, 64)}),
		sub("list", "List Server Tag rules", nil),
		sub("test", "Dry-run Server Tag rules for a member", []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Member to evaluate", Required: false}}),
		sub("sync", "Synchronize Server Tag rules with a bounded queue", []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Only this member", Required: false}}),
		sub("notify", "Configure the Guild Tag message sent to matched members", notifyOptions()),
	}
}

func identityOptions() []*discordgo.ApplicationCommandOption {
	return []*discordgo.ApplicationCommandOption{
		sub("status", "Show the current user's primary guild data", nil),
		sub("sync", "Synchronize one member or a bounded guild batch", []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Only this member", Required: false}}),
		sub("audit", "Show recent identity audit status", nil),
	}
}

func logsOptions() []*discordgo.ApplicationCommandOption {
	return []*discordgo.ApplicationCommandOption{
		sub("setup", "Set the current channel as the log channel", nil),
		sub("set", "Set or clear log events", []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Log channel", Required: true}, stringOption("events", "Comma-separated event names", true, 1, 512)}),
		sub("view", "View log configuration", nil),
		sub("test", "Send a safe action-log test embed", []*discordgo.ApplicationCommandOption{choiceOption("event", "Action-log event to test", false, logs.EventKeys()...)}),
	}
}

func notifyOptions() []*discordgo.ApplicationCommandOption {
	return []*discordgo.ApplicationCommandOption{
		{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Channel for the user notification", Required: false},
		stringOption("embed", "Embed name, or default", false, 0, 64),
		namedChoiceOption("ping", "Mention the matched member", false, namedChoice{"Mention member", "user"}, namedChoice{"Do not mention", "none"}),
	}
}

func embedOptions() []*discordgo.ApplicationCommandOption {
	return []*discordgo.ApplicationCommandOption{
		sub("create", "Create an embed to reuse in notifications", []*discordgo.ApplicationCommandOption{stringOption("name", "Embed name", true, 1, 64), stringOption("title", "Optional title", false, 0, 256), stringOption("description", "Optional description", false, 0, 4096), stringOption("color", "Hex color, for example #5865F2", false, 0, 16)}),
		sub("edit", "Open the visual editor for an embed", []*discordgo.ApplicationCommandOption{stringOption("name", "Embed name", true, 1, 64), stringOption("title", "Quick title change", false, 0, 256), stringOption("description", "Quick description change", false, 0, 4096)}),
		sub("delete", "Soft-delete a notification embed", []*discordgo.ApplicationCommandOption{stringOption("name", "Embed name", true, 1, 64)}),
		sub("list", "List notification embeds", nil),
		sub("preview", "Preview a notification embed with safe variables", []*discordgo.ApplicationCommandOption{stringOption("name", "Embed name", true, 1, 64)}),
		sub("send", "Send a notification embed to this channel", []*discordgo.ApplicationCommandOption{stringOption("name", "Embed name", true, 1, 64)}),
		sub("variables", "List safe template variables", nil),
	}
}

func sub(name, description string, options []*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionSubCommand, Name: name, Description: description, Options: options}
}

func group(name, description string, options []*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionSubCommandGroup, Name: name, Description: description, Options: options}
}

func stringOption(name, description string, required bool, min, max int) *discordgo.ApplicationCommandOption {
	option := &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionString, Name: name, Description: description, Required: required, MaxLength: max}
	if min > 0 {
		option.MinLength = &min
	}
	return option
}

func boolOption(name, description string, required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionBoolean, Name: name, Description: description, Required: required}
}

func roleOption(name, description string, required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionRole, Name: name, Description: description, Required: required}
}

type namedChoice struct {
	Name  string
	Value string
}

func choiceOption(name, description string, required bool, values ...string) *discordgo.ApplicationCommandOption {
	choices := make([]namedChoice, 0, len(values))
	for _, value := range values {
		choices = append(choices, namedChoice{Name: value, Value: value})
	}
	return namedChoiceOption(name, description, required, choices...)
}

func namedChoiceOption(name, description string, required bool, values ...namedChoice) *discordgo.ApplicationCommandOption {
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(values))
	for _, value := range values {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: value.Name, Value: value.Value})
	}
	return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionString, Name: name, Description: description, Required: required, Choices: choices}
}

func vanitySourceOption(required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionString,
		Name:        "source",
		Description: "What Discord value should trigger the rule",
		Required:    required,
		Choices: []*discordgo.ApplicationCommandOptionChoice{
			{Name: "Custom Status (profile text)", Value: "custom_status"},
			{Name: "Username", Value: "username"},
			{Name: "Global name", Value: "global_name"},
			{Name: "Server nickname", Value: "guild_nickname"},
			{Name: "Display name", Value: "display_name"},
		},
	}
}

func vanityComparisonOption(required bool) *discordgo.ApplicationCommandOption {
	return namedChoiceOption("comparison", "How the text is matched", required,
		namedChoice{"Exact match", "equals"},
		namedChoice{"Contains text", "contains"},
		namedChoice{"Starts with", "starts_with"},
		namedChoice{"Ends with", "ends_with"},
		namedChoice{"Regular expression", "regex"},
	)
}

func roleActionOption(required bool) *discordgo.ApplicationCommandOption {
	return namedChoiceOption("action", "What happens when it matches", required,
		namedChoice{"Add role", "add_role"},
		namedChoice{"Remove role", "remove_role"},
	)
}

func guildTagConditionOption(required bool) *discordgo.ApplicationCommandOption {
	return namedChoiceOption("condition", "What Server Tag data should match", required,
		namedChoice{"Identity guild is", "is_guild_id"},
		namedChoice{"Identity guild is not", "is_not_guild_id"},
		namedChoice{"Server Tag is enabled", "identity_enabled"},
		namedChoice{"Server Tag is disabled", "identity_disabled"},
		namedChoice{"Visible tag equals", "tag_equals"},
		namedChoice{"Visible tag is not", "tag_not_equals"},
	)
}

func commandPermissionText() string {
	return strconv.FormatInt(discordgo.PermissionManageGuild|discordgo.PermissionManageRoles, 10)
}
