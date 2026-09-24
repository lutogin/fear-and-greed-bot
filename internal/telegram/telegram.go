// Package telegram sends messages through the Telegram Bot API.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const DefaultBaseURL = "https://api.telegram.org"

// Client sends messages to one chat.
type Client struct {
	BaseURL string
	Token   string
	ChatID  string
	HTTP    *http.Client
}

func New(token, chatID string, httpClient *http.Client) *Client {
	return &Client{BaseURL: DefaultBaseURL, Token: token, ChatID: chatID, HTTP: httpClient}
}

type sendMessageRequest struct {
	ChatID             string             `json:"chat_id"`
	Text               string             `json:"text"`
	ParseMode          string             `json:"parse_mode"`
	LinkPreviewOptions linkPreviewOptions `json:"link_preview_options"`
}

type linkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

type apiResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

// Send sends an HTML-formatted message.
func (c *Client) Send(ctx context.Context, text string) error {
	body, err := json.Marshal(sendMessageRequest{
		ChatID:             c.ChatID,
		Text:               text,
		ParseMode:          "HTML",
		LinkPreviewOptions: linkPreviewOptions{IsDisabled: true},
	})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/bot" + c.Token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return c.redact(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return c.redact(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("telegram: read response: %w", err)
	}
	var r apiResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("telegram: unexpected response, HTTP %d", resp.StatusCode)
	}
	if !r.OK {
		return fmt.Errorf("telegram: %s (error code %d)", r.Description, r.ErrorCode)
	}
	return nil
}

// redact removes the bot token from errors: net/http puts the request URL,
// which contains the token, into its error messages, and those end up in logs.
func (c *Client) redact(err error) error {
	if c.Token == "" || !strings.Contains(err.Error(), c.Token) {
		return err
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		urlErr.URL = strings.ReplaceAll(urlErr.URL, c.Token, "<redacted>")
		if !strings.Contains(err.Error(), c.Token) {
			return err
		}
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.Token, "<redacted>"))
}
