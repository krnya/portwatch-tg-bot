package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	PortWatch = "portwatch"
	RSS       = "rss"
)

type Source struct {
	ID      string `yaml:"id"`
	Kind    string `yaml:"kind"`
	FeedURL string `yaml:"feed_url"`
}

type Config struct {
	PollInterval time.Duration `yaml:"poll_interval"`
	Sources      []Source      `yaml:"sources"`
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()

	cfg := Config{PollInterval: 2 * time.Minute}
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("expected one YAML document")
	}
	if cfg.PollInterval <= 0 || cfg.Sources == nil {
		return Config{}, errors.New("expected a positive poll_interval and a sources list")
	}

	ids := make(map[string]struct{}, len(cfg.Sources))
	for index, source := range cfg.Sources {
		if source.ID == "" || strings.TrimSpace(source.ID) != source.ID {
			return Config{}, fmt.Errorf("sources[%d]: invalid id", index)
		}
		if _, exists := ids[source.ID]; exists {
			return Config{}, fmt.Errorf("duplicate source id %q", source.ID)
		}
		ids[source.ID] = struct{}{}
		if source.Kind != PortWatch && source.Kind != RSS {
			return Config{}, fmt.Errorf("source %q: unsupported kind %q", source.ID, source.Kind)
		}
		address, err := url.Parse(source.FeedURL)
		if err != nil || address.Scheme != "https" || address.Hostname() == "" || address.User != nil || address.Fragment != "" {
			return Config{}, fmt.Errorf("source %q: feed_url must be an absolute HTTPS URL without credentials or fragment", source.ID)
		}
	}
	return cfg, nil
}
