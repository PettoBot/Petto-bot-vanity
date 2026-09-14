package discord

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/config"
	"github.com/PettoBot/vanity-tag-bot/internal/database"
	"github.com/PettoBot/vanity-tag-bot/internal/embeds"
	"github.com/PettoBot/vanity-tag-bot/internal/identity"
	"github.com/PettoBot/vanity-tag-bot/internal/logs"
	"github.com/PettoBot/vanity-tag-bot/internal/notifications"
	"github.com/PettoBot/vanity-tag-bot/internal/profile"
	"github.com/bwmarrin/discordgo"
)

type Bot struct {
	config               config.Config
	store                *database.Store
	session              *discordgo.Session
	identity             *identity.Service
	profile              *profile.Service
	logger               *slog.Logger
	logSink              *logs.Logger
	notifier             *notifications.Service
	embedRenderer        func(embeds.Template, embeds.Variables) (*discordgo.MessageEmbed, error)
	guildLocks           map[string]*sync.Mutex
	locksMu              sync.Mutex
	identityLocks        [64]sync.Mutex
	manualSyncMu         sync.Mutex
	manualSyncRunning    map[string]struct{}
	helpMu               sync.Mutex
	helpSessions         map[string]helpSession
	embedPanelMu         sync.Mutex
	embedPanels          map[string]embeds.Template
	requestTimeout       time.Duration
	enableReconcile      bool
	reconcileInterval    time.Duration
	reconcileMaxUsers    int
	reconcileConcurrency int
	registeredCommands   []*discordgo.ApplicationCommand
	closeOnce            sync.Once
}

type helpSession struct {
	OwnerID  string
	Category int
	Expires  time.Time
}

func New(cfg config.Config, store *database.Store, logger *slog.Logger) (*Bot, error) {
	if store == nil {
		return nil, fmt.Errorf("database store is required")
	}
	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, fmt.Errorf("create Discord session: %w", err)
	}
	// Discord derives the mobile badge from the Gateway identify properties.
	// Keep the bot's session identified as Android so Discord can render the
	// mobile presence indicator alongside its online status.
	session.Identify.Properties.OS = "Android"
	session.Identify.Properties.Browser = "Discord Android"
	session.Identify.Properties.Device = "Discord Android"
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMembers | discordgo.IntentsGuildPresences
	session.ShardID = cfg.ShardID
	session.ShardCount = cfg.ShardCount
	session.ShouldRetryOnRateLimit = true
	session.MaxRestRetries = 3
	if logger == nil {
		logger = slog.Default()
	}
	bot := &Bot{
		config: cfg, store: store, session: session, logger: logger,
		guildLocks: make(map[string]*sync.Mutex), manualSyncRunning: make(map[string]struct{}), helpSessions: make(map[string]helpSession), embedPanels: make(map[string]embeds.Template),
		requestTimeout: cfg.RequestTimeout, enableReconcile: cfg.EnableReconcile,
		reconcileInterval: cfg.ReconcileInterval, reconcileMaxUsers: cfg.ReconcileMaxUsers,
		reconcileConcurrency: cfg.ReconcileConcurrency,
	}
	sessionRegistry.Store(cfg.ApplicationID, session)
	bot.identity = identity.NewService(store, roleAPI{session: session})
	bot.profile = &profile.Service{Store: store, API: profileAPI{session: session}, Fetcher: profile.NewFetcher(cfg.AssetMaxBytes, cfg.AssetMaxPixels, cfg.AllowedAssetHosts)}
	approveEmoji := cfg.Emojis["APPROVE"]
	if approveEmoji == "" {
		approveEmoji = logs.DefaultApproveEmoji
	}
	denyEmoji := cfg.Emojis["DENY"]
	if denyEmoji == "" {
		denyEmoji = logs.DefaultDenyEmoji
	}
	bot.logSink = &logs.Logger{Store: store, Sender: session, ApproveEmoji: approveEmoji, DenyEmoji: denyEmoji}
	bot.notifier = &notifications.Service{Store: store, Sender: session}
	bot.identity.Engine.Logger = bot.logSink
	bot.identity.Engine.Notifier = bot.notifier
	bot.embedRenderer = embeds.Render
	bot.registerEventHandlers()
	return bot, nil
}

func (b *Bot) Open(ctx context.Context) error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("open Discord gateway: %w", err)
	}
	if err := b.registerCommands(ctx); err != nil {
		_ = b.session.Close()
		return err
	}
	b.startReconciler(ctx)
	return nil
}

func (b *Bot) Close() error {
	var err error
	b.closeOnce.Do(func() { sessionRegistry.Delete(b.config.ApplicationID); err = b.session.Close() })
	return err
}

func (b *Bot) Session() *discordgo.Session { return b.session }

func (b *Bot) registerCommands(ctx context.Context) error {
	commands := CommandCatalog()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	// This bot is public: commands are deliberately registered globally and
	// must not depend on a test guild or a deployment-specific server ID.
	created, err := b.session.ApplicationCommandBulkOverwrite(cfgApplicationID(b.config), "", commands, discordgo.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("register slash commands: %w", err)
	}
	b.registeredCommands = created
	b.logger.Info("global slash commands registered", "count", len(created), "shard_id", b.config.ShardID, "shard_count", b.config.ShardCount)
	return nil
}

func cfgApplicationID(cfg config.Config) string { return cfg.ApplicationID }

func (b *Bot) updateProfile(ctx context.Context, guildID string, update profile.MemberUpdate, current database.BotProfile) error {
	var err error
	b.withGuildLock(guildID, func() { err = b.profile.Update(ctx, guildID, update, current) })
	return err
}

func (b *Bot) updateProfileAsset(ctx context.Context, guildID, kind, rawURL string, current database.BotProfile) error {
	var err error
	b.withGuildLock(guildID, func() { err = b.profile.UpdateAsset(ctx, guildID, kind, rawURL, current) })
	return err
}
