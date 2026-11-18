# NewAPI Daily Check-in Plugin for CPA

[简体中文](README.md) | **English**

[Download](https://github.com/5ifish/cpa-plugin-newapi-checkin/releases/latest) · [Report an issue](https://github.com/5ifish/cpa-plugin-newapi-checkin/issues) · [Compatibility](docs/compatibility.md)

Manage accounts across NewAPI sites, schedule daily check-ins, and view credential health, execution history, and site-reported rewards from CPA's management interface. The plugin exposes management APIs only; it does not proxy model requests.

## Downloads

**v0.2.2** is built separately for the six native platforms below. The release workflow runs native builds and real CPA integration tests on every platform and collects archives and checksums only after all jobs pass. Use the Download link above; verification records are in the [compatibility notes](docs/compatibility.md). `amd64` means `x86_64 / x64`; `arm64` means `aarch64`. 32-bit ARM is not included.

| Platform | Release asset | Plugin inside the ZIP |
| --- | --- | --- |
| Windows x64 | `newapi-checkin_0.2.2_windows_amd64.zip` | `newapi-checkin.dll` |
| Windows ARM64 | `newapi-checkin_0.2.2_windows_arm64.zip` | `newapi-checkin.dll` |
| Linux x86_64 | `newapi-checkin_0.2.2_linux_amd64.zip` | `newapi-checkin.so` |
| Linux ARM64 | `newapi-checkin_0.2.2_linux_arm64.zip` | `newapi-checkin.so` |
| macOS Intel | `newapi-checkin_0.2.2_darwin_amd64.zip` | `newapi-checkin.dylib` |
| macOS Apple Silicon (M series) | `newapi-checkin_0.2.2_darwin_arm64.zip` | `newapi-checkin.dylib` |

Download archives and `checksums.txt` from the same release. On Linux, run `sha256sum -c checksums.txt --ignore-missing`. On macOS, compare `shasum -a 256 <archive>` with the checksum file; on Windows, use `Get-FileHash -Algorithm SHA256 <archive>`.

Linux requires **glibc 2.34 or newer**. See the [tested environments](docs/compatibility.md) for the actual distribution and CPA versions validated. Alpine/musl is unsupported. Select the OS and architecture of the CPA process; for Docker, use the container's runtime environment.

macOS builds target **macOS 15** by default. Intel and Apple Silicon packages are separate. Match the CPA process architecture: an Intel CPA running through Rosetta on Apple Silicon needs `darwin_amd64`. Linux containers on a Mac still need Linux packages.

## Installation

This is a CPA C ABI shared library, not a Go `plugin` package. The SDK dependency is pinned to **CPA v8.0.58**, commit `e5e6ad1e1b91fc20ab9964e6fb22c0c269b9d882`. The NewAPI API reference is commit `a3fd673bb7897691b1041aaf8d78bab6cc15e1d5`; custom and older deployments must provide the same API contract.

1. Extract the archive into the appropriate platform subdirectory under CPA's plugin directory: `plugins/windows/amd64/newapi-checkin.dll`, `plugins/windows/arm64/newapi-checkin.dll`, `plugins/linux/amd64/newapi-checkin.so`, `plugins/linux/arm64/newapi-checkin.so`, `plugins/darwin/amd64/newapi-checkin.dylib`, or `plugins/darwin/arm64/newapi-checkin.dylib`.
2. Enable the plugin in CPA's configuration. Merge the `plugins` section into an existing installation and keep your current management key.

```yaml
management:
  secret-key: "your-management-key"
plugins:
  enabled: true
  dir: "plugins"
  configs:
    newapi-checkin:
      enabled: true
      checkin-at: "09:00"
      jitter-minutes: 20
      timezone: "Asia/Shanghai"
```

3. Start CPA and open **NewAPI 签到** from its plugin menu, or visit `/v0/resource/plugins/newapi-checkin/console`.
4. Enter the CPA management key and add a site account. The key stays in the current tab's `sessionStorage`, never in the URL. Management authentication failures stop polling until a valid key is entered.

Static UI assets are public; account and task data require authentication through `/v0/management/plugins/newapi-checkin/…`. Use HTTPS for remote management.

The console's "refresh balances" action only refreshes each account's wallet balance; model usage lives in the statistics panel above the account list, which aggregates the bound accounts' site logs for 24h / 7d / 30d: the left card shows token shares by account or model, the right card a per-bucket stacked model chart and ranking, including cache rate, request counts, converted cost and average RPM/TPM. Results are cached per range in the state file and fetched live only when switching ranges or clicking "refresh statistics"; data comes from the site's own consumption log `/api/log/self`, aggregates only, no request details, and a failed log read only drops that account from the statistics. The bundled management UI currently uses Chinese; this repository provides both Chinese and English documentation.

### Per-account request status

Each account has a full-width timeline below its information row: 48 blocks of 30 minutes covering the last 24 hours. It aggregates that account's `/api/log/self` consumption (`type=2`) and error (`type=5`) records. Green means consumption with no recorded errors, mixed records use a success-ratio color gradient, red means errors only, and gray means no records. Unsupported responses, failed reads, or the 2,000-entry cap per log type show an unknown/incomplete striped state rather than a fabricated rate. Hover, tap, or use keyboard arrow keys for bucket details.

The visible accounts page checks the five-minute cache and starts bounded background refreshes; "刷新请求状态" forces a refresh. Status polling reads cache only, and the usage chart's range does not change this 24-hour window. Failed refreshes retain the old window marked stale; rebinds and deletion invalidate account health. No model probes are sent. **The record success ratio is not uptime:** site errors may include retry attempts or omit failures, and an empty error log does not prove no failures occurred. Only timestamps and counters are retained, not raw logs, error bodies, token names, or IP addresses.

Below the timeline, each account lists the **available models** it may call on its site, read from `/api/user/models` (the model names enabled for the account's user group) and rendered as chips with the full name on hover. The list is cached for twelve hours and shares the same background worker and "刷新请求状态" button; failed or unsupported reads show an unconfirmed state and keep the previous list marked stale, and a site that returns no models is shown as such. Only model names and timestamps are stored. The site address on the second identity line is a link that opens in a new tab (`noopener`).

## Account authentication

| Method | Requirements and limitations |
| --- | --- |
| PAT / access token | Recommended. Scoped PATs need `profile:read`, `wallet:read`, and `wallet:write`. The reference site defaults PAT expiry to 30 days; explicitly select **Never expires** if desired. The plugin's expiry field is a reminder, not a validity override. Leave it blank when unknown. Legacy tokens may be subject to a site-specific retirement date. |
| Site username and password | For the site's own local account, not LinuxDO credentials. Creates a dedicated session through the site's v2 password encryption protocol and does not save the password. Interactive 2FA/passkey challenges require user action. |
| Dedicated refresh cookie | Advanced import. Create a separate login session, confirm exclusive use, and stop using that session in browsers or other tools. Already-managed sessions and sessions with an uncertain refresh outcome cannot be imported again. |

Some NewAPI sites, including `new.xkool.cfd`, also require the `New-Api-User` header. When binding an access token, enter the same account's numeric ID in the optional site user ID field. You can find it in the headers of an authenticated `/api/user/self` browser request. Leave it blank for sites that do not require it. The plugin verifies the ID against the token identity and reuses the verified ID for rechecks, scheduled check-ins, and rebinding. The management API accepts the optional `upstream_user_id` field.

Login sessions last at most 30 days under the reference protocol. Refreshing an access token **does not extend the original session expiry**. Expired, revoked, or retired credentials require rebinding. Rebinding the same site/user preserves history, cancels previous work, and checks the new credentials.

Sites requiring Turnstile for check-in are saved in a paused state and cannot run unattended. Changing the credential type does not bypass this challenge. Site URLs must use HTTPS, with normal certificate validation and no redirect following; HTTP is permitted only for loopback testing.

Binding validates identity and reads check-in state. It does not submit a check-in to probe write access. The plugin confirms `wallet:write` only when an actual check-in is needed and succeeds.

## Login and check-in

Authentication and reward collection are separate, and each account picks a **check-in mode**:

- **Standard API check-in (default, `api`)**: upstream NewAPI's `/api/user/login` creates a session; `POST /api/user/checkin` grants check-in rewards. The plugin uses a PAT or a saved session for check-in without asking for a password every day.
- **Login-reward mode (`login`)**: for sites such as AgentRouter that grant the daily reward on a fresh login. These sites expose no usable check-in endpoint (live probing: `sign_in` 404, `checkin` rejects tokens), so the plugin only detects the reward via the access token and can optionally perform the daily login for you.

For `https://new.xkool.cfd`, the public `/api/status` response observed on 2026-11-09 reports `checkin_enabled=true` and `turnstile_check=false`. Its FAQ advertises 1–5 credits for daily check-in, so the standard mode applies. The site does not expose its version; public checks neither rule out custom login rewards nor demonstrate an authenticated reward claim.

### How login-reward mode works

Login mode supports a PAT/access token, a dedicated site refresh cookie, or the automatic third-party session channel (plus the site user ID where required). The third-party channel performs a site login during binding and saves the resulting access token and refresh cookie. Site-password binding is not available in login mode. Reward checks run daily on the schedule:

- **Reward detection**: a rise in the granted total from `GET /api/user/self` (`quota + used_quota`; spending does not change it) marks the reward as arrived only when `last_login_time` falls on the current execution day or the login callback returned a `checked_in` receipt; the delta is shown as an inferred amount. Sites that omit `last_login_time` and credit a fixed allowance on the first signed-in visit of each day (for example freegg.app) use `setting.allowance_date` equal to the execution day as the same-day signal. A rise without a same-day signal is adopted into the baseline instead of being claimed as today's reward. A newer signal without a total rise is recorded as amount-unknown (likely already claimed today). No hard-coded reward amount, no fabricated server date.
- **Semi-automatic (default)**: without an imported third-party cookie the account shows "login pending today" and the console offers an "open site login" link; log in once in any browser (usually after logging out the old session) and the next detection records the reward. "Detect reward" only queries evidence and never triggers a login.
- **Fully automatic (optional)**: after importing a GitHub/LinuxDO session cookie header, the plugin replays the OAuth login chain server-side every day over plain HTTP (request a one-time state from the site's `/api/oauth/state` — current NewAPI returns a `flow_token` from `POST`, while AgentRouter-style forks use `GET ?mode=login`; both are supported — hit the provider authorize endpoint with the cookie, then return to the site's `/api/oauth/<provider>` callback) — no browser, no check-in API. Each login uses an isolated site session; an attempt marker is persisted before the callback, so an uncertain result is never resolved by replaying the chain — only reward evidence is queried. An expired cookie, a consent prompt, or a site challenge pauses the account with a clear status; rechecking only refreshes evidence and does not clear an unverified third-party session pause.

**Before importing a cookie**: a third-party session cookie is equivalent to control of your GitHub/LinuxDO account. It is stored only in the CPA auth directory credential file, sent only to the matching provider domain, and never sent to the check-in site, written to state/logs/console/API responses, or shared anywhere else. Guard the CPA auth directory like a password, and accept the gray-zone ToS risk of automated logins (same as comparable open-source tools). Switching the account back to standard mode clears the stored cookie.

Enter the site's root URL and its personal access token, rather than a model API key or the CPA management key. Initial binding only validates and saves (login mode also records the reward baseline so the first day is not a false "pending"); scheduled work runs afterwards, and rebinding an existing account queues a follow-up check that may submit a check-in.

## Scheduling, retries, and statistics

- Default schedule: 09:00 daily with ±20 minutes of jitter; supported jitter is 0–120 minutes. Use an IANA time zone or leave it empty for the host's local zone. UI overrides take precedence over host configuration; resetting defaults clears them.
- Daily randomized times are persisted. After restart, overdue work that has not been queued is spread across a 2–5 minute recovery window. Accounts run 1–3 seconds apart.
- Each account work item has up to three business attempts, with 30/120-second base retry delays plus 0–10 seconds of jitter. `Retry-After` is respected. Manual and automatic work for the same account coalesces; different accounts have independent retry budgets.
- A new day's plan can take over pending prior-day work with a fresh daily budget while retaining its retry deadline and reconciliation marker. In-flight work finishes without concurrent submissions.
- The plugin queries `/api/user/checkin` first and skips submission when already checked in. After an uncertain POST outcome, it queries the site before deciding whether to retry. Final duplicate prevention depends on the site's uniqueness constraint; there is no cross-system exactly-once guarantee.
- Missed days are not backfilled. Dates come from the site; unknown dates stay unknown. Choose a time away from the site's midnight boundary.
- Cumulative rewards/counts are the site's `total_quota` and `total_checkins`, including activity outside the plugin. Raw quota is displayed without currency conversion or cross-site aggregation. Failed statistics refreshes retain old values marked stale and do not repeat a completed check-in POST.

## Persistence and recovery

Persist both directories independently:

- **CPA authentication directory**: contains `newapi-checkin-<full-account-hash>.json` credential files with access tokens/refresh cookies. Treat these as secrets.
- **Plugin state directory**: defaults to `~/.newapi-checkin-plugin`; override it with `NEWAPI_CHECKIN_PLUGIN_HOME`. Its `state.json` contains settings, account summaries, 30-day history, work items, and recovery markers. It does not store full tokens, cookies, passwords, or management keys.

Only one CPA process may own a credential/state directory. Multiple instances sharing rotating sessions are unsupported.

Before refreshing a credential, the plugin persists a pending marker. Business requests resume only after the new credentials have been saved and read back. If the upstream refresh succeeds but local storage fails, only the local save is retried. If the refresh response was lost or restart recovery finds only old credentials, the old cookie is not reused; a fresh session must be bound.

Corrupt state is not automatically overwritten. Stop the plugin, retain the file, and repair it or restore a backup before restarting. Runtime storage failures stop scheduled work and show `storage_error`; account summaries remain readable. Fix disk space, permissions, or file conflicts and restart for recovery.

Deletion checks file identity/ownership, persists a deletion marker, and retains history. Failed deletions remain visible and can be retried; automatic work stays paused. A missing credential file can be replaced by rebinding, but an existing corrupt or foreign file is not overwritten. A confirmed write-permission denial remains blocked until credentials are replaced, even when subsequent read requests succeed.

**External credential changes:** the plugin checks file identity, generation, and disabled state before refresh and save. However, CPA's `host.auth.save` does not provide compare-and-swap, leaving a race between the final check and write. Use the plugin's delete action, or stop it and wait for in-flight work before manipulating credentials externally.

## Build and test

Requires **Go 1.26.10 or newer** and a native C compiler for the target platform. Dependencies are pinned in `go.mod` / `go.sum`; build scripts ignore local `go.work` files.

Windows PowerShell: amd64 uses MinGW UCRT GCC. ARM64 uses MSYS2 CLANGARM64 package `mingw-w64-clang-aarch64-gcc-compat`, with `clangarm64/bin` on PATH and `$env:CC="aarch64-w64-mingw32-gcc"`:

```powershell
$env:PATH = "C:\path\to\go\bin;C:\path\to\mingw\bin;" + $env:PATH
./scripts/build.ps1
```

Linux with GCC:

```sh
sh scripts/build.sh
```

macOS with Xcode Command Line Tools and clang:

```sh
sh scripts/build-darwin.sh
```

Linux/macOS builds detect native `amd64` / `arm64` and write to `dist/linux-<arch>/` or `dist/darwin-<arch>/`; Windows detects the same two native architectures and writes to `dist/windows-<arch>/`. Scripts reject incompatible `GOOS/GOARCH` overrides. macOS defaults to `MACOSX_DEPLOYMENT_TARGET=15.0`; overriding it does not validate other OS versions. Package with Python 3 (usually `python3` on macOS/Linux):

```sh
python scripts/package-release.py --platform linux-amd64
# Other targets: linux-arm64, windows-amd64, windows-arm64, darwin-amd64, darwin-arm64
```

Run automated checks:

```sh
GOWORK=off CGO_ENABLED=1 go test -p=2 ./... -count=1
node --test tests/ui/*.test.mjs
python3 -m unittest discover -s tests/packaging -v
GOWORK=off go vet ./...
GOWORK=off CGO_ENABLED=1 go test -race -p=2 ./internal/core ./internal/credential ./internal/account ./internal/config ./internal/engine ./internal/app
```

Real-host integration tests require a CPA binary built from the pinned source. Tests use isolated configuration/auth/state directories and a controlled loopback NewAPI service. Missing `CPA_TEST_BIN` explicitly skips these tests and is not evidence of real-host compatibility.

```sh
CPA_TEST_BIN=/absolute/path/to/cpa GOWORK=off CGO_ENABLED=1 go test -p=2 -tags=integration ./tests/integration -count=1 -v
```

Set `CPA_TEST_PLUGIN=/absolute/path/to/newapi-checkin.so` (`.dll` on Windows or `.dylib` on macOS) to load an existing release binary. The ABI probe remains a separate test artifact and is never included in distribution packages. Test logs and directories are retained.

See [CHANGELOG](CHANGELOG.md) and [compatibility notes](docs/compatibility.md). Archive names match CPA's plugin installer. A GitHub release does not imply inclusion in the official plugin store.

## License

[MIT](LICENSE). Bundled dependencies retain their licenses in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES.md).
