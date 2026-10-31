package index

import "testing"

// Regression: a launcher-style query must show the application even when many
// folders and files match the same name earlier in memory.
func TestAppRanksFirstEvenWhenManyMatchesExist(t *testing.T) {
	tr := NewTree(`C:\`, 5)
	id := uint64(100)
	for i := 0; i < 50; i++ {
		id++
		tr.Put(Entry{ID: id, Parent: 5, Name: "capcut", Dir: true})
	}
	tr.Put(Entry{ID: 10, Parent: 5, Name: "Users", Dir: true})
	tr.Put(Entry{ID: 11, Parent: 10, Name: "templ", Dir: true})
	tr.Put(Entry{ID: 12, Parent: 11, Name: "AppData", Dir: true})
	tr.Put(Entry{ID: 13, Parent: 12, Name: "Local", Dir: true})
	tr.Put(Entry{ID: 14, Parent: 13, Name: "CapCut", Dir: true})
	tr.Put(Entry{ID: 15, Parent: 14, Name: "CapCut.exe"})

	hits := tr.Search("capcut", 8)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Kind != KindApp {
		t.Fatalf("first hit kind = %v, want KindApp: %+v", hits[0].Kind, hits[0])
	}
	if hits[0].Name != "CapCut" {
		t.Fatalf("app name = %q, want CapCut (extension hidden)", hits[0].Name)
	}
	if want := `C:\Users\templ\AppData\Local\CapCut\CapCut.exe`; hits[0].Path != want {
		t.Fatalf("path = %q, want %q", hits[0].Path, want)
	}
}

func TestPrefetchAndCacheRankLast(t *testing.T) {
	tr := NewTree(`C:\`, 5)
	tr.Put(Entry{ID: 10, Parent: 5, Name: "Windows", Dir: true})
	tr.Put(Entry{ID: 11, Parent: 10, Name: "Prefetch", Dir: true})
	tr.Put(Entry{ID: 12, Parent: 11, Name: "CAPCUT.EXE-965A6AD6.pf"})
	tr.Put(Entry{ID: 13, Parent: 5, Name: "Notes", Dir: true})
	tr.Put(Entry{ID: 14, Parent: 13, Name: "capcut-plan.txt"})

	hits := tr.Search("capcut", 8)
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d: %+v", len(hits), hits)
	}
	if hits[0].Name != "capcut-plan.txt" || hits[1].Kind != KindNoise {
		t.Fatalf("unexpected order: %+v", hits)
	}
}

func TestShortcutOutsideStartMenuIsAFile(t *testing.T) {
	tr := NewTree(`C:\`, 5)
	tr.Put(Entry{ID: 10, Parent: 5, Name: "Recent", Dir: true})
	tr.Put(Entry{ID: 11, Parent: 10, Name: "chrome.lnk"})
	hits := tr.Search("chrome", 8)
	if len(hits) != 1 || hits[0].Kind != KindFile {
		t.Fatalf("want one KindFile hit, got %+v", hits)
	}
}
