package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"patchdeck/api/internal/auth"
	"patchdeck/api/internal/config"
	"patchdeck/api/internal/crypto"
	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
	"patchdeck/api/internal/notify"
	"patchdeck/api/internal/ratelimit"
	"patchdeck/api/internal/rbac"
	"patchdeck/api/internal/scheduler"
	"patchdeck/api/internal/sshx"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

type app struct {
	cfg       config.Config
	db        *sql.DB
	secrets   *crypto.SealBox
	sshClient *sshx.Client
	notifier  *notify.Dispatcher
	sched     *scheduler.Engine
	limiter   *ratelimit.HostLimiter
	startTime time.Time
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	database, err := sql.Open("sqlite", cfg.DatabasePath)
	if err != nil {
		log.Fatalf("sqlite open: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	seal, err := crypto.NewSealBox(cfg.MasterKey)
	if err != nil {
		log.Fatalf("master key: %v", err)
	}

	sshClient := sshx.NewClient(cfg.SSHTimeout, nil)
	notifier := notify.NewDispatcher(cfg.AppriseBinPath, cfg.AppriseTimeout)
	notifierRuntime := notifier.RuntimeInfo()
	if notifierRuntime.Available {
		log.Printf("notifications: apprise runtime ready (%s %s)", notifierRuntime.BinPath, notifierRuntime.Version)
	} else {
		log.Printf("notifications: apprise runtime unavailable (%s): %s", notifierRuntime.BinPath, notifierRuntime.Error)
	}

	a := &app{
		cfg:       cfg,
		db:        database,
		secrets:   seal,
		sshClient: sshClient,
		notifier:  notifier,
		limiter:   ratelimit.NewHostLimiter(30 * time.Second),
		startTime: time.Now(),
	}
	a.sshClient = sshx.NewClient(cfg.SSHTimeout, a.verifyHostKey)
	a.sched = scheduler.NewEngine(database, a.sshClient, seal, notifier, cfg.AppriseURL)

	r := chi.NewRouter()

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": "patchdeck", "version": cfg.AppVersion})
	})
	r.Get("/api/health", a.health)

	r.Get("/api/setup", a.setupStatus)
	r.Post("/api/bootstrap", a.bootstrap)
	r.Post("/api/login", a.login)

	r.Group(func(pr chi.Router) {
		pr.Use(a.authMiddleware)
		pr.Get("/api/me", a.me)
		pr.Get("/api/hosts", a.listHosts)
		pr.Get("/api/hosts/connectivity", a.hostConnectivity)
		pr.Post("/api/hosts", a.createHost)
		pr.Put("/api/hosts/{id}", a.updateHost)
		pr.Delete("/api/hosts/{id}", a.deleteHost)
		pr.Get("/api/scans", a.listScans)
		pr.Post("/api/hosts/{id}/scan", a.scanHost)
		pr.Post("/api/hosts/{id}/apply", a.applyUpdates)
		pr.Post("/api/hosts/{id}/restart-services", a.restartServices)
		pr.Post("/api/hosts/{id}/power", a.powerAction)
		pr.Get("/api/jobs", a.listJobs)
		pr.Post("/api/jobs", a.createJob)
		pr.Post("/api/jobs/{id}/enabled", a.setJobEnabled)
		pr.Delete("/api/jobs/{id}", a.deleteJob)
		pr.Get("/api/settings/notifications", a.getNotificationSettings)
		pr.Get("/api/settings/notifications/runtime", a.getNotificationRuntime)
		pr.Put("/api/settings/notifications", a.putNotificationSettings)
		pr.Post("/api/settings/notifications/test", a.testNotificationSettings)
		pr.Get("/api/settings/audit", a.getAuditRetention)
		pr.Put("/api/settings/audit", a.putAuditRetention)
		pr.Get("/api/activity/export", a.exportActivity)
		pr.Put("/api/hosts/{id}/notifications", a.putHostNotificationPrefs)
		pr.Put("/api/hosts/{id}/operations", a.putHostOperationalControls)
		pr.Put("/api/hosts/{id}/host-key-policy", a.putHostKeyPolicy)
		pr.Get("/api/hosts/{id}/host-key-audit", a.listHostKeyAudit)
		pr.Post("/api/hosts/{id}/host-key/accept", a.acceptHostKeyFingerprint)
		pr.Post("/api/hosts/{id}/host-key/deny", a.denyHostKeyFingerprint)
		pr.Get("/api/hosts/{id}/scans", a.listScanHistory)
		pr.Get("/api/tags", a.listTags)
		pr.Post("/api/hosts/scan-all", a.scanAllHosts)
		pr.Get("/api/settings/tokens", a.listAPITokens)
		pr.Post("/api/settings/tokens", a.createAPIToken)
		pr.Delete("/api/settings/tokens/{id}", a.revokeAPIToken)
		pr.Get("/api/activity", a.listActivity)
	})

	// SSE streaming endpoints — use flexible auth (header OR query param token)
	r.Group(func(sr chi.Router) {
		sr.Use(a.authMiddlewareFlexible)
		sr.Get("/api/hosts/{id}/scan/stream", a.scanHostStream)
		sr.Get("/api/hosts/{id}/apply/stream", a.applyStream)
		sr.Get("/api/hosts/{id}/await-recovery", a.awaitRecovery)
	})

	// Serve static frontend files (SPA)
	staticDir := os.Getenv("PATCHDECK_STATIC_DIR")
	if staticDir == "" {
		staticDir = "/app/static"
	}
	if info, err := os.Stat(staticDir); err == nil && info.IsDir() {
		log.Printf("serving static files from %s", staticDir)
		fsys := http.Dir(staticDir)
		fileServer := http.FileServer(fsys)
		r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
			// Try to serve the file directly; if not found, serve index.html (SPA fallback)
			path := req.URL.Path
			if f, err := fsys.Open(path); err == nil {
				f.Close()
				fileServer.ServeHTTP(w, req)
				return
			}
			// SPA fallback: serve index.html for client-side routing
			if idx, err := fs.Stat(os.DirFS(staticDir), "index.html"); err == nil && !idx.IsDir() {
				req.URL.Path = "/"
				fileServer.ServeHTTP(w, req)
				return
			}
			http.NotFound(w, req)
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.sched.Run(ctx)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("patchdeck API listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
}

func (a *app) setupStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"bootstrap_required":   !db.HasUsers(a.db),
		"supported_roles":      rbac.SupportedRoles(),
		"bootstrap_roles":      []string{"admin"},
		"totp_optional":        true,
		"registration_enabled": a.cfg.RegistrationEnabled,
	})
}

func validateBootstrapRole(role string) string {
	if !rbac.IsSupportedRole(role) {
		return "unsupported role"
	}
	if role != "admin" {
		return "bootstrap role must be admin"
	}
	return ""
}

