// Package index holds an in-memory, concurrency-safe name index for one volume.
package index

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// maxDepth guards path resolution against corrupt parent loops.
const maxDepth = 256

// Entry is one file or folder stored in the index.
type Entry struct {
	ID     uint64
	Parent uint64
	Name   string
	Dir    bool
}

// Kind groups results the way a launcher does: programs first, then ordinary
// files and folders, then machine-generated noise such as caches and logs.
type Kind uint8

const (
	KindApp   Kind = iota // .exe programs and Start Menu or desktop shortcuts
	KindFile              // documents, folders and anything else
	KindNoise             // caches, logs, prefetch and similar
)

// Hit is one search result.
type Hit struct {
	Path string
	Name string
	Dir  bool
	// Rank: 0 = name equals query, 1 = name starts with query,
	// 2 = name contains query. Lower is better.
	Rank int
	Kind Kind
	// Label replaces the default type label ("Application", "Folder", "File")
	// when it is not empty, for example "Windows Settings".
	Label string
}

// Less orders hits for display: kind first, then rank, then shorter names.
func Less(a, b Hit) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Rank != b.Rank {
		return a.Rank < b.Rank
	}
	if len(a.Name) != len(b.Name) {
		return len(a.Name) < len(b.Name)
	}
	return a.Path < b.Path // a total order, so a longer search keeps the same first rows
}

type node struct {
	id     uint64
	parent uint64
	name   string
	lower  string
	dir    bool
	used   bool
}

// Tree stores entries in a flat slice with an id→slot map. Deleted slots are
// reused, so long-running updates do not grow the slice without bound.
type Tree struct {
	mu     sync.RWMutex
	root   string // e.g. `C:\`
	rootID uint64 // id of the volume root directory; never stored as an entry
	nodes  []node
	pos    map[uint64]int32
	free   []int32
}

// NewTree creates an empty tree for a volume.
func NewTree(root string, rootID uint64) *Tree {
	return NewTreeSized(root, rootID, 0)
}

// NewTreeSized creates an empty tree with room for capacity entries.
func NewTreeSized(root string, rootID uint64, capacity int) *Tree {
	return &Tree{
		root:   root,
		rootID: rootID,
		nodes:  make([]node, 0, capacity),
		pos:    make(map[uint64]int32, capacity),
	}
}

// Root returns the volume root, for example `C:\`.
func (t *Tree) Root() string { return t.root }

// RootID returns the id of the volume root directory.
func (t *Tree) RootID() uint64 { return t.rootID }

// Len returns the number of stored entries.
func (t *Tree) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.pos)
}

// Writer gives exclusive access to the tree for a batch of changes.
type Writer struct {
	t *Tree
}

// Update runs fn while holding the write lock, so a batch of changes is
// applied atomically with respect to searches.
func (t *Tree) Update(fn func(w *Writer)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fn(&Writer{t: t})
}

// Put inserts or replaces one entry.
func (t *Tree) Put(e Entry) {
	t.Update(func(w *Writer) { w.Put(e) })
}

// Has reports whether an id is currently stored.
func (w *Writer) Has(id uint64) bool {
	_, ok := w.t.pos[id]
	return ok
}

// Put inserts or replaces an entry. Entries with an empty name or the root id
// are ignored.
func (w *Writer) Put(e Entry) {
	t := w.t
	if e.ID == t.rootID || e.Name == "" {
		return
	}

	n := node{
		id:     e.ID,
		parent: e.Parent,
		name:   e.Name,
		lower:  strings.ToLower(e.Name),
		dir:    e.Dir,
		used:   true,
	}

	if p, ok := t.pos[e.ID]; ok {
		t.nodes[p] = n
		return
	}

	var p int32
	if k := len(t.free); k > 0 {
		p = t.free[k-1]
		t.free = t.free[:k-1]
		t.nodes[p] = n
	} else {
		p = int32(len(t.nodes))
		t.nodes = append(t.nodes, n)
	}
	t.pos[e.ID] = p
}

// Delete removes an entry if present.
func (w *Writer) Delete(id uint64) {
	t := w.t
	p, ok := t.pos[id]
	if !ok {
		return
	}
	t.nodes[p] = node{}
	t.free = append(t.free, p)
	delete(t.pos, id)
}

// Search returns up to limit matches for a case-insensitive substring query,
// best first (see Less). Applications rank above files, and files above
// caches and logs. Every entry is considered, so a good match is never
// dropped just because of where it sits in memory.
func (t *Tree) Search(query string, limit int) []Hit {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" || limit < 1 {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	// pool holds the best limit candidates seen so far, as a max-heap: the
	// worst of them is always pool[0].
	pool := make([]candidate, 0, min(limit, 1024))
	for i := range t.nodes {
		n := &t.nodes[i]
		if !n.used {
			continue
		}

		kind := classifyName(n.name, n.dir)
		rank := matchRank(n.lower, q)
		if kind == KindApp {
			// "CapCut.exe" should match the query "capcut" as an exact name.
			if r := matchRank(appStem(n.lower), q); r >= 0 && (rank < 0 || r < rank) {
				rank = r
			}
		}
		if rank < 0 {
			continue
		}

		c := candidate{slot: int32(i), rank: rank, kind: kind, nameLen: len(n.name)}
		// Refining the kind can only make a candidate worse, so one that
		// cannot beat the current worst is skipped before its path is built.
		if len(pool) == limit && !c.less(pool[0]) {
			continue
		}

		path, ok := t.pathLocked(n.id)
		if !ok {
			continue
		}
		c.kind = refineKind(c.kind, n.name, path)
		c.path = path

		if len(pool) < limit {
			pool = append(pool, c)
			heapUp(pool, len(pool)-1)
			continue
		}
		if c.less(pool[0]) {
			pool[0] = c
			heapDown(pool, 0)
		}
	}

	sort.Slice(pool, func(a, b int) bool { return pool[a].less(pool[b]) })

	hits := make([]Hit, 0, len(pool))
	for _, c := range pool {
		n := &t.nodes[c.slot]
		name := n.name
		if c.kind == KindApp {
			name = appStem(n.name)
		}
		hits = append(hits, Hit{Path: c.path, Name: name, Dir: n.dir, Rank: c.rank, Kind: c.kind})
	}
	return hits
}

