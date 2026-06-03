package sshx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"patchdeck/api/internal/crypto"
	"patchdeck/api/internal/models"

	"golang.org/x/crypto/ssh"
)

// FriendlySSHError translates raw SSH/network errors into user-friendly messages.
func FriendlySSHError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "Connection refused — verify host address and SSH port"
	case strings.Contains(msg, "no route to host"):
		return "Host unreachable — check network connectivity"
	case strings.Contains(msg, "i/o timeout"):
		return "Connection timed out — host may be offline or firewalled"
	case strings.Contains(msg, "unable to authenticate"),
		strings.Contains(msg, "no supported methods remain"),
		strings.Contains(msg, "permission denied"):
		return "SSH authentication failed — check credentials"
	case strings.Contains(msg, "handshake failed"):
		return "SSH handshake failed — host may have incompatible SSH configuration"
	case strings.Contains(msg, "no such host"),
		strings.Contains(msg, "lookup"):
		return "Host not found — check the hostname or IP address"
	case strings.Contains(msg, "connection reset"):
		return "Connection reset by host — the remote server closed the connection"
	default:
		return "SSH error: " + msg
	}
}

// aptLineRe parses apt list --upgradable output like:
// libfoo/jammy-security 1.2.3-1 amd64 [upgradable from: 1.2.2-1]
var aptLineRe = regexp.MustCompile(`^([^/]+)/(\S+)\s+(\S+)\s+(\S+)\s+\[upgradable from:\s+(\S+?)\]`)

// aptPhasedRe detects Ubuntu phased updates, e.g. "(phased 20%)" at end of line.
// These packages are shown as upgradable by apt but are intentionally withheld
// by APT's phased rollout mechanism — dist-upgrade will not install them.
var aptPhasedRe = regexp.MustCompile(`\(phased \d+%\)`)

// parseAptLine extracts PackageInfo from a single apt upgradable line.
// Falls back to name-only if parsing fails.
// Returns (info, isPhased) — callers may choose to exclude phased packages from counts.
func parseAptLine(line string) (models.PackageInfo, bool) {
	isPhased := aptPhasedRe.MatchString(line)
	m := aptLineRe.FindStringSubmatch(line)
	if m == nil {
		// Fallback: try to get at least the package name (before '/')
		if idx := strings.Index(line, "/"); idx > 0 {
			return models.PackageInfo{Name: line[:idx]}, isPhased
		}
		return models.PackageInfo{Name: line}, isPhased
	}
	return models.PackageInfo{
		Name:           m[1],
		Source:         m[2],
		NewVersion:     m[3],
		Arch:           m[4],
		CurrentVersion: m[5],
	}, isPhased
}

// parseSysinfo extracts os name, os version, uptime, and kernel from the __SYSINFO__ block.
func parseSysinfo(raw string) (osName, osVersion, uptime, kernel string) {
	startIdx := strings.Index(raw, "__SYSINFO_START__")
	endIdx := strings.Index(raw, "__SYSINFO_END__")
	if startIdx < 0 || endIdx < 0 || endIdx <= startIdx {
		return
	}
	block := raw[startIdx+len("__SYSINFO_START__") : endIdx]
	parts := strings.SplitN(block, "__SYSINFO_SEP__", 3)

	// Part 0: /etc/os-release content
	if len(parts) > 0 {
		for _, line := range strings.Split(parts[0], "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				val := strings.TrimPrefix(line, "PRETTY_NAME=")
				val = strings.Trim(val, `"'`)
				osName = val
			}
			if strings.HasPrefix(line, "VERSION_ID=") {
				val := strings.TrimPrefix(line, "VERSION_ID=")
				val = strings.Trim(val, `"'`)
				osVersion = val
			}
		}
	}
	// Part 1: uptime -p output
	if len(parts) > 1 {
		u := strings.TrimSpace(parts[1])
		u = strings.TrimPrefix(u, "up ")
		if u != "" {
			uptime = u
		}
	}
	// Part 2: uname -r output
	if len(parts) > 2 {
		k := strings.TrimSpace(parts[2])
		if k != "" {
			kernel = k
		}
	}
	return
}

