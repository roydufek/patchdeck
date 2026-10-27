package sshx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"patchdeck/api/internal/models"
)

// Connection reuse. Every remote command used to dial its own SSH connection, so one
// operation (escalation probe + the real command, or a restart's several steps) cost 2–4
// connections — and through a Warpgate bastion each connection is a separately recorded
// session. Commands now share one connection per host while it's in use and for poolIdle
// after, so an operation is one connection (one Warpgate session).
//
// The pool key covers everything that affects how the connection was authenticated and
// verified (address, user, credentials, host-key trust, offered app keys), so editing a host
// or rotating keys gets a fresh connection. A connection that drops (reboot, sshd restart)
// is noticed via ssh.Client.Wait and evicted; a stale one that only fails when a session is
// opened is evicted and redialed once.

const poolIdle = 20 * time.Second

type pooledConn struct {
	cli    *ssh.Client
	key    string
	users  int // sessions in flight
	last   time.Time
	dead   bool
	pooled bool // false = lost a dial race; closed when its last user releases it
}

type connPool struct {
	mu     sync.Mutex
	conns  map[string]*pooledConn
	reaper sync.Once
}

func newConnPool() *connPool { return &connPool{conns: map[string]*pooledConn{}} }

// poolKey identifies "the same connection" for a host. App-key fingerprints are included so
// a finished rotation stops reusing connections opened with the retired key.
func poolKey(host models.Host, signers []ssh.Signer) string {
	h := sha256.New()
	for _, s := range []string{host.ID, host.Address, strconv.Itoa(host.Port), host.SSHUser, host.AuthType,
		host.SecretCipher, host.HostKeyTrustMode, host.HostKeyPinned, host.HostKeyTrusted} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	for _, s := range signers {
		h.Write([]byte(ssh.FingerprintSHA256(s.PublicKey())))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// WithTimeouts returns a client with different timeouts that SHARES this one's connection
// pool, host-key verifier and app keys — for the quick connectivity checks, so a "Patchdeck
// key" host is reachable there too and a check reuses a scan's open connection.
func (c *Client) WithTimeouts(dial, exec time.Duration) *Client {
	cp := *c
	cp.timeout, cp.execTimeout = dial, exec
	return &cp
}

// SetAppKeySource wires Patchdeck's own keypair: fn returns the signers an "app_key" host is
// offered (the current key, plus the next one during a rotation).
func (c *Client) SetAppKeySource(fn func() []ssh.Signer) { c.appSigners = fn }

// authMethods builds the SSH auth for a host from its decrypted secrets.
func (c *Client) authMethods(host models.Host, sec secretBlob) ([]ssh.AuthMethod, []ssh.Signer, error) {
	switch host.AuthType {
	case "password":
		return []ssh.AuthMethod{ssh.Password(sec.Password)}, nil, nil
	case models.AuthAppKey:
		var signers []ssh.Signer
		if c.appSigners != nil {
			signers = c.appSigners()
		}
		if len(signers) == 0 {
			return nil, nil, errors.New("Patchdeck's SSH key is not available")
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signers...)}, signers, nil
	default:
		signer, err := parseSigner([]byte(sec.PrivateKeyPEM), sec.Passphrase)
		if err != nil {
			return nil, nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil, nil
	}
}

// dial opens a fresh SSH connection with the given auth, mapping errors to friendly ones
// (a HostKeyError passes through untouched).
func (c *Client) dial(host models.Host, auth []ssh.AuthMethod) (*ssh.Client, error) {
	cfg := &ssh.ClientConfig{User: host.SSHUser, Auth: auth, HostKeyCallback: c.verifyingHostKeyCallback(host), Timeout: c.timeout}
	cli, err := ssh.Dial("tcp", net.JoinHostPort(host.Address, strconv.Itoa(host.Port)), cfg)
	if err != nil {
		var hkErr *HostKeyError
		if errors.As(err, &hkErr) {
			return nil, err
		}
		return nil, fmt.Errorf("%s", FriendlySSHError(err))
	}
	return cli, nil
}

// acquire returns a live pooled connection for host (cached reports reuse), dialing if needed.
func (c *Client) acquire(host models.Host, sec secretBlob) (pc *pooledConn, cached bool, err error) {
	auth, signers, err := c.authMethods(host, sec)
	if err != nil {
		return nil, false, err
	}
	key := poolKey(host, signers)
	p := c.pool
	p.reaper.Do(func() { go p.reap() })

	p.mu.Lock()
	if pc := p.conns[key]; pc != nil && !pc.dead {
		pc.users++
		pc.last = time.Now()
		p.mu.Unlock()
		return pc, true, nil
	}
	p.mu.Unlock()

	cli, err := c.dial(host, auth)
	if err != nil {
		return nil, false, err
	}
	pc = &pooledConn{cli: cli, key: key, users: 1, last: time.Now()}
	p.mu.Lock()
	if existing := p.conns[key]; existing == nil || existing.dead {
		pc.pooled = true
		p.conns[key] = pc
	}
	p.mu.Unlock()
	go func() { // the transport closed (we closed it, the host rebooted, sshd restarted…)
		_ = cli.Wait()
		p.mu.Lock()
		pc.dead = true
		if p.conns[key] == pc {
			delete(p.conns, key)
		}
		p.mu.Unlock()
	}()
	return pc, false, nil
}

// release hands a connection back. broken evicts it (the command lost its connection or was
// aborted); it's closed once no session uses it.
func (c *Client) release(pc *pooledConn, broken bool) {
	p := c.pool
	p.mu.Lock()
	pc.users--
	pc.last = time.Now()
	if broken {
		pc.dead = true
		if p.conns[pc.key] == pc {
			delete(p.conns, pc.key)
		}
	}
	closeNow := (pc.dead || !pc.pooled) && pc.users <= 0
	p.mu.Unlock()
	if closeNow {
		_ = pc.cli.Close()
	}
}

// abort force-closes a connection to unblock a stuck or cancelled command. Other sessions
// sharing it fail too (rare: concurrent operations on the same host) and redial next time.
func (c *Client) abort(pc *pooledConn) {
	p := c.pool
	p.mu.Lock()
	pc.dead = true
	if p.conns[pc.key] == pc {
		delete(p.conns, pc.key)
	}
	p.mu.Unlock()
	_ = pc.cli.Close()
}

func (p *connPool) reap() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for range t.C {
		p.closeIdle(time.Now())
	}
}

func (p *connPool) closeIdle(now time.Time) {
	var idle []*ssh.Client
	p.mu.Lock()
	for k, pc := range p.conns {
		if pc.users <= 0 && now.Sub(pc.last) > poolIdle {
			delete(p.conns, k)
			pc.dead = true
			idle = append(idle, pc.cli)
		}
	}
	p.mu.Unlock()
	for _, cli := range idle {
		_ = cli.Close()
	}
}

// openSession returns a session on a pooled connection. A cached connection that turns out
// stale (or wedged: NewSession is bounded by the dial timeout) is evicted and redialed once.
func (c *Client) openSession(host models.Host, sec secretBlob) (*pooledConn, *ssh.Session, error) {
	for attempt := 0; ; attempt++ {
		pc, cached, err := c.acquire(host, sec)
		if err != nil {
			return nil, nil, err
		}
		sess, err := c.newSession(pc)
		if err == nil {
			return pc, sess, nil
		}
		c.release(pc, true)
		if !cached || attempt > 0 {
			return nil, nil, fmt.Errorf("SSH session error: %s", FriendlySSHError(err))
		}
	}
}

func (c *Client) newSession(pc *pooledConn) (*ssh.Session, error) {
	type res struct {
		s   *ssh.Session
		err error
	}
	ch := make(chan res, 1)
	go func() {
		s, err := pc.cli.NewSession()
		ch <- res{s, err}
	}()
	limit := c.timeout
	if limit <= 0 {
		limit = 20 * time.Second
	}
	t := time.NewTimer(limit)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.s, r.err
	case <-t.C:
		c.abort(pc) // unblocks NewSession
		r := <-ch
		if r.s != nil {
			r.s.Close()
		}
		return nil, errors.New("timed out opening an SSH session")
	}
}

// CheckAppKey reports whether host accepts ONE specific app key (the rotation's "is the new
// key installed here yet?" check). It always dials fresh, never using the pool, so the answer
// reflects that key alone. The normal host-key verification still applies.
func (c *Client) CheckAppKey(host models.Host, signer ssh.Signer) error {
	cli, err := c.dial(host, []ssh.AuthMethod{ssh.PublicKeys(signer)})
	if err != nil {
		return err
	}
	defer cli.Close()
	sess, err := cli.NewSession()
	if err != nil {
		return fmt.Errorf("SSH session error: %s", FriendlySSHError(err))
	}
	defer sess.Close()
	if err := sess.Run("true"); err != nil {
		return fmt.Errorf("logged in, but running a command failed: %v", err)
	}
	return nil
}
