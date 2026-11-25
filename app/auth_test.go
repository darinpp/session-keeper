package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIsNetworkError(t *testing.T) {
	require.True(t, isNetworkError(errors.New("dial tcp: lookup oidc.us-east-2.amazonaws.com: i/o timeout")))
	require.True(t, isNetworkError(errors.New("Post https://example.com: dial tcp: connection refused")))
	require.True(t, isNetworkError(errors.New("lookup example.com: no such host")))
	require.True(t, isNetworkError(errors.New("connect: network is unreachable")))
	require.True(t, isNetworkError(errors.New("dial tcp 1.2.3.4:443: no route to host")))
	// Wrapped errors (as returned by Authenticate)
	require.True(t, isNetworkError(fmt.Errorf("create token: %w",
		errors.New("operation error: dial tcp: lookup host: i/o timeout"))))
	// Non-network errors
	require.False(t, isNetworkError(errors.New("InvalidGrantException")))
	require.False(t, isNetworkError(errors.New("waiting for sign-in: context deadline exceeded")))
	require.False(t, isNetworkError(nil))
}

func TestWithWallClockTimeout_ExpiresWithDeadlineExceeded(t *testing.T) {
	ctx, cancel := withWallClockTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	<-ctx.Done()

	require.Equal(t, context.DeadlineExceeded, context.Cause(ctx))
}

func TestWithWallClockTimeout_CancelledByParent(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := withWallClockTimeout(parent, time.Hour)
	defer cancel()

	cancelParent()
	<-ctx.Done()

	require.Equal(t, context.Canceled, context.Cause(ctx))
}

// startFakeChrome runs a process whose command line contains dir and that, like
// Chrome, recreates dir and writes into it shortly after receiving SIGTERM.
func startFakeChrome(t *testing.T, dir string) {
	script := `trap 'sleep 0.3; mkdir -p "$0"; touch "$0/Variations"; exit 0' TERM; while :; do sleep 0.1; done`
	cmd := exec.Command("sh", "-c", script, dir)
	require.NoError(t, cmd.Start())
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
}

func TestRemoveChromeProfile_WaitsForChromeToExit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ExecName+"-chrome-test")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	startFakeChrome(t, dir)

	removeChromeProfile(dir)
	time.Sleep(time.Second)

	_, err := os.Stat(dir)
	require.True(t, os.IsNotExist(err))
}

func TestRemoveStaleChromeProfiles(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	stale := filepath.Join(os.TempDir(), ExecName+"-chrome-123")
	require.NoError(t, os.MkdirAll(stale, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(stale, "Variations"), nil, 0o600))

	removeStaleChromeProfiles()

	_, err := os.Stat(stale)
	require.True(t, os.IsNotExist(err))
}
