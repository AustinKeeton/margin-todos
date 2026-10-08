// margin-todos detector: finds hand-drawn checkboxes in the left margin of reMarkable notebook
// pages ("Margin large" template) and crops the handwriting to the right of each into an image.
//
//	detector -src DIR -out DIR -once [-all-templates]   scan everything once and exit
//	detector -src DIR -out DIR                          scan, then watch for page saves (Linux)
//
// Writes OUT/todos.json and OUT/img/*.png. Each to-do keeps the time it was first seen
// ("created", Unix ms) across rescans and restarts.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type store struct {
	src, out     string
	allTemplates bool
	byDoc        map[string][]todo
	created      map[string]int64
}

func (s *store) load() {
	s.byDoc, s.created = map[string][]todo{}, map[string]int64{}
	raw, err := os.ReadFile(filepath.Join(s.out, "todos.json"))
	if err != nil {
		return
	}
	var f struct{ Todos []todo }
	if json.Unmarshal(raw, &f) == nil {
		for _, t := range f.Todos {
			s.created[t.ID] = t.Created
		}
	}
}

// rescan re-reads one notebook; it reports whether its to-dos changed.
func (s *store) rescan(doc string) bool {
	todos, skipped, err := scanDoc(s.src, doc, s.out, s.allTemplates)
	if err != nil {
		if os.IsNotExist(err) { // notebook deleted
			_, had := s.byDoc[doc]
			delete(s.byDoc, doc)
			return had
		}
		log.Printf("%s: %v", doc, err)
		return false
	}
	if skipped > 0 {
		log.Printf("%s: skipped %d unreadable page(s)", doc, skipped)
	}
	now := time.Now().UnixMilli()
	keep := map[string]bool{}
	for i := range todos {
		id := todos[i].ID
		if s.created[id] == 0 {
			s.created[id] = now
		}
		todos[i].Created = s.created[id]
		keep[id] = true
	}
	// Images of to-dos that are gone (box erased).
	for _, t := range s.byDoc[doc] {
		if !keep[t.ID] && t.Image != nil {
			os.Remove(filepath.Join(s.out, *t.Image))
		}
	}
	before, _ := json.Marshal(s.byDoc[doc])
	if len(todos) == 0 {
		delete(s.byDoc, doc)
	} else {
		s.byDoc[doc] = todos
	}
	after, _ := json.Marshal(s.byDoc[doc])
	return string(before) != string(after)
}

func (s *store) save() error {
	all := []todo{}
	for _, ts := range s.byDoc {
		all = append(all, ts...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Notebook != b.Notebook {
			return a.Notebook < b.Notebook
		}
		if a.PageNumber != b.PageNumber {
			return a.PageNumber < b.PageNumber
		}
		return a.Y < b.Y
	})
	data, err := json.MarshalIndent(map[string]any{"todos": all}, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.out, "todos.json.tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.out, "todos.json"))
}

func allDocs(src string) []string {
	matches, _ := filepath.Glob(filepath.Join(src, "*.content"))
	docs := make([]string, 0, len(matches))
	for _, m := range matches {
		docs = append(docs, strings.TrimSuffix(filepath.Base(m), ".content"))
	}
	return docs
}

func main() {
	src := flag.String("src", "/home/root/.local/share/remarkable/xochitl", "notebooks directory")
	out := flag.String("out", "/home/root/todo", "output directory")
	once := flag.Bool("once", false, "scan once and exit")
	allTemplates := flag.Bool("all-templates", false, "search every page, not just Margin large (testing)")
	flag.Parse()
	log.SetFlags(0)

	if err := os.MkdirAll(filepath.Join(*out, "img"), 0755); err != nil {
		log.Fatal(err)
	}
	s := &store{src: *src, out: *out, allTemplates: *allTemplates}
	s.load()
	start := time.Now()
	for _, doc := range allDocs(*src) {
		s.rescan(doc)
	}
	if err := s.save(); err != nil {
		log.Fatal(err)
	}
	n := 0
	for _, ts := range s.byDoc {
		n += len(ts)
	}
	log.Printf("scanned %d notebooks in %v: %d to-dos", len(allDocs(*src)), time.Since(start).Round(time.Millisecond), n)
	if *once {
		return
	}
	if err := watch(s); err != nil {
		log.Fatal(err)
	}
}
