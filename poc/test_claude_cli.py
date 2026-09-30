import unittest
from pathlib import Path
from unittest.mock import patch

from claude_cli import cli_environment, compare_reports, timer_observation


class ProfileEnvironmentTests(unittest.TestCase):
    def test_explicit_profile_overrides_inherited_directory(self):
        with patch.dict("os.environ", {"CLAUDE_CONFIG_DIR": "/tmp/other-account"}, clear=True):
            env, _ = cli_environment("/tmp/selected-account")
        self.assertEqual(env["CLAUDE_CONFIG_DIR"], str(Path("/tmp/selected-account").resolve()))

    def test_no_profile_argument_preserves_inherited_directory(self):
        with patch.dict("os.environ", {"CLAUDE_CONFIG_DIR": "/tmp/selected-account"}, clear=True):
            env, _ = cli_environment()
        self.assertEqual(env["CLAUDE_CONFIG_DIR"], "/tmp/selected-account")

    def test_other_auth_sources_cannot_bypass_profile(self):
        overrides = {key: "private-value" for key in (
            "ANTHROPIC_API_KEY", "ANTHROPIC_PROFILE", "CLAUDE_CODE_OAUTH_TOKEN",
            "CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CODE_HOST_CREDS_FILE")}
        with patch.dict("os.environ", overrides, clear=True):
            env, removed = cli_environment("/tmp/selected-account")
        for key in overrides:
            self.assertNotIn(key, env)
            self.assertIn(key, removed)


class TimerEvidenceTests(unittest.TestCase):
    def usage(self, used=0, reset="2026-09-30T05:00:00Z"):
        return {"rate_limits_available": True, "rate_limits": {
            "five_hour": {"utilization": used, "resets_at": reset}}}

    def test_zero_percent_can_have_future_timer(self):
        result = timer_observation(self.usage(), 1790726400)
        self.assertEqual(result["status"], "running_candidate")

    def test_null_reset_is_not_inactive(self):
        self.assertEqual(timer_observation(self.usage(reset=None), 0)["status"], "unknown")

    def test_invalid_or_absent_data_is_unknown(self):
        samples = [{}, {"rate_limits_available": True, "rate_limits": None},
                   self.usage(used=None), self.usage(used=True), self.usage(used=float("nan")),
                   self.usage(used=101), self.usage(reset="2026-09-30T05:00:00"),
                   self.usage(reset="invalid")]
        for sample in samples:
            with self.subTest(sample=sample):
                self.assertEqual(timer_observation(sample, 0)["status"], "unknown")

    def reports(self):
        a = {"observed_at": "2026-09-30T00:00:00+00:00",
             "diagnostics": {"usage_endpoint_successes": 1},
             "timer": {"status": "running_candidate", "reset_epoch": 1790744400}}
        b = {**a, "observed_at": "2026-09-30T00:03:01+00:00"}
        return a, b

    def test_fixed_reset_with_fresh_reads_is_evidence(self):
        self.assertEqual(compare_reports(*self.reports())["status"], "running_evidence")

    def test_cached_response_is_not_fresh_evidence(self):
        a, b = self.reports()
        b["diagnostics"] = {"usage_endpoint_successes": 0}
        self.assertEqual(compare_reports(a, b)["status"], "unknown")

    def test_rolling_reset_is_not_proven_inactive(self):
        a, b = self.reports()
        b["timer"] = {**b["timer"], "reset_epoch": b["timer"]["reset_epoch"] + 181}
        self.assertEqual(compare_reports(a, b)["status"], "unknown")

    def test_crossing_previous_reset_is_not_same_window(self):
        a, b = self.reports()
        a["timer"] = {**a["timer"], "reset_epoch": 1790726500}
        self.assertEqual(compare_reports(a, b)["status"], "unknown")


if __name__ == "__main__":
    unittest.main()
