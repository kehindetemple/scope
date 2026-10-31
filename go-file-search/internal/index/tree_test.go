package index

import (
	"os"
	"path/filepath"
	"testing"
)

const testRoot = `C:\`
const testRootID = 5

func newTestTree() *Tree {
	t := NewTree(testRoot, testRootID)
	t.Put(Entry{ID: 6, Parent: testRootID, Name: "src", Dir: true})
	t.Put(Entry{ID: 7, Parent: 6, Name: "main.go"})
	t.Put(Entry{ID: 8, Parent: testRootID, Name: "readme.md"})
	t.Put(Entry{ID: 9, Parent: testRootID, Name: "read.txt"})
	t.Put(Entry{ID: 10, Parent: testRootID, Name: "my read notes.txt"})
	return t
}

func TestPathResolution(t *testing.T) {
	tr := newTestTree()
	hits := tr.Search("main.go", 10)
	if len(hits) != 1 {
		t.Fatalf("want 1 hit, got %d", len(hits))
	}
	if want := `C:\src\main.go`; hits[0].Path != want {
		t.Fatalf("path = %q, want %q", hits[0].Path, want)
	}
}

func TestSearchRanking(t *testing.T) {
	tr := newTestTree()
	hits := tr.Search("read", 10)

	if len(hits) != 3 {
		t.Fatalf("want 3 hits, got %d", len(hits))
	}
	// Prefix matches must come before plain substring matches.
	if hits[0].Rank != 1 || hits[1].Rank != 1 || hits[2].Rank != 2 {
		t.Fatalf("unexpected ranks: %+v", hits)
	}
	// Shorter name first within the same rank.
	if hits[0].Name != "read.txt" {
		t.Fatalf("first hit = %q, want read.txt", hits[0].Name)
	}
}

func TestExactMatchRanksFirst(t *testing.T) {
	tr := newTestTree()
	hits := tr.Search("readme.md", 10)
	if len(hits) != 1 || hits[0].Rank != 0 {
		t.Fatalf("exact match not ranked 0: %+v", hits)
	}
}

func TestSearchIsCaseInsensitiveAndRespectsLimit(t *testing.T) {
	tr := newTestTree()
	if got := len(tr.Search("READ", 2)); got != 2 {
		t.Fatalf("limit not applied: got %d hits", got)
	}
	if got := tr.Search("   ", 10); got != nil {
		t.Fatalf("blank query should return nil, got %v", got)
	}
}

func TestDeleteAndSlotReuse(t *testing.T) {
	tr := newTestTree()
	before := tr.Len()

	tr.Update(func(w *Writer) { w.Delete(8) })
	if tr.Len() != before-1 {
		t.Fatalf("Len after delete = %d, want %d", tr.Len(), before-1)
	}
	if hits := tr.Search("readme", 10); len(hits) != 0 {
		t.Fatalf("deleted entry still found: %+v", hits)
	}

	tr.Put(Entry{ID: 99, Parent: testRootID, Name: "new.txt"})
	if len(tr.nodes) != before {
		t.Fatalf("slot not reused: %d slots for %d entries", len(tr.nodes), tr.Len())
	}
}

func TestOrphanedEntryIsNotReturned(t *testing.T) {
	tr := newTestTree()
	tr.Update(func(w *Writer) { w.Delete(6) }) // delete "src", leaving main.go orphaned
	if hits := tr.Search("main.go", 10); len(hits) != 0 {
		t.Fatalf("orphaned entry returned: %+v", hits)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	tr := newTestTree()
	path := filepath.Join(t.TempDir(), "index.snap")

	state := VolumeState{Root: testRoot, RootID: testRootID, JournalID: 42, NextUSN: 1234, Tree: tr}
	if err := WriteSnapshot(path, []VolumeState{state}); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}

	vols, err := ReadSnapshot(path)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if len(vols) != 1 {
		t.Fatalf("want 1 volume, got %d", len(vols))
	}

	got := vols[0]
	if got.Root != testRoot || got.JournalID != 42 || got.NextUSN != 1234 {
		t.Fatalf("metadata mismatch: %+v", got)
	}
	if got.Tree.Len() != tr.Len() {
		t.Fatalf("entry count = %d, want %d", got.Tree.Len(), tr.Len())
	}
	if hits := got.Tree.Search("main.go", 10); len(hits) != 1 || hits[0].Path != `C:\src\main.go` {
		t.Fatalf("restored tree search wrong: %+v", hits)
	}
}

func TestMissingSnapshotIsNotAnError(t *testing.T) {
	vols, err := ReadSnapshot(filepath.Join(t.TempDir(), "missing.snap"))
	if err != nil || vols != nil {
		t.Fatalf("want nil,nil for missing file; got %v, %v", vols, err)
	}
}

func TestCorruptSnapshotIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.snap")
	if err := os.WriteFile(path, []byte("not a snapshot"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSnapshot(path); err == nil {
		t.Fatal("expected error for corrupt snapshot")
	}
}
