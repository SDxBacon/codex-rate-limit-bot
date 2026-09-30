#!/usr/bin/env python3
"""Bounded Claude CLI experiments. Never reads or exports credentials."""
import argparse
import datetime as dt
import json
import math
import os
from pathlib import Path
import queue
import re
import shutil
import signal
import subprocess
import tempfile
import threading
import time


def shape(value):
    """Observed types, not a claim about the complete protocol schema."""
    if value is None:
        return "null"
    if isinstance(value, dict):
        return {k: shape(v) for k, v in value.items()}
    if isinstance(value, list):
        variants = []
        for v in value:
            s = shape(v)
            if s not in variants:
                variants.append(s)
        return variants
    return type(value).__name__


def timer_observation(usage, now):
    if usage.get("rate_limits_available") is not True:
        return {"status": "unknown", "reason": "rate_limits_unavailable"}
    window = (usage.get("rate_limits") or {}).get("five_hour")
    if not isinstance(window, dict):
        return {"status": "unknown", "reason": "five_hour_missing_or_null"}
    used, reset = window.get("utilization"), window.get("resets_at")
    if isinstance(used, bool) or not isinstance(used, (int, float)) or not math.isfinite(used) or not 0 <= used <= 100:
        return {"status": "unknown", "reason": "utilization_invalid_or_null"}
    if reset is None:
        return {"status": "unknown", "reason": "null_reset_does_not_prove_inactive"}
    try:
        parsed = dt.datetime.fromisoformat(reset.replace("Z", "+00:00"))
        if parsed.tzinfo is None:
            raise ValueError("timezone missing")
        seconds = parsed.timestamp() - now
    except (ValueError, TypeError, AttributeError):
        return {"status": "unknown", "reason": "invalid_reset_timestamp"}
    return {"status": "running_candidate" if seconds > 0 else "unknown",
            "reason": "future_reset_requires_repeat_observation" if seconds > 0 else "reset_elapsed",
            "reset_epoch": parsed.timestamp(), "seconds_until_reset": round(seconds, 2)}


def compare_reports(previous, current):
    def stamp(report):
        return dt.datetime.fromisoformat(report.get("usage_observed_at", report["observed_at"])).timestamp()
    elapsed = stamp(current) - stamp(previous)
    result = {"elapsed_seconds": round(elapsed, 2), "status": "unknown"}
    if elapsed < 180 or elapsed >= 18000:
        return {**result, "reason": "need_samples_3_minutes_to_5_hours_apart"}
    for report in (previous, current):
        if report.get("diagnostics", {}).get("usage_endpoint_successes", 0) < 1:
            return {**result, "reason": "fresh_endpoint_response_not_confirmed"}
    a, b = previous.get("timer", {}), current.get("timer", {})
    if a.get("status") != "running_candidate" or b.get("status") != "running_candidate":
        return {**result, "reason": "missing_valid_future_reset"}
    if a["reset_epoch"] <= stamp(current):
        return {**result, "reason": "crossed_previous_reset"}
    delta = b["reset_epoch"] - a["reset_epoch"]
    result["reset_delta_seconds"] = round(delta, 3)
    if abs(delta) <= 1:
        return {**result, "status": "running_evidence", "reason": "same_future_reset_across_fresh_reads"}
    return {**result, "reason": "reset_changed_does_not_prove_inactive"}


