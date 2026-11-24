package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"portwatch/internal/bot"
	"portwatch/internal/config"
	"portwatch/internal/feed"
	"portwatch/internal/poll"
	"portwatch/internal/store"
	"portwatch/internal/telegram"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Error("portwatch stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	configPath := envOr("PORTWATCH_CONFIG", "config/sources.yaml")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load sources: %w", err)
	}
	client, err := telegram.NewClient(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if err != nil {
		return err
	}
	chatID, err := telegram.ParseChatID(os.Getenv("TELEGRAM_CHAT_ID"))
	if err != nil {
		return err
	}
	var ownerID int64
	if value := strings.TrimSpace(os.Getenv("TELEGRAM_OWNER_ID")); value != "" {
		ownerID, err = strconv.ParseInt(value, 10, 64)
		if err != nil || ownerID <= 0 {
			return fmt.Errorf("TELEGRAM_OWNER_ID must be a positive numeric Telegram user ID")
		}
	}
	db, err := store.Open(ctx, envOr("PORTWATCH_DB", "portwatch.sqlite"), chatID)
	if err != nil {
		return err
	}
	defer db.Close()

	wake := make(chan struct{}, 1)
	refresh := make(chan struct{}, 1)
	feeds := feed.NewClient()
	poller := poll.Poller{ConfigPath: configPath, Feed: feeds, Tracker: feeds, Store: db, Logger: logger, Wake: wake, Refresh: refresh}
	delivery := poll.Delivery{Store: db, Sender: client, Logger: logger, Wake: wake}
	commands := bot.Commands{Telegram: client, Commits: bot.NewGitHubCommits(), Store: db, Logger: logger,
		OwnerID: ownerID, ConfigPath: configPath, Refresh: refresh}
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		delivery.Run(ctx)
	}()
	go func() {
		defer workers.Done()
		commands.Run(ctx)
	}()
	logger.Info("portwatch started", "sources", len(cfg.Sources), "poll_interval", cfg.PollInterval.String())
	poller.Run(ctx, cfg)
	workers.Wait()
	return nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
