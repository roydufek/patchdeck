package notify

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// buildHTTP maps a target URL to a ready-to-send HTTP request for its scheme.
func buildHTTP(scheme, target, title, body string) (*httpMessage, error) {
	switch scheme {
	case "gotify", "gotifys":
		return buildGotify(scheme, target, title, body)
	case "ntfy", "ntfys":
		return buildNtfy(scheme, target, title, body)
	case "discord":
		return buildDiscord(target, title, body)
	case "tgram", "telegram":
		return buildTelegram(target, title, body)
	case "slack":
		return buildSlack(target, title, body)
	case "pover", "pushover":
		return buildPushover(target, title, body)
	case "json", "jsons", "form", "forms":
		return buildWebhook(scheme, target, title, body)
	}
	return nil, fmt.Errorf("unsupported notification scheme %q (supported: %s)", scheme, strings.Join(SupportedSchemes, ", "))
}

func jsonMessage(method, endpoint string, header http.Header, v any) (*httpMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("notification payload encode failed: %w", err)
	}
	return &httpMessage{method: method, url: endpoint, contentType: "application/json", header: header, body: b}, nil
}

// gotify://host[:port]/token[?priority=N]   (gotifys = https)
func buildGotify(scheme, target, title, body string) (*httpMessage, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid gotify URL: %w", scrub(err))
	}
	token := strings.Trim(u.Path, "/")
	if u.Host == "" || token == "" {
		return nil, fmt.Errorf("gotify URL needs a host and app token: gotify://host/token")
	}
	proto := "http"
	if scheme == "gotifys" {
		proto = "https"
	}
	priority := 5
	if p := u.Query().Get("priority"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			priority = n
		}
	}
	endpoint := fmt.Sprintf("%s://%s/message?token=%s", proto, u.Host, url.QueryEscape(token))
	return jsonMessage("POST", endpoint, nil, map[string]any{"title": title, "message": body, "priority": priority})
}

// ntfy://[user:pass@]host[:port]/topic  (ntfys = https) — or ntfy://topic for ntfy.sh
func buildNtfy(scheme, target, title, body string) (*httpMessage, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid ntfy URL: %w", scrub(err))
	}
	topic := strings.Trim(u.Path, "/")
	var base string
	if topic == "" {
		// ntfy://{topic} → ntfy.sh cloud (always https)
		topic, base = u.Host, "https://ntfy.sh"
	} else {
		proto := "http"
		if scheme == "ntfys" {
			proto = "https"
		}
		base = proto + "://" + u.Host
	}
	if topic == "" {
		return nil, fmt.Errorf("ntfy URL needs a topic: ntfy://host/topic or ntfy://topic")
	}
	header := http.Header{}
	header.Set("Title", sanitizeHeader(title))
	if u.User != nil {
		if pass, ok := u.User.Password(); ok {
			header.Set("Authorization", "Basic "+basicAuth(u.User.Username(), pass))
		} else if user := u.User.Username(); user != "" {
			header.Set("Authorization", "Bearer "+user)
		}
	}
	if tok := u.Query().Get("token"); tok != "" {
		header.Set("Authorization", "Bearer "+tok)
	}
	endpoint := strings.TrimRight(base, "/") + "/" + topic
	return &httpMessage{method: "POST", url: endpoint, header: header, body: []byte(body)}, nil
}

// discord://webhook_id/webhook_token
func buildDiscord(target, title, body string) (*httpMessage, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid discord URL: %w", scrub(err))
	}
	id, token := u.Host, strings.Trim(u.Path, "/")
	if id == "" || token == "" {
		return nil, fmt.Errorf("discord URL needs a webhook id and token: discord://webhook_id/webhook_token")
	}
	content := title
	if body != "" {
		content = title + "\n" + body
	}
	endpoint := fmt.Sprintf("https://discord.com/api/webhooks/%s/%s", url.PathEscape(id), url.PathEscape(token))
	return jsonMessage("POST", endpoint, nil, map[string]any{"content": truncateRunes(content, 1900), "username": title})
}

