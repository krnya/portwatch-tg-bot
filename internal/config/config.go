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
	for _, source := range cfg.Sources {
		if err := source.Validate(); err != nil {
			return Config{}, err
		}
		if _, exists := ids[source.ID]; exists {
			return Config{}, fmt.Errorf("duplicate source id %q", source.ID)
		}
		ids[source.ID] = struct{}{}
	}
	return cfg, nil
}

func (source Source) Validate() error {
	if source.ID == "" || strings.TrimSpace(source.ID) != source.ID {
		return errors.New("invalid source id")
	}
	if source.Kind != PortWatch && source.Kind != RSS {
		return fmt.Errorf("source %q: unsupported kind %q", source.ID, source.Kind)
	}
	address, err := url.Parse(source.FeedURL)
	if err != nil || address.Scheme != "https" || address.Hostname() == "" || address.User != nil || address.Fragment != "" {
		return fmt.Errorf("source %q: feed_url must be an absolute HTTPS URL without credentials or fragment", source.ID)
	}
	return nil
}
