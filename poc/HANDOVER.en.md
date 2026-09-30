# Handover: Claude CLI support

Status: 2026-09-30. The first Go monitoring integration, account type configuration, Discord rendering, and pinned Docker CLI installation are implemented. Not deployed; ARM64 container and test-channel acceptance remain outstanding.

## 1. Goal

Extend this Go-based Codex usage-monitoring bot to support Claude Code CLI accounts alongside Codex accounts. The bot currently polls every five minutes and updates one Discord message with remaining 5-hour and weekly quota, reset times, and per-account status. For Codex, it can also send a short `hello` when observations indicate that the 5-hour timer has not started.

The first Claude integration is monitoring only, labels possibly cached data, keeps timer status unknown, and never sends hello. Automatic hello is deferred to a later validated version.

Agreed direction:

- Add `accounts[i].type`: `codex` or `claude`; omission defaults to `codex` for backward compatibility.
- Target Raspberry Pi / Linux ARM64 containers, with separate account directories. Claude uses `CLAUDE_CONFIG_DIR`.
- Initially monitor Claude Pro/Max overall 5-hour and weekly quotas. Model-specific quotas and extra-usage billing are outside this first version.
- Prefer the CLI's experimental `get_usage` control interface, letting the CLI manage authentication. Internal interfaces are acceptable, but their behavior must be verified.
- Do not add Claude hello model/effort configuration in v1. Options discovery remains a PoC; future hello needs inactive-transition validation first.

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

Continue with an idle/expired-window experiment before implementing automatic hello. Authentication refresh and Linux/ARM64 operation also remain unverified.

Repository references:

- `poc/claude_cli.py` — runnable probe; `--hello` explicitly sends one real greeting, while ordinary runs only query.
- `poc/claude_options.py` — model/effort query; `--check-config-isolation` also probes two temporary profiles.
- `poc/models-response.observed.json` — complete observed model rows in a control-response envelope, with other initialize fields omitted.
- `poc/README.md` — reproduction commands, protocol details, and findings (Traditional Chinese).
- `poc/usage-response.observed-types.json` — observed types, not a formal or exhaustive JSON Schema.
- `poc/test_claude_cli.py` — ten tests.
- `data/claude-poc/` — local experiment reports; Git-ignored and not included in a clone.

## 3. Go monitoring integration

The production bot dispatches by account type (omission defaults to codex). Claude uses an explicit CLAUDE_CONFIG_DIR, temporary working directory, and 30-second control-only reads. Nullable percentages/resets, fractional utilization, retained prior snapshots on failure, type/home state reset, and cached-data disclosure are implemented. Claude timer remains unknown and no user frame is sent. Discord uses the AI account heading and still recovers every prior heading.

Docker pins Claude 2.1.284 and preserves the existing root mount, non-root UID, and init. See the project README for login, tests, and release acceptance. Fake CLI tests cover protocol, data, cancellation/child cleanup, mixed accounts, and rendering. An opt-in real Go read requires CLAUDE_TEST_CONFIG_DIR.

Additional macOS observation: the existing Pro login works with CLAUDE_CONFIG_DIR unset, while explicitly setting it to the same ~/.claude path selects a different Keychain entry on CLI 2.1.284 and reports logged out. Both Go and the existing Python PoC receive rate_limits_available=false. This validates the unavailable-data path, not a successful authenticated Go quota read. A dedicated profile needs its own login. Docker daemon is not running locally; ARM64 image, Linux subscription, and three-poll/restart test-channel acceptance remain unverified. No publication or deployment was performed.
