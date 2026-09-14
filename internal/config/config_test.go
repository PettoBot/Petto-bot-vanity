package config

import (
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		DiscordToken: "new-bot-token", ApplicationID: "123", DatabaseURL: "postgres://identity_bot:secret@postgres.private/vanity_tag_bot",
		ShardCount: 1, ReconcileInterval: time.Minute, ReconcileMaxUsers: 1, ReconcileConcurrency: 1, ManualSyncMaxUsers: 1, ManualSyncConcurrency: 1, GuildQueueSize: 1, RequestTimeout: time.Second,
		AssetMaxBytes: 1024, AssetMaxPixels: 1,
	}
}

func TestValidateRejectsSharedOrPettoCredentials(t *testing.T) {
	c := validConfig()
	c.DatabaseURL = "postgres://petto:secret@postgres.private/data-petto"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted a Petto database URL")
	}
	c = validConfig()
	c.DiscordToken = "petto-token"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted a Petto Discord token")
	}
}

func TestValidateAcceptsPrivateBotPlaceholders(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsInvalidManualSyncSettings(t *testing.T) {
	c := validConfig()
	c.ManualSyncMaxUsers = 0
	if err := c.Validate(); err == nil {
		t.Fatal("accepted zero MANUAL_SYNC_MAX_USERS")
	}
	c = validConfig()
	c.ManualSyncConcurrency = 0
	if err := c.Validate(); err == nil {
		t.Fatal("accepted zero MANUAL_SYNC_CONCURRENCY")
	}
}
