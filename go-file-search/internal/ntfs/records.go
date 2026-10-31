// Package ntfs keeps an index of an NTFS volume current using the MFT and the
// USN change journal. This file holds the platform-independent parts: parsing
// USN records and applying them to an index.Tree.
package ntfs

import (
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf16"

	"go-file-search/internal/index"
)

// RootFRN is the MFT record number of the NTFS root directory.
const RootFRN uint64 = 5

// USN change reasons (subset used here).
const (
	ReasonFileCreate    uint32 = 0x00000100
	ReasonFileDelete    uint32 = 0x00000200
	ReasonRenameNewName uint32 = 0x00002000
)

const (
	fileAttributeDirectory uint32 = 0x10
	frnMask                uint64 = 0x0000FFFFFFFFFFFF // low 48 bits = MFT index
	usnRecordV2            uint16 = 2
	minRecordV2                   = 60 // fixed header size before the name
)

// ErrJournalReset means the journal no longer holds the position we need,
// so the index must be rebuilt from the MFT.
var ErrJournalReset = errors.New("USN journal entries were deleted; full rescan required")

// ErrMalformed is returned when a USN buffer cannot be parsed.
var ErrMalformed = errors.New("ntfs: malformed USN record buffer")

// Record is one parsed USN_RECORD_V2.
type Record struct {
	FRN    uint64
	Parent uint64
	USN    int64
	Reason uint32
	Name   string
	Dir    bool
}

// ParseRecords parses the USN_RECORD_V2 entries in buf. The first headerSize
// bytes are the header returned by the Windows call (8 for both MFT
// enumeration and journal reads).
func ParseRecords(buf []byte, headerSize int) ([]Record, error) {
	var out []Record

	for off := headerSize; off+8 <= len(buf); {
		recLen := int(binary.LittleEndian.Uint32(buf[off:]))
		if recLen == 0 {
			break
		}
		if recLen < minRecordV2 || off+recLen > len(buf) {
			return out, ErrMalformed
		}

		rec := buf[off : off+recLen]
		if binary.LittleEndian.Uint16(rec[4:]) == usnRecordV2 {
			nameLen := int(binary.LittleEndian.Uint16(rec[56:]))
			nameOff := int(binary.LittleEndian.Uint16(rec[58:]))
			if nameOff+nameLen > recLen {
				return out, ErrMalformed
			}

			out = append(out, Record{
				FRN:    binary.LittleEndian.Uint64(rec[8:]) & frnMask,
				Parent: binary.LittleEndian.Uint64(rec[16:]) & frnMask,
				USN:    int64(binary.LittleEndian.Uint64(rec[24:])),
				Reason: binary.LittleEndian.Uint32(rec[40:]),
				Dir:    binary.LittleEndian.Uint32(rec[52:])&fileAttributeDirectory != 0,
				Name:   decodeName(rec[nameOff : nameOff+nameLen]),
			})
		}

		off += recLen
	}

	return out, nil
}

func decodeName(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

// hidden reports NTFS entries that should not appear in results: the root
// itself and system metafiles such as $MFT, $Bitmap and $Secure.
func hidden(r Record) bool {
	return r.FRN == RootFRN || (r.Parent == RootFRN && strings.HasPrefix(r.Name, "$"))
}

func entryOf(r Record) index.Entry {
	return index.Entry{ID: r.FRN, Parent: r.Parent, Name: r.Name, Dir: r.Dir}
}

// StoreRecords inserts records from a full MFT enumeration.
func StoreRecords(t *index.Tree, recs []Record) {
	t.Update(func(w *index.Writer) {
		for _, r := range recs {
			if hidden(r) {
				continue
			}
			w.Put(entryOf(r))
		}
	})
}

// ApplyRecords applies journal changes in order.
//
// For each record:
//   - FILE_DELETE removes the entry.
//   - If the entry is already indexed, it is updated with the record's name
//     and parent. This covers renames and other changes.
//   - A record for an unknown entry is only added if it is a create or a
//     rename target. Late CLOSE records for deleted files are ignored, so
//     deleted files cannot come back.
func ApplyRecords(t *index.Tree, recs []Record) {
	t.Update(func(w *index.Writer) {
		for _, r := range recs {
			if r.Reason&ReasonFileDelete != 0 {
				w.Delete(r.FRN)
				continue
			}
			if hidden(r) {
				continue
			}
			if w.Has(r.FRN) || r.Reason&(ReasonFileCreate|ReasonRenameNewName) != 0 {
				w.Put(entryOf(r))
			}
		}
	})
}
