package main

// Checkbox detection, the same rules as detect.py (kept in step; see README).

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	pageW   = 1404.0
	marginX = 244.0 // "Margin large": templateWidth/2 - templateHeight/2 + 478 on a 1404x1872 page

	boxMin, boxMax       = 20.0, 140.0
	aspectMin, aspectMax = 0.5, 2.0
	pathMin, pathMax     = 0.75, 1.4 // path length vs. bounding-box outline
	maxGap               = 0.3       // start-to-end distance as a share of the outline
	checkOverlap         = 0.3       // share of the box a margin mark must cover to count as a check
	bandPad              = 0.5       // band = box height, extended by this share above and below
	cropPad              = 12.0
	ink                  = 3.0
)

var templates = map[string]bool{"P Margin large": true}

type stroke struct {
	id                     string
	pts                    []point
	x0, y0, x1, y1, length float64
}

func newStroke(id string, pts []point) stroke {
	s := stroke{id: id, pts: pts, x0: math.Inf(1), y0: math.Inf(1), x1: math.Inf(-1), y1: math.Inf(-1)}
	for i, p := range pts {
		s.x0, s.y0 = math.Min(s.x0, p.x), math.Min(s.y0, p.y)
		s.x1, s.y1 = math.Max(s.x1, p.x), math.Max(s.y1, p.y)
		if i > 0 {
			s.length += math.Hypot(p.x-pts[i-1].x, p.y-pts[i-1].y)
		}
	}
	return s
}

// pageStrokes returns a page's strokes in page pixels: x from the page center plus the stroke's
// group anchor, y from the root text block's top.
func pageStrokes(p *page) []stroke {
	out := make([]stroke, 0, len(p.lines))
	for _, l := range p.lines {
		if len(l.points) < 2 {
			continue
		}
		ox := p.anchorX[l.parent]
		pts := make([]point, len(l.points))
		for i, q := range l.points {
			pts[i] = point{pageW/2 + ox + q.x, p.textTopY + q.y}
		}
		out = append(out, newStroke(l.id.String(), pts))
	}
	return out
}

func isCheckbox(s stroke) bool {
	w, h := s.x1-s.x0, s.y1-s.y0
	if s.x0 >= marginX || w < boxMin || w > boxMax || h < boxMin || h > boxMax {
		return false
	}
	outline := 2 * (w + h)
	gap := math.Hypot(s.pts[0].x-s.pts[len(s.pts)-1].x, s.pts[0].y-s.pts[len(s.pts)-1].y)
	return w/h >= aspectMin && w/h <= aspectMax &&
		s.length/outline >= pathMin && s.length/outline <= pathMax &&
		gap <= maxGap*outline
}

// covered is the share of box a covered by box b.
func covered(a, b stroke) float64 {
	ix := math.Max(0, math.Min(a.x1, b.x1)-math.Max(a.x0, b.x0))
	iy := math.Max(0, math.Min(a.y1, b.y1)-math.Max(a.y0, b.y0))
	return ix * iy / math.Max(1, (a.x1-a.x0)*(a.y1-a.y0))
}

type found struct {
	box     stroke
	checked bool
	text    []stroke
}

func findTodos(strokes []stroke) []found {
	var boxes []stroke
	isBox := map[string]bool{}
	for _, s := range strokes {
		if isCheckbox(s) {
			boxes = append(boxes, s)
			isBox[s.id] = true
		}
	}
	sort.SliceStable(boxes, func(i, j int) bool { return boxes[i].y0 < boxes[j].y0 })
	var out []found
	for _, b := range boxes {
		f := found{box: b}
		for _, m := range strokes {
			if !isBox[m.id] && m.x0 < marginX && covered(b, m) > checkOverlap {
				f.checked = true
				break
			}
		}
		pad := (b.y1 - b.y0) * bandPad
		lo, hi := b.y0-pad, b.y1+pad
		for _, s := range strokes {
			if mid := (s.y0 + s.y1) / 2; s.x0 >= marginX && mid >= lo && mid <= hi {
				f.text = append(f.text, s)
			}
		}
		out = append(out, f)
	}
	return out
}

