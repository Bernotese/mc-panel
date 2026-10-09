package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

const discordMaxContent = 2000

type discordSender struct {
	url    string
	client *http.Client
}

func (d *discordSender) Send(ctx context.Context, e Event) error {
	title, body := render(e)
	content := truncateRunes("**"+title+"**\n"+body, discordMaxContent)
	payload, err := json.Marshal(map[string]any{
		"content":          content,
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
	if err != nil {
		return &PermanentError{Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(payload))
	if err != nil {
		return &PermanentError{Err: errors.New("notify: discord: invalid webhook URL")}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mc-panel")
	resp, err := d.client.Do(req)
	if err != nil {
		return requestError(KindDiscord, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return checkStatus(KindDiscord, resp)
}
