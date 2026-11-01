// Package engine owns the drive indexes: it loads the saved snapshot, keeps
// NTFS volumes current from the change journal, rescans other drives, and
// answers searches across all of them.
package engine

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"go-file-search/internal/index"
	"go-file-search/internal/ntfs"
	"go-file-search/internal/volumes"
	"go-file-search/internal/walk"
)

// Options controls snapshot location and refresh timing.
type Options struct {
	SnapshotPath     string
	SnapshotInterval time.Duration
	PollInterval     time.Duration
	WalkRefresh      time.Duration
}

// folderIndex holds a walked index for a drive without a change journal. It is
// replaced atomically after each rescan.
type folderIndex struct {
	mu   sync.RWMutex
	tree *index.Tree
}

func (f *folderIndex) get() *index.Tree {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.tree
}

func (f *folderIndex) set(t *index.Tree) {
	f.mu.Lock()
	f.tree = t
	f.mu.Unlock()
}

type source struct {
	root string
	get  func() *index.Tree
}

// Engine indexes every fixed drive and answers searches.
type Engine struct {
	opts Options

	mu       sync.RWMutex
	sources  []source
	ntfsVols []*ntfs.Volume // fixed after Start; used for snapshots

	saveMu sync.Mutex
}

// New creates an engine. Call Start to begin indexing.
func New(opts Options) *Engine {
	return &Engine{opts: opts}
}

// Start loads the saved index and begins indexing every fixed drive in the
// background. It returns once the drives are known; searches work as soon as
// each drive is ready.
func (e *Engine) Start(ctx context.Context) error {
	saved, err := ntfs.LoadSnapshot(e.opts.SnapshotPath)
	if err != nil {
		log.Printf("ignoring saved index: %v", err)
	}

	drives, err := volumes.Fixed()
	if err != nil {
		return err
	}

	for _, d := range drives {
		if d.FileSystem == "NTFS" {
			vol := ntfs.NewVolume(d.Root)
			if s, ok := saved[d.Root]; ok {
				vol.Restore(s)
			}
			e.ntfsVols = append(e.ntfsVols, vol)
			e.addSource(d.Root, vol.Tree)
			go e.runNTFS(ctx, vol)
			continue
		}

		fi := &folderIndex{}
		e.addSource(d.Root, fi.get)
		go e.runFolderScan(ctx, d.Root, fi)
	}

	go e.saveLoop(ctx)
	return nil
}

// Search returns up to limit matches across all ready drives, best rank first.
func (e *Engine) Search(query string, limit int) []index.Hit {
	e.mu.RLock()
	srcs := append([]source(nil), e.sources...)
	e.mu.RUnlock()

	results := make([][]index.Hit, len(srcs))
	var wg sync.WaitGroup
	for i, s := range srcs {
		t := s.get()
		if t == nil {
			continue // drive not indexed yet
		}
		wg.Add(1)
		go func(i int, t *index.Tree) {
			defer wg.Done()
			results[i] = t.Search(query, limit)
		}(i, t)
	}
	wg.Wait()

	var all []index.Hit
	for _, r := range results {
		all = append(all, r...)
	}
	sort.SliceStable(all, func(i, j int) bool { return index.Less(all[i], all[j]) })
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

// Save writes the NTFS indexes that are ready to the snapshot file.
func (e *Engine) Save() error {
	e.saveMu.Lock()
	defer e.saveMu.Unlock()

	var states []index.VolumeState
	var unlocks []func()
	for _, v := range e.ntfsVols {
		s, unlock := v.Snapshot()
		if s.JournalID == 0 {
			unlock() // not synced yet; keep whatever is on disk
			continue
		}
		states = append(states, s)
		unlocks = append(unlocks, unlock)
	}
	if len(states) == 0 {
		return nil
	}

	err := index.WriteSnapshot(e.opts.SnapshotPath, states)
	for _, unlock := range unlocks {
		unlock()
	}
	return err
}

func (e *Engine) addSource(root string, get func() *index.Tree) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sources = append(e.sources, source{root: root, get: get})
}

func (e *Engine) replaceSource(root string, get func() *index.Tree) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.sources {
		if e.sources[i].root == root {
			e.sources[i].get = get
		}
	}
}

func (e *Engine) saveLoop(ctx context.Context) {
	ticker := time.NewTicker(e.opts.SnapshotInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.Save(); err != nil {
				log.Printf("could not save index: %v", err)
			}
		}
	}
}

// runNTFS keeps an NTFS volume current. If the fast path is unavailable, such
// as without administrator rights, it falls back to a folder scan.
func (e *Engine) runNTFS(ctx context.Context, vol *ntfs.Volume) {
	started := time.Now()
	log.Printf("[%s] indexing from NTFS metadata...", vol.Root)

	err := vol.Run(ctx, e.opts.PollInterval, func() {
		log.Printf("[%s] ready: %d entries (%s), live updates on",
			vol.Root, vol.Tree().Len(), time.Since(started).Round(time.Millisecond))
	}, func(format string, args ...any) {
		log.Printf(format, args...)
	})

	if err == nil || ctx.Err() != nil {
		return
	}

	log.Printf("[%s] fast index unavailable: %v", vol.Root, err)
	log.Printf("[%s] falling back to a folder scan (refreshes every %s)", vol.Root, e.opts.WalkRefresh)

	fi := &folderIndex{}
	e.replaceSource(vol.Root, fi.get)
	e.runFolderScan(ctx, vol.Root, fi)
}

// runFolderScan walks a drive and repeats the walk on a timer.
func (e *Engine) runFolderScan(ctx context.Context, root string, fi *folderIndex) {
	for {
		started := time.Now()
		t, err := walk.Build(root)
		if err != nil {
			log.Printf("[%s] folder scan error: %v", root, err)
		}
		fi.set(t)
		log.Printf("[%s] folder scan complete: %d entries (%s)",
			root, t.Len(), time.Since(started).Round(time.Millisecond))

		select {
		case <-ctx.Done():
			return
		case <-time.After(e.opts.WalkRefresh):
		}
	}
}
