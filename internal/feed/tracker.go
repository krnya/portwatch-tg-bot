package feed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"portwatch/internal/config"
)

type Progress struct {
	// Score is expressed in tenths of one percentage point, matching the website.
	Score     int
	Leader    string
	URL       string
	UpdatedAt time.Time
}

func (progress Progress) Percentage() string {
	return strconv.FormatFloat(float64(progress.Score)/10, 'f', -1, 64) + "%"
}

func (client *Client) FetchProgress(ctx context.Context, source config.Source) (Progress, error) {
	home, err := url.Parse(source.FeedURL)
	if err != nil {
		return Progress{}, err
	}
	base, found := strings.CutSuffix(home.Path, "/news/feed.xml")
	if !found {
		base = ""
	}
	home.Path, home.RawPath = base+"/", ""
	home.RawQuery, home.ForceQuery = "", false
	endpoint := home.JoinPath("data", "tracker.json")
	endpoint.RawQuery = "ts=" + strconv.FormatInt(time.Now().Unix()/60, 10)
	body, _, err := client.get(ctx, endpoint.String(), "application/json")
	if err != nil {
		return Progress{}, err
	}
	return parseProgress(body, home.String())
}

// trackerNumber handles the numeric and boolean milestone values used by PortWatch.
type trackerNumber float64

func (number *trackerNumber) UnmarshalJSON(value []byte) error {
	*number = 0
	switch string(value) {
	case "true":
		*number = 1
	default:
		v, err := strconv.ParseFloat(strings.TrimSpace(strings.Trim(string(value), `"`)), 64)
		if err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
			*number = trackerNumber(v)
		}
	}
	return nil
}

type trackerEffort struct {
	Name        string          `json:"name"`
	Nuked       json.RawMessage `json:"nuked"`
	ManualScore *float64        `json:"manualScore"`
	Groundwork  struct {
		Loader   trackerNumber `json:"loader"`
		Shaders  trackerNumber `json:"shaders"`
		Graphics trackerNumber `json:"graphics"`
		Proven   trackerNumber `json:"proven"`
	} `json:"groundwork"`
	GTA struct {
		Loads     trackerNumber `json:"loads"`
		Boots     trackerNumber `json:"boots"`
		Menu      trackerNumber `json:"menu"`
		InGame    trackerNumber `json:"ingame"`
		FPS       trackerNumber `json:"fps"`
		Stability string        `json:"stability"`
	} `json:"gta"`
	Stats struct {
		Stars int `json:"stars"`
	} `json:"stats"`
}

func parseProgress(body []byte, home string) (Progress, error) {
	var data struct {
		Efforts []trackerEffort `json:"efforts"`
		Updated time.Time       `json:"updated"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return Progress{}, fmt.Errorf("decode tracker JSON: %w", err)
	}
	if data.Efforts == nil {
		return Progress{}, errors.New("tracker JSON has no efforts")
	}
	progress := Progress{URL: home, UpdatedAt: data.Updated}
	var bestStars int
	for _, effort := range data.Efforts {
		if effort.archived() {
			continue
		}
		if effort.Name == "" {
			return Progress{}, errors.New("tracker JSON has an unnamed effort")
		}
		score := effort.score()
		if progress.Leader == "" || score > progress.Score || (score == progress.Score && effort.Stats.Stars > bestStars) {
			progress.Score, progress.Leader, bestStars = score, effort.Name, effort.Stats.Stars
		}
	}
	return progress, nil
}

func (effort trackerEffort) archived() bool {
	// Live entries use a metadata object for removed projects. Older data can use a boolean.
	switch string(effort.Nuked) {
	case "", "null", "false":
		return false
	default:
		return true
	}
}

var stability = map[string]float64{"often": 0.25, "sometimes": 0.55, "rarely": 0.8, "stable": 1}

// Keep these weights and milestones in sync with PortWatch VI's assets/js/scoring.js.
func (effort trackerEffort) score() int {
	if effort.ManualScore != nil {
		return int(math.Round(min(100, max(0, *effort.ManualScore)) * 10))
	}
	groundwork := effort.Groundwork
	score := 25 * (0.2*clamp01(groundwork.Loader) + 0.28*clamp01(groundwork.Shaders) +
		0.32*clamp01(groundwork.Graphics) + 0.2*clamp01(groundwork.Proven))
	gta := effort.GTA
	for index, value := range [...]trackerNumber{gta.Loads, gta.Boots, gta.Menu, gta.InGame} {
		milestone := float64(25 + 15*index)
		fraction := clamp01(value)
		if fraction < 1 {
			score += fraction * (milestone - score)
			return int(math.Round(score * 10))
		}
		score = milestone
	}
	score += 15*clamp01(gta.FPS/30) + 15*stability[gta.Stability]
	return int(math.Round(score * 10))
}

func clamp01(value trackerNumber) float64 {
	return min(1, max(0, float64(value)))
}
