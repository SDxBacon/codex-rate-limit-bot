# Handover: Claude CLI support

Status: 2026-09-30. The user deployed the earlier monitoring version on their Pi. Claude hello, shared timer/cooldown/rechecks, per-account hello settings, and an in-image Go timer PoC are now implemented. Both providers support Probe and Hello by default, without a Claude-only enable switch; real timer and test-channel acceptance are outstanding. This update has not been deployed.

## 1. Goal

Extend this Go-based Codex usage-monitoring bot to support Claude Code CLI accounts alongside Codex accounts. The bot currently polls every five minutes and updates one Discord message with remaining 5-hour and weekly quota, reset times, and per-account status. For Codex, it can also send a short `hello` when observations indicate that the 5-hour timer has not started.

The goal is full Probe/Hello behavior parity by default. Real timer validation is release acceptance, not a runtime activation switch. Unvalidated changes stay local and are not committed or deployed. Cached-data disclosure remains.

Agreed direction:

- Add `accounts[i].type`: `codex` or `claude`; omission defaults to `codex` for backward compatibility.
- Target Raspberry Pi / Linux ARM64 containers, with separate account directories. Claude uses `CLAUDE_CONFIG_DIR`.
- Initially monitor Claude Pro/Max overall 5-hour and weekly quotas. Model-specific quotas and extra-usage billing are outside this first version.
- Prefer the CLI's experimental `get_usage` control interface, letting the CLI manage authentication. Internal interfaces are acceptable, but their behavior must be verified.
- Claude accounts now accept hello_model (default sonnet) and hello_effort (default low; null omits it). Options discovery remains a PoC; release still requires real inactive-transition validation.

The user explicitly wants executable PoCs before treating this design as ready to implement.

## 2. PoC completed so far

Tested on macOS with Claude Code **2.1.284** and a real **Pro** login. Linux/ARM64 has not yet been tested.

| Experiment | Result |
| --- | --- |
| Read usage through the CLI | Successful. Start the CLI with stream-json input/output, then send `initialize` and `get_usage` control requests. Real quota data was retrieved without a model prompt. |
| Inspect response shape | Captured the observed field/type tree. Relevant fields are `rate_limits_available`, `rate_limits.five_hour`, and `rate_limits.seven_day`; windows contain `utilization` and `resets_at`. Missing/null values must be handled. |
| Observe the 5-hour timer | Two confirmed endpoint reads, 198.82 seconds apart, had reset timestamps differing by only 0.211 seconds. This supports a running timer for that sample, even though both reported 0% used. |
| Send hello with model and effort | Successful with Sonnet + low. `get_settings` reported `applied.model = claude-sonnet-5-5` and `applied.effort = low`; the actual response model matched and the turn succeeded. |
| Check unsupported effort | Haiku accepted `--effort low`, but reported `applied.effort = null`. Accepting a flag does not prove it took effect. |
| Account directory isolation | With Keychain access, the default directory is logged in as Pro. Two fresh `CLAUDE_CONFIG_DIR` directories are logged out and apply their separate Haiku/Sonnet settings. No login/logout or credential copying was performed. Linux credential storage is documented as `$CLAUDE_CONFIG_DIR/.credentials.json`; Linux/ARM64, two authenticated profiles, and refresh remain untested. |
| Discover model/effort choices | New `poc/claude_options.py` reads complete `initialize.models` rows and `get_settings.applied` without a model prompt. Five picker rows were returned: default, opus, Fable 5.1 `[1m]`, sonnet, haiku. The first four advertise low/medium/high/xhigh/max; Haiku omits effort fields. This is the CLI picker catalog, not all historical model IDs or proof of inference entitlement. |

Important limitations:

- **An inactive timer and the inactive → active transition have not been verified.** Do not infer inactivity from 0% usage, a null reset, or `limits[].is_active`. The earlier proposed `0% + null reset` rule was withdrawn.
- **A successful control response does not guarantee fresh quota data.** With nonessential traffic disabled, the CLI returned null or previous cached data despite the usage endpoint not succeeding. Its response does not expose freshness/source metadata; PoC debug counters are diagnostic evidence, not a stable production API.
- The successful hello did not demonstrate starting a new 5-hour window. CLI-applied effort was inspected; the outbound API payload was not intercepted.
- Ten local tests passed. They test conservative handling of missing, invalid, cached, and reset-crossing data, profile directory precedence and removal of alternate inherited credentials, not real inactive-account behavior or token refresh.

