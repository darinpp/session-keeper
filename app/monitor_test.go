package app

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssooidc/types"
	"github.com/stretchr/testify/require"
)

func TestTriggerAuth_Deduplication(t *testing.T) {
	mon := NewMonitor(nil)

	inst := SSOInstance{StartURL: "https://example.awsapps.com/start", Region: "us-east-1"}

	// Manually mark as in-flight
	mon.authInFlight.Store(inst.StartURL, true)

	// TriggerAuth should return immediately without doing anything
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		mon.TriggerAuth(ctx, inst, false)
		close(done)
	}()

	select {
	case <-done:
		// Returned immediately — deduplication worked
	case <-time.After(1 * time.Second):
		t.Fatal("TriggerAuth did not return; deduplication failed")
	}

	mon.authInFlight.Delete(inst.StartURL)
}

func TestTriggerAuth_ConcurrentCalls(t *testing.T) {
	// Keep the real Authenticate these calls run away from the user's own
	// token and client-registration cache.
	t.Setenv("HOME", t.TempDir())
	mon := NewMonitor(nil)

	inst := SSOInstance{StartURL: "https://example.awsapps.com/start", Region: "us-east-1"}

	// Track how many times auth actually runs (past the dedup check)
	var authCount int32

	// Pre-load the authInFlight to block both goroutines
	// Then release and see only one gets through at a time
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Launch two concurrent TriggerAuth calls
	// Both will try Authenticate which will fail (no AWS creds in test),
	// but only one should get past LoadOrStore
	done1 := make(chan struct{})
	done2 := make(chan struct{})

	// Temporarily store to simulate in-flight, then delete to let the real calls race
	mon.authInFlight.Store(inst.StartURL, true)

	go func() {
		mon.authInFlight.Delete(inst.StartURL)
		mon.TriggerAuth(ctx, inst, false)
		atomic.AddInt32(&authCount, 1)
		close(done1)
	}()
	go func() {
		// Small delay to let first goroutine claim the lock
		time.Sleep(10 * time.Millisecond)
		mon.TriggerAuth(ctx, inst, false)
		atomic.AddInt32(&authCount, 1)
		close(done2)
	}()

	<-done1
	<-done2

	// Both calls completed (one ran auth, one was deduplicated)
	require.Equal(t, int32(2), atomic.LoadInt32(&authCount))
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

func TestWaitForConnectivity_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, waitForConnectivity(ctx))
}