// candidate is a match that Search may return.
type candidate struct {
	slot    int32
	rank    int
	kind    Kind
	nameLen int
	path    string
}

func (c candidate) less(d candidate) bool {
	if c.kind != d.kind {
		return c.kind < d.kind
	}
	if c.rank != d.rank {
		return c.rank < d.rank
	}
	if c.nameLen != d.nameLen {
		return c.nameLen < d.nameLen
	}
	return c.path < d.path
}

// heapUp restores the max-heap after pool[i] was added at the end.
func heapUp(pool []candidate, i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !pool[p].less(pool[i]) {
			return // the parent is already the worse one
		}
		pool[p], pool[i] = pool[i], pool[p]
		i = p
	}
}

// heapDown restores the max-heap after pool[i] was replaced.
func heapDown(pool []candidate, i int) {
	n := len(pool)
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		w := l // the worse of the two children
		if r := l + 1; r < n && pool[l].less(pool[r]) {
			w = r
		}
		if !pool[i].less(pool[w]) {
			return // pool[i] is already the worse one
		}
		pool[i], pool[w] = pool[w], pool[i]
		i = w
	}
}

// matchRank returns 0, 1 or 2 for an exact, prefix or substring match of the
// lower-case name, or -1 for no match.
func matchRank(lower, q string) int {
	switch {
	case lower == q:
		return 0
	case strings.HasPrefix(lower, q):
		return 1
	case strings.Contains(lower, q):
		return 2
	}
	return -1
}

// classifyName gives the kind of an entry from its name alone.
func classifyName(name string, dir bool) Kind {
	if dir {
		return KindFile
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".exe", ".lnk":
		return KindApp
	case ".pf", ".cache", ".log", ".tmp", ".etl", ".dmp":
		return KindNoise
	}
	return KindFile
}

// noiseDirs are folder fragments (lower case) whose contents are machine
// generated: browser and editor caches, logs, prefetch data and the like.
var noiseDirs = []string{
	`\prefetch\`, `\cacheddata\`, `\cache\`, `\code cache\`, `\gpucache\`,
	`\cachestorage\`, `\service worker\`, `\logs\`, `\log\`, `\crashpad\`,
	`\winsxs\`, `\temp\`, `\node_modules\`, `\.git\`,
}

// refineKind uses the full path to demote entries that live in machine
// folders, and to treat .lnk files outside the Start Menu or desktop as files.
func refineKind(kind Kind, name, path string) Kind {
	lp := strings.ToLower(path)
	for _, seg := range noiseDirs {
		if strings.Contains(lp, seg) {
			return KindNoise
		}
	}
	if kind == KindApp && strings.EqualFold(filepath.Ext(name), ".lnk") &&
		!strings.Contains(lp, `\start menu\`) && !strings.Contains(lp, `\desktop\`) {
		return KindFile
	}
	return kind
}

// appStem drops a trailing .exe or .lnk, so "CapCut.exe" shows as "CapCut".
// It works on either case.
func appStem(name string) string {
	if len(name) > 4 {
		if ext := name[len(name)-4:]; strings.EqualFold(ext, ".exe") || strings.EqualFold(ext, ".lnk") {
			return name[:len(name)-4]
		}
	}
	return name
}

// pathLocked builds the full path of an entry by following parent links up
// to the volume root. The caller must hold at least the read lock.
func (t *Tree) pathLocked(id uint64) (string, bool) {
	var parts []string
	cur := id

	for depth := 0; depth < maxDepth; depth++ {
		if cur == t.rootID {
			var b strings.Builder
			b.WriteString(strings.TrimSuffix(t.root, `\`))
			for i := len(parts) - 1; i >= 0; i-- {
				b.WriteByte('\\')
				b.WriteString(parts[i])
			}
			return b.String(), true
		}

		p, ok := t.pos[cur]
		if !ok {
			return "", false // parent missing: entry is orphaned
		}
		n := &t.nodes[p]
		parts = append(parts, n.name)
		cur = n.parent
	}
	return "", false
}

// Range calls fn for every stored entry while holding the read lock.
func (t *Tree) Range(fn func(e Entry) error) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for i := range t.nodes {
		n := &t.nodes[i]
		if !n.used {
			continue
		}
		if err := fn(Entry{ID: n.id, Parent: n.parent, Name: n.name, Dir: n.dir}); err != nil {
			return err
		}
	}
	return nil
}