func (a *app) bootstrap(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.RegistrationEnabled {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Registration is disabled"})
		return
	}
	if db.HasUsers(a.db) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "bootstrap already completed"})
		return
	}
	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		Role       string `json:"role"`
		EnableTOTP *bool  `json:"enable_totp"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Role = strings.TrimSpace(strings.ToLower(req.Role))
	if req.Username == "" || len(req.Password) < 12 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username required and password must be >=12 chars"})
		return
	}
	if req.Role == "" {
		req.Role = "admin"
	}
	if errMsg := validateBootstrapRole(req.Role); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to process password"})
		return
	}
	enableTOTP := true
	if req.EnableTOTP != nil {
		enableTOTP = *req.EnableTOTP
	}

	secret := ""
	uri := ""
	if enableTOTP {
		secret, uri, err = auth.NewTOTPSecret("Patchdeck", req.Username)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to generate TOTP secret"})
			return
		}
	}
	if err := db.CreateInitialUser(a.db, req.Username, req.Role, hash, secret); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create admin account"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"otpauth": uri, "totp_enabled": enableTOTP, "role": req.Role, "message": "bootstrap complete"})
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	user, err := db.GetUserByUsername(a.db, req.Username)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	if strings.TrimSpace(user.TOTPSecret) != "" && !auth.ValidateTOTP(user.TOTPSecret, strings.TrimSpace(req.Code)) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	token, err := auth.SignJWT(a.cfg.JWTSecret, user.ID, user.Username, user.Role, 12*time.Hour)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to generate session token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (a *app) me(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	writeJSON(w, http.StatusOK, claims)
}

func (a *app) listHosts(w http.ResponseWriter, _ *http.Request) {
	hosts, err := db.ListHosts(a.db)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to load hosts"})
		return
	}
	writeJSON(w, http.StatusOK, hosts)
}

type hostConnectivityStatus struct {
	HostID      string `json:"host_id"`
	Connected   bool   `json:"connected"`
	CheckedAt   string `json:"checked_at"`
	Error       string `json:"error,omitempty"`
	Source      string `json:"source"`
	TimeoutSecs int    `json:"timeout_seconds"`
}

func (a *app) hostConnectivity(w http.ResponseWriter, _ *http.Request) {
	hosts, err := db.ListHosts(a.db)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to load hosts for connectivity check"})
		return
	}

	quickTimeout := 5 * time.Second
	checker := sshx.NewClient(quickTimeout, a.verifyHostKey)
	results := make([]hostConnectivityStatus, len(hosts))

	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, host := range hosts {
		wg.Add(1)
		go func(i int, host models.Host) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res := hostConnectivityStatus{
				HostID:      host.ID,
				CheckedAt:   time.Now().UTC().Format(time.RFC3339),
				Source:      "ssh_quick_check",
				TimeoutSecs: int(quickTimeout.Seconds()),
			}

			hostWithSecrets, err := db.GetHost(a.db, host.ID)
			if err != nil {
				res.Connected = false
				res.Error = "unable to load host secrets"
				results[i] = res
				return
			}

			if err := checker.CheckConnectivity(hostWithSecrets, a.secrets); err != nil {
				res.Connected = false
				res.Error = strings.TrimSpace(err.Error())
			} else {
				res.Connected = true
			}
			results[i] = res
		}(i, host)
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, results)
}

func (a *app) listScans(w http.ResponseWriter, _ *http.Request) {
	scans, err := db.ListScanSnapshots(a.db)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to load scan results"})
		return
	}
	writeJSON(w, http.StatusOK, scans)
}

type hostUpsertRequest struct {
	Name             string   `json:"name"`
	Address          string   `json:"address"`
	Port             int      `json:"port"`
	SSHUser          string   `json:"ssh_user"`
	AuthType         string   `json:"auth_type"`
	Password         string   `json:"password"`
	PrivateKeyPEM    string   `json:"private_key_pem"`
	Passphrase       string   `json:"passphrase"`
	SudoPassword     string   `json:"sudo_password"`
	HostKeyRequired  *bool    `json:"host_key_required"`
	HostKeyTrustMode string   `json:"host_key_trust_mode"`
	HostKeyPinned    string   `json:"host_key_pinned_fingerprint"`
	Tags             []string `json:"tags"`
}

func normalizeHostRequest(req *hostUpsertRequest) {
	req.Name = strings.TrimSpace(req.Name)
	req.Address = strings.TrimSpace(req.Address)
	req.SSHUser = strings.TrimSpace(req.SSHUser)
	req.AuthType = strings.TrimSpace(req.AuthType)
	req.Password = strings.TrimSpace(req.Password)
	req.PrivateKeyPEM = strings.TrimSpace(req.PrivateKeyPEM)
	req.Passphrase = strings.TrimSpace(req.Passphrase)
	req.SudoPassword = strings.TrimSpace(req.SudoPassword)
	req.HostKeyTrustMode = strings.TrimSpace(strings.ToLower(req.HostKeyTrustMode))
	req.HostKeyPinned = strings.TrimSpace(req.HostKeyPinned)
	if req.Port == 0 {
		req.Port = 22
	}
	if req.AuthType == "" {
		req.AuthType = "key"
	}
	if req.HostKeyTrustMode == "" {
		req.HostKeyTrustMode = "tofu"
	}
}

func validateHostRequest(req hostUpsertRequest) string {
	if req.Port < 1 || req.Port > 65535 {
		return "port must be between 1 and 65535"
	}
	if req.HostKeyRequired != nil && !*req.HostKeyRequired {
		return "host_key_required cannot be false in alpha; host key verification is mandatory"
	}
	if req.Name == "" || req.Address == "" || req.SSHUser == "" {
		return "name,address,ssh_user required"
	}
	if req.AuthType != "password" && req.AuthType != "key" {
		return "auth_type must be password|key"
	}
	if req.AuthType == "password" && req.Password == "" {
		return "password required when auth_type=password"
	}
	if req.AuthType == "key" && req.PrivateKeyPEM == "" {
		return "private_key_pem required when auth_type=key"
	}
	if req.HostKeyTrustMode != "tofu" && req.HostKeyTrustMode != "pinned" {
		return "host_key_trust_mode must be tofu|pinned"
	}
	if req.HostKeyTrustMode == "pinned" && req.HostKeyPinned == "" {
		return "host_key_pinned_fingerprint required when host_key_trust_mode=pinned"
	}
	return ""
}

func encryptHostSecrets(secrets *crypto.SealBox, req hostUpsertRequest) (string, error) {
	secretBlob := map[string]string{"password": req.Password, "private_key_pem": req.PrivateKeyPEM, "passphrase": req.Passphrase, "sudo_password": req.SudoPassword}
	b, _ := json.Marshal(secretBlob)
	return secrets.Encrypt(b)
}

func decodeHostSecrets(secrets *crypto.SealBox, cipher string) (map[string]string, error) {
	if cipher == "" {
		return map[string]string{}, nil
	}
	plain, err := secrets.Decrypt(cipher)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if len(plain) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *app) createHost(w http.ResponseWriter, r *http.Request) {
	var req hostUpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	normalizeHostRequest(&req)
	if errMsg := validateHostRequest(req); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}
	if req.AuthType == "password" {
		req.PrivateKeyPEM = ""
		req.Passphrase = ""
	}
	if req.AuthType == "key" {
		req.Password = ""
	}
	enc, err := encryptHostSecrets(a.secrets, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to encrypt host credentials"})
		return
	}
	h := models.Host{Name: req.Name, Address: req.Address, Port: req.Port, SSHUser: req.SSHUser, AuthType: req.AuthType, SecretCipher: enc, ChecksEnabled: true, AutoUpdatePolicy: "manual", HostKeyRequired: true, HostKeyTrustMode: req.HostKeyTrustMode, HostKeyPinned: req.HostKeyPinned}
	hostID, err := db.CreateHost(a.db, h)
	if err != nil {
		if errors.Is(err, db.ErrHostExists) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "host with same address/port/ssh_user already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save host"})
		return
	}
	if req.Tags != nil {
		_ = db.UpdateHostTags(a.db, hostID, req.Tags)
	}
	_ = db.RecordActivity(a.db, hostID, req.Name, "host_added", fmt.Sprintf("Host %s added (%s@%s:%d)", req.Name, req.SSHUser, req.Address, req.Port))
	writeJSON(w, http.StatusCreated, map[string]string{"message": "host added", "id": hostID})
}

func (a *app) updateHost(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := db.GetHost(a.db, id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "host not found"})
		return
	}
	var req hostUpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Address = strings.TrimSpace(req.Address)
	req.SSHUser = strings.TrimSpace(req.SSHUser)
	req.AuthType = strings.TrimSpace(req.AuthType)
	req.Password = strings.TrimSpace(req.Password)
	req.PrivateKeyPEM = strings.TrimSpace(req.PrivateKeyPEM)
	req.Passphrase = strings.TrimSpace(req.Passphrase)
	req.SudoPassword = strings.TrimSpace(req.SudoPassword)
	req.HostKeyTrustMode = strings.TrimSpace(strings.ToLower(req.HostKeyTrustMode))
	req.HostKeyPinned = strings.TrimSpace(req.HostKeyPinned)
	if req.Name == "" {
		req.Name = existing.Name
	}
	if req.Address == "" {
		req.Address = existing.Address
	}
	if req.Port == 0 {
		req.Port = existing.Port
	}
	if req.SSHUser == "" {
		req.SSHUser = existing.SSHUser
	}
	if req.AuthType == "" {
		req.AuthType = existing.AuthType
	}

	storedSecrets, err := decodeHostSecrets(a.secrets, existing.SecretCipher)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read existing host credentials"})
		return
	}
	if req.Password == "" {
		req.Password = storedSecrets["password"]
	}
	if req.PrivateKeyPEM == "" {
		req.PrivateKeyPEM = storedSecrets["private_key_pem"]
	}
	if req.Passphrase == "" {
		req.Passphrase = storedSecrets["passphrase"]
	}
	if req.SudoPassword == "" {
		req.SudoPassword = storedSecrets["sudo_password"]
	}
	if req.HostKeyTrustMode == "" {
		req.HostKeyTrustMode = existing.HostKeyTrustMode
	}
	if req.HostKeyTrustMode == "" {
		req.HostKeyTrustMode = "tofu"
	}
	if req.HostKeyPinned == "" {
		req.HostKeyPinned = existing.HostKeyPinned
	}

	if errMsg := validateHostRequest(req); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}
	if req.AuthType == "password" {
		req.PrivateKeyPEM = ""
		req.Passphrase = ""
	}
	if req.AuthType == "key" {
		req.Password = ""
	}
	enc, err := encryptHostSecrets(a.secrets, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to encrypt host credentials"})
		return
	}
	updated := models.Host{ID: id, Name: req.Name, Address: req.Address, Port: req.Port, SSHUser: req.SSHUser, AuthType: req.AuthType, SecretCipher: enc}
	if err := db.UpdateHost(a.db, updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "host not found"})
			return
		}
		if errors.Is(err, db.ErrHostExists) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "host with same address/port/ssh_user already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to update host"})
		return
	}
	if err := db.UpdateHostKeyPolicy(a.db, id, true, req.HostKeyTrustMode, req.HostKeyPinned); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to update host key policy"})
		return
	}
	if req.Tags != nil {
		_ = db.UpdateHostTags(a.db, id, req.Tags)
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "host updated"})
}

func (a *app) deleteHost(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	host, _ := db.GetHost(a.db, id)
	if err := db.DeleteHost(a.db, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 404, map[string]string{"error": "host not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": "Failed to delete host"})
		return
	}
	_ = db.RecordActivity(a.db, id, host.Name, "host_deleted", fmt.Sprintf("Host %s deleted", host.Name))
	writeJSON(w, 200, map[string]string{"message": "host deleted"})
}

func (a *app) scanHost(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if ok, retryAfter := a.limiter.Allow(id); !ok {
		writeJSON(w, 429, map[string]any{"error": "rate limited", "retry_after_seconds": retryAfter})
		return
	}
	host, err := db.GetHost(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}
	force := parseBoolQuery(r.URL.Query().Get("force"))
	if !host.ChecksEnabled && !force {
		writeJSON(w, 409, map[string]string{"error": "host checks are disabled for this host; enable checks or retry with force=true"})
		return
	}
	res, err := a.sshClient.ScanHost(host, a.secrets)
	if err != nil {
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			if a.notificationEnabledForHostEvent(host.ID, "scan_failure") {
				_ = a.notifier.Send(a.currentAppriseURL(), fmt.Sprintf("Patchdeck: scan FAILED on %s (%s)", host.Name, hkErr.Message))
			}
			_ = db.RecordActivity(a.db, host.ID, host.Name, "scan_fail", fmt.Sprintf("Scan failed: %s", hkErr.Message))
			writeJSON(w, 409, map[string]any{"error": hkErr.Message, "code": "host_key_mismatch", "operator_action_required": true, "expected_fingerprint": hkErr.ExpectedFingerprint, "presented_fingerprint": hkErr.PresentedFingerprint, "actions": []string{"accept_new_fingerprint", "deny_new_fingerprint"}})
			return
		}
		if a.notificationEnabledForHostEvent(host.ID, "scan_failure") {
			_ = a.notifier.Send(a.currentAppriseURL(), fmt.Sprintf("Patchdeck: scan FAILED on %s (%v)", host.Name, err))
		}
		_ = db.RecordActivity(a.db, host.ID, host.Name, "scan_fail", fmt.Sprintf("Scan failed: %v", err))
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	_ = db.UpsertScanResult(a.db, host.ID, res)
	_ = db.RecordActivity(a.db, host.ID, host.Name, "scan_ok", fmt.Sprintf("Scan completed: %d packages available", len(res.Packages)))
	if len(res.Packages) > 0 && a.notificationEnabledForHostEvent(host.ID, "updates_available") {
		_ = a.notifier.Send(a.currentAppriseURL(), fmt.Sprintf("Patchdeck: updates available on %s (%d packages)", host.Name, len(res.Packages)))
	}
	writeJSON(w, 200, res)
}

func (a *app) applyUpdates(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if ok, retryAfter := a.limiter.Allow(id); !ok {
		writeJSON(w, 429, map[string]any{"error": "rate limited", "retry_after_seconds": retryAfter})
		return
	}
	host, err := db.GetHost(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}
	force := parseBoolQuery(r.URL.Query().Get("force"))
	if !host.ChecksEnabled && !force {
		writeJSON(w, 409, map[string]string{"error": "host checks are disabled for this host; enable checks or retry with force=true"})
		return
	}
	res, err := a.sshClient.ApplyUpdates(host, a.secrets)
	if err != nil {
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			writeJSON(w, 409, map[string]any{"error": hkErr.Message, "code": "host_key_mismatch", "operator_action_required": true, "expected_fingerprint": hkErr.ExpectedFingerprint, "presented_fingerprint": hkErr.PresentedFingerprint, "actions": []string{"accept_new_fingerprint", "deny_new_fingerprint"}})
			return
		}
		if a.notificationEnabledForHostEvent(host.ID, "auto_apply_failure") {
			_ = a.notifier.Send(a.currentAppriseURL(), fmt.Sprintf("Patchdeck: apply FAILED on %s (%v)", host.Name, err))
		}
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if a.notificationEnabledForHostEvent(host.ID, "auto_apply_success") {
		_ = a.notifier.Send(a.currentAppriseURL(), fmt.Sprintf("Patchdeck: updates applied on %s. %d packages changed", host.Name, res.ChangedPackages))
	}
	_ = db.RecordActivity(a.db, host.ID, host.Name, "apply_ok", fmt.Sprintf("Applied updates: %d packages changed", res.ChangedPackages))
	writeJSON(w, 200, res)
}

func (a *app) restartServices(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if ok, retryAfter := a.limiter.Allow(id); !ok {
		writeJSON(w, 429, map[string]any{"error": "rate limited", "retry_after_seconds": retryAfter})
		return
	}
	host, err := db.GetHost(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}
	var req struct {
		Services []string `json:"services"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := a.sshClient.RestartServices(host, a.secrets, req.Services)
	if err != nil {
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			writeJSON(w, 409, map[string]any{"error": hkErr.Message, "code": "host_key_mismatch", "operator_action_required": true, "expected_fingerprint": hkErr.ExpectedFingerprint, "presented_fingerprint": hkErr.PresentedFingerprint, "actions": []string{"accept_new_fingerprint", "deny_new_fingerprint"}})
			return
		}
		_ = db.RecordActivity(a.db, host.ID, host.Name, "restart_fail", fmt.Sprintf("Service restart failed: %v", err))
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	_ = db.RecordActivity(a.db, host.ID, host.Name, "restart_ok", fmt.Sprintf("Restarted %d service(s): %s", len(req.Services), strings.Join(req.Services, ", ")))
	writeJSON(w, 200, res)
}

