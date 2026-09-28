package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/darinpp/session-keeper/app"
)

// infoPlist is the bundle's Info.plist. LSUIElement makes it a menu-bar agent
// with no Dock icon or app-switcher entry.
func infoPlist() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>%s</string>
	<key>CFBundleDisplayName</key><string>%s</string>
	<key>CFBundleIdentifier</key><string>%s</string>
	<key>CFBundleExecutable</key><string>%s</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
	<key>LSUIElement</key><true/>
	<key>LSMinimumSystemVersion</key><string>13.0</string>
</dict>
</plist>
`, app.AppName, app.AppName, app.BundleID, app.ExecName)
}

// buildBundle assembles a .app at dest whose executable is a copy of srcBinary.
func buildBundle(srcBinary, dest string) error {
	macOS := filepath.Join(dest, "Contents", "MacOS")
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, "Contents", "Info.plist"),
		[]byte(infoPlist()), 0o644); err != nil {
		return err
	}
	return copyExecutable(srcBinary, filepath.Join(macOS, app.ExecName))
}

// insideBundle reports the enclosing .app for an executable path, or "" if the
// executable isn't inside a bundle.
func insideBundle(exePath string) string {
	const marker = ".app/Contents/MacOS/"
	if i := strings.Index(exePath, marker); i >= 0 {
		return exePath[:i+len(".app")]
	}
	return ""
}

// doInstall places the app at BundlePath, registers it to open at login, and
// launches it. It works whether the running binary is a bare build or already
// inside a downloaded .app.
func doInstall() error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	dest := app.BundlePath()

	stopRunning(dest)

	switch srcBundle := insideBundle(src); {
	case srcBundle == dest:
		// Already installed in place; nothing to copy.
	case srcBundle != "":
		if err := replaceDir(srcBundle, dest); err != nil {
			return fmt.Errorf("copy app bundle: %w", err)
		}
	default:
		_ = os.RemoveAll(dest)
		if err := buildBundle(src, dest); err != nil {
			return fmt.Errorf("build app bundle: %w", err)
		}
	}

	if err := app.EnableLoginItem(); err != nil {
		return fmt.Errorf("register login item: %w", err)
	}
	if err := exec.Command("open", dest).Run(); err != nil {
		return fmt.Errorf("launch app: %w", err)
	}

	log.Printf("installed: %s", dest)
	log.Printf("opens at login; logs at %s", app.LogFile())
	return nil
}

// doUninstall unregisters the login item, stops the app, and removes the bundle.
func doUninstall() error {
	dest := app.BundlePath()
	_ = app.DisableLoginItem()
	stopRunning(dest)
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	log.Printf("removed login item and %s", dest)
	return nil
}

// stopRunning terminates any process running the bundle's executable so the
// files can be replaced.
func stopRunning(bundle string) {
	exe := filepath.Join(bundle, "Contents", "MacOS", app.ExecName)
	_ = exec.Command("pkill", "-f", exe).Run()
}

func replaceDir(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return exec.Command("cp", "-R", src, dst).Run()
}

func copyExecutable(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}