// parseScanOutput extracts all scan data including sysinfo from raw SSH output.
func parseScanOutput(raw string, hostID string) models.ScanResult {
	// Extract sysinfo before stripping markers
	osName, osVersion, uptime, kernel := parseSysinfo(raw)

	// Strip the sysinfo block from raw for package parsing
	cleanRaw := raw
	if startIdx := strings.Index(raw, "__SYSINFO_START__"); startIdx >= 0 {
		if endIdx := strings.Index(raw, "__SYSINFO_END__"); endIdx >= 0 {
			cleanRaw = raw[:startIdx] + raw[endIdx+len("__SYSINFO_END__"):]
		}
	}

	// Extract & strip the dist-upgrade simulation block. apt states deferred upgrades
	// explicitly there ("deferred due to phasing:" / "kept back:") — the only reliable
	// signal, since `apt list --upgradable` doesn't annotate phasing on Ubuntu.
	deferredReasons := map[string]string{}
	if s := strings.Index(cleanRaw, "__DISTUPGRADE_SIM_START__"); s >= 0 {
		if e := strings.Index(cleanRaw, "__DISTUPGRADE_SIM_END__"); e >= 0 && e > s {
			deferredReasons = parseDeferredFromSim(cleanRaw[s+len("__DISTUPGRADE_SIM_START__") : e])
			cleanRaw = cleanRaw[:s] + cleanRaw[e+len("__DISTUPGRADE_SIM_END__"):]
		}
	}

	// Extract reboot reason packages
	rebootReason := ""
	if startIdx := strings.Index(cleanRaw, "__REBOOT_PKGS_START__"); startIdx >= 0 {
		if endIdx := strings.Index(cleanRaw, "__REBOOT_PKGS_END__"); endIdx >= 0 {
			pkgBlock := cleanRaw[startIdx+len("__REBOOT_PKGS_START__") : endIdx]
			var rebootPkgs []string
			for _, line := range strings.Split(pkgBlock, "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					rebootPkgs = append(rebootPkgs, line)
				}
			}
			if len(rebootPkgs) > 0 {
				rebootReason = strings.Join(rebootPkgs, ", ")
			}
			cleanRaw = cleanRaw[:startIdx] + cleanRaw[endIdx+len("__REBOOT_PKGS_END__"):]
		}
	}

	lines := strings.Split(cleanRaw, "\n")
	pkgs := []models.PackageInfo{}
	deferredPkgs := []models.PackageInfo{}
	needsReboot := strings.Contains(cleanRaw, "__REBOOT__")
	needrestartFound := !strings.Contains(cleanRaw, "__NEEDRESTART_MISSING__")
	aptUpdateFailed := strings.Contains(cleanRaw, "__APT_UPDATE_FAILED__")
	services := []string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Listing") || strings.HasPrefix(line, "WARNING") || strings.HasPrefix(line, "__") {
			continue
		}
		if strings.Contains(line, " / ") || strings.Contains(line, "upgradable from:") {
			pkg, isPhased := parseAptLine(line)
			// Deferred (phased rollout / held back) upgrades won't be installed by Apply
			// right now, so they go in a separate list and don't inflate the count. The
			// dist-upgrade simulation is authoritative; the inline "(phased %)" tag is a
			// fallback for apt versions that annotate it.
			if reason, ok := deferredReasons[pkg.Name]; ok {
				pkg.DeferReason = reason
				deferredPkgs = append(deferredPkgs, pkg)
			} else if isPhased {
				pkg.DeferReason = "phased"
				deferredPkgs = append(deferredPkgs, pkg)
			} else {
				pkgs = append(pkgs, pkg)
			}
		}
		if strings.HasPrefix(line, "NEEDRESTART-SVC:") {
			if svc := strings.TrimSpace(strings.TrimPrefix(line, "NEEDRESTART-SVC:")); svc != "" {
				services = append(services, svc)
			}
		}
	}
	return models.ScanResult{
		HostID:           hostID,
		Packages:         pkgs,
		DeferredPackages: deferredPkgs,
		NeedsReboot:      needsReboot,
		RebootReason:     rebootReason,
		NeedsRestart:     services,
		NeedrestartFound: needrestartFound,
		AptUpdateFailed:  aptUpdateFailed,
		RawOutput:        raw,
		OsName:           osName,
		OsVersion:        osVersion,
		Uptime:           uptime,
		Kernel:           kernel,
	}
}

