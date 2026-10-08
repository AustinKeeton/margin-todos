package main

// Sends todos to the Mac (MarginTodosSync) so they appear in Apple Reminders on the iPhone.
// The tablet always initiates: it POSTs when todos or check marks change, and every 2 minutes
// while awake; the reply carries check marks changed on the phone, which are written back into
// state.json for the panel. Enabled by OUT/sync.json: {"url": "http://<mac>:8766/sync", "token": "..."}.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	pushEvery    = 2 * time.Minute
	pushDebounce = time.Second
)

type pusher struct {
	out, url, token string
	trig            chan struct{}
	client          *http.Client
	lastErr         string
}

func newPusher(out string) *pusher {
	raw, err := os.ReadFile(filepath.Join(out, "sync.json"))
	if err != nil {
		return nil
	}
	var cfg struct{ URL, Token string }
	if json.Unmarshal(raw, &cfg) != nil || cfg.URL == "" {
		log.Printf("sync.json: needs url and token")
		return nil
	}
	return &pusher{out: out, url: cfg.URL, token: cfg.Token, trig: make(chan struct{}, 1),
		client: &http.Client{Timeout: 30 * time.Second}}
}

// trigger asks for a send soon; repeated calls before it happens collapse into one.
func (p *pusher) trigger() {
	if p == nil {
		return
	}
	select {
	case p.trig <- struct{}{}:
	default:
	}
}

func (p *pusher) run() {
	tick := time.NewTicker(pushEvery)
	for {
		select {
		case <-p.trig:
			time.Sleep(pushDebounce)
			select { // fold triggers that arrived during the debounce
			case <-p.trig:
			default:
			}
		case <-tick.C:
		}
		need, err := p.send(nil)
		if err == nil && len(need) > 0 {
			_, err = p.send(need) // the Mac asked for images of new todos
		}
		p.report(err)
	}
}

func (p *pusher) report(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if msg != p.lastErr {
		if msg == "" {
			log.Printf("sync: connected to %s", p.url)
		} else {
			log.Printf("sync: %s", msg)
		}
		p.lastErr = msg
	}
}

func (p *pusher) send(withImages []string) ([]string, error) {
	todosRaw, err := os.ReadFile(filepath.Join(p.out, "todos.json"))
	if err != nil {
		return nil, err
	}
	var todos struct {
		Todos []json.RawMessage `json:"todos"`
	}
	if err := json.Unmarshal(todosRaw, &todos); err != nil {
		return nil, err
	}
	statePath := filepath.Join(p.out, "state.json")
	state := map[string]bool{}
	var stateModified int64
	if raw, err := os.ReadFile(statePath); err == nil {
		json.Unmarshal(raw, &state)
		if fi, err := os.Stat(statePath); err == nil {
			stateModified = fi.ModTime().UnixMilli()
		}
	}
	images := map[string]string{}
	for _, id := range withImages {
		if data, err := os.ReadFile(filepath.Join(p.out, "img", id+".png")); err == nil {
			images[id] = base64.StdEncoding.EncodeToString(data)
		}
	}
	if todos.Todos == nil {
		todos.Todos = []json.RawMessage{}
	}
	body, _ := json.Marshal(map[string]any{
		"todos": todos.Todos, "state": state, "stateModified": stateModified, "images": images,
	})
	req, _ := http.NewRequest("POST", p.url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s replied %s", p.url, resp.Status)
	}
	var reply struct {
		TabletUpdates map[string]bool `json:"tabletUpdates"`
		NeedImages    []string        `json:"needImages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, err
	}
	if len(reply.TabletUpdates) > 0 {
		p.applyUpdates(statePath, reply.TabletUpdates)
	}
	return reply.NeedImages, nil
}

// applyUpdates merges check marks changed on the phone into state.json (re-read first, so a
// tap made in the meantime isn't lost) and writes it atomically.
func (p *pusher) applyUpdates(path string, updates map[string]bool) {
	state := map[string]bool{}
	if raw, err := os.ReadFile(path); err == nil {
		json.Unmarshal(raw, &state)
	}
	for id, checked := range updates {
		state[id] = checked
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	tmp := path + ".tmp"
	if os.WriteFile(tmp, append(data, '\n'), 0644) == nil && os.Rename(tmp, path) == nil {
		log.Printf("sync: %d check mark(s) changed on the phone", len(updates))
	}
}
