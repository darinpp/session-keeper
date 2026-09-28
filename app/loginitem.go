package app

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Identity of the installed macOS app.
const (
	AppName  = "Session Keeper"
	BundleID = "com.darinpp.session-keeper"
	ExecName = "session-keeper"
)

// BundlePath is where the app installs itself, a normal .app under the user's
// Applications folder rather than a bare binary in a hidden directory.
func BundlePath() string {
	return filepath.Join(homeDir(), "Applications", AppName+".app")
}

// LoginItemEnabled reports whether the app is registered to open at login.
func LoginItemEnabled() bool {
	out, err := exec.Command("osascript", "-e",
		`tell application "System Events" to get the name of every login item`).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), AppName)
}

// EnableLoginItem registers the installed bundle to open at login, if it
// isn't already.
func EnableLoginItem() error {
	script := fmt.Sprintf(`tell application "System Events"
	if not (exists login item %q) then
		make login item at end with properties {path:%q, hidden:false}
	end if
end tell`, AppName, BundlePath())
	return exec.Command("osascript", "-e", script).Run()
}

// DisableLoginItem removes the open-at-login registration, if present.
func DisableLoginItem() error {
	script := fmt.Sprintf(`tell application "System Events"
	if exists login item %q then delete login item %q
end tell`, AppName, AppName)
	return exec.Command("osascript", "-e", script).Run()
}
