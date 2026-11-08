# NewAPI Daily Check-in Plugin for CPA

[简体中文](README.md) | **English**

[Download](https://github.com/5ifish/cpa-plugin-newapi-checkin/releases/latest) · [Report an issue](https://github.com/5ifish/cpa-plugin-newapi-checkin/issues) · [Compatibility](docs/compatibility.md)

Manage accounts across NewAPI sites, schedule daily check-ins, and view credential health, execution history, and site-reported rewards from CPA's management interface. The plugin exposes management APIs only; it does not proxy model requests.

## Downloads

Stable release **v0.1.0** provides three native packages. `amd64` means `x86_64 / x64`; `arm64` means `aarch64`. 32-bit ARM is not included.

| Platform | Release asset | Plugin inside the ZIP |
| --- | --- | --- |
| Windows x64 | [newapi-checkin_0.1.0_windows_amd64.zip](https://github.com/5ifish/cpa-plugin-newapi-checkin/releases/download/v0.1.0/newapi-checkin_0.1.0_windows_amd64.zip) | `newapi-checkin.dll` |
| Linux x86_64 | [newapi-checkin_0.1.0_linux_amd64.zip](https://github.com/5ifish/cpa-plugin-newapi-checkin/releases/download/v0.1.0/newapi-checkin_0.1.0_linux_amd64.zip) | `newapi-checkin.so` |
| Linux ARM64 | [newapi-checkin_0.1.0_linux_arm64.zip](https://github.com/5ifish/cpa-plugin-newapi-checkin/releases/download/v0.1.0/newapi-checkin_0.1.0_linux_arm64.zip) | `newapi-checkin.so` |

Verify archives against [`checksums.txt`](https://github.com/5ifish/cpa-plugin-newapi-checkin/releases/download/v0.1.0/checksums.txt). On Linux, run `sha256sum -c checksums.txt --ignore-missing`; on Windows, compare `Get-FileHash -Algorithm SHA256 <archive>` with the published checksum.

Linux requires **glibc 2.34 or newer**. See the [tested environments](docs/compatibility.md) for the actual distribution and CPA versions validated. Alpine/musl is unsupported. Select the OS and architecture of the CPA process; for Docker, use the container's runtime environment.

## Installation

This is a CPA C ABI shared library, not a Go `plugin` package. The SDK dependency is pinned to **CPA v8.0.58**, commit `e5e6ad1e1b91fc20ab9964e6fb22c0c269b9d882`. The NewAPI API reference is commit `a3fd673bb7897691b1041aaf8d78bab6cc15e1d5`; custom and older deployments must provide the same API contract.

1. Extract the archive into the appropriate platform subdirectory under CPA's plugin directory: `plugins/windows/amd64/newapi-checkin.dll`, `plugins/linux/amd64/newapi-checkin.so`, or `plugins/linux/arm64/newapi-checkin.so`.
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

Static UI assets are public; account and task data require authentication through `/v0/management/plugins/newapi-checkin/…`. Use HTTPS for remote management. The bundled management UI currently uses Chinese; this repository provides both Chinese and English documentation.

## Account authentication

| Method | Requirements and limitations |
| --- | --- |
| PAT / access token | Recommended. Scoped PATs need `profile:read`, `wallet:read`, and `wallet:write`. The reference site defaults PAT expiry to 30 days; explicitly select **Never expires** if desired. The plugin's expiry field is a reminder, not a validity override. Leave it blank when unknown. Legacy tokens may be subject to a site-specific retirement date. |
| Site username and password | For the site's own local account, not LinuxDO credentials. Creates a dedicated session through the site's v2 password encryption protocol and does not save the password. Interactive 2FA/passkey challenges require user action. |
| Dedicated refresh cookie | Advanced import. Create a separate login session, confirm exclusive use, and stop using that session in browsers or other tools. Already-managed sessions and sessions with an uncertain refresh outcome cannot be imported again. |

Login sessions last at most 30 days under the reference protocol. Refreshing an access token **does not extend the original session expiry**. Expired, revoked, or retired credentials require rebinding. Rebinding the same site/user preserves history, cancels previous work, and checks the new credentials.

Sites requiring Turnstile for check-in are saved in a paused state and cannot run unattended. Changing the credential type does not bypass this challenge. Site URLs must use HTTPS, with normal certificate validation and no redirect following; HTTP is permitted only for loopback testing.

Binding validates identity and reads check-in state. It does not submit a check-in to probe write access. The plugin confirms `wallet:write` only when an actual check-in is needed and succeeds.

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

Windows PowerShell with MinGW GCC:

```powershell
$env:PATH = "C:\path\to\go\bin;C:\path\to\mingw\bin;" + $env:PATH
./scripts/build.ps1
```

Linux with GCC:

```sh
sh scripts/build.sh
```

Linux builds detect native `amd64` / `arm64` and write to `dist/linux-<arch>/`; Windows writes to `dist/windows-amd64/`. Package with Python 3:

```sh
python scripts/package-release.py --platform linux-amd64
# Other targets: linux-arm64, windows-amd64
```

Run automated checks:

```sh
GOWORK=off CGO_ENABLED=1 go test -p=2 ./... -count=1
node --test tests/ui/console.test.mjs
GOWORK=off go vet ./...
GOWORK=off CGO_ENABLED=1 go test -race -p=2 ./internal/core ./internal/credential ./internal/account ./internal/config ./internal/engine ./internal/app
```

Real-host integration tests require a CPA binary built from the pinned source. Tests use isolated configuration/auth/state directories and a controlled loopback NewAPI service. Missing `CPA_TEST_BIN` explicitly skips these tests and is not evidence of real-host compatibility.

```sh
CPA_TEST_BIN=/absolute/path/to/cpa GOWORK=off CGO_ENABLED=1 go test -p=2 -tags=integration ./tests/integration -count=1 -v
```

Set `CPA_TEST_PLUGIN=/absolute/path/to/newapi-checkin.so` (or `.dll`) to load an existing release binary. The ABI probe remains a separate test artifact and is never included in distribution packages. Test logs and directories are retained.

See [CHANGELOG](CHANGELOG.md) and [compatibility notes](docs/compatibility.md). Archive names match CPA's plugin installer. A GitHub release does not imply inclusion in the official plugin store.

## License

[MIT](LICENSE). Bundled dependencies retain their licenses in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES.md).