// parseDeferredFromSim reads an `apt-get -s dist-upgrade` block and returns the
// packages apt reports as deferred → reason. apt prints a header line followed by
// indented, space-separated package names:
//
//	The following upgrades have been deferred due to phasing:
//	  apparmor cloud-init libapparmor1
//
// "deferred due to phasing" → "phased"; "kept back"/"held back" → "held". Names are
// collected from indented continuation lines until the next non-indented line.
func parseDeferredFromSim(sim string) map[string]string {
	out := map[string]string{}
	reason := ""
	for _, ln := range strings.Split(sim, "\n") {
		if strings.HasPrefix(ln, "The following") {
			lower := strings.ToLower(ln)
			switch {
			case strings.Contains(lower, "deferred due to phasing"):
				reason = "phased"
			case strings.Contains(lower, "kept back"), strings.Contains(lower, "held back"):
				reason = "held"
			default:
				reason = "" // some other "The following ..." section — not a deferral
			}
			continue
		}
		if reason != "" && len(ln) > 0 && (ln[0] == ' ' || ln[0] == '\t') {
			for _, name := range strings.Fields(ln) {
				out[name] = reason
			}
			continue
		}
		if strings.TrimSpace(ln) != "" {
			reason = "" // a non-indented, non-header line ends the current block
		}
	}
	return out
}

type HostKeyDecision struct {
	Allow                bool
	Reason               string
	ExpectedFingerprint  string
	PresentedFingerprint string
}

type HostKeyVerifier func(host models.Host, presentedFingerprint string) HostKeyDecision

type HostKeyError struct {
	HostID               string
	HostName             string
	Message              string
	ExpectedFingerprint  string
	PresentedFingerprint string
}

func (e *HostKeyError) Error() string { return e.Message }

type Client struct {
	timeout      time.Duration // TCP/SSH handshake dial timeout
	execTimeout  time.Duration // max wall-clock for a single remote command (0 = no cap)
	hostVerifier HostKeyVerifier
}

func NewClient(dialTimeout, execTimeout time.Duration, verifier HostKeyVerifier) *Client {
	return &Client{timeout: dialTimeout, execTimeout: execTimeout, hostVerifier: verifier}
}

type secretBlob struct {
	Password      string `json:"password"`
	PrivateKeyPEM string `json:"private_key_pem"`
	Passphrase    string `json:"passphrase"`
	SudoPassword  string `json:"sudo_password"`
}

// scanCmd gathers upgradable packages + sysinfo + reboot/needrestart state.
// LANG=C/LC_ALL=C force stable English apt output so the parser is locale-proof.
// `apt-get update 1>&2` sends update progress to STDERR (streamed for visibility but
// kept out of the STDOUT we parse), and its failure is reported via __APT_UPDATE_FAILED__
// WITHOUT aborting the listing — a transient mirror hiccup no longer zeroes the scan.
const scanCmd = `export LANG=C LC_ALL=C DEBIAN_FRONTEND=noninteractive
if ! apt-get update 1>&2; then echo __APT_UPDATE_FAILED__; fi
apt list --upgradable 2>/dev/null | tail -n +2
echo "__DISTUPGRADE_SIM_START__"; apt-get -s dist-upgrade 2>/dev/null || true; echo "__DISTUPGRADE_SIM_END__"
test -f /var/run/reboot-required && echo __REBOOT__ || true
test -f /var/run/reboot-required.pkgs && { echo "__REBOOT_PKGS_START__"; cat /var/run/reboot-required.pkgs 2>/dev/null; echo "__REBOOT_PKGS_END__"; } || true
if command -v needrestart >/dev/null 2>&1; then needrestart -b 2>/dev/null || true; else echo __NEEDRESTART_MISSING__; fi
echo "__SYSINFO_START__"; cat /etc/os-release 2>/dev/null || true; echo "__SYSINFO_SEP__"; uptime -p 2>/dev/null || uptime 2>/dev/null || true; echo "__SYSINFO_SEP__"; uname -r 2>/dev/null || true; echo "__SYSINFO_END__"`

