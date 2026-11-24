package bot

import (
	"context"
	"fmt"
	"strings"

	"portwatch/internal/config"
)

func (commands Commands) progress(ctx context.Context) (string, error) {
	cfg, err := config.Load(commands.ConfigPath)
	if err != nil {
		return "Не удалось прочитать настройки источников.", nil
	}
	sources, err := commands.Store.Sources(ctx, cfg.Sources)
	if err != nil {
		return "", err
	}
	var reply strings.Builder
	for _, source := range sources {
		if source.Kind != config.PortWatch {
			continue
		}
		progress, found, err := commands.Store.Progress(ctx, source.ID)
		if err != nil {
			return "", err
		}
		if !found {
			continue
		}
		entry := fmt.Sprintf("%s: %s\n", source.ID, progress.Percentage())
		if progress.Leader != "" {
			entry += "Лидер: " + progress.Leader + "\n"
		}
		entry += progress.URL
		if reply.Len()+len(entry)+2 > 3900 {
			break
		}
		if reply.Len() != 0 {
			reply.WriteString("\n\n")
		}
		reply.WriteString(entry)
	}
	if reply.Len() == 0 {
		return "Данные о прогрессе пока недоступны.", nil
	}
	return reply.String(), nil
}
