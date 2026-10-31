//go:build windows

package ntfs

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"go-file-search/internal/index"
)

const (
	fsctlQueryUsnJournal uint32 = 0x000900f4
	fsctlEnumUsnData     uint32 = 0x000900b3
	fsctlReadUsnJournal  uint32 = 0x000900bb

	errHandleEOF        windows.Errno = 38
	errJournalNotActive windows.Errno = 1179
	errJournalDeleted   windows.Errno = 1181

	ioBufferSize = 1 << 20
	reasonAll    = 0xFFFFFFFF
)

type journal struct {
	id             uint64
	nextUSN        int64
	lowestValidUSN int64
}

// Volume keeps the index of one NTFS volume current. Reads of the index never
// wait on journal processing; only the swap of a rebuilt tree takes a lock.
type Volume struct {
	Root string

	treeMu sync.RWMutex
	tree   *index.Tree

	// applyMu serializes journal updates, rebuilds and snapshots, so a
	// snapshot always sees a tree that matches its journal position.
	applyMu   sync.Mutex
	journalID uint64
	nextUSN   int64
}

// NewVolume returns an empty volume. Call Run to fill it.
func NewVolume(root string) *Volume {
	return &Volume{Root: root, tree: index.NewTree(root, RootFRN)}
}

// Tree returns the current index. The pointer changes after a rebuild, so
// callers should call Tree again rather than keep the result.
func (v *Volume) Tree() *index.Tree {
	v.treeMu.RLock()
	defer v.treeMu.RUnlock()
	return v.tree
}

func (v *Volume) setTree(t *index.Tree) {
	v.treeMu.Lock()
	v.tree = t
	v.treeMu.Unlock()
}

// Restore loads a snapshot. Run then brings it up to date from the journal.
func (v *Volume) Restore(s index.VolumeState) {
	v.applyMu.Lock()
	defer v.applyMu.Unlock()
	v.setTree(s.Tree)
	v.journalID = s.JournalID
	v.nextUSN = s.NextUSN
}

// Snapshot locks the volume and returns its state. The caller must call the
// returned function when it is done writing the state.
func (v *Volume) Snapshot() (index.VolumeState, func()) {
	v.applyMu.Lock()
	state := index.VolumeState{
		Root:      v.Root,
		RootID:    RootFRN,
		JournalID: v.journalID,
		NextUSN:   v.nextUSN,
		Tree:      v.Tree(),
	}
	return state, v.applyMu.Unlock
}

// Run opens the volume, brings the index up to date, calls onReady, and then
// applies journal changes every poll interval until ctx is done.
//
// It returns an error only if the volume cannot be used at all (for example,
// no administrator rights or no active journal). The caller can then fall back
// to a folder walk.
func (v *Volume) Run(ctx context.Context, poll time.Duration, onReady func(), logf func(string, ...any)) error {
	h, err := openVolume(v.Root)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)

	if err := v.catchUp(h); err != nil {
		return err
	}
	onReady()

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := v.catchUp(h); err != nil {
				logf("[%s] change tracking error: %v", v.Root, err)
			}
		}
	}
}

// catchUp rebuilds the index if the journal has been reset or does not match
// our position, and otherwise applies everything since our last position.
func (v *Volume) catchUp(h windows.Handle) error {
	j, err := queryJournal(h)
	if err != nil {
		return err
	}

	stale := v.journalID != j.id || v.nextUSN < j.lowestValidUSN || v.nextUSN > j.nextUSN
	if stale {
		return v.rebuild(h)
	}

	err = v.sync(h)
	if err == ErrJournalReset {
		return v.rebuild(h)
	}
	return err
}

// rebuild reads the whole MFT into a new tree and swaps it in. The journal
// position is taken before reading the MFT, and changes made during the read
// are re-applied afterwards. Applying them twice is harmless.
func (v *Volume) rebuild(h windows.Handle) error {
	j, err := queryJournal(h)
	if err != nil {
		return err
	}

	t := index.NewTree(v.Root, RootFRN)
	if err := enumerate(h, func(recs []Record) { StoreRecords(t, recs) }); err != nil {
		return err
	}

	v.applyMu.Lock()
	v.setTree(t)
	v.journalID = j.id
	v.nextUSN = j.nextUSN
	v.applyMu.Unlock()

	return v.sync(h)
}

