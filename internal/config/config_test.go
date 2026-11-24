package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	const source = "  - {id: portwatch-vi, kind: portwatch, feed_url: 'https://example.com/news/feed.xml'}\n"
	for _, test := range []struct {
		name  string
		yaml  string
		valid bool
	}{
		{"source", "sources:\n" + source, true},
		{"paused", "sources: []\n", true},
		{"missing list", "poll_interval: 2m\n", false},
		{"partial write", "sources: [", false},
		{"nonpositive interval", "poll_interval: 0s\nsources: []\n", false},
		{"duplicate id", "sources:\n" + source + source, false},
		{"invalid scheme", "sources: [{id: a, kind: rss, feed_url: 'http://example.com'}]", false},
		{"invalid kind", "sources: [{id: a, kind: html, feed_url: 'https://example.com'}]", false},
		{"trailing document", "sources: []\n---\nsources: []", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sources.yaml")
			if err := os.WriteFile(path, []byte(test.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if (err == nil) != test.valid {
				t.Fatalf("valid = %v, error = %v", test.valid, err)
			}
			if test.valid && (cfg.PollInterval != 2*time.Minute || cfg.Sources == nil) {
				t.Fatalf("unexpected config: %#v", cfg)
			}
		})
	}
}