// render draws strokes black on white, cropped to their extent plus a margin.
func render(text []stroke, path string) ([2]int, error) {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, s := range text {
		x0, y0 = math.Min(x0, s.x0), math.Min(y0, s.y0)
		x1, y1 = math.Max(x1, s.x1), math.Max(y1, s.y1)
	}
	x0, y0 = x0-cropPad, y0-cropPad
	w, h := int(x1-x0+cropPad), int(y1-y0+cropPad)
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	r := ink / 2
	dot := func(cx, cy float64) {
		for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
			for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
				if x >= 0 && y >= 0 && x < w && y < h && math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) <= r {
					img.SetGray(x, y, color.Gray{0})
				}
			}
		}
	}
	for _, s := range text {
		for i := 1; i < len(s.pts); i++ {
			a, b := s.pts[i-1], s.pts[i]
			steps := int(math.Ceil(math.Hypot(b.x-a.x, b.y-a.y)*2)) + 1
			for k := 0; k <= steps; k++ {
				t := float64(k) / float64(steps)
				dot(a.x+t*(b.x-a.x)-x0, a.y+t*(b.y-a.y)-y0)
			}
		}
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return [2]int{}, err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return [2]int{}, err
	}
	f.Close()
	return [2]int{w, h}, os.Rename(tmp, path)
}

type todo struct {
	ID           string  `json:"id"`
	Doc          string  `json:"doc"`
	Notebook     string  `json:"notebook"`
	Page         string  `json:"page"`
	PageNumber   int     `json:"pageNumber"`
	Y            int     `json:"y"`
	CheckedInInk bool    `json:"checkedInInk"`
	Image        *string `json:"image"`
	ImageSize    *[2]int `json:"imageSize"`
	Created      int64   `json:"created,omitempty"`
}

type contentFile struct {
	CPages struct {
		Pages []struct {
			ID       string `json:"id"`
			Deleted  json.RawMessage `json:"deleted"`
			Template *struct {
				Value string `json:"value"`
			} `json:"template"`
		} `json:"pages"`
	} `json:"cPages"`
}

// scanDoc finds the to-dos in one notebook. allTemplates disables the template filter (testing).
func scanDoc(src, doc, out string, allTemplates bool) ([]todo, int, error) {
	raw, err := os.ReadFile(filepath.Join(src, doc+".content"))
	if err != nil {
		return nil, 0, err
	}
	var c contentFile
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, 0, err
	}
	name := doc
	if m, err := os.ReadFile(filepath.Join(src, doc+".metadata")); err == nil {
		var meta struct {
			VisibleName string `json:"visibleName"`
		}
		if json.Unmarshal(m, &meta) == nil && meta.VisibleName != "" {
			name = meta.VisibleName
		}
	}
	order := map[string]int{}
	use := map[string]bool{}
	n := 0
	for _, p := range c.CPages.Pages {
		if len(p.Deleted) > 0 && string(p.Deleted) != "null" {
			continue
		}
		order[p.ID] = n
		n++
		if allTemplates || (p.Template != nil && templates[p.Template.Value]) {
			use[p.ID] = true
		}
	}
	var todos []todo
	skipped := 0
	rms, _ := filepath.Glob(filepath.Join(src, doc, "*.rm"))
	sort.Strings(rms)
	for _, rm := range rms {
		pageID := strings.TrimSuffix(filepath.Base(rm), ".rm")
		if _, ok := order[pageID]; !ok || !use[pageID] {
			continue
		}
		data, err := os.ReadFile(rm)
		if err != nil {
			continue
		}
		pg, err := parsePage(data)
		if err != nil {
			skipped++ // e.g. pages still in the older v5 format; edited pages are rewritten as v6
			continue
		}
		for _, f := range findTodos(pageStrokes(pg)) {
			id := doc[:8] + "-" + pageID[:8] + "-" + f.box.id
			t := todo{ID: id, Doc: doc, Notebook: name, Page: pageID, PageNumber: order[pageID] + 1,
				Y: int(math.Round(f.box.y0)), CheckedInInk: f.checked}
			if len(f.text) > 0 {
				img := "img/" + id + ".png"
				if size, err := render(f.text, filepath.Join(out, img)); err == nil {
					t.Image, t.ImageSize = &img, &size
				}
			}
			todos = append(todos, t)
		}
	}
	return todos, skipped, nil
}
