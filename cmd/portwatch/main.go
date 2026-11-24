package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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
	bot, err := telegram.NewClient(os.Getenv("TELEGRAM_BOT_TOKEN"), os.Getenv("TELEGRAM_CHAT_ID"))
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, envOr("PORTWATCH_DB", "portwatch.sqlite"))
	if err != nil {
		return err
	}
	defer db.Close()

	wake := make(chan struct{}, 1)
	poller := poll.Poller{ConfigPath: configPath, Feed: feed.NewClient(), Store: db, Logger: logger, Wake: wake}
	delivery := poll.Delivery{Store: db, Sender: bot, Logger: logger, Wake: wake}
	done := make(chan struct{})
	go func() {
		defer close(done)
		delivery.Run(ctx)
	}()
	logger.Info("portwatch started", "sources", len(cfg.Sources), "poll_interval", cfg.PollInterval.String())
	poller.Run(ctx, cfg)
	<-done
	return nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
