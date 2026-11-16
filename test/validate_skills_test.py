"""Import validate-skills.py in-process: no dispatch at import, checks callable directly."""
import importlib.util
from pathlib import Path
import sys
import unittest


def load():
    path = Path(__file__).resolve().parent / "validate-skills.py"
    spec = importlib.util.spec_from_file_location("validate_skills", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


class ValidateSkillsImportTest(unittest.TestCase):
    def test_import_runs_nothing(self):
        argv = sys.argv
        sys.argv = ["validate-skills.py", "--bogus"]  # dispatch at import would exit 2
        self.addCleanup(setattr, sys, "argv", argv)
        mod = load()
        self.assertTrue(callable(mod.check_frontmatter))

    def test_check_frontmatter_flags_missing_description(self):
        argv = sys.argv
        sys.argv = ["validate-skills.py", "--bogus"]
        self.addCleanup(setattr, sys, "argv", argv)
        issues = []
        load().check_frontmatter("skills/x/SKILL.md", "---\nname: x\n---\nbody\n", issues)
        self.assertEqual(issues, ["skills/x/SKILL.md: frontmatter missing required key 'description'"])

    def _manual_only_issues(self, policy):
        """check_manual_only on a manual-only skill whose openai.yaml holds `policy`."""
        import tempfile
        argv = sys.argv
        sys.argv = ["validate-skills.py", "--bogus"]
        self.addCleanup(setattr, sys, "argv", argv)
        mod = load()
        with tempfile.TemporaryDirectory() as tmp:
            skill = Path(tmp) / "skills" / "x"
            (skill / "agents").mkdir(parents=True)
            (skill / "agents" / "openai.yaml").write_text(policy, encoding="utf-8")
            text = "---\nname: x\ndescription: d\ndisable-model-invocation: true\n---\nbody\n"
            issues = []
            mod.check_manual_only(skill / "SKILL.md", text, issues)
            return [i.replace(tmp, "") for i in issues]

    def test_manual_only_policy_has_one_parser_path(self):
        # Flow style is valid YAML, so a PyYAML dev machine accepted it while
        # CI (no PyYAML) took the regex and refused it. One path: refused everywhere.
        issues = self._manual_only_issues("policy: {allow_implicit_invocation: false}\n")
        self.assertEqual(len(issues), 1)
        self.assertIn("needs policy.allow_implicit_invocation: false", issues[0])
        # The shape rota writes passes.
        self.assertEqual(self._manual_only_issues("policy:\n  allow_implicit_invocation: false\n"), [])

    def test_manual_only_does_not_import_yaml(self):
        src = (Path(__file__).resolve().parent / "validate-skills.py").read_text(encoding="utf-8")
        self.assertNotIn("import yaml", src)


if __name__ == "__main__":
    unittest.main()