// applyCmd installs all available upgrades. LANG=C keeps the "Setting up " markers
// (used to count changed packages) stable across locales.
const applyCmd = `export LANG=C LC_ALL=C DEBIAN_FRONTEND=noninteractive
apt-get -y dist-upgrade
test -f /var/run/reboot-required && echo __REBOOT__ || true`

func (c *Client) ScanHost(host models.Host, seal *crypto.SealBox) (models.ScanResult, error) {
	raw, err := c.runPrivileged(context.Background(), host, seal, scanCmd)
	if err != nil {
		return models.ScanResult{}, err
	}
	result := parseScanOutput(raw, host.ID)
	return result, nil
}

func (c *Client) ApplyUpdates(host models.Host, seal *crypto.SealBox) (models.ApplyResult, error) {
	raw, err := c.runPrivileged(context.Background(), host, seal, applyCmd)
	if err != nil {
		return models.ApplyResult{}, err
	}
	// Count only "Setting up" — each package prints both "Unpacking" and "Setting up",
	// so counting both doubles the result. "Setting up" is the final installation step.
	changed := strings.Count(raw, "Setting up ")
	return models.ApplyResult{ChangedPackages: changed, RawOutput: raw, NeedsReboot: strings.Contains(raw, "__REBOOT__")}, nil
}

func (c *Client) RestartServices(host models.Host, seal *crypto.SealBox, services []string) (models.RestartResult, error) {
	if len(services) == 0 {
		return models.RestartResult{Success: true, Output: "no services selected"}, nil
	}
	cmd := "systemctl restart " + strings.Join(services, " ")
	raw, err := c.runPrivileged(context.Background(), host, seal, cmd)
	return models.RestartResult{Services: services, Success: err == nil, Output: raw}, err
}

func (c *Client) Power(host models.Host, seal *crypto.SealBox, action string) error {
	// Use systemctl for graceful systemd-managed reboot/shutdown.
	// Wrap in nohup + setsid so the command survives SSH session teardown.
	// We intentionally ignore the EOF error caused by the server closing the connection.
	cmd := fmt.Sprintf("nohup setsid systemctl %s &>/dev/null & sleep 0.2", action)
	_, err := c.runPrivileged(context.Background(), host, seal, cmd)
	// A non-nil error here is often just the SSH connection dropping (expected on reboot).
	// Return nil so callers treat the action as successfully initiated.
	_ = err
	return nil
}

