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


if __name__ == "__main__":
    unittest.main()
