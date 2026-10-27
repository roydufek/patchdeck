package sshx

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"patchdeck/api/internal/crypto"
	"patchdeck/api/internal/models"
)

// fakeSSH is a real SSH server in-process: it accepts only the listed public keys, counts
// connections, records every exec'd command with its stdin, and answers via handler.
type fakeSSH struct {
	addr    string
	conns   atomic.Int32
	mu      sync.Mutex
	cmds    []string
	stdins  []string
	allowed map[string]bool // authorized key fingerprints
	handler func(cmd, stdin string) (string, uint32)
	live    []*ssh.ServerConn
}

func newFakeSSH(t *testing.T, allowed ...ssh.Signer) *fakeSSH {
	t.Helper()
	f := &fakeSSH{allowed: map[string]bool{}, handler: func(string, string) (string, uint32) { return "", 0 }}
	for _, s := range allowed {
		f.allowed[ssh.FingerprintSHA256(s.PublicKey())] = true
	}
	_, hk, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hk)
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		f.mu.Lock()
		ok := f.allowed[ssh.FingerprintSHA256(k)]
		f.mu.Unlock()
		if ok {
			return nil, nil
		}
		return nil, io.EOF
	}}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f.addr = ln.Addr().String()
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(nc, cfg)
		}
	}()
	return f
}

func (f *fakeSSH) serve(nc net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	f.conns.Add(1)
	f.mu.Lock()
	f.live = append(f.live, sc)
	f.mu.Unlock()
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		ch, rq, err := nch.Accept()
		if err != nil {
			continue
		}
		go func() {
			for req := range rq {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				var p struct{ Cmd string }
				_ = ssh.Unmarshal(req.Payload, &p)
				req.Reply(true, nil)
				in, _ := io.ReadAll(ch) // the client always sends EOF after stdin
				f.mu.Lock()
				f.cmds = append(f.cmds, p.Cmd)
				f.stdins = append(f.stdins, string(in))
				h := f.handler
				f.mu.Unlock()
				out, code := h(p.Cmd, string(in))
				_, _ = ch.Write([]byte(out))
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{code}))
				ch.Close()
				return
			}
		}()
	}
}

// dropAll closes every server-side connection (a host reboot / sshd restart).
func (f *fakeSSH) dropAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.live {
		c.Close()
	}
	f.live = nil
}

func (f *fakeSSH) commands() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...), append([]string(nil), f.stdins...)
}

