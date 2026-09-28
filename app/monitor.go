package app

import (
	"context"
	"errors"
	"log"
	"net"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssooidc/types"
)

// SessionState represents the current state of the SSO session.
type SessionState int

const (
	StateValid    SessionState = iota // session active
	StateWarning                      // expiring within warningThreshold
	StateExpired                      // expired, needs re-auth
	StateRenewing                     // renewal in progress
	StateOffline                      // network unavailable, waiting for connectivity
)

const warningThreshold = 15 * time.Minute
const connectivityCheckInterval = 15 * time.Second
const networkTimeout = 30 * time.Second

// SessionStatus is broadcast by the monitor to the UI.
type SessionStatus struct {
	State           SessionState
	Remaining       time.Duration
	Fraction        float64 // fraction of the token's lifetime remaining, in [0, 1]
	Instance        SSOInstance
	HasRefreshToken bool
}

// tokenStatus derives the display status for a cached token.
func tokenStatus(token *SSOToken, inst SSOInstance) SessionStatus {
	remaining := token.TimeRemaining()
	state := StateValid
	switch {
	case remaining == 0:
		state = StateExpired
	case remaining < warningThreshold:
		state = StateWarning
	}
	return SessionStatus{
		State:           state,
		Remaining:       remaining,
		Fraction:        token.RemainingFraction(),
		Instance:        inst,
		HasRefreshToken: token.RefreshToken != "",
	}
}

// Monitor watches SSO token expiry and attempts renewal.
type Monitor struct {
	instances        []SSOInstance
	statusCh         chan SessionStatus
	authInFlight     sync.Map       // tracks in-progress auth per StartURL
	lastRenewAttempt sync.Map       // per-StartURL time.Time of last failed refresh
	autoAuthGivenUp  sync.Map       // per-StartURL: true once an automatic re-auth has been tried this expiry
	deadRefresh      sync.Map       // per-StartURL: refresh token string AWS rejected with invalid_grant
	wg               sync.WaitGroup // tracks in-flight auth attempts for graceful shutdown
}

func NewMonitor(instances []SSOInstance) *Monitor {
	return &Monitor{
		instances: instances,
		statusCh:  make(chan SessionStatus, 1),
	}
}

// Wait blocks until all in-flight auth attempts finish.
func (m *Monitor) Wait() {
	m.wg.Wait()
}

func (m *Monitor) StatusCh() <-chan SessionStatus {
	return m.statusCh
}

// send delivers s to the UI without blocking; a dropped update is superseded
// by the next one or by the UI's own ticker.
func (m *Monitor) send(s SessionStatus) {
	select {
	case m.statusCh <- s:
	default:
	}
}

func (m *Monitor) sendStatus(token *SSOToken, inst SSOInstance) {
	m.send(tokenStatus(token, inst))
}

func checkConnectivity(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", "1.1.1.1:53")
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func waitForConnectivity(ctx context.Context) bool {
	log.Printf("waiting for connectivity (checking every %s)...", connectivityCheckInterval)
	ticker := time.NewTicker(connectivityCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if checkConnectivity(ctx) {
				return true
			}
		}
	}
}

func (m *Monitor) authenticateWithRetry(ctx context.Context, inst SSOInstance, background bool) (*SSOToken, error) {
	for {
		token, err := Authenticate(ctx, inst, background)
		if err == nil {
			return token, nil
		}
		if !isNetworkError(err) {
			return nil, err
		}
		log.Printf("network error for %s, waiting for connectivity...", inst.StartURL)
		m.send(SessionStatus{State: StateOffline, Instance: inst})
		if !waitForConnectivity(ctx) {
			return nil, ctx.Err()
		}
		log.Printf("connectivity restored, retrying auth for %s", inst.StartURL)
		m.send(SessionStatus{State: StateRenewing, Instance: inst})
	}
}

// TriggerAuth runs one sign-in for inst — silent (background) when the
// monitor starts it, visible when the user clicks Login — unless one is
// already in flight for that instance.
func (m *Monitor) TriggerAuth(ctx context.Context, inst SSOInstance, background bool) {
	if _, loaded := m.authInFlight.LoadOrStore(inst.StartURL, true); loaded {
		return
	}
	m.wg.Add(1)
	defer m.wg.Done()
	defer m.authInFlight.Delete(inst.StartURL)

	m.send(SessionStatus{State: StateRenewing, Instance: inst})

	token, err := m.authenticateWithRetry(ctx, inst, background)
	if err != nil {
		log.Printf("authentication failed for %s: %v", inst.StartURL, err)
		m.send(SessionStatus{State: StateExpired, Instance: inst})
		return
	}

	m.autoAuthGivenUp.Delete(inst.StartURL)
	m.sendStatus(token, inst)
}

