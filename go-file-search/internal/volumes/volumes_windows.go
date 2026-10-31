//go:build windows

// Package volumes lists the local drives to index.
package volumes

import (
	"fmt"

	"golang.org/x/sys/windows"
)

const driveFixed = 3 // GetDriveType result for internal hard drives

// Drive is a fixed local drive.
type Drive struct {
	Root       string // for example `C:\`
	FileSystem string // for example "NTFS"
}

// Fixed returns the fixed local drives and their filesystem names.
// Removable, network and optical drives are skipped.
func Fixed() ([]Drive, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, fmt.Errorf("list drives: %w", err)
	}

	var drives []Drive
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}

		root := string(rune('A'+i)) + `:\`
		p, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		if windows.GetDriveType(p) != driveFixed {
			continue
		}

		fsName := make([]uint16, 32)
		if err := windows.GetVolumeInformation(p, nil, 0, nil, nil, nil, &fsName[0], uint32(len(fsName))); err != nil {
			continue
		}

		drives = append(drives, Drive{Root: root, FileSystem: windows.UTF16ToString(fsName)})
	}
	return drives, nil
}
