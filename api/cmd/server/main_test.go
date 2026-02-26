package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"

	"github.com/go-chi/chi/v5"
)

func TestValidateCronExpression(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "valid all wildcard", expr: "* * * * *", want: ""},
		{name: "valid mixed", expr: "*/5 0-23 1,15 * 1-5", want: ""},
		{name: "invalid field count", expr: "* * * *", want: "cron_expr must contain 5 fields (minute hour day month weekday) or a macro like @daily"},
		{name: "valid macro daily", expr: "@daily", want: ""},
		{name: "valid macro hourly uppercase", expr: "@HOURLY", want: ""},
		{name: "invalid minute", expr: "60 * * * *", want: "invalid minute field: value must be between 0 and 59"},
		{name: "invalid hour", expr: "* 24 * * *", want: "invalid hour field: value must be between 0 and 23"},
		{name: "invalid day", expr: "* * 0 * *", want: "invalid day field: value must be between 1 and 31"},
		{name: "invalid month", expr: "* * * 13 *", want: "invalid month field: value must be between 1 and 12"},
		{name: "valid weekday seven", expr: "* * * * 7", want: ""},
		{name: "invalid macro", expr: "@sometimes", want: "cron_expr must contain 5 fields (minute hour day month weekday) or a macro like @daily"},
		{name: "invalid step", expr: "*/0 * * * *", want: "invalid minute field: step must be a positive integer"},
		{name: "valid month name", expr: "0 4 * jan mon", want: ""},
		{name: "valid weekday range names", expr: "0 4 * * mon-fri", want: ""},
		{name: "valid full names", expr: "0 4 * january monday", want: ""},
		{name: "valid mixed full name range", expr: "0 4 * * monday-friday", want: ""},
		{name: "invalid descending range", expr: "* 10-2 * * *", want: "invalid hour field: range start cannot be greater than range end"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateCronExpression(tt.expr)
			if got != tt.want {
				t.Fatalf("validateCronExpression(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestValidateCronField(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		min     int
		max     int
		label   string
		wantErr bool
	}{
		{name: "wildcard", field: "*", min: 0, max: 59, label: "minute", wantErr: false},
		{name: "step", field: "*/15", min: 0, max: 59, label: "minute", wantErr: false},
		{name: "range", field: "10-20", min: 0, max: 59, label: "minute", wantErr: false},
		{name: "list", field: "1,15,30", min: 0, max: 59, label: "minute", wantErr: false},
		{name: "range with step", field: "10-20/2", min: 0, max: 59, label: "minute", wantErr: false},
		{name: "empty segment", field: "1,,3", min: 0, max: 59, label: "minute", wantErr: true},
		{name: "out of bounds", field: "99", min: 0, max: 59, label: "minute", wantErr: true},
		{name: "non integer", field: "abc", min: 0, max: 59, label: "minute", wantErr: true},
		{name: "bad range", field: "20-10", min: 0, max: 59, label: "minute", wantErr: true},
		{name: "bad range bounds", field: "a-b", min: 0, max: 59, label: "minute", wantErr: true},
		{name: "bad step", field: "*/x", min: 0, max: 59, label: "minute", wantErr: true},
		{name: "weekday name", field: "mon", min: 0, max: 7, label: "weekday", wantErr: false},
		{name: "weekday full name", field: "monday", min: 0, max: 7, label: "weekday", wantErr: false},
		{name: "month full name", field: "september", min: 1, max: 12, label: "month", wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCronField(tt.field, tt.min, tt.max, tt.label)
			if tt.wantErr && err == nil {
				t.Fatalf("validateCronField(%q) expected error, got nil", tt.field)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateCronField(%q) expected nil, got %v", tt.field, err)
			}
		})
	}
}

