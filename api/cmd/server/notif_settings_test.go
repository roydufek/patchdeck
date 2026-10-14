package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
)

// TestPutNotificationSettingsKeepsOmittedFields: an API client written before the digest
// options existed must not silently flip delivery back, and a bad delivery value is refused.
func TestPutNotificationSettingsKeepsOmittedFields(t *testing.T) {
	conn, err := sql.Open("sqlite", "file:notifput?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	a := &app{db: conn}
	if s, _ := db.GetNotificationSettings(conn); s.Delivery != models.DeliveryDigest || s.DigestAlways {
		t.Fatalf("fresh defaults: delivery=%q always=%v, want digest/false", s.Delivery, s.DigestAlways)
	}

	put := func(body string) int {
		rec := httptest.NewRecorder()
		a.putNotificationSettings(rec, httptest.NewRequest(http.MethodPut, "/api/settings/notifications", strings.NewReader(body)))
		return rec.Code
	}
	if c := put(`{"delivery":"per_host","digest_always":true}`); c != 200 {
		t.Fatalf("set per_host: %d", c)
	}
	if c := put(`{"updates_available":false}`); c != 200 { // an old client's partial payload
		t.Fatalf("partial put: %d", c)
	}
	s, _ := db.GetNotificationSettings(conn)
	if s.Delivery != models.DeliveryPerHost || !s.DigestAlways || s.UpdatesAvailable {
		t.Fatalf("after partial put: %+v", s)
	}
	if c := put(`{"delivery":"hourly"}`); c != 400 {
		t.Fatalf("bad delivery: want 400, got %d", c)
	}
}

// TestNotifCardDelivery: the settings card reflects the stored delivery mode and toggle.
func TestNotifCardDelivery(t *testing.T) {
	for _, tc := range []struct {
		s    models.NotificationSettings
		want []string
	}{
		{models.NotificationSettings{Delivery: models.DeliveryDigest}, []string{`value="digest" checked`, `value="per_host">`, `name="digest_always" value="1">`}},
		{models.NotificationSettings{Delivery: models.DeliveryPerHost, DigestAlways: true}, []string{`value="digest">`, `value="per_host" checked`, `name="digest_always" value="1" checked`}},
	} {
		var b strings.Builder
		if err := nextTmpl.ExecuteTemplate(&b, "notifcard", map[string]any{"S": tc.s}); err != nil {
			t.Fatal(err)
		}
		for _, w := range tc.want {
			if !strings.Contains(b.String(), w) {
				t.Errorf("delivery=%q: card lacks %s", tc.s.Delivery, w)
			}
		}
	}
}
