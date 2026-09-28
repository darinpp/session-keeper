package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/darinpp/session-keeper/app"
)

func main() {
	args := os.Args[1:]
	hasFlag := func(flag string) bool { return slices.Contains(args, flag) }

	switch {
	case hasFlag("--install"):
		if err := doInstall(); err != nil {
			log.Fatalf("install: %v", err)
		}
		return
	case hasFlag("--uninstall"):
		if err := doUninstall(); err != nil {
			log.Fatalf("uninstall: %v", err)
		}
		return
	case hasFlag("--build-bundle"):
		dest := flagValue(args, "--build-bundle")
		exe, err := os.Executable()
		if err != nil {
			log.Fatalf("build-bundle: %v", err)
		}
		if err := buildBundle(exe, dest); err != nil {
			log.Fatalf("build-bundle: %v", err)
		}
		return
	case hasFlag("--test-auth"):
		testAuth()
		return
	}

	setupLogging()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("Received %s, shutting down...", sig)
		app.Quit()
	}()

	app.Run()
}

// flagValue returns the argument following flag, or "" if absent.
func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// setupLogging sends the log to LogFile when running as the installed .app
// (launched at login or via `open`, where there's no console), and leaves it
// on stderr for a plain terminal run.
func setupLogging() {
	if exe, err := os.Executable(); err != nil || insideBundle(exe) == "" {
		return
	}
	path := app.LogFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(f)
}

func testAuth() {
	log.SetFlags(log.Ltime)

	instances, err := app.ParseSSOInstances()
	if err != nil {
		log.Fatalf("Failed to parse SSO instances: %v", err)
	}

	fmt.Printf("Found %d SSO instance(s):\n", len(instances))
	for i, inst := range instances {
		fmt.Printf("  %d. %s (%s)\n", i+1, inst.StartURL, inst.Region)
	}

	inst := instances[0]
	fmt.Printf("\nTesting auth for: %s\n", inst.StartURL)

	if token, err := app.LoadToken(inst.StartURL); err == nil {
		if token.IsExpired() {
			fmt.Printf("Existing token is EXPIRED (was valid until %s)\n", token.ExpiresAt)
		} else {
			fmt.Printf("Existing token is VALID (%s remaining)\n", token.TimeRemaining().Round(time.Second))
			fmt.Println("Proceeding with fresh auth anyway...")
		}
	} else {
		fmt.Println("No cached token found")
	}

	fmt.Println("\nStarting authentication (browser should open)...")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	token, err := app.Authenticate(ctx, inst, false)
	if err != nil {
		log.Fatalf("Authentication FAILED: %v", err)
	}

	fmt.Println("\nAuthentication SUCCEEDED!")
	fmt.Printf("  Access token: %s...%s\n", token.AccessToken[:8], token.AccessToken[len(token.AccessToken)-4:])
	fmt.Printf("  Expires at:   %s\n", token.ExpiresAt)
	fmt.Printf("  Remaining:    %s\n", token.TimeRemaining().Round(time.Second))
	fmt.Printf("  Has refresh:  %v\n", token.RefreshToken != "")
	fmt.Printf("  Saved to:     %s\n", app.TokenCacheFile(inst.StartURL))
}