func (a *app) listActivity(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0
	hostID := strings.TrimSpace(r.URL.Query().Get("host_id"))
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			offset = n
		}
	}
	entries, err := db.ListActivity(a.db, limit, offset, hostID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to load activity log"})
		return
	}
	writeJSON(w, 200, entries)
}

func (a *app) getAuditRetention(w http.ResponseWriter, _ *http.Request) {
	days, err := db.GetAuditRetentionDays(a.db)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to load audit retention setting"})
		return
	}
	writeJSON(w, 200, map[string]any{"retention_days": days})
}

func (a *app) putAuditRetention(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RetentionDays int `json:"retention_days"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RetentionDays != 0 && req.RetentionDays < 30 {
		writeJSON(w, 400, map[string]string{"error": "Minimum retention is 30 days. Use 0 for unlimited."})
		return
	}
	if err := db.SetAuditRetentionDays(a.db, req.RetentionDays); err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to save audit retention setting"})
		return
	}
	writeJSON(w, 200, map[string]string{"message": "audit retention updated"})
}

func (a *app) exportActivity(w http.ResponseWriter, r *http.Request) {
	hostID := strings.TrimSpace(r.URL.Query().Get("host_id"))
	eventType := strings.TrimSpace(r.URL.Query().Get("event_type"))
	var from, to time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			from = t
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			to = t
		}
	}
	entries, err := db.ExportActivity(a.db, hostID, eventType, from, to)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to export activity log"})
		return
	}

	filename := "patchdeck-activity-" + time.Now().UTC().Format("2006-01-02") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(200)

	// CSV header
	fmt.Fprintf(w, "id,timestamp,host_id,host_name,event_type,summary\n")
	for _, e := range entries {
		// Escape CSV fields
		summary := strings.ReplaceAll(e.Summary, "\"", "\"\"")
		hostName := strings.ReplaceAll(e.HostName, "\"", "\"\"")
		fmt.Fprintf(w, "%d,%s,%s,\"%s\",%s,\"%s\"\n",
			e.ID,
			e.CreatedAt.UTC().Format(time.RFC3339),
			e.HostID,
			hostName,
			e.EventType,
			summary,
		)
	}
}

func (a *app) powerAction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if ok, retryAfter := a.limiter.Allow(id); !ok {
		writeJSON(w, 429, map[string]any{"error": "rate limited", "retry_after_seconds": retryAfter})
		return
	}
	host, err := db.GetHost(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Action != "reboot" && req.Action != "shutdown" {
		writeJSON(w, 400, map[string]string{"error": "action must be reboot|shutdown"})
		return
	}
	if err := a.sshClient.Power(host, a.secrets, req.Action); err != nil {
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			writeJSON(w, 409, map[string]any{"error": hkErr.Message, "code": "host_key_mismatch", "operator_action_required": true, "expected_fingerprint": hkErr.ExpectedFingerprint, "presented_fingerprint": hkErr.PresentedFingerprint, "actions": []string{"accept_new_fingerprint", "deny_new_fingerprint"}})
			return
		}
		_ = db.RecordActivity(a.db, host.ID, host.Name, req.Action+"_fail", fmt.Sprintf("%s failed: %v", strings.Title(req.Action), err))
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	_ = db.RecordActivity(a.db, host.ID, host.Name, req.Action+"_ok", fmt.Sprintf("%s initiated on %s", strings.Title(req.Action), host.Name))
	writeJSON(w, 200, map[string]string{"message": "ok"})
}

func (a *app) listJobs(w http.ResponseWriter, _ *http.Request) {
	jobs, err := db.ListJobs(a.db)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to load scheduled jobs"})
		return
	}
	writeJSON(w, 200, jobs)
}

func (a *app) createJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HostID    string   `json:"host_id"`
		HostIDs   []string `json:"host_ids"`
		TagFilter string   `json:"tag_filter"`
		Name      string   `json:"name"`
		CronExpr  string   `json:"cron_expr"`
		Mode      string   `json:"mode"`
		Enabled   *bool    `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.HostID = strings.TrimSpace(req.HostID)
	req.TagFilter = strings.TrimSpace(req.TagFilter)
	req.Name = strings.TrimSpace(req.Name)
	req.CronExpr = strings.TrimSpace(req.CronExpr)
	req.Mode = strings.TrimSpace(req.Mode)

	// Clean up host_ids
	var cleanHostIDs []string
	for _, id := range req.HostIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			cleanHostIDs = append(cleanHostIDs, id)
		}
	}

	if req.CronExpr == "" {
		writeJSON(w, 400, map[string]string{"error": "cron_expr required"})
		return
	}
	// Must have at least one targeting mechanism
	if req.HostID == "" && len(cleanHostIDs) == 0 && req.TagFilter == "" {
		writeJSON(w, 400, map[string]string{"error": "at least one of host_id, host_ids, or tag_filter is required"})
		return
	}
	if errMsg := validateCronExpression(req.CronExpr); errMsg != "" {
		writeJSON(w, 400, map[string]string{"error": errMsg})
		return
	}
	if req.Mode == "" {
		req.Mode = "scan"
	}
	if req.Mode != "scan" && req.Mode != "apply" && req.Mode != "scan_apply" {
		writeJSON(w, 400, map[string]string{"error": "mode must be scan|apply|scan_apply"})
		return
	}

	// Validate hosts exist (for single host_id or multi host_ids)
	if req.HostID != "" && len(cleanHostIDs) == 0 {
		// Legacy single-host mode
		host, err := db.GetHost(a.db, req.HostID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, 404, map[string]string{"error": "host not found"})
				return
			}
			writeJSON(w, 500, map[string]string{"error": "Failed to load host"})
			return
		}
		if req.Mode == "apply" || req.Mode == "scan_apply" {
			if errMsg := validateJobModeAgainstHostControls(host, "apply"); errMsg != "" {
				writeJSON(w, 409, map[string]string{"error": errMsg})
				return
			}
		}
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	j := models.Job{
		HostID:    req.HostID,
		HostIDs:   cleanHostIDs,
		TagFilter: req.TagFilter,
		Name:      req.Name,
		CronExpr:  req.CronExpr,
		Mode:      req.Mode,
		Enabled:   enabled,
	}
	if err := db.CreateJob(a.db, j); err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to create scheduled job"})
		return
	}
	writeJSON(w, 201, map[string]string{"message": "scheduled"})
}

