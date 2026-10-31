// Package walk builds an index by walking a directory tree. It is the fallback
// for drives without an NTFS change journal (FAT32, exFAT, ReFS, ...).
package walk

import (
	"io/fs"
	"path/filepath"

	"go-file-search/internal/index"
)

// rootID is the synthetic id of the volume root in walked trees.
const rootID uint64 = 1

// Build walks root and returns a fresh index of everything under it.
// Unreadable folders are skipped rather than stopping the walk.
func Build(root string) (*index.Tree, error) {
	t := index.NewTree(root, rootID)
	cleanRoot := filepath.Clean(root)

	// Maps folder paths to their ids so children can refer to parents.
	folders := map[string]uint64{cleanRoot: rootID}
	next := rootID + 1

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		clean := filepath.Clean(path)
		if clean == cleanRoot {
			return nil
		}

		parent, ok := folders[filepath.Dir(clean)]
		if !ok {
			return nil
		}

		id := next
		next++
		if d.IsDir() {
			folders[clean] = id
		}

		t.Put(index.Entry{ID: id, Parent: parent, Name: d.Name(), Dir: d.IsDir()})
		return nil
	})

	return t, err
}
