package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/config"
	"github.com/PettoBot/vanity-tag-bot/internal/database"
	discordbot "github.com/PettoBot/vanity-tag-bot/internal/discord"
	httpserver "github.com/PettoBot/vanity-tag-bot/internal/http"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := database.ApplyMigrations(ctx, pool, cfg.MigrationsDir); err != nil {
		logger.Error("migrations failed", "error", err)
		os.Exit(1)
	}
	bot, err := discordbot.New(cfg, database.NewStore(pool), logger)
	if err != nil {
		logger.Error("bot initialization failed", "error", err)
		os.Exit(1)
	}
	if err := bot.Open(ctx); err != nil {
		logger.Error("discord startup failed", "error", err)
		os.Exit(1)
	}
	defer bot.Close()

	health := httpserver.New(cfg.HTTPAddr, pool, bot.Session())
	go func() {
		if err := health.ListenAndServe(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, syscall.EINVAL) {
			logger.Error("health server stopped", "error", err)
		}
	}()
	logger.Info("Vanity Tag Bot is running", "health_addr", cfg.HTTPAddr)
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = health.Shutdown(shutdownCtx)
	_ = bot.Close()
	logger.Info("Vanity Tag Bot stopped")
}
