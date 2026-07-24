package fsops

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher watches a set of directories (the expanded, visible ones) and
// delivers debounced batches of dirty directory paths on Events.
type Watcher struct {
	Events chan []string

	fsw      *fsnotify.Watcher
	debounce time.Duration

	mu      sync.Mutex
	watched map[string]bool
}

func NewWatcher(debounce time.Duration) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		Events:   make(chan []string, 16),
		fsw:      fsw,
		debounce: debounce,
		watched:  map[string]bool{},
	}
	go w.loop()
	return w, nil
}

// Sync makes the watched set exactly dirs. Errors adding individual watches
// (e.g. a dir deleted between listing and watching) are ignored; the next
// refresh will drop the dir from the desired set anyway.
func (w *Watcher) Sync(dirs []string) {
	want := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		want[d] = true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for d := range w.watched {
		if !want[d] {
			_ = w.fsw.Remove(d)
			delete(w.watched, d)
		}
	}
	for d := range want {
		if !w.watched[d] {
			if err := w.fsw.Add(d); err == nil {
				w.watched[d] = true
			}
		}
	}
}

func (w *Watcher) Close() {
	_ = w.fsw.Close()
}

func (w *Watcher) isWatched(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.watched[path]
}

func (w *Watcher) loop() {
	pending := map[string]bool{}
	var timer <-chan time.Time
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			// An event names a child of a watched dir; the dir to re-read is
			// its parent. If the named path is itself watched (dir removed or
			// renamed), both it and its parent are stale.
			pending[filepath.Dir(ev.Name)] = true
			if w.isWatched(ev.Name) {
				pending[ev.Name] = true
			}
			if timer == nil {
				timer = time.After(w.debounce)
			}
		case <-timer:
			timer = nil
			batch := make([]string, 0, len(pending))
			for d := range pending {
				batch = append(batch, d)
			}
			pending = map[string]bool{}
			select {
			case w.Events <- batch:
			default: // receiver is behind; merge into the next batch
				for _, d := range batch {
					pending[d] = true
				}
				timer = time.After(w.debounce)
			}
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
		}
	}
}
