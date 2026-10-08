"""Find hand-drawn checkboxes in the left margin of reMarkable notebook pages and crop the
handwriting to the right of each one into an image.

    python detect.py <xochitl-dir> <out-dir>

<xochitl-dir> holds notebooks as the tablet stores them (<doc>.content, <doc>.metadata,
<doc>/<page>.rm). Writes <out-dir>/todos.json and <out-dir>/img/<id>.png.

Built for the "Margin large" template (portrait): the margin line is at x = 244 page px
(templateWidth/2 - templateHeight/2 + 478, on a 1404x1872 page) and lines are 97 px apart.
"""
import json
import math
import sys
from dataclasses import dataclass
from pathlib import Path

from PIL import Image, ImageDraw
from rmscene import read_tree, si

PAGE_W = 1404
MARGIN_X = 244
# Only pages on this template are searched: elsewhere, box shapes near the left edge are usually
# drawings (352 false hits across older notebooks on Blank, Margin small and Lines small pages).
TEMPLATES = {"P Margin large"}

# A checkbox: one stroke in the margin, roughly square, about as long as its own outline, and
# closed (it ends near where it started). Measured on real boxes: 57x44 px, path 196 vs outline 204.
BOX_MIN, BOX_MAX = 20, 140          # px, each side
BOX_ASPECT = (0.5, 2.0)             # width / height
BOX_PATH_VS_PERIMETER = (0.75, 1.4)
BOX_MAX_GAP = 0.3                   # start-to-end distance, as a share of the outline

# The to-do: strokes right of the margin whose vertical center is within the box's height,
# extended by half of it above and below (covers ascenders/descenders, not the next line).
BAND_PAD = 0.5
CROP_PAD = 12                       # px around the cropped handwriting
INK = 3                             # px stroke width in the cropped image


@dataclass
class Stroke:
    id: str
    pts: list  # [(x, y)] in page px

    @property
    def box(self):
        xs = [p[0] for p in self.pts]
        ys = [p[1] for p in self.pts]
        return min(xs), min(ys), max(xs), max(ys)

    @property
    def length(self):
        return sum(math.dist(a, b) for a, b in zip(self.pts, self.pts[1:]))


def page_strokes(rm_path: Path) -> list[Stroke]:
    """All pen strokes on a page, in page pixels.

    Since 3.x, handwriting sits in groups anchored to the page's text block: x is relative to
    the page center plus the group's anchor_origin_x, y to the text block's top.
    """
    with open(rm_path, "rb") as fh:
        tree = read_tree(fh)
    top = tree.root_text.pos_y if tree.root_text else 0

    def groups(g):
        yield g
        for child in g.children.values():
            if isinstance(child, si.Group):
                yield from groups(child)

    strokes = []
    for g in groups(tree.root):
        ox = g.anchor_origin_x.value if getattr(g, "anchor_origin_x", None) else 0
        for item in g.children.sequence_items():
            line = item.value
            if isinstance(line, si.Line) and len(line.points) > 1:
                pts = [(PAGE_W / 2 + ox + p.x, top + p.y) for p in line.points]
                strokes.append(Stroke(f"{item.item_id.part1}.{item.item_id.part2}", pts))
    return strokes


def is_checkbox(s: Stroke) -> bool:
    x0, y0, x1, y1 = s.box
    w, h = x1 - x0, y1 - y0
    if x0 >= MARGIN_X or not (BOX_MIN <= w <= BOX_MAX and BOX_MIN <= h <= BOX_MAX):
        return False
    perimeter = 2 * (w + h)
    return (BOX_ASPECT[0] <= w / h <= BOX_ASPECT[1]
            and BOX_PATH_VS_PERIMETER[0] <= s.length / perimeter <= BOX_PATH_VS_PERIMETER[1]
            and math.dist(s.pts[0], s.pts[-1]) <= BOX_MAX_GAP * perimeter)


