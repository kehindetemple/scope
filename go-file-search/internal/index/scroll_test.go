package index

import (
	"fmt"
	"testing"
)

// Regression: the search box scrolls through long result lists by asking for
// more results as the user nears the end. The first rows must not change when
// the limit grows, or the list would jump while scrolling.
func TestLongListKeepsOrderWhenLimitGrows(t *testing.T) {
	tr := NewTree(`C:\`, 5)
	tr.Put(Entry{ID: 6, Parent: 5, Name: "pics", Dir: true})
	for i := 0; i < 500; i++ {
		// Same name length and rank for every file, so only the path breaks ties.
		tr.Put(Entry{ID: uint64(100 + i), Parent: 6, Name: fmt.Sprintf("img%03d.png", (i*37)%500)})
	}

	small := tr.Search(".png", 50)
	big := tr.Search(".png", 400)
	all := tr.Search(".png", 5000)

	if len(small) != 50 || len(big) != 400 || len(all) != 500 {
		t.Fatalf("got %d, %d and %d hits, want 50, 400 and 500", len(small), len(big), len(all))
	}
	for i := range small {
		if small[i].Path != big[i].Path || small[i].Path != all[i].Path {
			t.Fatalf("row %d changed when the limit grew: %q, %q, %q", i, small[i].Path, big[i].Path, all[i].Path)
		}
	}
	for i := 1; i < len(all); i++ {
		if !Less(all[i-1], all[i]) {
			t.Fatalf("hits %d and %d are out of order: %q, %q", i-1, i, all[i-1].Path, all[i].Path)
		}
	}
}