The idle/expired-window tool is implemented; obtain real subscription evidence locally in isolation before activating automatic hello. Authentication refresh and Linux/ARM64 operation also remain unverified.

Repository references:

- `poc/claude_cli.py` — runnable probe; `--hello` explicitly sends one real greeting, while ordinary runs only query.
- `poc/claude_options.py` — model/effort query; `--check-config-isolation` also probes two temporary profiles.
- `poc/models-response.observed.json` — complete observed model rows in a control-response envelope, with other initialize fields omitted.
- `poc/README.md` — reproduction commands, protocol details, and findings (Traditional Chinese).
- `poc/usage-response.observed-types.json` — observed types, not a formal or exhaustive JSON Schema.
- `poc/test_claude_cli.py` — ten tests.
- `data/claude-poc/` — local experiment reports; Git-ignored and not included in a clone.

## 3. Go monitoring integration

The production bot dispatches by account type (omission defaults to codex). Claude uses an explicit CLAUDE_CONFIG_DIR, temporary working directory, and 30-second control-only reads. Nullable percentages/resets, fractional utilization, retained prior snapshots on failure, type/home state reset, and cached-data disclosure are implemented. Usage probes never send user frames. Both providers use the shared timer/hello behavior by default. Discord uses the AI account heading and still recovers every prior heading.

Docker pins Claude 2.1.284 and preserves the existing root mount, non-root UID, and init. See the project README for login, tests, and release acceptance. Fake CLI tests cover protocol, data, cancellation/child cleanup, mixed accounts, and rendering. An opt-in real Go read requires CLAUDE_TEST_CONFIG_DIR.

Additional macOS observation: the existing Pro login works with CLAUDE_CONFIG_DIR unset, while explicitly setting it to the same ~/.claude path selects a different Keychain entry on CLI 2.1.284 and reports logged out. Both Go and the existing Python PoC receive rate_limits_available=false. This validates the unavailable-data path, not a successful authenticated Go quota read. A dedicated profile needs its own login. The production ARM64 image now builds locally. Claude 2.1.284 runs as a non-root user; a network-isolated UID 1000 container with init and a fake CLI verified one hello, a writable profile, and a 0600 report. Real Linux subscription timer and three-poll/restart test-channel acceptance remain unverified. This update has not been deployed.

## 4. Full lifecycle and release acceptance

Claude accounts accept hello_model (default sonnet) and hello_effort (default low, null omits the flag). A shared Go session implementation serves reads and hello. Hello checks initialize/get_settings before one user frame and requires a successful terminal result. Both providers share attempts, cooldown, immediate bounded rechecks, nullable-data guards, process cleanup, and Discord attempt timestamps. Codex flags remain unchanged; settings-only reloads preserve cooldown.

The built binary includes a claude-poc command runnable in the production image without Python or Discord access. See [CLAUDE_TIMER.zh-TW.md](CLAUDE_TIMER.zh-TW.md). Validation requires an active window naturally expiring, fresh rolling idle samples, one manual Sonnet/low hello, and fresh fixed new-window samples. Debug counters are PoC evidence only; raw debug files are deleted and reports use 0600 permissions. Production never uses debug freshness inference.

No real passing timer report exists yet. Complete local isolated validation, three test-channel polls and a restart before committing/releasing. There is no provider-specific runtime activation switch. The existing user-added /home/luo/.claude bind mount is preserved so the absolute symlink target is visible in the container.

Validation: all Go tests, race, vet, ten Python PoC tests, Linux ARM64 compilation, Compose configuration, and the fake-CLI container smoke test passed. No real Claude greeting or Discord publication was performed.

## 5. Corrected validation/deployment boundary

The user explicitly prohibits committing unvalidated changes and pulling them onto the Pi. Remaining timer/hello validation must run locally in an isolated Linux ARM64 container. poc/compose.validation.yaml is a separate project without production Discord configuration, account directories, or state.json. The procedure now uses an independent local subscription test profile; an authenticated profile is still missing. Fake CLI success and ARM64 compilation are not real timer validation. No commit, push, or Pi deployment has been performed.

## 6. Corrected default feature parity

The user requires Claude and Codex to provide usage monitoring and hello by default. The Claude-only environment activation flag, provider readiness callback, and blocked state field were removed. Both providers register Probe and Hello and share timer, cooldown, and recheck behavior. Incomplete real testing prevents release, not default Claude functionality. Changes remain local without commit, push, or deployment.