func TestValidateBootstrapRole(t *testing.T) {
	tests := []struct {
		name string
		role string
		want string
	}{
		{name: "admin allowed", role: "admin", want: ""},
		{name: "operator blocked for initial user", role: "operator", want: "bootstrap role must be admin"},
		{name: "viewer blocked for initial user", role: "viewer", want: "bootstrap role must be admin"},
		{name: "unknown rejected", role: "owner", want: "unsupported role"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateBootstrapRole(tt.role)
			if got != tt.want {
				t.Fatalf("validateBootstrapRole(%q) = %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

func TestValidateAppriseTarget(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		allowEmpty bool
		want       string
	}{
		{name: "required when empty", raw: "", allowEmpty: false, want: "apprise_url is required"},
		{name: "allow empty", raw: "", allowEmpty: true, want: ""},
		{name: "reject whitespace", raw: "gotify://token host", want: "apprise_url must not contain whitespace"},
		{name: "reject multiple delimiters", raw: "gotify://a,discord://b", want: "apprise_url must be a single destination URL for now"},
		{name: "accept mailto", raw: "mailto:ops@example.com", want: ""},
		{name: "reject missing scheme", raw: "discord.com/webhook", want: "apprise_url must look like an Apprise target URL (example: gotify://, discord://, mailto://)"},
		{name: "accept scheme", raw: "discord://webhook-id/token", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateAppriseTarget(tt.raw, tt.allowEmpty)
			if got != tt.want {
				t.Fatalf("validateAppriseTarget(%q, %v) = %q, want %q", tt.raw, tt.allowEmpty, got, tt.want)
			}
		})
	}
}

func TestNormalizeHostOperationalControls(t *testing.T) {
	manual := "manual"
	scheduled := "scheduled_apply"
	blank := "   "
	trueVal := true
	falseVal := false

	tests := []struct {
		name          string
		current       models.Host
		checksEnabled *bool
		autoPolicy    *string
		wantChecks    bool
		wantPolicy    string
	}{
		{name: "keep existing when omitted", current: models.Host{ChecksEnabled: true, AutoUpdatePolicy: "scheduled_apply"}, wantChecks: true, wantPolicy: "scheduled_apply"},
		{name: "default missing current policy to manual", current: models.Host{ChecksEnabled: true, AutoUpdatePolicy: ""}, wantChecks: true, wantPolicy: "manual"},
		{name: "apply explicit checks only", current: models.Host{ChecksEnabled: true, AutoUpdatePolicy: "manual"}, checksEnabled: &falseVal, wantChecks: false, wantPolicy: "manual"},
		{name: "apply explicit policy only", current: models.Host{ChecksEnabled: false, AutoUpdatePolicy: "manual"}, autoPolicy: &scheduled, wantChecks: false, wantPolicy: "scheduled_apply"},
		{name: "normalize case and spaces", current: models.Host{ChecksEnabled: false, AutoUpdatePolicy: "manual"}, checksEnabled: &trueVal, autoPolicy: &manual, wantChecks: true, wantPolicy: "manual"},
		{name: "preserve empty explicit policy for validator", current: models.Host{ChecksEnabled: true, AutoUpdatePolicy: "manual"}, autoPolicy: &blank, wantChecks: true, wantPolicy: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotChecks, gotPolicy := normalizeHostOperationalControls(tt.checksEnabled, tt.autoPolicy, tt.current)
			if gotChecks != tt.wantChecks || gotPolicy != tt.wantPolicy {
				t.Fatalf("normalizeHostOperationalControls(...) = (%v, %q), want (%v, %q)", gotChecks, gotPolicy, tt.wantChecks, tt.wantPolicy)
			}
		})
	}
}

func TestValidateJobModeAgainstHostControls(t *testing.T) {
	tests := []struct {
		name string
		host models.Host
		mode string
		want string
	}{
		{name: "scan allowed when checks disabled", host: models.Host{ChecksEnabled: false, AutoUpdatePolicy: "manual"}, mode: "scan", want: ""},
		{name: "apply blocked when checks disabled", host: models.Host{ChecksEnabled: false, AutoUpdatePolicy: "scheduled_apply"}, mode: "apply", want: "cannot schedule apply job while host checks are disabled"},
		{name: "apply blocked when policy manual", host: models.Host{ChecksEnabled: true, AutoUpdatePolicy: "manual"}, mode: "apply", want: "cannot schedule apply job while host auto_update_policy is manual"},
		{name: "apply allowed for scheduled_apply", host: models.Host{ChecksEnabled: true, AutoUpdatePolicy: "scheduled_apply"}, mode: "apply", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateJobModeAgainstHostControls(tt.host, tt.mode)
			if got != tt.want {
				t.Fatalf("validateJobModeAgainstHostControls(%+v, %q) = %q, want %q", tt.host, tt.mode, got, tt.want)
			}
		})
	}
}

func newHostKeyTestApp(t *testing.T, trustMode, trusted, pinned string) (*app, models.Host, *sql.DB) {
	t.Helper()
	database, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("sql open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	a := &app{db: database}
	_, err = db.CreateHost(database, models.Host{
		Name:             "alpha-host",
		Address:          "10.0.0.10",
		Port:             22,
		SSHUser:          "root",
		AuthType:         "password",
		SecretCipher:     "cipher",
		HostKeyRequired:  true,
		HostKeyTrustMode: trustMode,
		HostKeyTrusted:   trusted,
		HostKeyPinned:    pinned,
	})
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	hosts, err := db.ListHosts(database)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("list hosts: %v len=%d", err, len(hosts))
	}
	return a, hosts[0], database
}

func TestVerifyHostKeyTOFUFirstTrustAndMismatchBlock(t *testing.T) {
	a, host, database := newHostKeyTestApp(t, "tofu", "", "")
	defer database.Close()

	decision := a.verifyHostKey(host, "SHA256:first")
	if !decision.Allow {
		t.Fatalf("expected first trust to allow, got blocked: %+v", decision)
	}

	host, _ = db.GetHost(database, host.ID)
	if host.HostKeyTrusted != "SHA256:first" {
		t.Fatalf("expected trusted fingerprint to be persisted, got %q", host.HostKeyTrusted)
	}

	blocked := a.verifyHostKey(host, "SHA256:rotated")
	if blocked.Allow {
		t.Fatalf("expected mismatch to block")
	}
	if !strings.Contains(blocked.Reason, "possible MITM") {
		t.Fatalf("expected MITM warning reason, got %q", blocked.Reason)
	}

	host, _ = db.GetHost(database, host.ID)
	if host.HostKeyPending != "SHA256:rotated" {
		t.Fatalf("expected pending fingerprint to be set, got %q", host.HostKeyPending)
	}
	events, err := db.ListHostKeyAuditEvents(database, host.ID, 10)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(events) == 0 || events[0].Event != "host_key_mismatch_blocked" {
		t.Fatalf("expected latest audit event host_key_mismatch_blocked, got %+v", events)
	}
}

func TestAcceptHostKeyFingerprintRequiresPendingAndAudits(t *testing.T) {
	a, host, database := newHostKeyTestApp(t, "tofu", "SHA256:old", "")
	defer database.Close()
	if err := db.SetHostKeyPendingFingerprint(database, host.ID, "SHA256:new"); err != nil {
		t.Fatalf("set pending: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/hosts/"+host.ID+"/host-key/accept", strings.NewReader(`{"note":"maintenance rotation"}`))
	rec := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", host.ID)
	req = req.WithContext(contextWithRoute(req, rctx))
	a.acceptHostKeyFingerprint(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	host, _ = db.GetHost(database, host.ID)
	if host.HostKeyTrusted != "SHA256:new" || host.HostKeyPending != "" {
		t.Fatalf("expected accepted fingerprint promoted and pending cleared, host=%+v", host)
	}
	events, _ := db.ListHostKeyAuditEvents(database, host.ID, 5)
	if len(events) == 0 || events[0].Event != "host_key_rotation_accepted" {
		t.Fatalf("expected host_key_rotation_accepted audit event, got %+v", events)
	}
}

func contextWithRoute(req *http.Request, rctx *chi.Context) context.Context {
	ctx := req.Context()
	return context.WithValue(ctx, chi.RouteCtxKey, rctx)
}
