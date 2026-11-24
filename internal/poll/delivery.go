package poll

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"portwatch/internal/feed"
	"portwatch/internal/store"
	"portwatch/internal/telegram"
)

type Sender interface {
	Send(context.Context, string) (int64, error)
}

type Delivery struct {
	Store  *store.Store
	Sender Sender
	Logger *slog.Logger
	Wake   <-chan struct{}
}

func (worker Delivery) Run(ctx context.Context) {
	for ctx.Err() == nil {
		item, err := worker.Store.OldestPending(ctx)
		if err != nil {
			worker.Logger.Error("read notification queue", "error", err)
			wait(ctx, 5*time.Second)
			continue
		}
		if item.QueueID == 0 {
			select {
			case <-ctx.Done():
			case <-worker.Wake:
			}
			continue
		}
		if !wait(ctx, time.Until(item.NextAttemptAt)) {
			return
		}
		worker.deliver(ctx, item)
	}
}

func (worker Delivery) deliver(ctx context.Context, item store.Pending) {
	messageID, sendErr := worker.Sender.Send(ctx, message(item.Article))
	if sendErr != nil && ctx.Err() != nil {
		return
	}
	var retryAt time.Time
	if sendErr != nil {
		retryAt = time.Now().Add(retryDelay(sendErr, item.Attempts))
		worker.Logger.Warn("notification will be retried", "source", item.SourceID, "article", item.Article.ID,
			"retry_at", retryAt, "error", sendErr)
	}

	for {
		// Persist the outcome even if a shutdown happened while Telegram responded.
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		var err error
		if sendErr == nil {
			err = worker.Store.MarkSent(writeCtx, item.QueueID, messageID)
		} else {
			err = worker.Store.RetryAt(writeCtx, item.QueueID, retryAt)
		}
		cancel()
		if err == nil {
			if sendErr == nil {
				worker.Logger.Info("notification sent", "source", item.SourceID, "article", item.Article.ID, "message_id", messageID)
			}
			return
		}
		worker.Logger.Error("save notification outcome", "error", err)
		if !wait(ctx, 5*time.Second) {
			return
		}
	}
}

func wait(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func retryDelay(err error, attempts int) time.Duration {
	var apiError *telegram.APIError
	if errors.As(err, &apiError) {
		if apiError.Code == 429 && apiError.RetryAfter > 0 {
			return apiError.RetryAfter
		}
		if apiError.Code >= 400 && apiError.Code < 500 && apiError.Code != 429 {
			return 5 * time.Minute
		}
	}
	return min((15*time.Second)<<min(attempts, 5), 5*time.Minute)
}

func message(article feed.Article) string {
	// Budget conservatively in UTF-16 units while keeping the full link.
	urlBudget := len(article.URL)
	title, used := fitText(article.Title, min(1000, 4096-urlBudget-4))
	summary, _ := fitText(article.Summary, 4096-urlBudget-used-4)
	if summary == "" {
		return title + "\n\n" + article.URL
	}
	return title + "\n\n" + summary + "\n\n" + article.URL
}

func fitText(value string, budget int) (string, int) {
	if budget <= 0 || value == "" {
		return "", 0
	}
	units := 0
	for index, r := range value {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > budget {
			if units == budget {
				last, size := utf8.DecodeLastRuneInString(value[:index])
				index -= size
				units--
				if last > 0xffff {
					units--
				}
			}
			return strings.TrimSpace(value[:index]) + "…", units + 1
		}
		units += width
	}
	return value, units
}
