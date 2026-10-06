"""Offline runner regressions: subprocess responses are faked, never paid calls."""
import contextlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import run


def response(result, **fields):
    return subprocess.CompletedProcess(
        ["claude"], 0, json.dumps(dict(result=result, total_cost_usd=0.25, **fields)), "")


class RunnerTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        for skill in ("rota-work", "rota-ship"):
            folder = self.root / "skills" / skill
            folder.mkdir(parents=True)
            (folder / "SKILL.md").write_text("---\ndescription: A test skill.\n---\n")
        self.cases = [
            dict(id="bad-load", skill="rota-work", expect="load", request="bad-load"),
            dict(id="bad-skip", skill="rota-work", expect="skip", request="bad-skip"),
            dict(id="good", skill="rota-work", expect="load", request="good"),
        ]
        (self.root / "triggers.json").write_text(json.dumps(dict(cases=self.cases)))
        (self.root / "scenarios").mkdir()
        (self.root / "scenarios" / "work.json").write_text(json.dumps(dict(
            skill="rota-work", scenarios=[
                # Negative-only assertions expose empty-output false passes.
                dict(id=c["id"], query=c["id"], situation="test", must=[],
                     mustNot=["forbidden"], order=[]) for c in self.cases])))
        self.enterContext(patch.object(run, "HERE", self.root))
        self.enterContext(patch.object(run, "SKILLS", self.root / "skills"))

    def batch(self, mode, bad_response):
        def fake_call(*args, **kwargs):
            if "bad-" in kwargs["input"]:
                if isinstance(bad_response, Exception):
                    raise bad_response
                return bad_response
            return response("SKILL: rota-work" if mode == "triggers" else "SAY: done")

        output = self.root / "results.json"
        stdout = io.StringIO()
        with patch.object(run.subprocess, "run", side_effect=fake_call), \
                patch.object(run.sys, "argv", ["run.py", mode, "--workers", "2", "--out", str(output)]), \
                contextlib.redirect_stdout(stdout):
            status = run.main()
        return status, json.loads(output.read_text())["results"], stdout.getvalue()

    def assert_failed_batch(self, mode, bad_response, diagnostic):
        status, results, stdout = self.batch(mode, bad_response)
        self.assertEqual(status, 1)
        self.assertEqual([r["id"] for r in results], [c["id"] for c in self.cases])
        self.assertEqual([r["ok"] for r in results], [False, False, True])
        for result in results[:2]:
            self.assertIn(diagnostic, "; ".join(result["fails"]))
            self.assertIn("FAIL " + result["id"], stdout)
        self.assertIn("PASS good", stdout)

    def test_call_failures_preserve_other_results(self):
        failures = [
            (subprocess.CompletedProcess(["claude"], 7, "", "authentication failed"), "exit 7"),
            (subprocess.CompletedProcess(["claude"], 7, "", ""), "exit 7"),
            # Even plausible stdout from a failed process must never be scored.
            (subprocess.CompletedProcess(["claude"], 7, response("SKILL: rota-work").stdout, ""), "exit 7"),
            (subprocess.TimeoutExpired("claude", 300), "timed out"),
            (FileNotFoundError("claude not found"), "claude not found"),
            (PermissionError("permission denied"), "permission denied"),
            (UnicodeDecodeError("utf-8", b"\xff", 0, 1, "invalid byte"), "decode"),
            (subprocess.CompletedProcess(["claude"], 0, "not JSON", ""), "JSON"),
            (subprocess.CompletedProcess(["claude"], 0, "", ""), "JSON"),
            (response("model error", is_error=True), "model error"),
        ]
        for mode in ("triggers", "scenarios"):
            for failure, diagnostic in failures:
                with self.subTest(mode=mode, failure=failure):
                    self.assert_failed_batch(mode, failure, diagnostic)

    def test_invalid_json_payloads_preserve_other_results(self):
        payloads = [[], None, "text", {}, {"result": None}, {"result": []}]
        payloads += [dict(result="SKILL: rota-work", total_cost_usd=cost)
                     for cost in ("invalid", None, True, -1, 10**400, float("inf"), float("nan"))]
        for payload in payloads:
            for mode in ("triggers", "scenarios"):
                with self.subTest(mode=mode, payload=payload):
                    self.assert_failed_batch(mode, subprocess.CompletedProcess(
                        ["claude"], 0, json.dumps(payload), ""), "response")

    def test_invalid_routing_never_passes_load_or_skip(self):
        for reply in ("", "   ", "rota-work", "SKILL: invented", "SKILL: NONE",
                      "SKILL:\nrota-work", "SKILL: rota-work extra", "SKILL: rota-work!",
                      "Explanation\nSKILL: rota-work", "SKILL: none\nSKILL: rota-work",
                      "```\nSKILL: rota-work\n```"):
            with self.subTest(reply=reply):
                self.assert_failed_batch("triggers", response(reply), "routing")

    def test_valid_routes_keep_load_and_skip_semantics(self):
        for reply, expected in (("SKILL: rota-work", [True, False, True]),
                                ("SKILL: none", [False, True, True]),
                                ("SKILL: rota-ship\n", [False, True, True])):
            with self.subTest(reply=reply):
                _, results, _ = self.batch("triggers", response(reply))
                self.assertEqual([r["ok"] for r in results], expected)
                self.assertEqual([r["cost"] for r in results], [0.25] * 3)

    def test_scenario_behavior_is_still_scored(self):
        self.assert_failed_batch("scenarios", response("SAY: forbidden"), "forbidden")

    def test_process_diagnostic_retains_stderr(self):
        with patch.object(run.subprocess, "run", return_value=subprocess.CompletedProcess(
                ["claude"], 1, "", "authentication failed")):
            _, _, error = run.ask("unused", "system", "request")
        self.assertIn("exit 1", error)
        self.assertIn("authentication failed", error)

    def test_successful_batches_return_zero(self):
        for mode, reply in (("triggers", "SKILL: rota-work"), ("scenarios", "SAY: done")):
            with self.subTest(mode=mode), \
                    patch.object(run.subprocess, "run", return_value=response(reply)), \
                    patch.object(run.sys, "argv", ["run.py", mode]), \
                    contextlib.redirect_stdout(io.StringIO()):
                if mode == "triggers":
                    (self.root / "triggers.json").write_text(json.dumps(dict(cases=[self.cases[0]])))
                self.assertEqual(run.main(), 0)


if __name__ == "__main__":
    unittest.main()
