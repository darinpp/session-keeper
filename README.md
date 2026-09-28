# Session Keeper

Session Keeper is a macOS menu-bar app that keeps an AWS IAM Identity Center (SSO) session alive: it signs in via the browser using an authorization_code + PKCE flow, caches the token in the same format the AWS CLI uses (`~/.aws/sso/cache/`), and silently renews it in the background before it expires.

## Requirements

- macOS on Apple Silicon (arm64)
- An SSO instance configured in `~/.aws/config` (`sso_start_url` + `sso_region`)

## Install

1. Download the latest `session-keeper-macos-arm64.zip` from [Releases](../../releases) and unzip it to get `Session Keeper.app`.
2. Move `Session Keeper.app` to your `Applications` folder, then clear the quarantine flag macOS puts on downloaded apps (skipping this makes it refuse to open):
   ```
   xattr -dr com.apple.quarantine "/Applications/Session Keeper.app"
   ```
3. Open it. It runs as a menu-bar icon (no Dock icon). Logs go to `~/Library/Logs/session-keeper.log`. If you have no signed-in session yet, the icon shows 🚫 — click **Login** once to sign in.
4. To have it open automatically at login, enable **Start at Login** from its menu.

To remove it: disable **Start at Login**, then drag `Session Keeper.app` to the Trash.

## Usage

The menu-bar icon is a moon phase showing how much of the current token's lifetime is left (🌕 fresh → 🌑 about to expire), 🚫 for no session/error, 📡 for offline/retrying. Click it for:

- **Login** — re-authenticate now (opens the browser)
- **Token** — test helpers: **Refresh** (expire the access token, keep the refresh token), **Federation** (expire and invalidate the refresh token, forcing a fresh sign-in), **Remove** (same as Federation, but skips the silent browser method to test the fallback)
- **Chrome Profile** — choose which Chrome profile silent renewal copies cookies from; defaults to whichever profile Chrome itself would open
- **Start at Login** — open automatically when you log in
- **Open Log** — open the log file
- **Quit**

## Silent renewal

When a session needs to re-authenticate automatically, the app launches a disposable, headless copy of Chrome using an isolated copy of the chosen profile's cookies — nothing ever becomes visible, and the copy is deleted immediately after. This requires Google Chrome and `sqlite3` (bundled with macOS) to be installed. If it's ever unavailable or fails, the app does nothing further automatically and waits for a manual **Login** click.

## Security Q&A

**What does the app talk to over the network?**
Only AWS IAM Identity Center OIDC (`oidc.<region>.amazonaws.com`) and a TCP dial to `1.1.1.1:53` to detect connectivity. There is no telemetry.

**How is the sign-in protected?**
It uses the `authorization_code` grant with PKCE (S256) and a random `state` that is verified on the callback. The callback listener binds to `127.0.0.1` on a random port and is shut down as soon as the flow finishes.

**Where are tokens stored?**
In `~/.aws/sso/cache/`, the same place and format the AWS CLI uses, so no new secret store is introduced. Files are written atomically with mode `0600` in a `0700` directory, and permissions are restricted before any token data is written. Tokens are never logged.

**Does the app decrypt Chrome cookies?**
No. It only runs `sqlite3 .backup` to copy the `Cookies` database and a `DELETE` to strip Google's cookies from the copy; it never reads the Keychain. Decryption happens inside the headless Chrome, using Chrome's own "Chrome Safe Storage" key exactly as a normal Chrome launch does.

**Does copying the cookies grant access that wasn't already there?**
No. Chrome's profile directory isn't protected by macOS privacy controls (TCC), so any process running as your user can already read it. The app gains nothing beyond what your user account already has.

**What if a copy is left behind, e.g. after a crash?**
It sits in your per-user `$TMPDIR` in a `0700` directory, still encrypted with the same key, and readable only by the same processes that can read the original profile. The one difference is that its session cookies aren't cleared if you sign out in your real Chrome. The app doesn't sweep orphaned copies, so remove any `$TMPDIR/session-keeper-chrome-*` directory by hand.

**Why are Google cookies removed from the copy?**
Chrome runs background Google account-consistency checks using whatever Google cookies a profile holds. Seeing the same Google session in two Chrome processes at once makes Google revoke it, signing the real browser out. Those cookies aren't needed for the SSO flow.

**Does silent renewal get around the identity provider's policies?**
No. The same sign-in completes without interaction in your normal browser whenever the IdP session is still valid; the headless path only avoids showing a tab. If the IdP requires interactive sign-in or MFA, the headless attempt times out after 20 seconds and the app waits for a manual **Login**. Refresh tokens are bounded by the session duration configured in IAM Identity Center.

**Could a crafted `sso_region` redirect the sign-in elsewhere?**
No. The AWS SDK rejects an invalid region ("invalid input region") while registering the client, before any browser is opened.

## Running from source

- `go build -o session-keeper . && ./session-keeper` — run in place, logs to the terminal
- `./session-keeper --install` — build `Session Keeper.app` into `~/Applications`, register it to open at login, and launch it
- `./session-keeper --uninstall` — remove the login item and the installed app
- `./session-keeper --test-auth` — one-shot CLI login test, no menu bar
