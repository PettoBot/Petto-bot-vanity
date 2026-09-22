package discord

import (
	"testing"
	"time"

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

func TestHelpCatalogIncludesDescriptionsExamplesAndPermissions(t *testing.T) {
	commands := buildHelpCommands()
	var vanityAdd *helpCommand
	for index := range commands {
		if commands[index].Name == "/vanity add" {
			vanityAdd = &commands[index]
			break
		}
	}
	if vanityAdd == nil {
		t.Fatal("/vanity add missing from help catalog")
	}
	if vanityAdd.Description == "" || vanityAdd.Example == "" || vanityAdd.Permission == "" {
		t.Fatalf("incomplete help metadata: %#v", *vanityAdd)
	}
	if vanityAdd.Permission != "Manage Server + Manage Roles" {
		t.Fatalf("unexpected permission text: %q", vanityAdd.Permission)
	}
	var cmds *helpCommand
	for index := range commands {
		if commands[index].Name == "/cmds" {
			cmds = &commands[index]
			break
		}
	}
	if cmds == nil || cmds.Usage != "/cmds [command]" || cmds.Example != "/cmds command:vanity add" {
		t.Fatalf("/cmds help metadata is incomplete: %#v", cmds)
	}
	matches := searchHelpCommands(commands, "vanity add")
	if len(matches) != 1 || matches[0].Name != "/vanity add" {
		t.Fatalf("concrete command search failed: %#v", matches)
	}
	if matches := searchHelpCommands(commands, "definitely-not-a-command"); len(matches) != 0 {
		t.Fatalf("unknown command unexpectedly matched: %#v", matches)
	}
}

func TestActorIDSupportsDMInteractions(t *testing.T) {
	event := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{User: &discordgo.User{ID: "dm-user"}}}
	if got := actorID(event); got != "dm-user" {
		t.Fatalf("actorID in DM = %q, want dm-user", got)
	}
}

func TestHelpSessionExpiresAndCloseDeletesIt(t *testing.T) {
	bot := &Bot{helpSessions: make(map[string]helpSession)}
	id, _ := bot.createHelpSession("owner", 0)
	if _, ok := bot.helpAllowed(id, "owner"); !ok {
		t.Fatal("fresh help session was rejected")
	}
	if _, ok := bot.helpAllowed(id, "other"); ok {
		t.Fatal("help session accepted a different user")
	}
	bot.closeHelpSession(id)
	if _, ok := bot.helpAllowed(id, "owner"); ok {
		t.Fatal("closed help session still exists")
	}

	id, _ = bot.createHelpSession("owner", 0)
	bot.helpMu.Lock()
	session := bot.helpSessions[id]
	session.Expires = time.Now().Add(-time.Second)
	bot.helpSessions[id] = session
	bot.helpMu.Unlock()
	if _, ok := bot.helpAllowed(id, "owner"); ok {
		t.Fatal("expired help session was accepted")
	}
}

func TestBoundedMemberLimit(t *testing.T) {
	cases := map[int]int{-10: 1, 0: 1, 1: 1, 999: 999, 1000: 1000, 2500: 2500, 10000: 10000, 25000: 10000}
	for input, want := range cases {
		if got := boundedMemberLimit(input); got != want {
			t.Fatalf("boundedMemberLimit(%d)=%d want %d", input, got, want)
		}
	}
}

func TestHelpSessionUpdateKeepsEphemeralFlagOutOfUpdatePayload(t *testing.T) {
	bot := &Bot{helpSessions: make(map[string]helpSession)}
	id, _ := bot.createHelpSession("owner", 0)
	data, err := bot.helpPanelForSession(id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if data.Flags != 0 {
		t.Fatalf("message update unexpectedly changed flags: %v", data.Flags)
	}
}

func TestLogsSetupAcceptsOptionalChannel(t *testing.T) {
	var setup *discordgo.ApplicationCommandOption
	for _, option := range logsOptions() {
		if option.Name == "setup" {
			setup = option
			break
		}
	}
	if setup == nil {
		t.Fatal("/logs setup is missing")
	}
	for _, option := range setup.Options {
		if option.Name == "channel" {
			if option.Type != discordgo.ApplicationCommandOptionChannel {
				t.Fatalf("/logs setup channel type = %v", option.Type)
			}
			if option.Required {
				t.Fatal("/logs setup channel should remain optional so current channel is still supported")
			}
			return
		}
	}
	t.Fatal("/logs setup is missing channel option")
}