def cli_environment(config_dir=None, essential_only=False):
    env = dict(os.environ)
    # Use the CLI's login, not an inherited API key, gateway, or model override.
    removed = []
    for key in list(env):
        if key.startswith(("ANTHROPIC_", "CLAUDE_CODE_USE_")) or key in {
            "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR",
            "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_SIMPLE", "CLAUDE_CODE_EXTRA_BODY",
            "CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CODE_HOST_CREDS_FILE",
        }:
            removed.append(key)
            del env[key]
    if config_dir:
        env["CLAUDE_CONFIG_DIR"] = str(Path(config_dir).expanduser().resolve())
    env.pop("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", None)
    env.update(DISABLE_AUTOUPDATER="1", DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1")
    if essential_only:
        env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
    return env, sorted(removed)


class CLI:
    def __init__(self, args, cwd):
        env, removed = cli_environment(args.config_dir, args.essential_only)
        binary = Path(shutil.which(args.binary) or args.binary).resolve()
        self.version = subprocess.check_output([str(binary), "--version"], env=env, text=True, timeout=10).strip()
        self.command = [str(binary), "-p", "--input-format", "stream-json", "--output-format", "stream-json",
                        "--verbose", "--no-session-persistence", "--safe-mode", "--tools", "",
                        "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}',
                        "--permission-mode", "dontAsk",
                        "--system-prompt", "Reply to greetings briefly. Do not use tools."]
        if args.model is not None:
            self.command += ["--model", args.model]
        if args.effort is not None:
            self.command += ["--effort", args.effort]
        self.debug_file = Path(cwd) / "cli-debug.log"
        self.command += ["--debug-file", str(self.debug_file)]
        self.proc = subprocess.Popen(self.command, cwd=cwd, env=env, stdin=subprocess.PIPE,
                                     stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True,
                                     start_new_session=True)
        self.events = queue.Queue()
        self.counter = 0
        self.seen = []
        self.removed_env_names = sorted(removed)
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()

    def _read(self):
        try:
            for line in self.proc.stdout:
                try:
                    self.events.put(json.loads(line))
                except json.JSONDecodeError:
                    self.events.put({"type": "invalid_json"})
        finally:
            self.events.put({"type": "eof"})

    def send(self, value):
        self.proc.stdin.write(json.dumps(value) + "\n")
        self.proc.stdin.flush()

    def wait(self, predicate, timeout):
        deadline = time.monotonic() + timeout
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError("CLI response timed out")
            try:
                event = self.events.get(timeout=remaining)
            except queue.Empty:
                raise TimeoutError("CLI response timed out") from None
            if event.get("type") in {"eof", "invalid_json"}:
                raise RuntimeError(event["type"])
            self.seen.append(event)
            if predicate(event):
                return event

    def request(self, subtype, timeout=30, **fields):
        self.counter += 1
        request_id = str(self.counter)
        self.send({"type": "control_request", "request_id": request_id,
                   "request": {"subtype": subtype, **fields}})
        event = self.wait(lambda e: e.get("type") == "control_response" and
                          e.get("response", {}).get("request_id") == request_id, timeout)
        response = event["response"]
        if response.get("subtype") != "success":
            # Do not leak the CLI's arbitrary diagnostic text into reports.
            message = str(response.get("error", "")).lower()
            category = next((k for k in ["429", "401", "403", "not supported", "not logged", "authentication"]
                             if k in message), "control_error")
            raise RuntimeError(f"{subtype}: {category}")
        return response.get("response", {})

    def close(self):
        try:
            self.proc.stdin.close()
            self.proc.wait(timeout=2)
        except (subprocess.TimeoutExpired, BrokenPipeError):
            pass
        finally:
            try:
                os.killpg(self.proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            self.proc.wait()
            self.reader.join(timeout=2)
            self.proc.stdout.close()

    def diagnostics(self):
        # Raw debug data remains in the temporary directory and is deleted.
        # Export only fixed categories/counters, never log lines or credentials.
        raw = self.debug_file.read_text(errors="replace") if self.debug_file.exists() else ""
        return {
            "usage_endpoint_attempts": raw.count("fetchUtilization: GET /api/oauth/usage"),
            "usage_endpoint_successes": raw.count("fetchUtilization: 200"),
            "usage_snapshot_cache_hit": "Usage read answered from a snapshot" in raw,
            "usage_fetch_failed": "Failed to load usage data" in raw,
            "http_error_statuses_in_debug": sorted(set(re.findall(r"status code (\d{3})", raw))),
            "usage_fieldless_body": "Usage fetch returned a fieldless" in raw,
            "known_error_categories": [s for s in ("ENOTFOUND", "ECONNREFUSED", "EPERM", "ETIMEDOUT",
                "essential-traffic", "non-essential", "nonessential", "auth_rejected", "rate_limited",
                "http_401", "http_403", "http_429", "traffic policy") if s in raw],
        }


def probe(args):
    report = {"observed_at": dt.datetime.now(dt.timezone.utc).isoformat(),
              "requested_model": args.model, "requested_effort": args.effort,
              "essential_only": args.essential_only,
              "hello_requested": args.hello, "hello_sent": False}
    with tempfile.TemporaryDirectory(prefix="claude-poc-work-") as cwd:
        cli = CLI(args, cwd)
        try:
            report["cli_version"] = cli.version
            report["removed_env_names"] = cli.removed_env_names
            init = cli.request("initialize")
            report["initialize_response_shape"] = shape(init)
            report["models"] = [{k: m[k] for k in ("value", "resolvedModel", "supportsEffort", "supportedEffortLevels") if k in m}
                                for m in init.get("models", [])]
            settings = cli.request("get_settings")
            applied = settings.get("applied", {})
            report["applied"] = {k: applied[k] for k in ("model", "effort") if k in applied}
            report["applied_shape"] = shape(applied)
            usage = cli.request("get_usage", skip_behaviors=True)
            report["usage_observed_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
            report["usage_response_shape"] = shape(usage)
            report["usage"] = {k: usage[k] for k in ("session", "subscription_type", "rate_limits_available", "rate_limits") if k in usage}
            report["timer"] = timer_observation(usage, time.time())
            if args.hello:
                if usage.get("rate_limits_available") is not True:
                    report["hello_skipped"] = "subscription_usage_unavailable"
                elif applied.get("effort") != args.effort:
                    report["hello_skipped"] = "requested_effort_not_applied"
                else:
                    report["hello_sent"] = True
                    cli.send({"type": "user", "message": {"role": "user", "content": "hello"},
                              "parent_tool_use_id": None, "session_id": ""})
                    result = cli.wait(lambda e: e.get("type") == "result", 90)
                    report["hello_result"] = {k: result[k] for k in ("subtype", "is_error", "num_turns", "usage", "modelUsage", "total_cost_usd") if k in result}
                    report["assistant_models"] = sorted({e.get("message", {}).get("model") for e in cli.seen
                                                         if e.get("type") == "assistant" and e.get("message", {}).get("model")})
                    # The known hello response is useful evidence; no account metadata is exported.
                    report["hello_text"] = [c["text"] for e in cli.seen if e.get("type") == "assistant"
                                            for c in e.get("message", {}).get("content", []) if c.get("type") == "text"]
                    after = cli.request("get_usage", skip_behaviors=True)
                    report["after_usage_observed_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
                    report["after_usage"] = {k: after[k] for k in ("subscription_type", "rate_limits_available", "rate_limits") if k in after}
                    report["after_usage_response_shape"] = shape(after)
                    report["after_timer"] = timer_observation(after, time.time())
        except (RuntimeError, TimeoutError, BrokenPipeError) as exc:
            report["failure"] = str(exc)
        finally:
            cli.close()
            report["diagnostics"] = cli.diagnostics()
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="claude")
    parser.add_argument("--config-dir", help="CLI login directory; omit for current default login")
    parser.add_argument("--model", default="sonnet")
    parser.add_argument("--effort", choices=["low", "medium", "high", "xhigh", "max"], default="low")
    parser.add_argument("--hello", action="store_true", help="Send one real greeting, consuming subscription quota")
    parser.add_argument("--essential-only", action="store_true", help="Compare with nonessential network traffic disabled")
    parser.add_argument("--compare", type=Path, help="Compare with a prior read at least 3 minutes earlier")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    report = probe(args)
    if args.compare:
        report["comparison"] = compare_reports(json.loads(args.compare.read_text()), report)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump(report, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(json.dumps(report, indent=2, ensure_ascii=False))
    return 1 if report.get("failure") else 0


if __name__ == "__main__":
    raise SystemExit(main())
