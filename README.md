# margin-todos

Handwritten todos for the reMarkable 2. Draw a checkbox in the left margin of a page that uses the
**Margin large** template, write the todo to its right, and it shows up in a list you can open from
the writing screen: a small tab near the bottom-right corner, 1 cm from the edge. The list is per
notebook. Tap a checkbox to complete or reopen a todo; tap the todo itself to jump to that place
in the notebook. Open todos come first, then completed ones, newest first within each. There's no
handwriting recognition yet: each todo is an image of what you wrote.

Tested on firmware **3.22.4.2** with xovi v19.

## Pieces

| Piece | Where it runs | What it does |
|---|---|---|
| `detector/` (Go) | tablet, `margin-todos.service` | Watches notebook saves with inotify, finds checkboxes, renders each todo's handwriting to a PNG, writes `/home/root/todo/todos.json` |
| `tablet/TodoPanel.qml` | inside reMarkable's app | The tab and panel; reads `todos.json`, stores check marks in `state.json` |
| `tablet/margin-todos.qmd` | xovi / qt-resource-rebuilder | Inserts a `Loader` for `TodoPanel.qml` into the writing screen (`DocumentView.qml`, `Item#_uiContainer`) |
| `tools/detect.py` | Mac | The reference detector (Python + rmscene); the Go one must match it |
| `sync/MarginTodosSync.swift` | Mac mini, background app | Receives the tablet's todos and keeps one Apple Reminders list per notebook (iCloud → iPhone) |

## Detection

On "Margin large" pages, the margin line is at x = 244 page px (`templateWidth/2 - templateHeight/2 + 478`
on a 1404×1872 page), and lines are 97 px apart.

- **Checkbox:** one stroke left of the margin, 20–140 px on a side, aspect ratio 0.5–2, path length
  0.75–1.4 × its bounding-box outline, and ending within 30% of the outline from where it started.
  (Measured on real boxes: 57×44 px, path 196 against an outline of 204.)
- **Checked in ink:** another margin stroke covers more than 30% of the box. That's the starting
  state; taps in the list override it.
- **The todo:** strokes right of the margin whose vertical center lies within the box's height,
  extended by half of it above and below. Measured relative to the box, so it doesn't depend on
  the template lines.
- **Only Margin large pages.** Box-shaped drawings near the left edge of other pages are common:
  352 false hits across 50 older notebooks without this filter, 0 with it.
- **Page coordinates:** since 3.x, strokes sit in groups anchored to the page's text block:
  x = 702 + group `anchor_origin_x` + point x, y = root text `pos_y` + point y.
- Pages still in the older v5 format are skipped; the tablet rewrites a page as v6 once it's edited.

`detector/rmfile.go` reads only what detection needs from the v6 format (line items, group anchors,
root text position), ported from [rmscene](https://github.com/ricklupton/rmscene). Checked against
`tools/detect.py`: identical results on the 352-detection set (ids, page, y, checked, image size).

## Live updates

reMarkable's app saves a page while you work on it and again when you close the notebook. The
detector waits on inotify (no polling, no CPU while idle), and rescans a notebook 1.5 s after its
last write. A full scan of 51 notebooks takes about 0.2 s on the tablet; the service uses about
6.5 MB of memory. Each todo keeps the time it was first seen (`created`), which orders the list.

## UI notes

- reMarkable's app reads the pen in its own thread straight into the canvas, so the panel can't
  block it: use a finger on the panel. Writing over the open panel inks the page underneath.
- The tablet's font has no ✓ or ▾ glyphs, so the check and chevron are drawn shapes.
- Jumping: `documentView.openPage(document.pageForId(pageId), ScrollPosition.Restore)` after
  `LibraryController.setScrollPosition(docId, page, y + 0.6 × view height)` (a saved scroll position is
  the page y at the bottom of the view). Found in the app's own QML.
- `TodoPanel.qml` loads when the app starts: restart xochitl after changing it.

## iPhone sync (Apple Reminders)

The tablet sends; nothing connects to it. When todos or check marks change, and every 2 minutes
while awake, the detector POSTs to the Mac (`/home/root/todo/sync.json`: url + shared token):
the todos, the tablet's check marks with their last-change time, and images the Mac asked for.
The reply carries check marks changed on the phone, merged back into `state.json`.

On the Mac, `MarginTodosSync.app` (in `/Applications`, run by the LaunchAgent
`com.audie.margin-todos-sync` in the Background session):

- **One list per notebook**, named after it, following renames. An existing list with the same
  name is used rather than duplicated.
- **Titles from on-device OCR** (Vision, handwriting): set once when the reminder is created and
  never overwritten, so edits on the phone stick. 5/5 correct on the first real todos.
- **Checked state syncs both ways:** whichever side changed since the last sync wins; if both did,
  the more recent change wins (phone: reminder's last-modified time; tablet: `state.json` mtime).
- **Erasing a box** on the tablet deletes its reminder. **Deleting a reminder** on the phone leaves
  it gone (the todo is remembered as dismissed while its box exists).
- State in `~/Library/Application Support/MarginTodosSync/` (`config.json` with the port and
  token, `state.json`, `images/`, `log.txt`).

Setup: `sync/build-app.sh`, copy `MarginTodosSync.app` to `/Applications`, write `config.json`
(`{"token": "...", "port": 8766}`), then **open the app once on the Mac's own screen**: it asks
for Reminders access, which macOS only shows in the desktop session. After that the LaunchAgent can
run it in the background (the grant follows the app). Rebuilding changes the ad-hoc signature, so
macOS may ask again.

## Install

Needs [xovi](https://github.com/asivery/rm-xovi-extensions) with qt-resource-rebuilder and a hashtab
(`xovi/rebuild_hashtable`). The detector runs regardless; only the tab needs xovi.

**Starting xovi at boot.** xovi is tethered by design (a reboot comes back stock).
`xovi-autostart.service` runs `/home/root/xovi/autostart` after the app starts, which starts xovi
with a crash-loop guard: each boot counts an attempt in `/home/root/xovi/autostart-attempts`, two
minutes of the app staying up resets it, and after two attempts in a row that didn't, the tablet
stays stock until that file is deleted. If a mod crashed the app, the cost is at most two extra
reboots. (xovi's author warns against auto-start on *encrypted* tablets, where it can boot-loop
before `/home` is unlocked; this tablet's `/home` is a plain partition.)

```sh
scripts/build.sh                 # Go → detector/margin-todos-detector (linux/arm, static)
scripts/install.sh [ssh-host]    # detector service, panel, xovi diff; restarts xochitl
```

## Tools

- `tools/detect.py <xochitl-dir> <out-dir> [--all-templates]`: reference detector.
- `tools/unhash.py <hashtab> <file.qmd>`: translates community hashed `.qmd` files into readable
  names, to learn what they patch. Output contains reMarkable's own names: keep it local.
- `tools/qrcdump/`: an `LD_PRELOAD` library that saves an app's compiled-in Qt resources as they
  register, to read the app's QML for reference. Keep the dump local; it's reMarkable's code.
