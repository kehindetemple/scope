package ntfs

import "go-file-search/internal/index"

// LoadSnapshot reads the saved index and returns the volumes keyed by root
// path (for example `C:\`). A missing file returns an empty map.
func LoadSnapshot(path string) (map[string]index.VolumeState, error) {
	vols, err := index.ReadSnapshot(path)
	if err != nil {
		return nil, err
	}

	byRoot := make(map[string]index.VolumeState, len(vols))
	for _, v := range vols {
		byRoot[v.Root] = v
	}
	return byRoot, nil
}
