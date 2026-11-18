// Package notify sends short messages to a push service: ntfy, a Discord
// webhook, or any URL that accepts a plain text POST.
package notify

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Send posts title and message to rawURL. ntfy shows the title as the
// notification's heading; Discord gets both in one message; anything else
// gets the message as the body and the title in a Title header.
func Send(ctx context.Context, rawURL, title, message string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("not an http(s) URL: %q", rawURL)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var body []byte
	contentType := "text/plain; charset=utf-8"
	if isDiscord(u) {
		body, _ = json.Marshal(map[string]string{"content": "**" + title + "**\n" + message})
		contentType = "application/json"
	} else {
		body = []byte(message)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	if !isDiscord(u) {
		// Header values cannot hold line breaks or most non-ASCII; ntfy
		// reads RFC 2047 encoded titles.
		req.Header.Set("Title", headerValue(title))
		req.Header.Set("Tags", "clapper")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return redact(err, u)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

func isDiscord(u *url.URL) bool {
	host := strings.TrimPrefix(u.Hostname(), "www.")
	return (host == "discord.com" || host == "discordapp.com") && strings.HasPrefix(u.Path, "/api/webhooks/")
}

func headerValue(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for _, r := range s {
		if r > 126 {
			return "=?UTF-8?B?" + b64(s) + "?="
		}
	}
	return s
}

// redact keeps webhook secrets in the URL path and query out of errors,
// which end up in the activity log.
func redact(err error, u *url.URL) error {
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), u.String(), u.Scheme+"://"+u.Host+"/…"))
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