func newSigner(t *testing.T) ssh.Signer {
	_, k, _ := ed25519.GenerateKey(rand.Reader)
	s, err := ssh.NewSignerFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func appKeyHost(t *testing.T, f *fakeSSH, sudoPass string) (models.Host, *crypto.SealBox) {
	seal, _ := crypto.NewSealBox(strings.Repeat("k", 32))
	b, _ := json.Marshal(secretBlob{SudoPassword: sudoPass})
	cipher, _ := seal.Encrypt(b)
	host, port, _ := net.SplitHostPort(f.addr)
	p, _ := strconv.Atoi(port)
	return models.Host{ID: "h1", Name: "web-01", Address: host, Port: p, SSHUser: "patchdeck:web-01", AuthType: models.AuthAppKey, SecretCipher: cipher}, seal
}

func testClient(signers ...ssh.Signer) *Client {
	c := NewClient(5*time.Second, 10*time.Second, func(models.Host, string) HostKeyDecision { return HostKeyDecision{Allow: true} })
	c.SetAppKeySource(func() []ssh.Signer { return signers })
	return c
}

// Through Warpgate the target logs in as root: the probe must detect that, skip sudo/su, and
// the probe + the real command must share ONE connection (one Warpgate session).
func TestRootTargetOneConnection(t *testing.T) {
	key := newSigner(t)
	f := newFakeSSH(t, key)
	f.handler = func(cmd, _ string) (string, uint32) {
		if cmd == escalationProbe {
			return "__PD_ROOT__\n", 0
		}
		return "done\n", 0
	}
	host, seal := appKeyHost(t, f, "")
	c := testClient(key)
	out, err := c.runPrivileged(context.Background(), host, seal, "apt-get -s upgrade")
	if err != nil || out != "done" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	cmds, _ := f.commands()
	if len(cmds) != 2 || cmds[1] != runAsRootCommand("apt-get -s upgrade") {
		t.Fatalf("root path must run the command directly (no sudo/su): %q", cmds)
	}
	// A follow-up operation within the idle window reuses the connection too.
	if _, err := c.CheckConnectivity(host, seal); err != nil {
		t.Fatal(err)
	}
	if n := f.conns.Load(); n != 1 {
		t.Fatalf("want 1 connection for probe + command + follow-up, got %d", n)
	}
}

// The su fallback must hand the password to su on stdin, never on the command line.
func TestSuPasswordOnStdin(t *testing.T) {
	key := newSigner(t)
	f := newFakeSSH(t, key)
	const pw = "s3cret-root-pw"
	f.handler = func(cmd, stdin string) (string, uint32) {
		switch {
		case cmd == escalationProbe:
			return "__PD_NOSUDO__\n", 0
		case strings.HasPrefix(cmd, "sudo -S"):
			return "", 1 // no sudo for this user
		case strings.HasPrefix(cmd, "su -c ") && stdin == pw+"\n":
			return "ok\n", 0
		}
		return "", 1
	}
	host, seal := appKeyHost(t, f, pw)
	out, err := testClient(key).runPrivileged(context.Background(), host, seal, "id -u")
	if err != nil || out != "ok" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	cmds, stdins := f.commands()
	for i, c := range cmds {
		if strings.Contains(c, pw) {
			t.Fatalf("password leaked into command %d: %q", i, c)
		}
	}
	if last := cmds[len(cmds)-1]; last != suCommand(runAsRootCommand("id -u")) || stdins[len(stdins)-1] != pw+"\n" {
		t.Fatalf("su command %q / stdin %q", last, stdins[len(stdins)-1])
	}
	if n := f.conns.Load(); n != 1 {
		t.Fatalf("the whole ladder should share one connection, got %d", n)
	}
}

// Mid-rotation both keys are offered: a host that only trusts the NEW key still works.
func TestAppKeyOffersBothKeys(t *testing.T) {
	cur, next := newSigner(t), newSigner(t)
	f := newFakeSSH(t, next)
	host, seal := appKeyHost(t, f, "")
	if _, err := testClient(cur, next).CheckConnectivity(host, seal); err != nil {
		t.Fatalf("host trusting only the next key must still accept: %v", err)
	}
	if _, err := testClient(cur).CheckConnectivity(host, seal); err == nil {
		t.Fatal("with only the old key offered, auth must fail")
	}
}

// CheckAppKey answers for ONE key and never borrows a pooled connection.
func TestCheckAppKeyIsolated(t *testing.T) {
	cur, next := newSigner(t), newSigner(t)
	f := newFakeSSH(t, cur)
	host, seal := appKeyHost(t, f, "")
	c := testClient(cur, next)
	if _, err := c.CheckConnectivity(host, seal); err != nil { // leaves a pooled connection open
		t.Fatal(err)
	}
	if err := c.CheckAppKey(host, next); err == nil {
		t.Fatal("next key isn't installed: the check must fail, not reuse the pooled connection")
	}
	if err := c.CheckAppKey(host, cur); err != nil {
		t.Fatalf("installed key: %v", err)
	}
}

// A pooled connection that died (reboot / sshd restart) is redialed, not reported as an error.
func TestStalePooledConnectionRedials(t *testing.T) {
	key := newSigner(t)
	f := newFakeSSH(t, key)
	f.handler = func(string, string) (string, uint32) { return "1 2\n", 0 }
	host, seal := appKeyHost(t, f, "")
	c := testClient(key)
	if _, err := c.CheckConnectivity(host, seal); err != nil {
		t.Fatal(err)
	}
	f.dropAll()
	time.Sleep(100 * time.Millisecond)
	if _, err := c.CheckConnectivity(host, seal); err != nil {
		t.Fatalf("after a drop the next command should redial: %v", err)
	}
	if n := f.conns.Load(); n != 2 {
		t.Fatalf("want a fresh connection after the drop, got %d total", n)
	}
}

// Editing a host's credentials must not reuse a connection made with the old ones.
func TestPoolKeyTracksCredentials(t *testing.T) {
	key := newSigner(t)
	f := newFakeSSH(t, key)
	host, seal := appKeyHost(t, f, "")
	c := testClient(key)
	_, _ = c.CheckConnectivity(host, seal)
	b, _ := json.Marshal(secretBlob{SudoPassword: "changed"})
	host.SecretCipher, _ = seal.Encrypt(b)
	_, _ = c.CheckConnectivity(host, seal)
	if n := f.conns.Load(); n != 2 {
		t.Fatalf("changed credentials should dial fresh, got %d connections", n)
	}
}

// Idle connections are closed after poolIdle.
func TestIdleConnectionsClosed(t *testing.T) {
	key := newSigner(t)
	f := newFakeSSH(t, key)
	host, seal := appKeyHost(t, f, "")
	c := testClient(key)
	_, _ = c.CheckConnectivity(host, seal)
	c.pool.closeIdle(time.Now().Add(poolIdle + time.Second))
	c.pool.mu.Lock()
	n := len(c.pool.conns)
	c.pool.mu.Unlock()
	if n != 0 {
		t.Fatalf("idle connection still pooled (%d)", n)
	}
}

// A connection-level failure must surface as itself, not as "privilege escalation failed".
func TestEscalationSurfacesAuthErrors(t *testing.T) {
	f := newFakeSSH(t) // accepts no keys
	host, seal := appKeyHost(t, f, "pw")
	_, err := testClient(newSigner(t)).runPrivileged(context.Background(), host, seal, "true")
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("want the auth failure, got %v", err)
	}
}
