package feed

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"portwatch/internal/config"
)

func TestParseHeadlineScore(t *testing.T) {
	for _, test := range []struct {
		name    string
		effort  string
		percent string
	}{
		{"groundwork", `"groundwork":{"loader":1,"shaders":0.5,"graphics":0.5,"proven":1}`, "17.5%"},
		{"partial in-game", `"gta":{"loads":true,"boots":1,"menu":1,"ingame":0.8}`, "67%"},
		{"FPS", `"gta":{"loads":1,"boots":1,"menu":1,"ingame":true,"fps":5}`, "72.5%"},
		{"stability", `"gta":{"loads":1,"boots":1,"menu":1,"ingame":1,"fps":30,"stability":"rarely"}`, "97%"},
		{"manual score", `"manualScore":76,"gta":{"loads":1,"boots":1,"menu":1,"ingame":1,"fps":5}`, "76%"},
	} {
		t.Run(test.name, func(t *testing.T) {
			json := `{"efforts":[{"name":"Archived","manualScore":100,"nuked":{"at":"2026-11-24","note":"Removed"}},{"name":"Legacy archive","manualScore":100,"nuked":true},{"name":"Leader","nuked":false,` + test.effort + `}],"history":[{"leader":90}]}`
			progress, err := parseProgress([]byte(json), "https://example.org/")
			if err != nil || progress.Percentage() != test.percent || progress.Leader != "Leader" {
				t.Fatalf("progress = %+v (%s), error = %v", progress, progress.Percentage(), err)
			}
		})
	}
	for _, invalid := range []string{`<html>challenge</html>`, `{"history":[{"leader":90}]}`, `{"efforts":[{}]}`} {
		if _, err := parseProgress([]byte(invalid), ""); err == nil {
			t.Errorf("accepted invalid tracker data %q", invalid)
		}
	}
}

func TestFetchProgressUsesConfiguredSiteAndPrefix(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mirror/data/tracker.json" || r.URL.Query().Get("ts") == "" || r.URL.Query().Get("rss") != "" {
			t.Errorf("unexpected tracker request %s", r.URL)
		}
		io.WriteString(w, `{"efforts":[{"name":"Other","manualScore":72.5},{"name":"Leader","manualScore":72.5,"stats":{"stars":1}}]}`)
	}))
	defer server.Close()
	client := Client{http: server.Client()}
	progress, err := client.FetchProgress(t.Context(), config.Source{FeedURL: server.URL + "/mirror/news/feed.xml?rss=1"})
	if err != nil || progress.Score != 725 || progress.Leader != "Leader" || progress.URL != server.URL+"/mirror/" {
		t.Fatalf("progress = %+v, error = %v", progress, err)
	}
}
