package telegram

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSend(t *testing.T) {
	for _, test := range []struct {
		name string
		code int
		body string
	}{
		{"success", 200, `{"ok":true,"result":{"message_id":99}}`},
		{"rate limit", 429, `{"ok":false,"error_code":429,"parameters":{"retry_after":42}}`},
		{"server error", 500, `unavailable`},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient("123:test-token", "-1001234567890")
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					ChatID int64  `json:"chat_id"`
					Text   string `json:"text"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.ChatID != -1001234567890 ||
					payload.Text != "Headline\n\nhttps://example.com/news/story" || r.Method != http.MethodPost || r.URL.Path != "/bot123:test-token/sendMessage" {
					t.Errorf("unexpected Telegram request: %s %s, payload=%+v, error=%v", r.Method, r.URL.Path, payload, err)
				}
				w.WriteHeader(test.code)
				io.WriteString(w, test.body)
			}))
			client.http = server.Client()
			client.endpoint = server.URL + "/bot123:test-token/sendMessage"
			defer server.Close()

			messageID, err := client.Send(t.Context(), "Headline\n\nhttps://example.com/news/story")
			if test.code == 200 {
				if err != nil || messageID != 99 {
					t.Fatalf("message ID = %d, error = %v", messageID, err)
				}
				return
			}
			var apiError *APIError
			if !errors.As(err, &apiError) || apiError.Code != test.code || (test.code == 429 && apiError.RetryAfter != 42*time.Second) {
				t.Fatalf("unexpected API error: %#v (%v)", apiError, err)
			}
		})
	}
}

func TestTransportErrorDoesNotExposeToken(t *testing.T) {
	client, err := NewClient("123:test-token", "@channel")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client.endpoint = server.URL + "/bot123:test-token/sendMessage"
	server.Close()
	if _, err := client.Send(t.Context(), "test"); err == nil || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("unexpected transport error: %v", err)
	}
}
