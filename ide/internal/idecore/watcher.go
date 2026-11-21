package idecore

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileChangeEvent coalesces debounced filesystem changes under Root.
// Changed holds relative slash paths; when Overflow is true the batch exceeded
// the cap and the receiver should treat the whole workspace as stale.
// GitChanged is true when .git state files (HEAD, index, MERGE_HEAD, REBASE_HEAD) changed.
type FileChangeEvent struct {
	Root       string   `json:"root"`
	Changed    []string `json:"changed"`
	Overflow   bool     `json:"overflow"`
	GitChanged bool     `json:"gitChanged,omitempty"`
}

const (
	watchDebounce = 250 * time.Millisecond
	watchMaxBatch = 400
)

// FileWatcher recursively watches root for filesystem changes (agents editing
// files, git operations, external tools) and delivers them as debounced,
// coalesced batches to onChange. Directories in ignoredDirs plus .hivemind are
// excluded, mirroring the FileIndex walk. New directories get watches as they
// appear, and root/nested .git directories are watched non-recursively for
// HEAD/index/MERGE_HEAD/REBASE_HEAD transitions.
type FileWatcher struct {
	root     string
	onChange func(FileChangeEvent)

	w        *fsnotify.Watcher
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	pend     map[string]bool
	gitDirty bool
}

// NewFileWatcher constructs a watcher for root. Call Start to begin.
func NewFileWatcher(root string, onChange func(FileChangeEvent)) (*FileWatcher, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil, err
	}
	if onChange == nil {
		onChange = func(FileChangeEvent) {}
	}
	return &FileWatcher{
		root:     abs,
		onChange: onChange,
		pend:     make(map[string]bool),
		done:     make(chan struct{}),
	}, nil
}

// Start registers the recursive watches and spawns the event loop.
func (fw *FileWatcher) Start() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	fw.mu.Lock()
	if fw.w != nil { // already started
		fw.mu.Unlock()
		_ = w.Close()
		return nil
	}
	fw.w = w
	fw.mu.Unlock()

	if err := fw.watchTree(fw.root); err != nil {
		_ = w.Close()
		return err
	}
	fw.watchGitDirs()

	go fw.loop()
	return nil
}

// Close stops the watcher. Safe to call more than once.
func (fw *FileWatcher) Close() {
	fw.once.Do(func() {
		fw.mu.Lock()
		w := fw.w
		fw.w = nil
		fw.mu.Unlock()
		close(fw.done)
		if w != nil {
			_ = w.Close()
		}
	})
}

func (fw *FileWatcher) loop() {
	flush := time.NewTimer(watchDebounce)
	defer flush.Stop()
	for {
		fw.mu.Lock()
		w := fw.w
		fw.mu.Unlock()
		if w == nil {
			return
		}
		select {
		case <-fw.done:
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			fw.handle(ev)
			if !flush.Stop() {
				// drain a pending tick so the next Reset starts a fresh window.
				select {
				case <-flush.C:
				default:
				}
			}
			flush.Reset(watchDebounce)
		case <-flush.C:
			fw.emitFlush()
			flush.Reset(watchDebounce)
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			_ = err // watcher keeps running on transient read errors
		}
	}
}

func (fw *FileWatcher) handle(ev fsnotify.Event) {
	rel, err := filepath.Rel(fw.root, ev.Name)
	if err != nil {
		return
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return
	}
	if isWatchedGitStateRel(rel) {
		fw.mu.Lock()
		fw.gitDirty = true
		fw.mu.Unlock()
		return
	}
	if (rel == ".git" || strings.HasSuffix(rel, "/.git")) && ev.Has(fsnotify.Create) {
		if info, sErr := os.Stat(ev.Name); sErr == nil && info.IsDir() {
			fw.mu.Lock()
			w := fw.w
			fw.gitDirty = true
			fw.mu.Unlock()
			if w != nil {
				_ = w.Add(ev.Name)
			}
		}
		return
	}
	if isIgnoredRelPath(rel) {
		return
	}
	if ev.Has(fsnotify.Create) {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			go fw.watchTree(ev.Name)
		}
	}
	fw.mu.Lock()
	fw.pend[rel] = true
	fw.mu.Unlock()
}

// emitFlush drains the pending batch into one FileChangeEvent. Called from a
// timer flush by the loop; exported for tests.
func (fw *FileWatcher) emitFlush() {
	fw.mu.Lock()
	if len(fw.pend) == 0 && !fw.gitDirty {
		fw.mu.Unlock()
		return
	}
	changed := make([]string, 0, len(fw.pend))
	overflow := false
	for rel := range fw.pend {
		changed = append(changed, rel)
	}
	gitChanged := fw.gitDirty
	fw.pend = make(map[string]bool)
	fw.gitDirty = false
	fw.mu.Unlock()

	if len(changed) > watchMaxBatch {
		overflow = true
		changed = nil
	}
	sort.Strings(changed)
	fw.onChange(FileChangeEvent{
		Root:       fw.root,
		Changed:    changed,
		Overflow:   overflow,
		GitChanged: gitChanged,
	})
}

// watchGitDirs adds non-recursive watches to <root>/.git and nested repo .git
// folders (up to depth 2) so branch switches and staging changes emit GitChanged
// without watching .git/objects.
func (fw *FileWatcher) watchGitDirs() {
	var scan func(dir string, depth int)
	scan = func(dir string, depth int) {
		fw.mu.Lock()
		w := fw.w
		fw.mu.Unlock()
		if w == nil {
			return
		}
		gitDir := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
			_ = w.Add(gitDir)
		}
		if depth >= 2 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if shouldSkipRepoScanDir(name) {
				continue
			}
			scan(filepath.Join(dir, name), depth+1)
		}
	}
	scan(fw.root, 0)
}

func isWatchedGitStateRel(rel string) bool {
	clean := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(rel)), "./")
	var rest string
	switch {
	case strings.HasPrefix(clean, ".git/"):
		rest = clean[len(".git/"):]
	default:
		if idx := strings.LastIndex(clean, "/.git/"); idx >= 0 {
			rest = clean[idx+len("/.git/"):]
		} else {
			return false
		}
	}
	switch rest {
	case "HEAD", "index", "MERGE_HEAD", "REBASE_HEAD":
		return true
	default:
		return false
	}
}

func (fw *FileWatcher) watchTree(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != dir {
			if ignoredDirs[d.Name()] || d.Name() == ".hivemind" {
				return filepath.SkipDir
			}
		}
		fw.mu.Lock()
		w := fw.w
		fw.mu.Unlock()
		if w == nil {
			return filepath.SkipAll
		}
		if err := w.Add(path); err != nil {
			return nil // unreadable or already removed; keep walking
		}
		return nil
	})
}

// isIgnoredRelPath reports whether a workspace-relative slash path touches an
// excluded directory (ignoredDirs or .hivemind).
func isIgnoredRelPath(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if ignoredDirs[p] || p == ".hivemind" {
			return true
		}
	}
	return false
}