def overlaps(a, b) -> float:
    """Share of box a covered by box b."""
    ix = max(0, min(a[2], b[2]) - max(a[0], b[0]))
    iy = max(0, min(a[3], b[3]) - max(a[1], b[1]))
    area = max(1, (a[2] - a[0]) * (a[3] - a[1]))
    return ix * iy / area


def find_todos(strokes: list[Stroke]):
    boxes = [s for s in strokes if is_checkbox(s)]
    box_ids = {b.id for b in boxes}
    margin_marks = [s for s in strokes if s.id not in box_ids and s.box[0] < MARGIN_X]
    for b in sorted(boxes, key=lambda s: s.box[1]):
        x0, y0, x1, y1 = b.box
        checked = any(overlaps(b.box, m.box) > 0.3 for m in margin_marks)
        pad = (y1 - y0) * BAND_PAD
        band = (y0 - pad, y1 + pad)
        text = [s for s in strokes
                if s.box[0] >= MARGIN_X and band[0] <= (s.box[1] + s.box[3]) / 2 <= band[1]]
        yield b, checked, text


def render(text: list[Stroke], path: Path):
    xs = [p[0] for s in text for p in s.pts]
    ys = [p[1] for s in text for p in s.pts]
    x0, y0 = min(xs) - CROP_PAD, min(ys) - CROP_PAD
    img = Image.new("L", (int(max(xs) - x0 + CROP_PAD), int(max(ys) - y0 + CROP_PAD)), 255)
    d = ImageDraw.Draw(img)
    for s in text:
        d.line([(x - x0, y - y0) for x, y in s.pts], fill=0, width=INK, joint="curve")
    img.save(path, optimize=True)
    return img.size


def main(src: Path, out: Path):
    (out / "img").mkdir(parents=True, exist_ok=True)
    todos = []
    skipped = 0
    for content in sorted(src.glob("*.content")):
        doc = content.stem
        meta = src / f"{doc}.metadata"
        name = json.loads(meta.read_text()).get("visibleName", doc) if meta.exists() else doc
        pages = json.loads(content.read_text()).get("cPages", {}).get("pages", [])
        order = {p["id"]: i for i, p in enumerate(pages) if not p.get("deleted")}
        on_template = {p["id"] for p in pages
                       if (p.get("template") or {}).get("value") in TEMPLATES}
        for rm in sorted((src / doc).glob("*.rm")):
            if rm.stem not in order or (rm.stem not in on_template and not ALL_TEMPLATES):
                continue
            try:
                strokes = page_strokes(rm)
            except Exception:
                # e.g. pages still in the older v5 format, untouched since an older release;
                # the tablet rewrites a page in v6 as soon as it's edited, so new boxes are v6.
                skipped += 1
                continue
            for box, checked, text in find_todos(strokes):
                tid = f"{doc[:8]}-{rm.stem[:8]}-{box.id}"
                size = render(text, out / "img" / f"{tid}.png") if text else None
                todos.append({
                    "id": tid, "doc": doc, "notebook": name, "page": rm.stem,
                    "pageNumber": order[rm.stem] + 1, "y": round(box.box[1]),
                    "checkedInInk": checked, "image": f"img/{tid}.png" if size else None,
                    "imageSize": size,
                })
    todos.sort(key=lambda t: (t["notebook"], t["pageNumber"], t["y"]))
    (out / "todos.json").write_text(json.dumps({"todos": todos}, indent=2) + "\n")
    print(f"{len(todos)} to-dos ->", out / "todos.json", f"({skipped} unreadable pages skipped)")
    for t in todos:
        print(f"  {'[x]' if t['checkedInInk'] else '[ ]'} {t['notebook']} p{t['pageNumber']} y={t['y']}  {t['image']} {t['imageSize']}")


if __name__ == "__main__":
    # --all-templates searches every page (testing); see TEMPLATES for why that's not the default.
    ALL_TEMPLATES = "--all-templates" in sys.argv[3:]
    main(Path(sys.argv[1]), Path(sys.argv[2]))