func (a *app) setJobEnabled(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := db.UpdateJobEnabled(a.db, id, req.Enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 404, map[string]string{"error": "job not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": "Failed to update job"})
		return
	}
	writeJSON(w, 200, map[string]any{"message": "updated", "enabled": req.Enabled})
}

func (a *app) deleteJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := db.DeleteJob(a.db, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 404, map[string]string{"error": "job not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": "Failed to delete job"})
		return
	}
	writeJSON(w, 200, map[string]string{"message": "deleted"})
}

func (a *app) currentAppriseURL() string {
	s, err := db.GetNotificationSettings(a.db)
	if err == nil && strings.TrimSpace(s.AppriseURL) != "" {
		return s.AppriseURL
	}
	return a.cfg.AppriseURL
}

func (a *app) notificationEnabledForHostEvent(hostID, eventKey string) bool {
	s, err := db.GetNotificationSettings(a.db)
	if err != nil {
		return false
	}
	switch eventKey {
	case "updates_available":
		if !s.UpdatesAvailable {
			return false
		}
	case "auto_apply_success":
		if !s.AutoApplySuccess {
			return false
		}
	case "auto_apply_failure":
		if !s.AutoApplyFailure {
			return false
		}
	case "scan_failure":
		if !s.ScanFailure {
			return false
		}
	}
	prefs, err := db.GetHostNotificationPrefs(a.db, hostID)
	if err != nil {
		return false
	}
	switch eventKey {
	case "updates_available":
		return prefs.UpdatesAvailable
	case "auto_apply_success":
		return prefs.AutoApplySuccess
	case "auto_apply_failure":
		return prefs.AutoApplyFailure
	case "scan_failure":
		return prefs.ScanFailure
	default:
		return true
	}
}

