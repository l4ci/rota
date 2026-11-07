# The videos page catalog: reads videos/out/manifest.json, the episode front
# matter, the videos/README.md description table and MP4 movie-header
# durations (no render tools). site-sync.py expands the catalog marker with
# video_catalog(); nothing else reads these files.
import json, os, struct, sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fmlite  # noqa: E402

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def movie_seconds(path):
    """Read the ISO BMFF movie-header duration, seeking past the media data."""
    with open(path, "rb") as f:
        def scan(end):
            while f.tell() + 8 <= end:
                start = f.tell()
                size, kind = struct.unpack(">I4s", f.read(8))
                if size == 1:
                    size = struct.unpack(">Q", f.read(8))[0]
                elif size == 0:
                    size = end - start
                if size < f.tell() - start or start + size > end:
                    break
                if kind == b"moov":
                    return scan(start + size)
                if kind == b"mvhd":
                    data = f.read(min(size - (f.tell() - start), 32))
                    if len(data) >= 20 and data[0] == 0:
                        scale, duration = struct.unpack(">II", data[12:20])
                    elif len(data) >= 32 and data[0] == 1:
                        scale, duration = struct.unpack(">IQ", data[20:32])
                    else:
                        break
                    if scale and duration:
                        return round(duration / scale)
                    break
                f.seek(start + size)
            raise ValueError("%s: no MP4 movie duration; run git lfs pull before syncing" % path)

        try:
            return scan(os.fstat(f.fileno()).st_size)
        except struct.error:
            raise ValueError("%s: truncated MP4 box; run git lfs pull before syncing" % path)


def video_catalog(repo=REPO):
    root = os.path.join(repo, "videos")
    manifest = os.path.join(root, "out", "manifest.json")
    if not os.path.exists(manifest):
        return "No episodes published yet."
    with open(manifest, encoding="utf-8") as f:
        episodes = json.load(f)["episodes"]
    descriptions = {}
    with open(os.path.join(root, "README.md"), encoding="utf-8") as f:
        for line in f:
            cells = [c.strip() for c in line.strip().strip("|").split("|")]
            if len(cells) == 8 and cells[0].isdigit():
                descriptions[int(cells[0])] = cells[3]
    blocks = []
    for slug, episode in sorted(episodes.items()):
        front, _ = fmlite.load_front_matter(os.path.join(root, "episodes", slug + ".md"))
        number = int(front["number"])
        outputs = episode["outputs"]
        name = slug + ".mp4"
        seconds = movie_seconds(os.path.join(root, "out", name))

        def media(filename):
            return ("https://media.githubusercontent.com/media/l4ci/rota/main/videos/out/%s?v=%s"
                    % (filename, outputs[filename]))

        links = ["[Watch video (MP4)](%s)" % media(name)]
        if slug + ".vertical.mp4" in outputs:
            links.append("[Portrait video (MP4)](%s)" % media(slug + ".vertical.mp4"))
        if slug + ".vtt" in outputs:
            links.append("[Captions (VTT)](https://raw.githubusercontent.com/l4ci/rota/main/videos/out/%s.vtt?v=%s)"
                         % (slug, outputs[slug + ".vtt"]))
        blocks.append("## Episode %d: %s\n{: #episode-%d}\n\nLength: %d:%02d\n\n%s.\n\n%s"
                      % (number, front["title"], number, seconds // 60, seconds % 60,
                         descriptions[number].rstrip("."), " · ".join(links)))
    return "\n\n".join(blocks) or "No episodes published yet."
