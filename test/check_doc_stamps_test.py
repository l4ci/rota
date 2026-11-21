"""check-doc-stamps.py reads a doc's front matter through scripts/fmlite.py: the refusals
for a stamp left in the doc, a missing refs list and an unclosed block cross that seam (#4084)."""
import importlib.util
from pathlib import Path
import unittest


def load():
    path = Path(__file__).resolve().parent / "check-doc-stamps.py"
    spec = importlib.util.spec_from_file_location("check_doc_stamps", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


class ParseRefsTest(unittest.TestCase):
    def setUp(self):
        self.parse = load().parse_refs

    def test_block_list_is_read(self):
        self.assertEqual(self.parse("---\nrefs:\n  - internal/gate\n  - a.go\n---\nbody\n"),
                         (["internal/gate", "a.go"], None))

    def test_quoted_ref_goes_through_fmlite(self):
        self.assertEqual(self.parse('---\nrefs:\n  - "internal/gate"\n---\n'),
                         (["internal/gate"], None))

    def test_verified_sha_belongs_in_stamps(self):
        refs, err = self.parse("---\nverified-sha: abc1234\nrefs:\n  - a\n---\n")
        self.assertEqual(refs, [])
        self.assertIn("verified-sha: belongs in docs/contributing/contract/STAMPS", err)

    def test_missing_refs(self):
        refs, err = self.parse("---\ntitle: x\n---\n")
        self.assertEqual((refs, err), ([], "missing refs: (a list of repo paths)"))

    def test_empty_refs(self):
        self.assertEqual(self.parse("---\nrefs:\n---\n")[1], "missing refs: (a list of repo paths)")

    def test_scalar_refs_refused(self):
        self.assertIn("refs: must be a block list", self.parse("---\nrefs: internal/gate\n---\n")[1])

    def test_bool_refs_refused(self):
        self.assertIn("refs: must be a block list", self.parse("---\nrefs: true\n---\n")[1])

    def test_nested_mapping_refs_refused(self):
        self.assertIn("refs: must be a block list", self.parse("---\nrefs:\n  a: b\n---\n")[1])

    def test_unclosed_front_matter(self):
        self.assertEqual(self.parse("---\nrefs:\n  - a\n"), ([], "frontmatter is not closed with ---"))

    def test_no_front_matter(self):
        self.assertEqual(self.parse("# title\n"), ([], "no frontmatter (expected a leading --- block)"))

    def test_unparseable_line_names_the_problem(self):
        refs, err = self.parse("---\nrefs:\n  - a\n\tbad\n---\n")
        self.assertEqual(refs, [])
        self.assertTrue(err.startswith("frontmatter: "), err)


if __name__ == "__main__":
    unittest.main()