// tgram://bottoken/chatid   (telegram:// also accepted). Only the first chat id is used.
func buildTelegram(target, title, body string) (*httpMessage, error) {
	parts := strings.Split(strings.Trim(stripScheme(target), "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("telegram URL needs a bot token and chat id: tgram://bottoken/chatid")
	}
	text := title
	if body != "" {
		text = title + "\n" + body
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", parts[0])
	return jsonMessage("POST", endpoint, nil, map[string]any{"chat_id": parts[1], "text": text})
}

// slack://[botname@]TokenA/TokenB/TokenC[/channel]
func buildSlack(target, title, body string) (*httpMessage, error) {
	rest := strings.Trim(stripScheme(target), "/")
	if at := strings.Index(rest, "@"); at != -1 && !strings.Contains(rest[:at], "/") {
		rest = rest[at+1:] // drop optional botname@
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("slack URL needs three webhook tokens: slack://TokenA/TokenB/TokenC")
	}
	text := title
	if body != "" {
		text = title + "\n" + body
	}
	payload := map[string]any{"text": text}
	if len(parts) >= 4 && parts[3] != "" {
		ch := parts[3]
		if !strings.HasPrefix(ch, "#") && !strings.HasPrefix(ch, "@") {
			ch = "#" + ch
		}
		payload["channel"] = ch
	}
	endpoint := fmt.Sprintf("https://hooks.slack.com/services/%s/%s/%s", parts[0], parts[1], parts[2])
	return jsonMessage("POST", endpoint, nil, payload)
}

// pover://userkey@token  (pushover:// also accepted)
func buildPushover(target, title, body string) (*httpMessage, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid pushover URL: %w", scrub(err))
	}
	if u.User == nil || u.User.Username() == "" || u.Host == "" {
		return nil, fmt.Errorf("pushover URL needs a user key and token: pover://userkey@token")
	}
	form := url.Values{}
	form.Set("token", u.Host)
	form.Set("user", u.User.Username())
	form.Set("title", title)
	form.Set("message", body)
	if p := u.Query().Get("priority"); p != "" {
		form.Set("priority", p)
	}
	return &httpMessage{
		method:      "POST",
		url:         "https://api.pushover.net/1/messages.json",
		contentType: "application/x-www-form-urlencoded",
		body:        []byte(form.Encode()),
	}, nil
}

// json://host/path (jsons = https) posts JSON {title,message,...}; form:// posts form data.
// Extra query parameters are passed through as additional payload fields.
func buildWebhook(scheme, target, title, body string) (*httpMessage, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid webhook URL: %w", scrub(err))
	}
	if u.Host == "" {
		return nil, fmt.Errorf("webhook URL needs a host: json://host/path")
	}
	proto := "http"
	if scheme == "jsons" || scheme == "forms" {
		proto = "https"
	}
	endpoint := proto + "://" + u.Host + u.Path
	header := http.Header{}
	if u.User != nil {
		if pass, ok := u.User.Password(); ok {
			header.Set("Authorization", "Basic "+basicAuth(u.User.Username(), pass))
		}
	}
	extra := u.Query()
	if scheme == "form" || scheme == "forms" {
		form := url.Values{}
		form.Set("title", title)
		form.Set("message", body)
		for k, vs := range extra {
			for _, v := range vs {
				form.Add(k, v)
			}
		}
		return &httpMessage{method: "POST", url: endpoint, contentType: "application/x-www-form-urlencoded", header: header, body: []byte(form.Encode())}, nil
	}
	payload := map[string]any{"title": title, "message": body}
	for k, vs := range extra {
		if len(vs) > 0 {
			payload[k] = vs[0]
		}
	}
	return jsonMessage("POST", endpoint, header, payload)
}

func stripScheme(target string) string {
	if i := strings.Index(target, "://"); i != -1 {
		return target[i+3:]
	}
	if i := strings.Index(target, ":"); i != -1 {
		return target[i+1:]
	}
	return target
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// sanitizeHeader strips CR/LF so a value placed in an HTTP/email header can't inject
// additional headers.
func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}
