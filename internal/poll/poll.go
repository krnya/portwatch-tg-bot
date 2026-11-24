package poll

import (
	"context"
	"log/slog"
	"time"

	"portwatch/internal/config"
	"portwatch/internal/feed"
	"portwatch/internal/store"
)

type Fetcher interface {
	Fetch(context.Context, config.Source) ([]feed.Article, error)
}

type Poller struct {
	ConfigPath string
	Feed       Fetcher
	Store      *store.Store
	Logger     *slog.Logger
	Wake       chan<- struct{}
}

func (poller Poller) Run(ctx context.Context, cfg config.Config) {
	for {
		started := time.Now()
		poller.poll(ctx, cfg)
		timer := time.NewTimer(time.Until(started.Add(cfg.PollInterval)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		updated, err := config.Load(poller.ConfigPath)
		if err != nil {
			poller.Logger.Warn("keeping previous source configuration", "error", err)
			continue
		}
		cfg = updated
	}
}

func (poller Poller) poll(ctx context.Context, cfg config.Config) {
	for _, source := range cfg.Sources {
		if ctx.Err() != nil {
			return
		}
		articles, err := poller.Feed.Fetch(ctx, source)
		if err != nil {
			poller.Logger.Warn("fetch feed", "source", source.ID, "error", err)
			continue
		}
		if err := poller.Store.RecordPoll(ctx, source.ID, articles); err != nil {
			poller.Logger.Error("record feed", "source", source.ID, "error", err)
			continue
		}
		poller.Logger.Debug("feed processed", "source", source.ID, "articles", len(articles))
		select {
		case poller.Wake <- struct{}{}:
		default:
		}
	}
}