func (m *Monitor) Run(ctx context.Context) {
	m.checkAll(ctx)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkAll(ctx)
		}
	}
}

// refreshWithTimeout bounds a single RefreshToken attempt so a hung network
// call can never block checkAll — and therefore Monitor.Run's ticker loop —
// indefinitely.
func refreshWithTimeout(ctx context.Context, inst SSOInstance, token *SSOToken) (*SSOToken, error) {
	ctx, cancel := context.WithTimeout(ctx, networkTimeout)
	defer cancel()
	return RefreshToken(ctx, inst, token)
}

func (m *Monitor) checkAll(ctx context.Context) {
	for _, inst := range m.instances {
		if _, inFlight := m.authInFlight.Load(inst.StartURL); inFlight {
			continue
		}

		token, err := LoadToken(inst.StartURL)
		if err != nil || token.IsExpired() {
			if token != nil && m.tryRefresh(ctx, inst, token) {
				continue
			}
			m.send(SessionStatus{State: StateExpired, Instance: inst})
			if m.shouldAutoAuth(inst.StartURL) {
				m.autoAuthGivenUp.Store(inst.StartURL, true)
				go m.TriggerAuth(ctx, inst, true)
			}
			continue
		}

		if m.shouldRenew(inst.StartURL, token) && m.tryRefresh(ctx, inst, token) {
			continue
		}
		m.sendStatus(token, inst)
	}
}

// tryRefresh renews token via its refresh token, if it has a usable one, and
// reports whether that succeeded.
func (m *Monitor) tryRefresh(ctx context.Context, inst SSOInstance, token *SSOToken) bool {
	if !m.refreshable(inst.StartURL, token) {
		return false
	}
	refreshed, err := refreshWithTimeout(ctx, inst, token)
	if err != nil {
		log.Printf("refresh failed for %s: %v", inst.StartURL, err)
		m.lastRenewAttempt.Store(inst.StartURL, time.Now())
		m.noteRefreshFailure(inst.StartURL, token, err)
		return false
	}
	m.lastRenewAttempt.Delete(inst.StartURL)
	m.sendStatus(refreshed, inst)
	return true
}

// shouldRenew reports whether a still-valid token has one third or less of
// its lifetime remaining, and the retry backoff (a quarter of the token's
// lifetime) has elapsed since the last failed refresh. A token issued with a
// TTL under minDisplayTTL is never due: it means the underlying session is
// already at its ceiling, so a refresh can only return the same remainder —
// it's left to expire and be re-authenticated instead.
func (m *Monitor) shouldRenew(startURL string, token *SSOToken) bool {
	received, exp, ok := token.times()
	if !ok {
		return false
	}
	span := exp.Sub(received)
	if span < minDisplayTTL {
		return false
	}
	if time.Now().Before(received.Add(span * 2 / 3)) {
		return false
	}
	lastAttempt, _ := m.lastRenewAttempt.Load(startURL)
	last, _ := lastAttempt.(time.Time)
	return time.Since(last) >= span/4
}

// refreshable reports whether token has a refresh token worth trying — one
// that AWS hasn't already rejected as invalid_grant. A new sign-in brings a
// different refresh token, so it's tried again automatically.
func (m *Monitor) refreshable(startURL string, token *SSOToken) bool {
	if token.RefreshToken == "" {
		return false
	}
	dead, _ := m.deadRefresh.Load(startURL)
	return dead != token.RefreshToken
}

// noteRefreshFailure remembers token's refresh token as dead if AWS rejected
// it with invalid_grant; other failures (e.g. network) leave it retryable.
func (m *Monitor) noteRefreshFailure(startURL string, token *SSOToken, err error) {
	if _, ok := errors.AsType[*types.InvalidGrantException](err); ok {
		m.deadRefresh.Store(startURL, token.RefreshToken)
	}
}

// shouldAutoAuth reports whether an expired token is still worth an automatic
// re-authentication attempt. A failed attempt can't succeed by retrying on
// the next tick — the identity provider needs a fresh interactive login
// either way — so it's tried at most once per expiry and then left alone
// until a TriggerAuth success (automatic or manual) resets it.
func (m *Monitor) shouldAutoAuth(startURL string) bool {
	_, gaveUp := m.autoAuthGivenUp.Load(startURL)
	return !gaveUp
}
