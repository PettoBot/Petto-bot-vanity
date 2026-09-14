package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains only configuration for Vanity Tag Bot. It deliberately does
// not know anything about Petto's runtime, credentials, or storage.
type Config struct {
	DiscordToken          string
	ApplicationID         string
	OwnerID               string
	DatabaseURL           string
	DeveloperIDs          map[string]struct{}
	MigrationsDir         string
	HTTPAddr              string
	ShardID               int
	ShardCount            int
	PresenceText          string
	EnableReconcile       bool
	ReconcileInterval     time.Duration
	ReconcileMaxUsers     int
	ReconcileConcurrency  int
	ManualSyncMaxUsers    int
	ManualSyncConcurrency int
	GuildQueueSize        int
	RequestTimeout        time.Duration
	AssetMaxBytes         int64
	AssetMaxPixels        int64
	AllowedAssetHosts     map[string]struct{}
	WebsiteURL            string
	DashboardURL          string
	DocsURL               string
	SupportURL            string
	Emojis                map[string]string
}

func Load() (Config, error) {
	c := Config{
		DiscordToken:      strings.TrimSpace(os.Getenv("DISCORD_TOKEN")),
		ApplicationID:     strings.TrimSpace(os.Getenv("DISCORD_APPLICATION_ID")),
		OwnerID:           strings.TrimSpace(os.Getenv("DISCORD_OWNER_ID")),
		DatabaseURL:       strings.TrimSpace(os.Getenv("DATABASE_URL")),
		MigrationsDir:     envOr("MIGRATIONS_DIR", "migrations"),
		HTTPAddr:          envOr("HTTP_ADDR", ":8080"),
		PresenceText:      envOr("PRESENCE_TEXT", "Vanity & Guild Tags"),
		WebsiteURL:        strings.TrimSpace(os.Getenv("WEBSITE_URL")),
		DashboardURL:      strings.TrimSpace(os.Getenv("DASHBOARD_URL")),
		DocsURL:           strings.TrimSpace(os.Getenv("DOCS_URL")),
		SupportURL:        strings.TrimSpace(os.Getenv("SUPPORT_URL")),
		Emojis:            loadEmojis(),
		DeveloperIDs:      splitSet(os.Getenv("DEVELOPER_IDS")),
		AllowedAssetHosts: splitSet(os.Getenv("ASSET_ALLOWED_HOSTS")),
	}

	var err error
	if c.EnableReconcile, err = boolEnv("ENABLE_MEMBER_RECONCILIATION", false); err != nil {
		return Config{}, err
	}
	c.ShardCount, err = intEnv("SHARD_COUNT", 1)
	if err != nil {
		return Config{}, err
	}
	c.ShardID, err = intEnv("SHARD_ID", 0)
	if err != nil {
		return Config{}, err
	}
	minutes, err := intEnv("RECONCILIATION_INTERVAL_MINUTES", 60)
	if err != nil {
		return Config{}, err
	}
	c.ReconcileInterval = time.Duration(minutes) * time.Minute
	c.ReconcileMaxUsers, err = intEnv("RECONCILIATION_MAX_USERS", 250)
	if err != nil {
		return Config{}, err
	}
	c.ReconcileConcurrency, err = intEnv("RECONCILIATION_CONCURRENCY", 2)
	if err != nil {
		return Config{}, err
	}
	c.ManualSyncMaxUsers, err = intEnv("MANUAL_SYNC_MAX_USERS", 1000)
	if err != nil {
		return Config{}, err
	}
	c.ManualSyncConcurrency, err = intEnv("MANUAL_SYNC_CONCURRENCY", 4)
	if err != nil {
		return Config{}, err
	}
	c.GuildQueueSize, err = intEnv("GUILD_QUEUE_SIZE", 64)
	if err != nil {
		return Config{}, err
	}
	timeoutSeconds, err := intEnv("REQUEST_TIMEOUT_SECONDS", 10)
	if err != nil {
		return Config{}, err
	}
	c.RequestTimeout = time.Duration(timeoutSeconds) * time.Second
	c.AssetMaxBytes, err = int64Env("ASSET_MAX_BYTES", 8<<20)
	if err != nil {
		return Config{}, err
	}
	c.AssetMaxPixels, err = int64Env("ASSET_MAX_PIXELS", 4096*4096)
	if err != nil {
		return Config{}, err
	}

	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) Validate() error {
	switch {
	case c.DiscordToken == "":
		return errors.New("DISCORD_TOKEN is required")
	case strings.Contains(strings.ToLower(c.DiscordToken), "petto"):
		return errors.New("DISCORD_TOKEN appears to belong to another bot")
	case c.ApplicationID == "":
		return errors.New("DISCORD_APPLICATION_ID is required")
	case c.DatabaseURL == "":
		return errors.New("DATABASE_URL is required")
	case strings.Contains(strings.ToLower(c.DatabaseURL), "petto") || strings.Contains(strings.ToLower(c.DatabaseURL), "supabase"):
		return errors.New("DATABASE_URL must point to the private database exclusive to Vanity Tag Bot")
	case c.ShardCount < 1:
		return errors.New("SHARD_COUNT must be positive")
	case c.ShardID < 0 || c.ShardID >= c.ShardCount:
		return errors.New("SHARD_ID must be between 0 and SHARD_COUNT-1")
	case len([]rune(c.PresenceText)) > 128:
		return errors.New("PRESENCE_TEXT must be at most 128 characters")
	case c.ReconcileInterval < time.Minute:
		return errors.New("RECONCILIATION_INTERVAL_MINUTES must be at least 1")
	case c.ReconcileMaxUsers < 1:
		return errors.New("RECONCILIATION_MAX_USERS must be positive")
	case c.ReconcileConcurrency < 1:
		return errors.New("RECONCILIATION_CONCURRENCY must be positive")
	case c.ManualSyncMaxUsers < 1:
		return errors.New("MANUAL_SYNC_MAX_USERS must be positive")
	case c.ManualSyncConcurrency < 1:
		return errors.New("MANUAL_SYNC_CONCURRENCY must be positive")
	case c.GuildQueueSize < 1:
		return errors.New("GUILD_QUEUE_SIZE must be positive")
	case c.RequestTimeout < time.Second:
		return errors.New("REQUEST_TIMEOUT_SECONDS must be at least 1")
	case c.AssetMaxBytes < 1024:
		return errors.New("ASSET_MAX_BYTES is too small")
	}
	if parsed, err := url.Parse(c.DatabaseURL); err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		if err != nil {
			return fmt.Errorf("DATABASE_URL must be a valid PostgreSQL URL: %w", err)
		}
		return fmt.Errorf("DATABASE_URL must use postgres:// or postgresql://")
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func boolEnv(key string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be boolean: %w", key, err)
	}
	return parsed, nil
}

func intEnv(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return parsed, nil
}

func int64Env(key string, fallback int64) (int64, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return parsed, nil
}

func splitSet(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result[item] = struct{}{}
		}
	}
	return result
}

func loadEmojis() map[string]string {
	result := make(map[string]string)
	for _, name := range []string{"STAR", "APPROVE", "DENY", "ALERT", "WARNING", "HAMMER", "PREV", "NEXT", "CLOSE"} {
		result[name] = strings.TrimSpace(os.Getenv("EMOJI_" + name))
	}
	return result
}
