#!/usr/bin/env python3
"""movie_seconds reads ISO BMFF durations; the catalog reads the videos/ tree."""
import os
import struct
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "scripts"))
import video_catalog  # noqa: E402


def box(kind, data):
    return struct.pack(">I4s", len(data) + 8, kind) + data


def movie(seconds, version=0, scale=1000):
    fields = (struct.pack(">QQIQ", 0, 0, scale, seconds * scale) if version
              else struct.pack(">IIII", 0, 0, scale, seconds * scale))
    return box(b"ftyp", b"isom\0\0\0\0") + box(b"moov", box(b"mvhd", bytes([version, 0, 0, 0]) + fields))


class MovieSecondsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)

    def write(self, data):
        path = os.path.join(self.tmp.name, "a.mp4")
        with open(path, "wb") as f:
            f.write(data)
        return path

    def test_32_bit_mvhd(self):
        self.assertEqual(video_catalog.movie_seconds(self.write(movie(45))), 45)

    def test_64_bit_mvhd(self):
        self.assertEqual(video_catalog.movie_seconds(self.write(movie(125, version=1, scale=600))), 125)

    def test_missing_duration_names_lfs(self):
        path = self.write(b"version https://git-lfs.github.com/spec/v1\noid sha256:" + b"a" * 64 + b"\nsize 123\n")
        with self.assertRaisesRegex(ValueError, "git lfs pull"):
            video_catalog.movie_seconds(path)

    def test_zero_duration_errors(self):
        with self.assertRaisesRegex(ValueError, "no MP4 movie duration"):
            video_catalog.movie_seconds(self.write(movie(0)))


class CatalogTest(unittest.TestCase):
    def test_no_manifest(self):
        with tempfile.TemporaryDirectory() as root:
            self.assertEqual(video_catalog.video_catalog(root), "No episodes published yet.")


if __name__ == "__main__":
    unittest.main()
