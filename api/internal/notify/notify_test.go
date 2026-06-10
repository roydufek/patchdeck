package notify

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// decode a built httpMessage's JSON body into a map for assertions.
func jsonFields(t *testing.T, m *httpMessage) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(m.body, &out); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, string(m.body))
	}
	return out
}

func TestSchemeOf(t *testing.T) {
	cases := map[string]string{
		"gotify://h/t":            "gotify",
		"GOTIFYS://h/t":           "gotifys",
		"mailto://u@h":            "mailto",
		"discord://id/tok":        "discord",
		"no-scheme":               "",
		"":                        "",
	}
	for in, want := range cases {
		if got := SchemeOf(in); got != want {
			t.Errorf("SchemeOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSchemeSupported(t *testing.T) {
	for _, s := range []string{"gotify", "ntfys", "tgram", "telegram", "pover", "mailtos", "jsons", "form"} {
		if !SchemeSupported(s) {
			t.Errorf("SchemeSupported(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"twilio", "matrix", "xmpp", "", "http"} {
		if SchemeSupported(s) {
			t.Errorf("SchemeSupported(%q) = true, want false", s)
		}
	}
}

func TestBuildGotify(t *testing.T) {
	m, err := buildHTTP("gotifys", "gotifys://gotify.example.com/AbC123?priority=8", "Patchdeck", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if m.url != "https://gotify.example.com/message?token=AbC123" {
		t.Errorf("url = %q", m.url)
	}
	f := jsonFields(t, m)
	if f["title"] != "Patchdeck" || f["message"] != "hello" {
		t.Errorf("payload = %v", f)
	}
	if f["priority"].(float64) != 8 {
		t.Errorf("priority = %v, want 8", f["priority"])
	}
	// gotify:// must be http (insecure), gotifys:// https.
	m2, _ := buildHTTP("gotify", "gotify://h/t", "Patchdeck", "x")
	if !strings.HasPrefix(m2.url, "http://") {
		t.Errorf("gotify:// should be http, got %q", m2.url)
	}
}

func TestBuildNtfy(t *testing.T) {
	// Self-hosted with topic in path.
	m, err := buildHTTP("ntfys", "ntfys://ntfy.example.com/patches", "Patchdeck", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if m.url != "https://ntfy.example.com/patches" {
		t.Errorf("url = %q", m.url)
	}
	if m.header.Get("Title") != "Patchdeck" {
		t.Errorf("Title header = %q", m.header.Get("Title"))
	}
	if string(m.body) != "hello" {
		t.Errorf("body = %q", string(m.body))
	}
	// Topic-only → ntfy.sh cloud over https.
	c, _ := buildHTTP("ntfy", "ntfy://mytopic", "Patchdeck", "x")
	if c.url != "https://ntfy.sh/mytopic" {
		t.Errorf("cloud url = %q", c.url)
	}
}

func TestBuildDiscord(t *testing.T) {
	m, err := buildHTTP("discord", "discord://123456789/abcdefXYZ", "Patchdeck", "updates available")
	if err != nil {
		t.Fatal(err)
	}
	if m.url != "https://discord.com/api/webhooks/123456789/abcdefXYZ" {
		t.Errorf("url = %q", m.url)
	}
	f := jsonFields(t, m)
	if !strings.Contains(f["content"].(string), "updates available") {
		t.Errorf("content = %v", f["content"])
	}
}

func TestBuildTelegram(t *testing.T) {
	// Bot tokens contain ':' which must NOT be parsed as a host:port.
	m, err := buildHTTP("tgram", "tgram://123456789:AAExample-Token/987654", "Patchdeck", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if m.url != "https://api.telegram.org/bot123456789:AAExample-Token/sendMessage" {
		t.Errorf("url = %q", m.url)
	}
	f := jsonFields(t, m)
	if f["chat_id"] != "987654" {
		t.Errorf("chat_id = %v", f["chat_id"])
	}
}

func TestBuildSlack(t *testing.T) {
	m, err := buildHTTP("slack", "slack://T00000000/B00000000/XXXXXXXXXXXX/alerts", "Patchdeck", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if m.url != "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXX" {
		t.Errorf("url = %q", m.url)
	}
	f := jsonFields(t, m)
	if f["channel"] != "#alerts" {
		t.Errorf("channel = %v, want #alerts", f["channel"])
	}
	// botname@ prefix is dropped.
	m2, err := buildHTTP("slack", "slack://patchbot@T1/B2/C3", "Patchdeck", "hi")
	if err != nil || m2.url != "https://hooks.slack.com/services/T1/B2/C3" {
		t.Errorf("botname prefix not dropped: url=%q err=%v", m2.url, err)
	}
}

func TestBuildPushover(t *testing.T) {
	m, err := buildHTTP("pover", "pover://uQiRzpo4DXghDmr9QzzfQu27cmVRsG@azGDORePK8gMaC0QOYAMyEEuzJnyUi", "Patchdeck", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if m.url != "https://api.pushover.net/1/messages.json" {
		t.Errorf("url = %q", m.url)
	}
	vals, _ := url.ParseQuery(string(m.body))
	if vals.Get("token") != "azGDORePK8gMaC0QOYAMyEEuzJnyUi" || vals.Get("user") != "uQiRzpo4DXghDmr9QzzfQu27cmVRsG" {
		t.Errorf("form = %v", vals)
	}
	if vals.Get("message") != "hi" {
		t.Errorf("message = %q", vals.Get("message"))
	}
}

func TestBuildWebhookJSON(t *testing.T) {
	m, err := buildHTTP("jsons", "jsons://hooks.example.com/ingest?source=patchdeck", "Patchdeck", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if m.url != "https://hooks.example.com/ingest" {
		t.Errorf("url = %q", m.url)
	}
	f := jsonFields(t, m)
	if f["title"] != "Patchdeck" || f["message"] != "hi" || f["source"] != "patchdeck" {
		t.Errorf("payload = %v", f)
	}
}

func TestBuildWebhookForm(t *testing.T) {
	m, err := buildHTTP("form", "form://hooks.example.com/x", "Patchdeck", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if m.contentType != "application/x-www-form-urlencoded" {
		t.Errorf("contentType = %q", m.contentType)
	}
	vals, _ := url.ParseQuery(string(m.body))
	if vals.Get("title") != "Patchdeck" || vals.Get("message") != "hi" {
		t.Errorf("form = %v", vals)
	}
}

func TestParseEmail(t *testing.T) {
	tgt, err := parseEmail("mailtos://alerts@example.com:secret@smtp.example.com:465/?to=ops@example.com&name=PD")
	if err != nil {
		t.Fatal(err)
	}
	if tgt.host != "smtp.example.com" || tgt.port != "465" || !tgt.implicitTLS {
		t.Errorf("server = %s:%s tls=%v", tgt.host, tgt.port, tgt.implicitTLS)
	}
	if tgt.username != "alerts@example.com" || tgt.password != "secret" {
		t.Errorf("auth = %q / %q", tgt.username, tgt.password)
	}
	if len(tgt.to) != 1 || tgt.to[0] != "ops@example.com" {
		t.Errorf("to = %v", tgt.to)
	}
	if tgt.from != "alerts@example.com" || tgt.fromName != "PD" {
		t.Errorf("from = %q name = %q", tgt.from, tgt.fromName)
	}
	// mailto:// defaults to STARTTLS port 587.
	d, err := parseEmail("mailto://u:p@smtp.example.com/?to=x@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if d.port != "587" || d.implicitTLS {
		t.Errorf("default port/tls = %s/%v", d.port, d.implicitTLS)
	}
}

func TestEmailMessageNoHeaderInjection(t *testing.T) {
	msg := string(buildEmailMessage("a@b.com", "Patch\r\nBcc: evil@x.com", []string{"c@d.com"}, "Patchdeck", "line1\nline2"))
	// The injected CRLF in the display name must not produce a standalone Bcc header line.
	header := msg[:strings.Index(msg, "\r\n\r\n")]
	for _, line := range strings.Split(header, "\r\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "bcc:") {
			t.Errorf("header injection not prevented — stray header line %q in:\n%s", line, header)
		}
	}
	if !strings.Contains(msg, "line1\r\nline2") {
		t.Errorf("body CRLF normalization failed")
	}
}

func TestUnsupportedScheme(t *testing.T) {
	d := NewDispatcher(0)
	err := d.Send("twilio://sid:token@from/to", "hi")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected unsupported-scheme error, got %v", err)
	}
}

func TestEmptyTargetIsNoOp(t *testing.T) {
	if err := NewDispatcher(0).Send("   ", "hi"); err != nil {
		t.Errorf("empty target should be a no-op, got %v", err)
	}
}

func TestRuntimeInfo(t *testing.T) {
	ri := NewDispatcher(0).RuntimeInfo()
	if !ri.Available || ri.Engine != "native" || len(ri.SupportedSchemes) == 0 {
		t.Errorf("RuntimeInfo = %+v", ri)
	}
}