// CheckConnectivity verifies SSH reachability and, in the same round-trip, returns
// the host's uptime in seconds (read from /proc/uptime — cheap, locale-proof, and
// reboot-accurate). A non-nil error means unreachable; the uptime is best-effort
// (0 if it can't be read/parsed) and never affects the reachability verdict.
func (c *Client) CheckConnectivity(host models.Host, seal *crypto.SealBox) (int64, error) {
	out, err := c.run(context.Background(), host, seal, "cat /proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(out) // "<uptime_seconds> <idle_seconds>"
	if len(fields) == 0 {
		return 0, nil
	}
	secs, perr := strconv.ParseFloat(fields[0], 64)
	if perr != nil {
		return 0, nil
	}
	return int64(secs), nil
}

func shellSingleQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func runAsRootCommand(command string) string {
	return "sh -lc " + shellSingleQuote(command)
}

func (c *Client) runPrivileged(ctx context.Context, host models.Host, seal *crypto.SealBox, command string) (string, error) {
	sec, err := decodeSecrets(host.SecretCipher, seal)
	if err != nil {
		return "", err
	}

	rootCmd := runAsRootCommand(command)

	if out, err := c.run(ctx, host, seal, "sudo -n "+rootCmd); err == nil {
		return out, nil
	}

	sudoPass := strings.TrimSpace(sec.SudoPassword)
	if sudoPass == "" {
		return "", fmt.Errorf("privilege escalation required: passwordless sudo unavailable and sudo/root password not provided")
	}

	// Pass the password via stdin to `sudo -S` rather than embedding it in the command
	// line (where it'd be visible to ps/auditd on the remote host for the run window).
	if out, err := c.runWithStdin(ctx, host, seal, "sudo -S -p '' "+rootCmd, strings.NewReader(sudoPass+"\n")); err == nil {
		return out, nil
	}

	suFallback := fmt.Sprintf("printf '%%s\\n' %s | su -c %s root", shellSingleQuote(sudoPass), shellSingleQuote(rootCmd))
	if out, err := c.run(ctx, host, seal, suFallback); err == nil {
		return out, nil
	}

	return "", fmt.Errorf("privilege escalation failed: sudo -n, sudo -S, and su fallback all failed (verify sudo/root password and host policy)")
}

// verifyingHostKeyCallback always verifies the presented host key via the configured
// verifier (TOFU/pinning policy lives there). It fails CLOSED: if no verifier is set,
// the connection is refused rather than silently trusting any key — defense-in-depth
// so a future code path can't accidentally disable host-key checking.
func (c *Client) verifyingHostKeyCallback(host models.Host) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if c.hostVerifier == nil {
			return fmt.Errorf("host key verification is required but no verifier is configured")
		}
		decision := c.hostVerifier(host, ssh.FingerprintSHA256(key))
		if decision.Allow {
			return nil
		}
		message := strings.TrimSpace(decision.Reason)
		if message == "" {
			message = "host key verification failed"
		}
		return &HostKeyError{HostID: host.ID, HostName: host.Name, Message: message, ExpectedFingerprint: decision.ExpectedFingerprint, PresentedFingerprint: decision.PresentedFingerprint}
	}
}

func (c *Client) run(ctx context.Context, host models.Host, seal *crypto.SealBox, command string) (string, error) {
	return c.runWithStdin(ctx, host, seal, command, nil)
}

// runWithStdin executes command over SSH, optionally feeding stdin — used to pass a
// sudo password to `sudo -S` without exposing it on the remote command line (ps/audit).
func (c *Client) runWithStdin(ctx context.Context, host models.Host, seal *crypto.SealBox, command string, stdin io.Reader) (string, error) {
	sec, err := decodeSecrets(host.SecretCipher, seal)
	if err != nil {
		return "", err
	}

	cfg := &ssh.ClientConfig{User: host.SSHUser, HostKeyCallback: c.verifyingHostKeyCallback(host), Timeout: c.timeout}
	if host.AuthType == "password" {
		cfg.Auth = []ssh.AuthMethod{ssh.Password(sec.Password)}
	} else {
		signer, err := parseSigner([]byte(sec.PrivateKeyPEM), sec.Passphrase)
		if err != nil {
			return "", err
		}
		cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	}
	addr := net.JoinHostPort(host.Address, fmt.Sprintf("%d", host.Port))
	cli, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		// Wrap raw SSH/network dial errors in user-friendly messages,
		// but pass through HostKeyError as-is (already user-friendly).
		var hkErr *HostKeyError
		if errors.As(err, &hkErr) {
			return "", err
		}
		return "", fmt.Errorf("%s", FriendlySSHError(err))
	}
	defer cli.Close()
	sess, err := cli.NewSession()
	if err != nil {
		return "", fmt.Errorf("SSH session error: %s", FriendlySSHError(err))
	}
	defer sess.Close()
	// Capture stdout and stderr separately so diagnostic noise (e.g. apt-get
	// update progress, apt's "unstable CLI" warning) never pollutes the stdout
	// we parse.
	var outBuf, errBuf bytes.Buffer
	sess.Stdout = &outBuf
	sess.Stderr = &errBuf
	if stdin != nil {
		sess.Stdin = stdin
	}
	runErr := c.runWithDeadline(ctx, cli, func() error { return sess.Run(command) })
	out := strings.TrimSpace(outBuf.String())
	if runErr != nil {
		// Errors usually land on stderr — prefer it for the human-facing snippet,
		// then fall back to stdout, then the raw error.
		if snippet := lastLines(errBuf.String(), 3); snippet != "" {
			return out, fmt.Errorf("%s", snippet)
		}
		if snippet := lastLines(out, 3); snippet != "" {
			return out, fmt.Errorf("%s", snippet)
		}
		return out, runErr
	}
	return out, nil
}

