package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestCommandCatalogIsSlashOnlyAndComplete(t *testing.T) {
	commands := CommandCatalog()
	want := map[string]bool{"cmds": true, "setup": true, "config": true, "set": true, "vanity": true, "guildtag": true, "identity": true, "logs": true, "embed": true}
	for _, command := range commands {
		if !want[command.Name] {
			t.Fatalf("unexpected public command %q", command.Name)
		}
		delete(want, command.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing commands: %#v", want)
	}
}

func TestRequiredCommandOptionsComeBeforeOptionalOptions(t *testing.T) {
	var check func(string, []*discordgo.ApplicationCommandOption)
	check = func(path string, options []*discordgo.ApplicationCommandOption) {
		optionalSeen := false
		for _, option := range options {
			if !option.Required {
				optionalSeen = true
			}
			if option.Required && optionalSeen {
				t.Errorf("%s: required option %q appears after an optional option", path, option.Name)
			}
			check(path+" "+option.Name, option.Options)
		}
	}
	for _, command := range CommandCatalog() {
		check("/"+command.Name, command.Options)
	}
}

func TestSetOwnsProfileCommandsAndAcceptsUploads(t *testing.T) {
	var set *discordgo.ApplicationCommand
	var config *discordgo.ApplicationCommand
	for _, command := range CommandCatalog() {
		switch command.Name {
		case "set":
			set = command
		case "config":
			config = command
		}
	}
	if set == nil || config == nil {
		t.Fatal("set or config command is missing")
	}
	for _, option := range config.Options {
		if option.Name == "profile" {
			t.Fatal("profile commands should not be exposed under /config")
		}
	}
	var checkProfileOptions func([]*discordgo.ApplicationCommandOption)
	checkProfileOptions = func(options []*discordgo.ApplicationCommandOption) {
		for _, option := range options {
			if option.Name == "avatar" || option.Name == "banner" {
				foundAttachment := false
				for _, child := range option.Options {
					if child.Name == "file" && child.Type == discordgo.ApplicationCommandOptionAttachment {
						foundAttachment = true
					}
				}
				if !foundAttachment {
					t.Fatalf("/set %s has no upload option", option.Name)
				}
			}
			checkProfileOptions(option.Options)
		}
	}
	checkProfileOptions(set.Options)
}

func TestOptionStringReadsEntityIDsWithoutPanicking(t *testing.T) {
	options := map[string]*discordgo.ApplicationCommandInteractionDataOption{
		"role":    {Name: "role", Type: discordgo.ApplicationCommandOptionRole, Value: "123456789"},
		"channel": {Name: "channel", Type: discordgo.ApplicationCommandOptionChannel, Value: "987654321"},
		"text":    {Name: "text", Type: discordgo.ApplicationCommandOptionString, Value: "hello"},
	}
	if got := optionString(options, "role"); got != "123456789" {
		t.Fatalf("role option = %q, want role ID", got)
	}
	if got := optionString(options, "channel"); got != "987654321" {
		t.Fatalf("channel option = %q, want channel ID", got)
	}
	if got := optionString(options, "text"); got != "hello" {
		t.Fatalf("string option = %q, want hello", got)
	}
}

func TestNotifyCommandsOwnChannelEmbedAndPingOptions(t *testing.T) {
	for _, command := range CommandCatalog() {
		if command.Name != "vanity" && command.Name != "guildtag" {
			continue
		}
		var notify *discordgo.ApplicationCommandOption
		for _, option := range command.Options {
			if option.Name == "notify" {
				notify = option
			}
		}
		if notify == nil {
			t.Fatalf("/%s notify is missing", command.Name)
		}
		got := map[string]bool{}
		for _, option := range notify.Options {
			got[option.Name] = true
		}
		for _, name := range []string{"channel", "embed", "ping"} {
			if !got[name] {
				t.Fatalf("/%s notify is missing %s option", command.Name, name)
			}
		}
	}
	for _, option := range logsOptions() {
		if option.Name == "template" {
			t.Fatal("/logs must not expose notification template configuration")
		}
	}
}
