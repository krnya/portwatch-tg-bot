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

type ProgressFetcher interface {
	FetchProgress(context.Context, config.Source) (feed.Progress, error)
}

type Poller struct {
	ConfigPath string
	Feed       Fetcher
	Tracker    ProgressFetcher
	Store      *store.Store
	Logger     *slog.Logger
	Wake       chan<- struct{}
	Refresh    <-chan struct{}
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
		case <-poller.Refresh:
			timer.Stop()
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
	sources, err := poller.Store.Sources(ctx, cfg.Sources)
	if err != nil {
		poller.Logger.Error("load source overrides", "error", err)
		return
	}
	for _, source := range sources {
		if ctx.Err() != nil {
			return
		}
		if source.Kind == config.PortWatch && poller.Tracker != nil {
			poller.pollProgress(ctx, source)
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
		poller.wakeDelivery()
	}
}

func (poller Poller) pollProgress(ctx context.Context, source config.Source) {
	progress, err := poller.Tracker.FetchProgress(ctx, source)
	if err != nil {
		poller.Logger.Warn("fetch tracker progress", "source", source.ID, "error", err)
		return
	}
	changed, err := poller.Store.RecordProgress(ctx, source.ID, progress)
	if err != nil {
		poller.Logger.Error("record tracker progress", "source", source.ID, "error", err)
		return
	}
	if changed {
		poller.Logger.Info("tracker progress changed", "source", source.ID, "score", progress.Percentage(), "leader", progress.Leader)
		poller.wakeDelivery()
	}
}

func (poller Poller) wakeDelivery() {
	select {
	case poller.Wake <- struct{}{}:
	default:
	}
}