// sync applies journal records after the current position.
func (v *Volume) sync(h windows.Handle) error {
	v.applyMu.Lock()
	defer v.applyMu.Unlock()

	next, err := readJournal(h, v.journalID, v.nextUSN, func(recs []Record) {
		ApplyRecords(v.tree, recs)
	})
	v.nextUSN = next
	return err
}

func openVolume(root string) (windows.Handle, error) {
	path := `\\.\` + trimSlash(root)
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	h, err := windows.CreateFile(
		p,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return 0, fmt.Errorf("open volume %s (run as administrator): %w", root, err)
	}
	return h, nil
}

func trimSlash(root string) string {
	if n := len(root); n > 0 && root[n-1] == '\\' {
		return root[:n-1]
	}
	return root
}

func queryJournal(h windows.Handle) (journal, error) {
	var out [56]byte
	var returned uint32

	err := windows.DeviceIoControl(h, fsctlQueryUsnJournal, nil, 0, &out[0], uint32(len(out)), &returned, nil)
	if err == errJournalNotActive {
		return journal{}, fmt.Errorf("NTFS change journal is not active on this volume (enable it with: fsutil usn createjournal m=33554432 a=8388608 <drive>)")
	}
	if err != nil {
		return journal{}, fmt.Errorf("query USN journal: %w", err)
	}

	return journal{
		id:             binary.LittleEndian.Uint64(out[0:]),
		nextUSN:        int64(binary.LittleEndian.Uint64(out[16:])),
		lowestValidUSN: int64(binary.LittleEndian.Uint64(out[24:])),
	}, nil
}

// enumerate reads every MFT record on the volume in buffer-sized batches.
func enumerate(h windows.Handle, emit func([]Record)) error {
	in := make([]byte, 24) // MFT_ENUM_DATA_V0: StartFileReferenceNumber, LowUsn, HighUsn
	binary.LittleEndian.PutUint64(in[16:], math.MaxInt64)

	buf := make([]byte, ioBufferSize)
	for {
		var returned uint32
		err := windows.DeviceIoControl(h, fsctlEnumUsnData, &in[0], uint32(len(in)), &buf[0], uint32(len(buf)), &returned, nil)
		if err == errHandleEOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("enumerate MFT: %w", err)
		}
		if returned < 8 {
			return nil
		}

		recs, perr := ParseRecords(buf[:returned], 8)
		if perr != nil {
			return perr
		}
		if len(recs) > 0 {
			emit(recs)
		}

		copy(in[0:8], buf[0:8]) // next file reference to start from
	}
}

// readJournal returns records after startUSN and the position to resume from.
// It does not wait for new records, so it returns quickly when there are none.
func readJournal(h windows.Handle, journalID uint64, startUSN int64, emit func([]Record)) (int64, error) {
	in := make([]byte, 40) // READ_USN_JOURNAL_DATA_V0
	binary.LittleEndian.PutUint64(in[0:], uint64(startUSN))
	binary.LittleEndian.PutUint32(in[8:], reasonAll)
	binary.LittleEndian.PutUint64(in[32:], journalID)

	buf := make([]byte, ioBufferSize)
	next := startUSN
	for {
		var returned uint32
		err := windows.DeviceIoControl(h, fsctlReadUsnJournal, &in[0], uint32(len(in)), &buf[0], uint32(len(buf)), &returned, nil)
		if err == errJournalDeleted {
			return next, ErrJournalReset
		}
		if err != nil {
			return next, fmt.Errorf("read USN journal: %w", err)
		}
		if returned < 8 {
			return next, nil
		}

		recs, perr := ParseRecords(buf[:returned], 8)
		if perr != nil {
			return next, perr
		}
		if len(recs) > 0 {
			emit(recs)
		}

		next = int64(binary.LittleEndian.Uint64(buf[0:8]))
		if returned == 8 {
			return next, nil
		}
		copy(in[0:8], buf[0:8])
	}
}
