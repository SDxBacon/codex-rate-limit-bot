#!/usr/bin/env python3
"""Read Claude CLI model/effort options without sending a model prompt."""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

from claude_cli import CLI, cli_environment


def auth_status(args, cwd):
    env, _ = cli_environment(args.config_dir)
    binary = str(Path(shutil.which(args.binary) or args.binary).resolve())
    result = subprocess.run([binary, "auth", "status", "--json"], cwd=cwd,
                            env=env, capture_output=True, text=True, timeout=20)
    try:
        raw = json.loads(result.stdout)
    except json.JSONDecodeError:
        raise RuntimeError("auth_status: invalid_json") from None
    # Account names, email, IDs and arbitrary diagnostics are deliberately omitted.
    return {"exit_code": result.returncode, **{k: raw[k] for k in
            ("loggedIn", "authMethod", "apiProvider", "subscriptionType") if k in raw}}


def probe(args):
    env, removed = cli_environment(args.config_dir)
    config_dir = str(Path(env.get("CLAUDE_CONFIG_DIR", "~/.claude")).expanduser().resolve())
    report = {"observed_at": dt.datetime.now(dt.timezone.utc).isoformat(),
              "config_dir": config_dir, "model_prompt_sent": False,
              "removed_env_names": removed}
    with tempfile.TemporaryDirectory(prefix="claude-options-work-") as cwd:
        report["auth_status"] = auth_status(args, cwd)
        cli = CLI(args, cwd)
        try:
            report["cli_version"] = cli.version
            init = cli.request("initialize")
            if not isinstance(init.get("models"), list):
                raise RuntimeError("initialize: models_missing_or_invalid")
            # Preserve every model field, including optional fields and disabled rows.
            # Other initialize fields can contain account metadata and are not exported.
            frame = cli.seen[-1]
            report["initialize_models_response"] = {
                "type": frame["type"], "response": {
                    "subtype": frame["response"]["subtype"],
                    "request_id": frame["response"]["request_id"],
                    "response": {"models": init["models"]}}}
            applied = cli.request("get_settings").get("applied", {})
            report["applied"] = {k: applied[k] for k in ("model", "effort") if k in applied}
        finally:
            cli.close()
    return report


def check_isolation(args):
    """Compare current login with two fresh profiles, without logging in/out."""
    observations = []
    with tempfile.TemporaryDirectory(prefix="claude-config-isolation-") as root:
        for name, model in (("account-a", "haiku"), ("account-b", "sonnet")):
            path = Path(root) / name
            path.mkdir(mode=0o700)
            (path / "settings.json").write_text(json.dumps({"model": model}))
            profile_args = argparse.Namespace(**vars(args))
            profile_args.config_dir = str(path)
            profile_args.model = profile_args.effort = None
            observation = probe(profile_args)
            # Store only filenames/modes; never open credential files.
            observation["files_created"] = [
                {"path": str(p.relative_to(path)), "mode": oct(p.stat().st_mode & 0o777)}
                for p in sorted(path.rglob("*")) if p.is_file()]
            observation["settings_model"] = model
            observations.append(observation)
    return {"profiles": observations,
            "both_fresh_profiles_logged_out": all(
                p["auth_status"].get("loggedIn") is False for p in observations),
            "settings_separate": all(p.get("applied", {}).get("model", "").startswith(
                "claude-" + p["settings_model"] + "-") for p in observations),
            "temporary_profiles_removed": True}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="claude")
    parser.add_argument("--config-dir", help="Set CLAUDE_CONFIG_DIR for this process")
    parser.add_argument("--model", help="Optional session model; omit to inspect account default")
    parser.add_argument("--effort", help="Optional effort to inspect via applied.effort")
    parser.add_argument("--check-config-isolation", action="store_true",
                        help="Also inspect two temporary logged-out profiles")
    parser.add_argument("--output", type=Path, help="Also save JSON with file mode 0600")
    args = parser.parse_args()
    args.essential_only = False
    try:
        report = probe(args)
        if args.check_config_isolation:
            report["config_isolation"] = check_isolation(args)
    except (RuntimeError, TimeoutError, BrokenPipeError, subprocess.SubprocessError, OSError) as exc:
        # Never print arbitrary CLI stderr, settings, or credential contents.
        report = {"failure": type(exc).__name__, "model_prompt_sent": False}
    output = json.dumps(report, indent=2, ensure_ascii=False) + "\n"
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as stream:
            stream.write(output)
    print(output, end="")
    return 1 if "failure" in report else 0


if __name__ == "__main__":
    raise SystemExit(main())
