package main

// Live updates: inotify on the notebooks directory and each notebook's page folder. A burst of
// writes to one notebook (the app saves pages and .content together) is rescanned once, 1.5 s
// after the last write. No polling: the process sleeps until the kernel reports a write.

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const settle = 1500 * time.Millisecond

func watch(s *store) error {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
	if err != nil {
		return err
	}
	const mask = syscall.IN_CLOSE_WRITE | syscall.IN_MOVED_TO | syscall.IN_CREATE | syscall.IN_DELETE
	dirs := map[int32]string{} // watch descriptor -> notebook id ("" for the top directory)
	add := func(path, doc string) {
		wd, err := syscall.InotifyAddWatch(fd, path, mask)
		if err == nil {
			dirs[int32(wd)] = doc
		}
	}
	add(s.src, "")
	for _, doc := range allDocs(s.src) {
		add(filepath.Join(s.src, doc), doc)
	}
	log.Printf("watching %d notebooks", len(dirs)-1)

	events := make(chan string, 64)
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, err := syscall.Read(fd, buf)
			if err != nil {
				if err == syscall.EINTR {
					continue
				}
				log.Fatalf("inotify: %v", err)
			}
			for off := 0; off+syscall.SizeofInotifyEvent <= n; {
				ev := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[off]))
				name := ""
				if ev.Len > 0 {
					raw := buf[off+syscall.SizeofInotifyEvent : off+syscall.SizeofInotifyEvent+int(ev.Len)]
					name = string(bytes.TrimRight(raw, "\x00"))
				}
				off += syscall.SizeofInotifyEvent + int(ev.Len)
				doc, ok := dirs[ev.Wd]
				if !ok {
					continue
				}
				switch {
				case doc != "" && strings.HasSuffix(name, ".rm"):
					events <- doc
				case doc == "" && ev.Mask&syscall.IN_ISDIR != 0 && ev.Mask&syscall.IN_CREATE != 0:
					add(filepath.Join(s.src, name), name) // a new notebook's page folder
				case doc == "" && (strings.HasSuffix(name, ".content") || strings.HasSuffix(name, ".metadata")):
					events <- strings.TrimSuffix(strings.TrimSuffix(name, ".content"), ".metadata")
				}
			}
		}
	}()

	pending := map[string]bool{}
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		select {
		case doc := <-events:
			pending[doc] = true
			timer.Reset(settle)
		case <-timer.C:
			changed := false
			for doc := range pending {
				start := time.Now()
				if s.rescan(doc) {
					changed = true
					log.Printf("%s: to-dos updated (%v)", doc, time.Since(start).Round(time.Millisecond))
				}
			}
			pending = map[string]bool{}
			if changed {
				if err := s.save(); err != nil {
					log.Printf("save: %v", err)
				}
			}
		}
	}
}
