// Package notify delivers Patchdeck notifications to user-configured destinations.
//
// It speaks a subset of the Apprise URL convention NATIVELY in Go (no python/Apprise
// CLI dependency), so the runtime image carries no interpreter. The configured
// destination is still an Apprise-style URL (gotify://, ntfy://, discord://, …) so
// existing configurations keep working; only the delivery engine changed.
package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SupportedSchemes is the set of URL schemes the native dispatcher can deliver to.
// Surfaced via RuntimeInfo so the settings UI can list them.
var SupportedSchemes = []string{
	"gotify", "gotifys", // Gotify (gotifys = https)
	"ntfy", "ntfys", // ntfy.sh / self-hosted
	"discord",           // Discord webhook
	"tgram", "telegram", // Telegram bot
	"slack",             // Slack incoming webhook
	"pover", "pushover", // Pushover
	"mailto", "mailtos", // Email via SMTP (mailtos = implicit TLS)
	"json", "jsons", "form", "forms", // Generic webhook (json/form body)
}

const defaultTitle = "Patchdeck"

// Dispatcher sends notifications over HTTP(S) and SMTP. It is safe for concurrent use.
type Dispatcher struct {
	client  *http.Client
	timeout time.Duration
}

// RuntimeInfo describes the notification engine for the settings UI. With the native
// engine delivery is always Available (there is no external binary to be missing).
type RuntimeInfo struct {
	Available        bool     `json:"available"`
	Engine           string   `json:"engine"`
	Version          string   `json:"version"`
	SupportedSchemes []string `json:"supported_schemes"`
}

// NewDispatcher returns a dispatcher whose HTTP/SMTP attempts are bounded by timeout.
func NewDispatcher(timeout time.Duration) *Dispatcher {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Dispatcher{client: &http.Client{Timeout: timeout}, timeout: timeout}
}

func (d *Dispatcher) RuntimeInfo() RuntimeInfo {
	return RuntimeInfo{
		Available:        true,
		Engine:           "native",
		Version:          "native",
		SupportedSchemes: SupportedSchemes,
	}
}

// Send delivers the title "Patchdeck" + body to the target URL. An empty target is a
// no-op (notifications simply disabled). The error never includes the raw target URL,
// which may embed tokens or passwords.
func (d *Dispatcher) Send(target, body string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	scheme := SchemeOf(target)
	if scheme == "" {
		return errors.New("notification target is missing a scheme (expected e.g. gotify://…)")
	}

	switch scheme {
	case "mailto", "mailtos":
		return d.sendEmail(target, defaultTitle, body)
	}

	msg, err := buildHTTP(scheme, target, defaultTitle, body)
	if err != nil {
		return err
	}
	return d.execute(msg)
}

// SchemeOf returns the lowercased URL scheme (the part before the first ':'), or "".
func SchemeOf(target string) string {
	i := strings.Index(target, ":")
	if i <= 0 {
		return ""
	}
	return strings.ToLower(target[:i])
}

// SchemeSupported reports whether the dispatcher can deliver to the given scheme.
func SchemeSupported(scheme string) bool {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	for _, s := range SupportedSchemes {
		if s == scheme {
			return true
		}
	}
	return false
}

// httpMessage is a ready-to-send HTTP request, kept as plain data so the URL→request
// mapping for each scheme can be unit-tested without a network.
type httpMessage struct {
	method      string
	url         string
	contentType string
	header      http.Header
	body        []byte
}

func (d *Dispatcher) execute(m *httpMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, m.method, m.url, bytes.NewReader(m.body))
	if err != nil {
		return fmt.Errorf("notification request could not be built: %w", scrub(err))
	}
	if m.contentType != "" {
		req.Header.Set("Content-Type", m.contentType)
	}
	for k, vs := range m.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("notification delivery failed: %w", scrub(err))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notification rejected by %s (HTTP %d)", hostOf(m.url), resp.StatusCode)
	}
	return nil
}

// scrub strips the (possibly secret-bearing) URL out of net/http errors so tokens and
// passwords never reach logs or API responses — only the underlying cause survives.
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "destination"
}
