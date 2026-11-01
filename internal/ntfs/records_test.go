package ntfs

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"

	"go-file-search/internal/index"
)

// makeRecord builds one USN_RECORD_V2 the way Windows lays it out.
func makeRecord(frn, parent uint64, usn int64, reason, attrs uint32, name string) []byte {
	units := utf16.Encode([]rune(name))
	nameLen := len(units) * 2
	recLen := (minRecordV2 + nameLen + 7) &^ 7

	buf := make([]byte, recLen)
	binary.LittleEndian.PutUint32(buf[0:], uint32(recLen))
	binary.LittleEndian.PutUint16(buf[4:], 2)
	binary.LittleEndian.PutUint64(buf[8:], frn)
	binary.LittleEndian.PutUint64(buf[16:], parent)
	binary.LittleEndian.PutUint64(buf[24:], uint64(usn))
	binary.LittleEndian.PutUint32(buf[40:], reason)
	binary.LittleEndian.PutUint32(buf[52:], attrs)
	binary.LittleEndian.PutUint16(buf[56:], uint16(nameLen))
	binary.LittleEndian.PutUint16(buf[58:], minRecordV2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(buf[minRecordV2+2*i:], u)
	}
	return buf
}

// buffer prepends the 8-byte header that Windows returns before the records.
func buffer(recs ...[]byte) []byte {
	out := make([]byte, 8)
	for _, r := range recs {
		out = append(out, r...)
	}
	return out
}

func TestParseRecords(t *testing.T) {
	buf := buffer(
		makeRecord(10, RootFRN, 100, ReasonFileCreate, 0x20, "a.txt"),
		makeRecord(11, RootFRN, 101, 0, fileAttributeDirectory, "docs"),
	)

	recs, err := ParseRecords(buf, 8)
	if err != nil {
		t.Fatalf("ParseRecords: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	if recs[0].FRN != 10 || recs[0].Parent != RootFRN || recs[0].Name != "a.txt" || recs[0].Dir {
		t.Fatalf("record 0 wrong: %+v", recs[0])
	}
	if recs[1].Name != "docs" || !recs[1].Dir || recs[1].USN != 101 {
		t.Fatalf("record 1 wrong: %+v", recs[1])
	}
}

func TestParseRecordsStripsReuseCounter(t *testing.T) {
	// Top 16 bits of a file reference are a sequence number; only the low
	// 48 bits identify the MFT record.
	frn := uint64(3)<<48 | 77
	recs, err := ParseRecords(buffer(makeRecord(frn, RootFRN, 1, 0, 0, "x")), 8)
	if err != nil || len(recs) != 1 {
		t.Fatalf("parse failed: %v", err)
	}
	if recs[0].FRN != 77 {
		t.Fatalf("FRN = %d, want 77", recs[0].FRN)
	}
}

func TestParseRecordsRejectsTruncatedBuffer(t *testing.T) {
	rec := makeRecord(10, RootFRN, 1, 0, 0, "abc")
	if _, err := ParseRecords(buffer(rec)[:len(rec)], 8); err == nil {
		t.Fatal("expected error for truncated record")
	}
}

func TestApplyCreateRenameDelete(t *testing.T) {
	tree := index.NewTree(`C:\`, RootFRN)
	const file = 10

	// Create.
	ApplyRecords(tree, mustParse(t, buffer(
		makeRecord(file, RootFRN, 1, ReasonFileCreate, 0x20, "a.txt"),
	)))
	if hits := tree.Search("a.txt", 5); len(hits) != 1 || hits[0].Path != `C:\a.txt` {
		t.Fatalf("created file not found: %+v", hits)
	}

	// Rename a.txt -> b.txt (old name record, then new name record).
	ApplyRecords(tree, mustParse(t, buffer(
		makeRecord(file, RootFRN, 2, 0x1000, 0x20, "a.txt"),
		makeRecord(file, RootFRN, 3, 0x2000, 0x20, "b.txt"),
	)))
	if hits := tree.Search("a.txt", 5); len(hits) != 0 {
		t.Fatalf("old name still present: %+v", hits)
	}
	if hits := tree.Search("b.txt", 5); len(hits) != 1 {
		t.Fatalf("renamed file not found: %+v", hits)
	}

	// Delete, followed by a late CLOSE for the same file. The CLOSE must not
	// bring the file back.
	ApplyRecords(tree, mustParse(t, buffer(
		makeRecord(file, RootFRN, 4, ReasonFileDelete|0x80000000, 0x20, "b.txt"),
		makeRecord(file, RootFRN, 5, 0x80000000, 0x20, "b.txt"),
	)))
	if tree.Len() != 0 {
		t.Fatalf("entries after delete = %d, want 0", tree.Len())
	}
}

func TestStoreRecordsSkipsSystemFiles(t *testing.T) {
	tree := index.NewTree(`C:\`, RootFRN)
	StoreRecords(tree, mustParse(t, buffer(
		makeRecord(RootFRN, RootFRN, 1, 0, fileAttributeDirectory, "."),
		makeRecord(6, RootFRN, 2, 0, 0, "$MFT"),
		makeRecord(7, RootFRN, 3, 0, 0, "report.pdf"),
	)))
	if tree.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (only report.pdf)", tree.Len())
	}
}

func mustParse(t *testing.T, buf []byte) []Record {
	t.Helper()
	recs, err := ParseRecords(buf, 8)
	if err != nil {
		t.Fatalf("ParseRecords: %v", err)
	}
	return recs
}
