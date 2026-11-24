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
}

type APIError struct {
	Code       int
	RetryAfter time.Duration
}

func (err *APIError) Error() string {
	return fmt.Sprintf("Telegram API returned code %d", err.Code)
}

func NewClient(token string) (*Client, error) {
	if !tokenPattern.MatchString(token) {
		return nil, errors.New("TELEGRAM_BOT_TOKEN must be a Telegram bot token")
	}
	return &Client{
		http: &http.Client{
			Timeout: 35 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		endpoint: "https://api.telegram.org/bot" + token,
	}, nil
}

func ParseChatID(chat string) (string, error) {
	chat = strings.TrimSpace(chat)
	if id, err := strconv.ParseInt(chat, 10, 64); err == nil && id != 0 {
		return strconv.FormatInt(id, 10), nil
	}
	if chat == "" || (strings.HasPrefix(chat, "@") && len(chat) > 1) {
		return chat, nil
	}
	return "", errors.New("TELEGRAM_CHAT_ID must be a nonzero number or @username")
}

func (client *Client) Send(ctx context.Context, chat, text string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var chatID any = chat
	if id, err := strconv.ParseInt(chat, 10, 64); err == nil {
		chatID = id
	}
	payload := struct {
		ChatID any    `json:"chat_id"`
		Text   string `json:"text"`
	}{chatID, text}
	var result struct {
		MessageID int64 `json:"message_id"`
	}
	if err := client.request(ctx, "sendMessage", payload, &result); err != nil {
		return 0, err
	}
	if result.MessageID == 0 {
		return 0, errors.New("invalid Telegram sendMessage response")
	}
	return result.MessageID, nil
}

func (client *Client) request(ctx context.Context, method string, args, dest any) error {
	payload, err := json.Marshal(args)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint+"/"+method, bytes.NewReader(payload))
	if err != nil {
		return errors.New("create Telegram request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		// The transport error includes the request URL, which contains the token.
		return errors.New("Telegram request failed or timed out")
	}
	defer response.Body.Close()

	var result struct {
		OK         bool            `json:"ok"`
		ErrorCode  int             `json:"error_code"`
		Result     json.RawMessage `json:"result"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result)
	if response.StatusCode != http.StatusOK || result.ErrorCode != 0 {
		code := response.StatusCode
		if result.ErrorCode != 0 {
			code = result.ErrorCode
		}
		return &APIError{Code: code, RetryAfter: time.Duration(result.Parameters.RetryAfter) * time.Second}
	}
	if err != nil || !result.OK || json.Unmarshal(result.Result, dest) != nil {
		return errors.New("invalid Telegram API response")
	}
	return nil
}