func (a *app) getNotificationSettings(w http.ResponseWriter, _ *http.Request) {
	s, err := db.GetNotificationSettings(a.db)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to load notification settings"})
		return
	}
	if strings.TrimSpace(s.AppriseURL) == "" {
		s.AppriseURL = a.cfg.AppriseURL
	}
	writeJSON(w, 200, s)
}

func (a *app) getNotificationRuntime(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, a.notifier.RuntimeInfo())
}

func (a *app) ensureNotifierAvailableForTarget(target string) error {
	if strings.TrimSpace(target) == "" {
		return nil
	}
	runtime := a.notifier.RuntimeInfo()
	if runtime.Available {
		return nil
	}
	if runtime.Error == "" {
		return fmt.Errorf("apprise runtime unavailable at %s", runtime.BinPath)
	}
	return fmt.Errorf("apprise runtime unavailable at %s: %s", runtime.BinPath, runtime.Error)
}

func (a *app) putNotificationSettings(w http.ResponseWriter, r *http.Request) {
	var req models.NotificationSettings
	if !decodeJSON(w, r, &req) {
		return
	}
	if errMsg := validateAppriseTarget(req.AppriseURL, true); errMsg != "" {
		writeJSON(w, 400, map[string]string{"error": errMsg})
		return
	}
	if err := a.ensureNotifierAvailableForTarget(req.AppriseURL); err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	if err := db.UpsertNotificationSettings(a.db, req); err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to save notification settings"})
		return
	}
	writeJSON(w, 200, map[string]string{"message": "notification settings updated"})
}

func (a *app) testNotificationSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppriseURL string `json:"apprise_url"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	target := strings.TrimSpace(req.AppriseURL)
	if target == "" {
		target = a.currentAppriseURL()
	}
	if errMsg := validateAppriseTarget(target, false); errMsg != "" {
		writeJSON(w, 400, map[string]string{"error": errMsg})
		return
	}
	if err := a.ensureNotifierAvailableForTarget(target); err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	body := fmt.Sprintf("Patchdeck notification test from %s", time.Now().UTC().Format(time.RFC3339))
	if err := a.notifier.Send(target, body); err != nil {
		writeJSON(w, 502, map[string]string{"error": "Test notification failed to send — check your Apprise URL"})
		return
	}
	writeJSON(w, 200, map[string]string{"message": "test notification sent"})
}

func validateAppriseTarget(raw string, allowEmpty bool) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		if allowEmpty {
			return ""
		}
		return "apprise_url is required"
	}
	if len(value) > 2048 {
		return "apprise_url is too long"
	}
	if strings.ContainsAny(value, " \t\n\r") {
		return "apprise_url must not contain whitespace"
	}
	if strings.ContainsAny(value, ",;") {
		return "apprise_url must be a single destination URL for now"
	}
	if strings.HasPrefix(strings.ToLower(value), "mailto:") {
		return ""
	}
	if !strings.Contains(value, "://") {
		return "apprise_url must look like an Apprise target URL (example: gotify://, discord://, mailto://)"
	}
	return ""
}

func (a *app) putHostNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	hostID := chi.URLParam(r, "id")
	if _, err := db.GetHost(a.db, hostID); err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}
	var req models.HostNotificationPrefs
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := db.UpsertHostNotificationPrefs(a.db, hostID, req); err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to save host notification preferences"})
		return
	}
	writeJSON(w, 200, map[string]string{"message": "host notification preferences updated"})
}

func (a *app) putHostOperationalControls(w http.ResponseWriter, r *http.Request) {
	hostID := chi.URLParam(r, "id")
	host, err := db.GetHost(a.db, hostID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 404, map[string]string{"error": "host not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": "Failed to load host"})
		return
	}
	var req struct {
		ChecksEnabled    *bool   `json:"checks_enabled"`
		AutoUpdatePolicy *string `json:"auto_update_policy"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	checksEnabled, policy := normalizeHostOperationalControls(req.ChecksEnabled, req.AutoUpdatePolicy, host)
	if policy == "" {
		writeJSON(w, 400, map[string]string{"error": "auto_update_policy must not be empty"})
		return
	}
	if policy != "manual" && policy != "scheduled_apply" {
		writeJSON(w, 400, map[string]string{"error": "auto_update_policy must be manual|scheduled_apply"})
		return
	}
	if err := db.UpdateHostOperationalControls(a.db, hostID, checksEnabled, policy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 404, map[string]string{"error": "host not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": "Failed to update host operational controls"})
		return
	}
	writeJSON(w, 200, map[string]string{"message": "host operational controls updated"})
}

func normalizeHostOperationalControls(checksEnabled *bool, autoUpdatePolicy *string, current models.Host) (bool, string) {
	nextChecksEnabled := current.ChecksEnabled
	if checksEnabled != nil {
		nextChecksEnabled = *checksEnabled
	}
	nextPolicy := strings.ToLower(strings.TrimSpace(current.AutoUpdatePolicy))
	if nextPolicy == "" {
		nextPolicy = "manual"
	}
	if autoUpdatePolicy != nil {
		nextPolicy = strings.ToLower(strings.TrimSpace(*autoUpdatePolicy))
	}
	return nextChecksEnabled, nextPolicy
}

