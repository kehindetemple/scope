package index

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const snapshotMagic = "GFSNAP01"

// Sanity limits so a corrupt snapshot cannot force huge allocations.
const (
	maxVolumes     = 64
	maxRootLen     = 1024
	maxNameLen     = 4096
	maxEntriesEach = 1 << 26
)

// ErrCorruptSnapshot is returned when a snapshot file cannot be parsed.
var ErrCorruptSnapshot = errors.New("snapshot file is corrupt")

// VolumeState is the persisted state of one volume.
type VolumeState struct {
	Root      string
	RootID    uint64
	JournalID uint64 // USN journal the NextUSN position belongs to
	NextUSN   int64  // next journal position to read after this snapshot
	Tree      *Tree
}

// WriteSnapshot writes all volumes to path. The file is written to a temporary
// name and renamed, so an interrupted write never replaces a good snapshot.
// The caller must make sure the trees are not modified during the write.
func WriteSnapshot(path string, vols []VolumeState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	enc := &encoder{w: bufio.NewWriterSize(f, 1<<20)}
	enc.write([]byte(snapshotMagic))
	enc.u32(uint32(len(vols)))

	for _, v := range vols {
		count := v.Tree.Len()

		enc.str(v.Root)
		enc.u64(v.RootID)
		enc.u64(v.JournalID)
		enc.u64(uint64(v.NextUSN))
		enc.u64(uint64(count))

		var written int
		err := v.Tree.Range(func(e Entry) error {
			var flags byte
			if e.Dir {
				flags = 1
			}
			enc.u64(e.ID)
			enc.u64(e.Parent)
			enc.byte(flags)
			enc.str(e.Name)
			written++
			return enc.err
		})
		if err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
		if written != count {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("snapshot %s: wrote %d entries, expected %d", v.Root, written, count)
		}
	}

	if enc.err == nil {
		enc.err = enc.w.Flush()
	}
	if enc.err == nil {
		enc.err = f.Sync()
	}
	closeErr := f.Close()
	if enc.err == nil {
		enc.err = closeErr
	}
	if enc.err != nil {
		os.Remove(tmp)
		return enc.err
	}

	return os.Rename(tmp, path)
}

// ReadSnapshot loads volumes from path. A missing file is not an error; it
// returns nil volumes.
func ReadSnapshot(path string) ([]VolumeState, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec := &decoder{r: bufio.NewReaderSize(f, 1<<20)}

	magic := make([]byte, len(snapshotMagic))
	dec.full(magic)
	if dec.err != nil || string(magic) != snapshotMagic {
		return nil, ErrCorruptSnapshot
	}

	count := dec.u32()
	if dec.err == nil && count > maxVolumes {
		return nil, ErrCorruptSnapshot
	}

	var vols []VolumeState
	for i := uint32(0); i < count && dec.err == nil; i++ {
		v := VolumeState{}
		v.Root = dec.str(maxRootLen)
		v.RootID = dec.u64()
		v.JournalID = dec.u64()
		v.NextUSN = int64(dec.u64())
		n := dec.u64()
		if dec.err == nil && n > maxEntriesEach {
			return nil, ErrCorruptSnapshot
		}

		t := NewTreeSized(v.Root, v.RootID, int(n))
		for j := uint64(0); j < n && dec.err == nil; j++ {
			id := dec.u64()
			parent := dec.u64()
			flags := dec.byte()
			name := dec.str(maxNameLen)
			if dec.err == nil {
				t.Update(func(w *Writer) {
					w.Put(Entry{ID: id, Parent: parent, Name: name, Dir: flags&1 != 0})
				})
			}
		}
		v.Tree = t
		vols = append(vols, v)
	}

	if dec.err != nil {
		return nil, fmt.Errorf("read snapshot: %w", dec.err)
	}
	return vols, nil
}

type encoder struct {
	w   *bufio.Writer
	err error
	buf [8]byte
}

func (e *encoder) write(p []byte) {
	if e.err == nil {
		_, e.err = e.w.Write(p)
	}
}

func (e *encoder) u32(v uint32) {
	binary.LittleEndian.PutUint32(e.buf[:4], v)
	e.write(e.buf[:4])
}

func (e *encoder) u64(v uint64) {
	binary.LittleEndian.PutUint64(e.buf[:8], v)
	e.write(e.buf[:8])
}

func (e *encoder) byte(v byte) {
	e.buf[0] = v
	e.write(e.buf[:1])
}

func (e *encoder) str(s string) {
	e.u32(uint32(len(s)))
	e.write([]byte(s))
}

type decoder struct {
	r   *bufio.Reader
	err error
	buf [8]byte
}

func (d *decoder) full(p []byte) {
	if d.err == nil {
		_, d.err = io.ReadFull(d.r, p)
	}
}

func (d *decoder) u32() uint32 {
	d.full(d.buf[:4])
	return binary.LittleEndian.Uint32(d.buf[:4])
}

func (d *decoder) u64() uint64 {
	d.full(d.buf[:8])
	return binary.LittleEndian.Uint64(d.buf[:8])
}

func (d *decoder) byte() byte {
	d.full(d.buf[:1])
	return d.buf[0]
}

func (d *decoder) str(max uint32) string {
	n := d.u32()
	if d.err == nil && n > max {
		d.err = ErrCorruptSnapshot
		return ""
	}
	p := make([]byte, n)
	d.full(p)
	return string(p)
}