// runWithDeadline runs fn (sess.Run or sess.Wait) but aborts after c.execTimeout
// by closing the underlying connection — the only reliable way to unblock an SSH
// command stuck on, e.g., a held apt/dpkg lock. A zero execTimeout disables the cap.
func (c *Client) runWithDeadline(ctx context.Context, cli *ssh.Client, fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()

	// execTimeout cap; 0 disables it (a nil channel never fires in the select).
	var timeout <-chan time.Time
	if c.execTimeout > 0 {
		t := time.NewTimer(c.execTimeout)
		defer t.Stop()
		timeout = t.C
	}

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Caller cancelled (e.g. SSE client disconnected). Close the connection to
		// unblock the in-flight Run/Wait, then let the goroutine drain.
		cli.Close()
		<-done
		return fmt.Errorf("operation cancelled")
	case <-timeout:
		cli.Close() // force-unblock the stuck Run/Wait
		<-done      // let the goroutine observe the closed conn and exit
		return fmt.Errorf("command timed out after %s (host may have a held apt/dpkg lock or be unresponsive)", c.execTimeout)
	}
}

// lastLines returns the final n non-empty-trimmed lines of s joined by " | ",
// used to build a compact human-facing error snippet.
func lastLines(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	start := 0
	if len(lines) > n {
		start = len(lines) - n
	}
	return strings.TrimSpace(strings.Join(lines[start:], " | "))
}

func parseSigner(key []byte, passphrase string) (ssh.Signer, error) {
	if passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
	}
	return ssh.ParsePrivateKey(key)
}

func decodeSecrets(cipher string, seal *crypto.SealBox) (secretBlob, error) {
	plain, err := seal.Decrypt(cipher)
	if err != nil {
		return secretBlob{}, err
	}
	var sb secretBlob
	return sb, json.Unmarshal(plain, &sb)
}

// RunStreaming executes a command and calls onLine for each output line.
// Returns the full output when done.
func (c *Client) RunStreaming(ctx context.Context, host models.Host, seal *crypto.SealBox, command string, onLine func(line string)) (string, error) {
	return c.runStreamingWithStdin(ctx, host, seal, command, onLine, nil)
}

func (c *Client) runStreamingWithStdin(ctx context.Context, host models.Host, seal *crypto.SealBox, command string, onLine func(line string), stdin io.Reader) (string, error) {
	sec, err := decodeSecrets(host.SecretCipher, seal)
	if err != nil {
		return "", err
	}

	cfg := &ssh.ClientConfig{User: host.SSHUser, HostKeyCallback: c.verifyingHostKeyCallback(host), Timeout: c.timeout}
	if host.AuthType == "password" {
		cfg.Auth = []ssh.AuthMethod{ssh.Password(sec.Password)}
	} else {
		signer, err := parseSigner([]byte(sec.PrivateKeyPEM), sec.Passphrase)
		if err != nil {
			return "", err
		}
		cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	}
	addr := net.JoinHostPort(host.Address, fmt.Sprintf("%d", host.Port))
	cli, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		var hkErr *HostKeyError
		if errors.As(err, &hkErr) {
			return "", err
		}
		return "", fmt.Errorf("%s", FriendlySSHError(err))
	}
	defer cli.Close()
	sess, err := cli.NewSession()
	if err != nil {
		return "", fmt.Errorf("SSH session error: %s", FriendlySSHError(err))
	}
	defer sess.Close()

	// Separate pipes for stdout and stderr. Both are streamed live to the UI (so the
	// operator sees apt-get update progress etc.), but ONLY stdout is accumulated for
	// parsing — keeping diagnostic noise out of the parsed result.
	outPr, outPw := io.Pipe()
	errPr, errPw := io.Pipe()
	sess.Stdout = outPw
	sess.Stderr = errPw
	if stdin != nil {
		sess.Stdin = stdin
	}

	if err := sess.Start(command); err != nil {
		outPw.Close()
		errPw.Close()
		return "", err
	}

	var accumulated bytes.Buffer // stdout only — this is what gets parsed
	var stderrTail bytes.Buffer
	// onLine writes to a single SSE response; serialize calls from both readers.
	var emitMu sync.Mutex
	emit := func(line string) {
		if onLine == nil {
			return
		}
		emitMu.Lock()
		onLine(line)
		emitMu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(outPr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			accumulated.WriteString(line)
			accumulated.WriteString("\n")
			emit(line)
		}
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(errPr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			stderrTail.WriteString(line)
			stderrTail.WriteString("\n")
			emit(line)
		}
	}()

	waitErr := c.runWithDeadline(ctx, cli, func() error { return sess.Wait() })
	outPw.Close()
	errPw.Close()
	wg.Wait()

	out := strings.TrimSpace(accumulated.String())
	if waitErr != nil {
		if snippet := lastLines(stderrTail.String(), 3); snippet != "" {
			return out, fmt.Errorf("%s", snippet)
		}
		if snippet := lastLines(out, 3); snippet != "" {
			return out, fmt.Errorf("%s", snippet)
		}
		return out, waitErr
	}
	return out, nil
}

