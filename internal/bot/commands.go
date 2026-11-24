package bot

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"portwatch/internal/config"
	"portwatch/internal/store"
	"portwatch/internal/telegram"
)

type Telegram interface {
	Updates(context.Context, int64) ([]telegram.Update, error)
	Send(context.Context, string, string) (int64, error)
}

type Commands struct {
	Telegram   Telegram
	Store      *store.Store
	Logger     *slog.Logger
	OwnerID    int64
	ConfigPath string
	Refresh    chan<- struct{}
}

func (commands Commands) Run(ctx context.Context) {
	var offset int64
	for ctx.Err() == nil {
		updates, err := commands.Telegram.Updates(ctx, offset)
		if err == nil && len(updates) == 0 {
			// An empty response has acknowledged the previous batch.
			offset = 0
		}
		for _, update := range updates {
			if err = commands.handle(ctx, update.Message); err != nil {
				break
			}
			offset = update.ID + 1
		}
		if err == nil || ctx.Err() != nil {
			continue
		}
		commands.Logger.Warn("process Telegram updates", "error", err)
		delay := 5 * time.Second
		var apiError *telegram.APIError
		if errors.As(err, &apiError) {
			delay = max(delay, apiError.RetryAfter)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (commands Commands) handle(ctx context.Context, update *telegram.Message) error {
	if update == nil || update.Chat.Type != "private" || update.Chat.ID <= 0 || update.From.IsBot || update.From.ID <= 0 {
		return nil
	}
	fields := strings.Fields(update.Text)
	if len(fields) == 0 {
		return nil
	}
	command, _, _ := strings.Cut(fields[0], "@")
	chat := strconv.FormatInt(update.Chat.ID, 10)
	var reply string
	var err error
	switch command {
	case "/start", "/subscribe":
		if !commands.Store.IsDefaultChat(chat) {
			err = commands.Store.Subscribe(ctx, chat)
		}
		reply = "Вы подписаны на новые публикации PortWatch. Отписаться — /unsubscribe."
	case "/unsubscribe":
		if commands.Store.IsDefaultChat(chat) {
			reply = "Этот чат получает рассылку через TELEGRAM_CHAT_ID в настройках бота."
			break
		}
		err = commands.Store.Unsubscribe(ctx, chat)
		reply = "Вы отписались. Подписаться снова — /subscribe."
	case "/domains", "/adddomain", "/setdomain", "/removedomain":
		if commands.OwnerID == 0 || update.From.ID != commands.OwnerID {
			reply = "Управление доменами доступно владельцу бота."
			break
		}
		reply, err = commands.domains(ctx, command, fields[1:])
	case "/help":
		reply = "Подписаться на новости — /subscribe. Отписаться — /unsubscribe."
		if update.From.ID == commands.OwnerID {
			reply += "\nУправление источниками — /domains."
		}
	default:
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := commands.Telegram.Send(ctx, chat, reply); err != nil {
		commands.Logger.Warn("send Telegram command reply", "command", command, "error", err)
	}
	return nil
}

func (commands Commands) domains(ctx context.Context, command string, args []string) (string, error) {
	cfg, err := config.Load(commands.ConfigPath)
	if err != nil {
		commands.Logger.Warn("load configuration for Telegram command", "error", err)
		return "Не удалось прочитать настройки источников.", nil
	}
	sources, err := commands.Store.Sources(ctx, cfg.Sources)
	if err != nil {
		return "", err
	}
	if command == "/domains" {
		var reply strings.Builder
		reply.WriteString("/adddomain ID DOMAIN\n/setdomain ID DOMAIN\n/removedomain ID\n\nИсточники:")
		if len(sources) == 0 {
			reply.WriteString(" список пуст.")
		}
		for _, source := range sources {
			line := "\n" + source.ID + " — " + source.FeedURL
			if reply.Len()+len(line) > 3900 {
				reply.WriteString("\n…")
				break
			}
			reply.WriteString(line)
		}
		return reply.String(), nil
	}
	remove := command == "/removedomain"
	if (remove && len(args) != 1) || (!remove && len(args) != 2) {
		if remove {
			return "Формат: /removedomain ID", nil
		}
		return "Формат: " + command + " ID DOMAIN", nil
	}
	source := config.Source{ID: args[0], Kind: config.PortWatch}
	found := false
	for _, existing := range sources {
		if existing.ID == source.ID {
			source, found = existing, true
			break
		}
	}
	if command == "/adddomain" && found {
		return "Такой ID уже есть. Используйте /setdomain.", nil
	}
	if command != "/adddomain" && !found {
		return "Источник с таким ID не найден. Список — /domains.", nil
	}
	if !remove {
		source.FeedURL, err = domainURL(source.FeedURL, args[1])
		if err != nil || source.Validate() != nil {
			return "Укажите домен или полный HTTPS-адрес RSS.", nil
		}
	}
	if err := commands.Store.SaveSource(ctx, source, remove); err != nil {
		return "", err
	}
	select {
	case commands.Refresh <- struct{}{}:
	default:
	}
	return "Список источников обновлён. Проверить — /domains.", nil
}

func domainURL(previous, input string) (string, error) {
	if strings.Contains(input, "://") {
		return input, nil
	}
	domain, err := url.Parse("https://" + input)
	if err != nil || domain.Hostname() == "" || domain.User != nil || domain.Path != "" || domain.RawQuery != "" || domain.Fragment != "" {
		return "", errors.New("invalid domain")
	}
	if previous == "" {
		domain.Path = "/news/feed.xml"
		return domain.String(), nil
	}
	address, err := url.Parse(previous)
	if err != nil {
		return "", err
	}
	address.Host = domain.Host
	return address.String(), nil
}
