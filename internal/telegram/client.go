package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var tokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

type Client struct {
	http     *http.Client
	endpoint string
	chatID   any
}

type APIError struct {
	Code       int
	RetryAfter time.Duration
}

func (err *APIError) Error() string {
	return fmt.Sprintf("Telegram sendMessage returned code %d", err.Code)
}

func NewClient(token, chat string) (*Client, error) {
	if !tokenPattern.MatchString(token) {
		return nil, errors.New("TELEGRAM_BOT_TOKEN must be a Telegram bot token")
	}
	chat = strings.TrimSpace(chat)
	var chatID any
	switch id, err := strconv.ParseInt(chat, 10, 64); {
	case err == nil && id != 0:
		chatID = id
	case strings.HasPrefix(chat, "@") && len(chat) > 1:
		chatID = chat
	default:
		return nil, errors.New("TELEGRAM_CHAT_ID must be a nonzero number or @username")
	}
	return &Client{
		http: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		endpoint: "https://api.telegram.org/bot" + token + "/sendMessage",
		chatID:   chatID,
	}, nil
}

func (client *Client) Send(ctx context.Context, text string) (int64, error) {
	payload, err := json.Marshal(struct {
		ChatID any    `json:"chat_id"`
		Text   string `json:"text"`
	}{client.chatID, text})
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, errors.New("create Telegram request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		// The transport error includes the request URL, which contains the token.
		return 0, errors.New("Telegram request failed or timed out")
	}
	defer response.Body.Close()

	var result struct {
		OK        bool `json:"ok"`
		ErrorCode int  `json:"error_code"`
		Result    struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result)
	if response.StatusCode != http.StatusOK || result.ErrorCode != 0 {
		code := response.StatusCode
		if result.ErrorCode != 0 {
			code = result.ErrorCode
		}
		return 0, &APIError{Code: code, RetryAfter: time.Duration(result.Parameters.RetryAfter) * time.Second}
	}
	if err != nil || !result.OK || result.Result.MessageID == 0 {
		return 0, errors.New("invalid Telegram sendMessage response")
	}
	return result.Result.MessageID, nil
}