func (a *app) verifyHostKey(host models.Host, presentedFingerprint string) sshx.HostKeyDecision {
	fp := strings.TrimSpace(presentedFingerprint)
	if fp == "" {
		return sshx.HostKeyDecision{Allow: false, Reason: "empty host key fingerprint from server", PresentedFingerprint: fp}
	}
	mode := strings.ToLower(strings.TrimSpace(host.HostKeyTrustMode))
	if mode == "" {
		mode = "tofu"
	}
	trusted := strings.TrimSpace(host.HostKeyTrusted)
	pinned := strings.TrimSpace(host.HostKeyPinned)

	if mode == "pinned" {
		if pinned == "" {
			_ = db.SetHostKeyPendingFingerprint(a.db, host.ID, fp)
			_ = db.RecordHostKeyAudit(a.db, host.ID, "host_key_mismatch_blocked", trusted, fp, "pinned mode configured without fingerprint")
			return sshx.HostKeyDecision{Allow: false, Reason: "Host key verification blocked connection: pinned mode requires a fingerprint. Set or accept fingerprint explicitly.", PresentedFingerprint: fp}
		}
		if pinned != fp {
			_ = db.SetHostKeyPendingFingerprint(a.db, host.ID, fp)
			_ = db.RecordHostKeyAudit(a.db, host.ID, "host_key_mismatch_blocked", pinned, fp, "presented fingerprint does not match pinned fingerprint")
			return sshx.HostKeyDecision{Allow: false, Reason: "Host key mismatch detected (possible MITM). Connection blocked until operator accepts or denies new fingerprint.", ExpectedFingerprint: pinned, PresentedFingerprint: fp}
		}
		_ = db.TrustHostKeyFirstUse(a.db, host.ID, fp)
		_ = db.MarkHostKeyVerified(a.db, host.ID)
		return sshx.HostKeyDecision{Allow: true, PresentedFingerprint: fp}
	}

	if trusted == "" {
		_ = db.TrustHostKeyFirstUse(a.db, host.ID, fp)
		_ = db.RecordHostKeyAudit(a.db, host.ID, "host_key_first_trust", "", fp, "tofu first successful trust")
		return sshx.HostKeyDecision{Allow: true, PresentedFingerprint: fp}
	}
	if trusted != fp {
		_ = db.SetHostKeyPendingFingerprint(a.db, host.ID, fp)
		_ = db.RecordHostKeyAudit(a.db, host.ID, "host_key_mismatch_blocked", trusted, fp, "tofu mismatch blocked pending operator decision")
		return sshx.HostKeyDecision{Allow: false, Reason: "Host key mismatch detected (possible MITM). Connection blocked until operator accepts or denies new fingerprint.", ExpectedFingerprint: trusted, PresentedFingerprint: fp}
	}
	_ = db.MarkHostKeyVerified(a.db, host.ID)
	return sshx.HostKeyDecision{Allow: true, PresentedFingerprint: fp}
}