// runPrivilegedStreaming is like runPrivileged but streams output line-by-line.
func (c *Client) runPrivilegedStreaming(ctx context.Context, host models.Host, seal *crypto.SealBox, command string, onLine func(line string)) (string, error) {
	sec, err := decodeSecrets(host.SecretCipher, seal)
	if err != nil {
		return "", err
	}

	rootCmd := runAsRootCommand(command)

	// Test passwordless sudo with a no-op command — do NOT run the real command
	// non-streaming first, as that would execute it twice (wasting time and producing
	// inconsistent results if apt state changes between runs).
	if _, err := c.run(ctx, host, seal, "sudo -n true"); err == nil {
		return c.RunStreaming(ctx, host, seal, "sudo -n "+rootCmd, onLine)
	}

	sudoPass := strings.TrimSpace(sec.SudoPassword)
	if sudoPass == "" {
		return "", fmt.Errorf("privilege escalation required: passwordless sudo unavailable and sudo/root password not provided")
	}

	// Password to `sudo -S` via stdin, not the command line (ps/audit exposure).
	if out, err := c.runStreamingWithStdin(ctx, host, seal, "sudo -S -p '' "+rootCmd, onLine, strings.NewReader(sudoPass+"\n")); err == nil {
		return out, nil
	}

	suFallback := fmt.Sprintf("printf '%%s\\n' %s | su -c %s root", shellSingleQuote(sudoPass), shellSingleQuote(rootCmd))
	if out, err := c.RunStreaming(ctx, host, seal, suFallback, onLine); err == nil {
		return out, nil
	}

	return "", fmt.Errorf("privilege escalation failed: sudo -n, sudo -S, and su fallback all failed (verify sudo/root password and host policy)")
}

// ScanHostStreaming runs a scan with streaming output via onLine callback.
func (c *Client) ScanHostStreaming(ctx context.Context, host models.Host, seal *crypto.SealBox, onLine func(line string)) (models.ScanResult, error) {
	raw, err := c.runPrivilegedStreaming(ctx, host, seal, scanCmd, onLine)
	if err != nil {
		return models.ScanResult{}, err
	}
	result := parseScanOutput(raw, host.ID)
	return result, nil
}

// ApplyUpdatesStreaming runs apply with streaming output via onLine callback.
func (c *Client) ApplyUpdatesStreaming(ctx context.Context, host models.Host, seal *crypto.SealBox, onLine func(line string)) (models.ApplyResult, error) {
	raw, err := c.runPrivilegedStreaming(ctx, host, seal, applyCmd, onLine)
	if err != nil {
		return models.ApplyResult{}, err
	}
	// Count only "Setting up" — each package prints both "Unpacking" and "Setting up",
	// so counting both doubles the result. "Setting up" is the final installation step.
	changed := strings.Count(raw, "Setting up ")
	return models.ApplyResult{ChangedPackages: changed, RawOutput: raw, NeedsReboot: strings.Contains(raw, "__REBOOT__")}, nil
}
