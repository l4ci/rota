#!/usr/bin/env python3
"""Exercise the generated video catalog through the normal site sync."""
import json
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile
import unittest


REPO = Path(__file__).resolve().parents[1]


def movie(seconds, version=0):
    def box(kind, data):
        return struct.pack(">I4s", len(data) + 8, kind) + data

    # ISO BMFF movie header: version/flags, creation, modification, scale, duration.
    fields = (struct.pack(">QQIQ", 0, 0, 1000, seconds * 1000) if version
              else struct.pack(">IIII", 0, 0, 1000, seconds * 1000))
    return box(b"ftyp", b"isom\0\0\0\0") + box(b"moov", box(b"mvhd", bytes([version, 0, 0, 0]) + fields))


class VideoCatalogTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        root = Path(self.tmp.name)
        self.repo, self.site = root / "repo", root / "site"
        self.repo.mkdir()
        self.site.mkdir()
        for name in ("docs", "scripts", "skills"):
            shutil.copytree(REPO / name, self.repo / name)
        shutil.copyfile(REPO / "llms.txt", self.repo / "llms.txt")
        (self.repo / "videos" / "episodes").mkdir(parents=True)
        self.out = self.repo / "videos" / "out"
        self.out.mkdir()
        shutil.copyfile(REPO / "videos/README.md", self.repo / "videos/README.md")
        shutil.copytree(REPO / "videos/episodes", self.repo / "videos/episodes", dirs_exist_ok=True)
        self.manifest = {"episodes": {}}
        self.publish("01-why-rota", 45)

    def publish(self, slug, seconds, version=0, digest="a" * 64):
        outputs = {slug + suffix: digest for suffix in (".mp4", ".vertical.mp4", ".vtt")}
        self.manifest["episodes"][slug] = {"outputs": outputs}
        (self.out / (slug + ".mp4")).write_bytes(movie(seconds, version))
        (self.out / "manifest.json").write_text(json.dumps(self.manifest))

    def sync(self):
        result = subprocess.run(["python3", str(self.repo / "scripts/site-sync.py"), str(self.site)],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return self.site / "rota/docs/videos.md"

    def test_published_episode(self):
        page = self.sync()
        self.assertTrue(page.exists(), "site-sync must generate a videos entry point")
        text = page.read_text()
        self.assertIn("Episode 1: Why rota exists", text)
        self.assertIn("0:45", text)
        self.assertIn("Name the problem (fast agents, unsafe merges)", text)
        self.assertIn("https://media.githubusercontent.com/media/l4ci/rota/main/videos/out/01-why-rota.mp4", text)
        self.assertIn("#episode-1", text)
        self.assertNotIn("Install and set up a project", text)
        self.assertIn("/rota/docs/videos/", (self.site / "_data/rota/nav.yml").read_text())

    def test_new_episode_and_rerender_need_only_sync(self):
        old = self.sync().read_text()
        self.publish("01-why-rota", 52, digest="b" * 64)
        self.publish("02-install-and-setup", 89, version=1)
        text = self.sync().read_text()
        self.assertIn("Episode 2: Install and set up a project", text)
        self.assertIn("1:29", text)
        self.assertIn("Get the binary, the skills and a working", text)
        self.assertIn("Length: 0:52", text)
        self.assertIn("01-why-rota.mp4?v=" + "a" * 64, old)
        self.assertNotIn("01-why-rota.mp4?v=" + "a" * 64, text)
        self.assertIn("01-why-rota.mp4?v=" + "b" * 64, text)
        self.assertIn("01-why-rota.vertical.mp4?v=" + "b" * 64, text)
        self.assertIn("01-why-rota.vtt?v=" + "b" * 64, text)
        self.assertEqual(text, self.sync().read_text(), "sync must be idempotent")

    def test_unpulled_lfs_media_fails_with_action(self):
        (self.out / "01-why-rota.mp4").write_text(
            "version https://git-lfs.github.com/spec/v1\noid sha256:" + "a" * 64 + "\nsize 123\n")
        result = subprocess.run(["python3", str(self.repo / "scripts/site-sync.py"), str(self.site)],
                                capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("git lfs pull", result.stderr)
        self.assertNotIn("Traceback", result.stderr)


if __name__ == "__main__":
    unittest.main()
