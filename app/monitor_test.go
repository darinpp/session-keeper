package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssooidc/types"
	"github.com/stretchr/testify/require"
)

// blockingAuth stubs Monitor.authenticate: each call reports its background
// flag on calls and blocks until its context is cancelled.
func blockingAuth(calls chan<- bool) func(context.Context, SSOInstance, bool) (*SSOToken, error) {
	return func(ctx context.Context, _ SSOInstance, background bool) (*SSOToken, error) {
		calls <- background
		<-ctx.Done()
		return nil, ctx.Err()
	}
}

func TestTriggerAuth_BackgroundSkippedWhileInFlight(t *testing.T) {
	mon := NewMonitor(nil)
	calls := make(chan bool, 2)
	mon.authenticate = blockingAuth(calls)
	mon.connected = func(context.Context) bool { return true }
	inst := SSOInstance{StartURL: "https://example.awsapps.com/start", Region: "us-east-1"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mon.TriggerAuth(ctx, inst, false)
	<-calls
	mon.TriggerAuth(ctx, inst, true)

	require.Empty(t, calls)
}

func TestTriggerAuth_ManualSupersedesInFlight(t *testing.T) {
	mon := NewMonitor(nil)
	calls := make(chan bool, 2)
	mon.authenticate = blockingAuth(calls)
	mon.connected = func(context.Context) bool { return true }
	inst := SSOInstance{StartURL: "https://example.awsapps.com/start", Region: "us-east-1"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mon.TriggerAuth(ctx, inst, true)
	<-calls
	go mon.TriggerAuth(ctx, inst, false)

	require.False(t, <-calls)
}

func TestTriggerAuth_SupersededAttemptKeepsSlot(t *testing.T) {
	mon := NewMonitor(nil)
	calls := make(chan bool, 2)
	mon.authenticate = blockingAuth(calls)
	mon.connected = func(context.Context) bool { return true }
	inst := SSOInstance{StartURL: "https://example.awsapps.com/start", Region: "us-east-1"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mon.TriggerAuth(ctx, inst, true)
	<-calls
	go mon.TriggerAuth(ctx, inst, false)
	<-calls

	require.True(t, mon.authInFlight(inst.StartURL))
}

func TestTriggerAuth_BackgroundWaitsForConnectivity(t *testing.T) {
	mon := NewMonitor(nil)
	calls := make(chan bool, 1)
	mon.authenticate = blockingAuth(calls)
	mon.connected = func(context.Context) bool { return false }
	inst := SSOInstance{StartURL: "https://example.awsapps.com/start", Region: "us-east-1"}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		mon.TriggerAuth(ctx, inst, true)
		close(done)
	}()
	require.Equal(t, StateOffline, (<-mon.StatusCh()).State)
	cancel()
	<-done

	require.Empty(t, calls)
}

func TestCheckAll_ExpiredTokenShowsRenewingWhileAutoAuthRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	inst := SSOInstance{StartURL: "https://example.awsapps.com/start", Region: "us-east-1"}
	require.NoError(t, SaveToken(&SSOToken{
		StartURL:  inst.StartURL,
		Region:    inst.Region,
		ExpiresAt: time.Now().UTC().Add(-time.Minute).Format(timeFormat),
	}))
	mon := NewMonitor([]SSOInstance{inst})
	calls := make(chan bool, 1)
	mon.authenticate = blockingAuth(calls)
	mon.connected = func(context.Context) bool { return true }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mon.checkAll(ctx)
	<-calls

	require.Equal(t, StateRenewing, (<-mon.StatusCh()).State)
}

func TestMonitorRun_ExitsOnContextCancel(t *testing.T) {
	mon := NewMonitor(nil)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		mon.Run(ctx)
		close(done)
	}()

	// Cancel immediately
	cancel()

	select {
	case <-done:
		// Exited promptly
	case <-time.After(2 * time.Second):
		t.Fatal("Monitor.Run did not exit on context cancel")
	}
}

func TestMonitorWait(t *testing.T) {
	mon := NewMonitor(nil)

	// Nothing in flight — should return immediately
	done := make(chan struct{})
	go func() {
		mon.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Wait blocked with nothing in flight")
	}
}

func TestRefreshable_InvalidGrantMarksTokenDead(t *testing.T) {
	mon := NewMonitor(nil)
	const url = "https://example.awsapps.com/start"
	token := &SSOToken{RefreshToken: "rt-1"}

	mon.noteRefreshFailure(url, token, fmt.Errorf("refresh token: %w", &types.InvalidGrantException{}))

	require.False(t, mon.refreshable(url, token))
}

func TestRefreshable_NewRefreshTokenIsRetried(t *testing.T) {
	mon := NewMonitor(nil)
	const url = "https://example.awsapps.com/start"

	mon.noteRefreshFailure(url, &SSOToken{RefreshToken: "rt-1"}, &types.InvalidGrantException{})

	require.True(t, mon.refreshable(url, &SSOToken{RefreshToken: "rt-2"}))
}

func TestRefreshable_OtherErrorsStayRetryable(t *testing.T) {
	mon := NewMonitor(nil)
	const url = "https://example.awsapps.com/start"
	token := &SSOToken{RefreshToken: "rt-1"}

	mon.noteRefreshFailure(url, token, errors.New("dial tcp: i/o timeout"))

	require.True(t, mon.refreshable(url, token))
}

func TestShouldAutoAuth_RetriedAfterWake(t *testing.T) {
	mon := NewMonitor(nil)
	const url = "https://example.awsapps.com/start"
	mon.autoAuthGivenUp.Store(url, true)

	mon.ResetAutoAuth()

	require.True(t, mon.shouldAutoAuth(url))
}

func TestWaitForConnectivity_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, waitForConnectivity(ctx))
}
