package telegram

import "context"

type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message"`
}

type Message struct {
	Chat struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	From struct {
		ID    int64 `json:"id"`
		IsBot bool  `json:"is_bot"`
	} `json:"from"`
	Text string `json:"text"`
}

func (client *Client) Updates(ctx context.Context, offset int64) ([]Update, error) {
	args := struct {
		Offset         int64    `json:"offset"`
		Timeout        int      `json:"timeout"`
		Limit          int      `json:"limit"`
		AllowedUpdates []string `json:"allowed_updates"`
	}{offset, 25, 25, []string{"message"}}
	var updates []Update
	err := client.request(ctx, "getUpdates", args, &updates)
	return updates, err
}