func (a *app) putHostKeyPolicy(w http.ResponseWriter, r *http.Request) {
	hostID := chi.URLParam(r, "id")
	host, err := db.GetHost(a.db, hostID)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}
	var req struct {
		HostKeyRequired  *bool  `json:"host_key_required"`
		HostKeyTrustMode string `json:"host_key_trust_mode"`
		HostKeyPinned    string `json:"host_key_pinned_fingerprint"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.HostKeyRequired != nil && !*req.HostKeyRequired {
		writeJSON(w, 400, map[string]string{"error": "host_key_required cannot be false in alpha; host key verification is mandatory"})
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.HostKeyTrustMode))
	if mode == "" {
		mode = host.HostKeyTrustMode
	}
	if mode == "" {
		mode = "tofu"
	}
	pinned := strings.TrimSpace(req.HostKeyPinned)
	if pinned == "" {
		pinned = host.HostKeyPinned
	}
	if mode != "tofu" && mode != "pinned" {
		writeJSON(w, 400, map[string]string{"error": "host_key_trust_mode must be tofu|pinned"})
		return
	}
	if mode == "pinned" && pinned == "" {
		writeJSON(w, 400, map[string]string{"error": "host_key_pinned_fingerprint required when host_key_trust_mode=pinned"})
		return
	}
	if err := db.UpdateHostKeyPolicy(a.db, hostID, true, mode, pinned); err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to update host key policy"})
		return
	}
	if mode == "pinned" {
		_ = db.RecordHostKeyAudit(a.db, hostID, "host_key_policy_pinned", host.HostKeyPinned, pinned, "operator updated host key policy")
	}
	writeJSON(w, 200, map[string]string{"message": "host key policy updated"})
}

func (a *app) listHostKeyAudit(w http.ResponseWriter, r *http.Request) {
	hostID := chi.URLParam(r, "id")
	if _, err := db.GetHost(a.db, hostID); err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	events, err := db.ListHostKeyAuditEvents(a.db, hostID, limit)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to load host key audit trail"})
		return
	}
	writeJSON(w, 200, events)
}

func (a *app) acceptHostKeyFingerprint(w http.ResponseWriter, r *http.Request) {
	hostID := chi.URLParam(r, "id")
	host, err := db.GetHost(a.db, hostID)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if r.ContentLength > 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > 240 {
		writeJSON(w, 400, map[string]string{"error": "note must be 240 characters or fewer"})
		return
	}
	pending := strings.TrimSpace(host.HostKeyPending)
	if pending == "" {
		writeJSON(w, 409, map[string]string{"error": "no pending fingerprint to accept"})
		return
	}
	if err := db.AcceptHostKeyPendingFingerprint(a.db, hostID); err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to accept host key fingerprint"})
		return
	}
	auditNote := "operator accepted new host key fingerprint"
	if note != "" {
		auditNote = auditNote + ": " + note
	}
	_ = db.RecordHostKeyAudit(a.db, hostID, "host_key_rotation_accepted", host.HostKeyTrusted, pending, auditNote)
	writeJSON(w, 200, map[string]string{"message": "host key fingerprint accepted"})
}

func (a *app) denyHostKeyFingerprint(w http.ResponseWriter, r *http.Request) {
	hostID := chi.URLParam(r, "id")
	host, err := db.GetHost(a.db, hostID)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if r.ContentLength > 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > 240 {
		writeJSON(w, 400, map[string]string{"error": "note must be 240 characters or fewer"})
		return
	}
	pending := strings.TrimSpace(host.HostKeyPending)
	if pending == "" {
		writeJSON(w, 409, map[string]string{"error": "no pending fingerprint to deny"})
		return
	}
	if err := db.ClearHostKeyPendingFingerprint(a.db, hostID); err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to deny host key fingerprint"})
		return
	}
	auditNote := "operator denied new host key fingerprint"
	if note != "" {
		auditNote = auditNote + ": " + note
	}
	_ = db.RecordHostKeyAudit(a.db, hostID, "host_key_mismatch_denied", host.HostKeyTrusted, pending, auditNote)
	writeJSON(w, 200, map[string]string{"message": "pending host key fingerprint denied"})
}

func (a *app) health(w http.ResponseWriter, _ *http.Request) {
	hostsCount, _ := db.CountHosts(a.db)
	dbOK := a.db.Ping() == nil
	uptimeSeconds := int(time.Since(a.startTime).Seconds())
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"version":        a.cfg.AppVersion,
		"uptime_seconds": uptimeSeconds,
		"hosts_count":    hostsCount,
		"db_ok":          dbOK,
	})
}

func (a *app) listScanHistory(w http.ResponseWriter, r *http.Request) {
	hostID := chi.URLParam(r, "id")
	if _, err := db.GetHost(a.db, hostID); err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}
	history, err := db.ListScanHistory(a.db, hostID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to load scan history"})
		return
	}
	writeJSON(w, 200, history)
}

func (a *app) listTags(w http.ResponseWriter, _ *http.Request) {
	tags, err := db.ListAllTags(a.db)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to load tags"})
		return
	}
	writeJSON(w, 200, tags)
}

func (a *app) scanAllHosts(w http.ResponseWriter, _ *http.Request) {
	hosts, err := db.ListHosts(a.db)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to load hosts"})
		return
	}
	type scanAllResult struct {
		HostID        string `json:"host_id"`
		HostName      string `json:"host_name"`
		Success       bool   `json:"success"`
		PackagesCount int    `json:"packages_count,omitempty"`
		Error         string `json:"error,omitempty"`
	}
	var results []scanAllResult
	for _, host := range hosts {
		if !host.ChecksEnabled {
			continue
		}
		if ok, _ := a.limiter.Allow(host.ID); !ok {
			results = append(results, scanAllResult{HostID: host.ID, HostName: host.Name, Success: false, Error: "rate limited"})
			continue
		}
		fullHost, err := db.GetHost(a.db, host.ID)
		if err != nil {
			results = append(results, scanAllResult{HostID: host.ID, HostName: host.Name, Success: false, Error: err.Error()})
			continue
		}
		res, err := a.sshClient.ScanHost(fullHost, a.secrets)
		if err != nil {
			results = append(results, scanAllResult{HostID: host.ID, HostName: host.Name, Success: false, Error: err.Error()})
			continue
		}
		_ = db.UpsertScanResult(a.db, host.ID, res)
		results = append(results, scanAllResult{HostID: host.ID, HostName: host.Name, Success: true, PackagesCount: len(res.Packages)})
	}
	if results == nil {
		results = []scanAllResult{}
	}
	writeJSON(w, 200, results)
}

func (a *app) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := strings.TrimSpace(r.Header.Get("Authorization"))
		if authz == "" || !strings.HasPrefix(authz, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		// Try JWT first
		claims, err := auth.ParseJWT(a.cfg.JWTSecret, token)
		if err == nil {
			next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), claims)))
			return
		}
		// Try API token (pd_ prefix)
		if strings.HasPrefix(token, "pd_") {
			valid, _, dbErr := db.ValidateAPIToken(a.db, token)
			if dbErr != nil || !valid {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			// API token auth — use a synthetic claims with role=admin
			apiClaims := &auth.Claims{Username: "api-token", Role: "admin"}
			next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), apiClaims)))
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	})
}

func validateCronExpression(expr string) string {
	expr = strings.TrimSpace(expr)
	if isCronMacro(expr) {
		return ""
	}

	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return "cron_expr must contain 5 fields (minute hour day month weekday) or a macro like @daily"
	}
	limits := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	labels := []string{"minute", "hour", "day", "month", "weekday"}
	for i, field := range parts {
		if err := validateCronField(field, limits[i][0], limits[i][1], labels[i]); err != nil {
			return fmt.Sprintf("invalid %s field: %v", labels[i], err)
		}
	}
	return ""
}

func isCronMacro(expr string) bool {
	switch strings.ToLower(strings.TrimSpace(expr)) {
	case "@yearly", "@annually", "@monthly", "@weekly", "@daily", "@midnight", "@hourly":
		return true
	default:
		return false
	}
}

func validateCronField(field string, min int, max int, label string) error {
	for _, seg := range strings.Split(field, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return errors.New("empty segment")
		}
		if seg == "*" {
			continue
		}

		base, step, hasStep := strings.Cut(seg, "/")
		if hasStep {
			stepVal, err := strconv.Atoi(step)
			if err != nil || stepVal < 1 {
				return errors.New("step must be a positive integer")
			}
		}

		if base == "*" {
			continue
		}
		if strings.Contains(base, "-") {
			loText, hiText, ok := strings.Cut(base, "-")
			if !ok {
				return errors.New("invalid range")
			}
			lo, errLo := cronValueToInt(loText, label)
			hi, errHi := cronValueToInt(hiText, label)
			if errLo != nil || errHi != nil {
				return errors.New("range bounds must be integers or valid names")
			}
			if lo > hi {
				return errors.New("range start cannot be greater than range end")
			}
			if lo < min || hi > max {
				return fmt.Errorf("range must be between %d and %d", min, max)
			}
			continue
		}
		val, err := cronValueToInt(base, label)
		if err != nil {
			return errors.New("value must be an integer, *, range, step, or valid name")
		}
		if val < min || val > max {
			return fmt.Errorf("value must be between %d and %d", min, max)
		}
	}
	return nil
}

func cronValueToInt(value string, label string) (int, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	if n, err := strconv.Atoi(v); err == nil {
		return n, nil
	}

	switch label {
	case "month":
		monthNames := map[string]int{
			"jan": 1, "january": 1,
			"feb": 2, "february": 2,
			"mar": 3, "march": 3,
			"apr": 4, "april": 4,
			"may": 5,
			"jun": 6, "june": 6,
			"jul": 7, "july": 7,
			"aug": 8, "august": 8,
			"sep": 9, "september": 9,
			"oct": 10, "october": 10,
			"nov": 11, "november": 11,
			"dec": 12, "december": 12,
		}
		if n, ok := monthNames[v]; ok {
			return n, nil
		}
	case "weekday":
		weekdayNames := map[string]int{
			"sun": 0, "sunday": 0,
			"mon": 1, "monday": 1,
			"tue": 2, "tuesday": 2,
			"wed": 3, "wednesday": 3,
			"thu": 4, "thursday": 4,
			"fri": 5, "friday": 5,
			"sat": 6, "saturday": 6,
		}
		if n, ok := weekdayNames[v]; ok {
			return n, nil
		}
	}

	return 0, errors.New("invalid value")
}

func validateJobModeAgainstHostControls(host models.Host, mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "scan" {
		return ""
	}
	if !host.ChecksEnabled {
		return "cannot schedule apply job while host checks are disabled"
	}
	return ""
}

func (a *app) listAPITokens(w http.ResponseWriter, _ *http.Request) {
	tokens, err := db.ListAPITokens(a.db)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to load API tokens"})
		return
	}
	writeJSON(w, 200, tokens)
}

func (a *app) createAPIToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, 400, map[string]string{"error": "name is required"})
		return
	}
	plaintext, id, err := db.CreateAPIToken(a.db, name)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to create API token"})
		return
	}
	writeJSON(w, 201, map[string]string{
		"id":         id,
		"name":       name,
		"token":      plaintext,
		"created_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func (a *app) revokeAPIToken(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := db.RevokeAPIToken(a.db, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 404, map[string]string{"error": "token not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": "Failed to revoke API token"})
		return
	}
	writeJSON(w, 200, map[string]string{"message": "token revoked"})
}

func parseBoolQuery(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

const maxJSONBodyBytes = 1 << 20 // 1 MiB

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

// friendlyError wraps a raw error into a user-friendly message based on context.
func friendlyError(err error, context string) string {
	if err == nil {
		return context
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "sql: no rows in result set"):
		return context + " not found"
	case strings.Contains(msg, "connection refused"):
		return "Could not connect to host — check that the address and port are correct"
	case strings.Contains(msg, "i/o timeout"):
		return "Connection timed out — the host may be unreachable"
	default:
		if context != "" {
			return context + ": " + msg
		}
		return msg
	}
}

// authMiddlewareFlexible checks Authorization header first, then falls back to ?token= query param.
// This allows EventSource (SSE) which cannot set custom headers.
func (a *app) authMiddlewareFlexible(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		authz := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(authz, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
		}
		if token == "" {
			token = strings.TrimSpace(r.URL.Query().Get("token"))
		}
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		claims, err := auth.ParseJWT(a.cfg.JWTSecret, token)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), claims)))
	})
}

func sseWrite(w http.ResponseWriter, flusher http.Flusher, event string, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(b))
	flusher.Flush()
}

func (a *app) scanHostStream(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	host, err := db.GetHost(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}
	force := parseBoolQuery(r.URL.Query().Get("force"))
	if !host.ChecksEnabled && !force {
		writeJSON(w, 409, map[string]string{"error": "host checks are disabled for this host; enable checks or retry with force=true"})
		return
	}

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]string{"error": "streaming not supported"})
		return
	}
	w.WriteHeader(200)
	flusher.Flush()

	sseWrite(w, flusher, "start", map[string]string{"host_id": host.ID, "host_name": host.Name, "mode": "scan"})

	seq := 0
	onLine := func(line string) {
		seq++
		sseWrite(w, flusher, "line", map[string]any{"text": line, "seq": seq})
	}

	res, err := a.sshClient.ScanHostStreaming(host, a.secrets, onLine)
	if err != nil {
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			if a.notificationEnabledForHostEvent(host.ID, "scan_failure") {
				_ = a.notifier.Send(a.currentAppriseURL(), fmt.Sprintf("Patchdeck: scan FAILED on %s (%s)", host.Name, hkErr.Message))
			}
			sseWrite(w, flusher, "error", map[string]any{
				"error": hkErr.Message, "code": "host_key_mismatch",
				"expected_fingerprint":  hkErr.ExpectedFingerprint,
				"presented_fingerprint": hkErr.PresentedFingerprint,
			})
		} else {
			if a.notificationEnabledForHostEvent(host.ID, "scan_failure") {
				_ = a.notifier.Send(a.currentAppriseURL(), fmt.Sprintf("Patchdeck: scan FAILED on %s (%v)", host.Name, err))
			}
			sseWrite(w, flusher, "error", map[string]string{"error": err.Error()})
		}
		sseWrite(w, flusher, "done", map[string]string{})
		return
	}

	_ = db.UpsertScanResult(a.db, host.ID, res)
	_ = db.RecordActivity(a.db, host.ID, host.Name, "scan_ok", fmt.Sprintf("Scan completed: %d packages available", len(res.Packages)))
	if len(res.Packages) > 0 && a.notificationEnabledForHostEvent(host.ID, "updates_available") {
		_ = a.notifier.Send(a.currentAppriseURL(), fmt.Sprintf("Patchdeck: updates available on %s (%d packages)", host.Name, len(res.Packages)))
	}
	sseWrite(w, flusher, "result", res)
	sseWrite(w, flusher, "done", map[string]string{})
}

func (a *app) applyStream(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	host, err := db.GetHost(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}
	force := parseBoolQuery(r.URL.Query().Get("force"))
	if !host.ChecksEnabled && !force {
		writeJSON(w, 409, map[string]string{"error": "host checks are disabled for this host; enable checks or retry with force=true"})
		return
	}
	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]string{"error": "streaming not supported"})
		return
	}
	w.WriteHeader(200)
	flusher.Flush()

	sseWrite(w, flusher, "start", map[string]string{"host_id": host.ID, "host_name": host.Name, "mode": "apply"})

	seq := 0
	unpackCount := 0
	setupCount := 0
	onLine := func(line string) {
		seq++
		sseWrite(w, flusher, "line", map[string]any{"text": line, "seq": seq})

		// Parse progress from apt output
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "Reading package lists") {
			sseWrite(w, flusher, "progress", map[string]any{"phase": "preparing", "percent": nil, "message": "Reading package lists..."})
		} else if strings.HasPrefix(trimmedLine, "Unpacking ") {
			unpackCount++
			sseWrite(w, flusher, "progress", map[string]any{"phase": "unpacking", "percent": nil, "message": trimmedLine, "count": unpackCount})
		} else if strings.HasPrefix(trimmedLine, "Setting up ") {
			setupCount++
			total := unpackCount
			if total == 0 {
				total = setupCount
			}
			var pct any
			if total > 0 {
				pct = int(float64(setupCount) / float64(total) * 100)
			}
			sseWrite(w, flusher, "progress", map[string]any{"phase": "configuring", "percent": pct, "message": trimmedLine, "current": setupCount, "total": total})
		}
	}

	res, err := a.sshClient.ApplyUpdatesStreaming(host, a.secrets, onLine)
	if err != nil {
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			sseWrite(w, flusher, "error", map[string]any{
				"error": hkErr.Message, "code": "host_key_mismatch",
				"expected_fingerprint":  hkErr.ExpectedFingerprint,
				"presented_fingerprint": hkErr.PresentedFingerprint,
			})
		} else {
			if a.notificationEnabledForHostEvent(host.ID, "auto_apply_failure") {
				_ = a.notifier.Send(a.currentAppriseURL(), fmt.Sprintf("Patchdeck: apply FAILED on %s (%v)", host.Name, err))
			}
			_ = db.RecordActivity(a.db, host.ID, host.Name, "apply_fail", fmt.Sprintf("Apply failed: %v", err))
			sseWrite(w, flusher, "error", map[string]string{"error": err.Error()})
		}
		sseWrite(w, flusher, "done", map[string]string{})
		return
	}

	if a.notificationEnabledForHostEvent(host.ID, "auto_apply_success") {
		_ = a.notifier.Send(a.currentAppriseURL(), fmt.Sprintf("Patchdeck: updates applied on %s. %d packages changed", host.Name, res.ChangedPackages))
	}
	_ = db.RecordActivity(a.db, host.ID, host.Name, "apply_ok", fmt.Sprintf("Applied updates: %d packages changed", res.ChangedPackages))
	sseWrite(w, flusher, "result", res)
	sseWrite(w, flusher, "done", map[string]string{})
}

func (a *app) awaitRecovery(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	host, err := db.GetHost(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "host not found"})
		return
	}

	// Parse timeout query param (default 180, max 300)
	timeoutSec := 180
	if raw := strings.TrimSpace(r.URL.Query().Get("timeout")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			timeoutSec = n
		}
	}
	if timeoutSec > 300 {
		timeoutSec = 300
	}

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]string{"error": "streaming not supported"})
		return
	}
	w.WriteHeader(200)
	flusher.Flush()

	sseWrite(w, flusher, "start", map[string]any{
		"host_id":   host.ID,
		"host_name": host.Name,
		"timeout":   timeoutSec,
	})

	// Use a short SSH timeout (5s) for connectivity checks
	checker := sshx.NewClient(5*time.Second, a.verifyHostKey)

	ctx := r.Context()
	start := time.Now()

	// Wait 10 seconds initial delay (host needs time to begin shutting down)
	select {
	case <-time.After(10 * time.Second):
	case <-ctx.Done():
		sseWrite(w, flusher, "done", map[string]string{})
		return
	}

	attempt := 0
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	deadline := time.Duration(timeoutSec) * time.Second

	// Check immediately after initial delay, then every 10s
	for {
		attempt++
		elapsed := time.Since(start).Seconds()

		connected := false
		if err := checker.CheckConnectivity(host, a.secrets); err == nil {
			connected = true
		}

		sseWrite(w, flusher, "ping", map[string]any{
			"attempt":         attempt,
			"elapsed_seconds": int(elapsed),
			"connected":       connected,
		})

		if connected {
			sseWrite(w, flusher, "result", map[string]any{
				"recovered":       true,
				"elapsed_seconds": int(elapsed),
			})
			sseWrite(w, flusher, "done", map[string]string{})
			return
		}

		if time.Since(start) >= deadline {
			sseWrite(w, flusher, "result", map[string]any{
				"recovered":       false,
				"elapsed_seconds": int(elapsed),
				"timeout":         true,
			})
			sseWrite(w, flusher, "done", map[string]string{})
			return
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			sseWrite(w, flusher, "done", map[string]string{})
			return
		}
	}
}
