//go:build windows

package hotkey

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procPeekMessageW       = user32.NewProc("PeekMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
	procSetWindowsHookExW  = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx     = user32.NewProc("CallNextHookEx")
	procGetAsyncKeyState   = user32.NewProc("GetAsyncKeyState")
	procKeybdEvent         = user32.NewProc("keybd_event")
	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
	procGetModuleHandleW   = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmHotkey   = 0x0312
	wmApp      = 0x8000
	pmNoRemove = 0x0000
	hotkeyID   = 1

	whKeyboardLL = 13
	whMouseLL    = 14

	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105

	wmLButtonDown = 0x0201
	wmRButtonDown = 0x0204
	wmMButtonDown = 0x0207
	wmMouseWheel  = 0x020A
	wmXButtonDown = 0x020B

	llkhfInjected    = 0x10
	keyEventKeyUp    = 0x0002
	vkMask           = 0xE8 // an unassigned key, used to stop Windows opening Start
	errHotkeyInUse   = syscall.Errno(1409)
	captureTimeLimit = 20 * time.Second
)

// msg matches the Win32 MSG structure.
type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	x, y    int32
}

// kbdllHook matches KBDLLHOOKSTRUCT.
type kbdllHook struct {
	VkCode    uint32
	ScanCode  uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type request struct {
	combo Combo
	reply chan error
}

// Manager owns one global hotkey. Its message loop runs on a dedicated OS
// thread, because WM_HOTKEY and the keyboard hook are delivered to the thread
// that installed them.
type Manager struct {
	onPress  func()
	threadID uint32

	mu           sync.Mutex
	pending      *request
	tracker      Tracker
	captureFn    func(CaptureEvent)
	captureUntil time.Time
	kbHook       uintptr
	mouseHook    uintptr
}

// There is one manager per process; the hook callbacks find it here.
var hookOwner *Manager

var (
	keyboardCallback = syscall.NewCallback(keyboardProc)
	mouseCallback    = syscall.NewCallback(mouseProc)
)

func keyboardProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) == 0 && hookOwner != nil && lParam != 0 {
		info := (*kbdllHook)(unsafe.Pointer(lParam))
		if hookOwner.handleKey(uint32(wParam), info) {
			return 1 // swallowed
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

func mouseProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) == 0 && hookOwner != nil {
		switch uint32(wParam) {
		case wmLButtonDown, wmRButtonDown, wmMButtonDown, wmXButtonDown, wmMouseWheel:
			hookOwner.mu.Lock()
			hookOwner.tracker.Other()
			hookOwner.mu.Unlock()
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

// NewManager starts the message loop. onPress runs on its own goroutine each
// time the hotkey is pressed.
func NewManager(onPress func()) (*Manager, error) {
	m := &Manager{onPress: onPress}
	hookOwner = m
	ready := make(chan struct{})
	go m.loop(ready)
	<-ready
	return m, nil
}

// Set replaces the current hotkey. It returns an error if the combination is
// reserved by Windows or already used by another program; the previous hotkey
// then stays active.
func (m *Manager) Set(c Combo) error {
	reply := make(chan error, 1)

	m.mu.Lock()
	m.pending = &request{combo: c, reply: reply}
	m.mu.Unlock()

	// Wake the message loop so it processes the request.
	r, _, err := procPostThreadMessageW.Call(uintptr(m.threadID), wmApp, 0, 0)
	if r == 0 {
		return fmt.Errorf("wake hotkey thread: %w", err)
	}
	return <-reply
}

// StartCapture begins recording a new shortcut. fn is called from the
// keyboard hook for every change: keep it fast (post a message, do not block).
// While recording, key presses are swallowed so Windows and other programs do
// not react to them. Recording ends by itself after a few seconds.
func (m *Manager) StartCapture(fn func(CaptureEvent)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.kbHook == 0 {
		return errors.New("the keyboard hook could not be installed, so shortcuts cannot be recorded")
	}
	m.captureFn = fn
	m.captureUntil = time.Now().Add(captureTimeLimit)
	m.tracker.StartCapture()
	return nil
}

// StopCapture ends recording.
func (m *Manager) StopCapture() {
	m.mu.Lock()
	m.tracker.StopCapture()
	m.captureFn = nil
	m.mu.Unlock()
}

func (m *Manager) isCapturing() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tracker.Active()
}

func asyncKeyDown(vk uint32) bool {
	s, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return int16(s) < 0
}

// handleKey runs inside the keyboard hook. It reports whether to swallow the key.
func (m *Manager) handleKey(msg uint32, k *kbdllHook) bool {
	if k.Flags&llkhfInjected != 0 {
		return false // our own neutral key, or another program's
	}
	var down bool
	switch msg {
	case wmKeyDown, wmSysKeyDown:
		down = true
	case wmKeyUp, wmSysKeyUp:
	default:
		return false
	}

	var timedOut func(CaptureEvent)
	m.mu.Lock()
	if m.tracker.Capturing() && time.Now().After(m.captureUntil) {
		m.tracker.StopCapture()
		timedOut = m.captureFn
		m.captureFn = nil
	}
	// A modifier release can be missed (for example while the PC was locked),
	// so check the real keyboard before trusting what is remembered.
	for _, vk := range []uint32{0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0x5B, 0x5C} {
		if vk != k.VkCode && m.tracker.IsDown(vk) && !asyncKeyDown(vk) {
			m.tracker.Clear(vk)
		}
	}
	res := m.tracker.Key(k.VkCode, down)
	fn := m.captureFn
	m.mu.Unlock()

	if timedOut != nil {
		timedOut(CaptureEvent{Kind: CaptureCancel})
	}
	if res.Event != nil && fn != nil {
		fn(*res.Event)
	}
	if res.Mask {
		procKeybdEvent.Call(vkMask, 0, 0, 0)
		procKeybdEvent.Call(vkMask, 0, keyEventKeyUp, 0)
	}
	if res.Trigger {
		go m.onPress()
	}
	return res.Swallow
}

func (m *Manager) loop(ready chan<- struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	id, _, _ := procGetCurrentThreadId.Call()
	m.threadID = uint32(id)

	// Create this thread's message queue before anyone posts to it.
	var peek msg
	procPeekMessageW.Call(uintptr(unsafe.Pointer(&peek)), 0, 0, 0, pmNoRemove)

	// The hooks are optional: without them only normal shortcuts work.
	module, _, _ := procGetModuleHandleW.Call(0)
	m.kbHook, _, _ = procSetWindowsHookExW.Call(whKeyboardLL, keyboardCallback, module, 0)
	m.mouseHook, _, _ = procSetWindowsHookExW.Call(whMouseLL, mouseCallback, module, 0)
	close(ready)

	var current *Combo
	for {
		var in msg
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0)
		if int32(r) <= 0 { // error or WM_QUIT
			return
		}

		switch in.message {
		case wmApp:
			m.mu.Lock()
			req := m.pending
			m.pending = nil
			m.mu.Unlock()
			if req != nil {
				current = m.apply(current, req)
			}
		case wmHotkey:
			if !m.isCapturing() {
				go m.onPress()
			}
		}
	}
}

func (m *Manager) setTarget(mods uint32) {
	m.mu.Lock()
	m.tracker.SetTarget(mods)
	m.mu.Unlock()
}

// apply switches to the requested hotkey. It returns the combo now in effect.
func (m *Manager) apply(previous *Combo, req *request) *Combo {
	c := req.combo

	if why := Reserved(c); why != "" {
		req.reply <- fmt.Errorf("%s is reserved by Windows (%s). Choose another combination.", c, why)
		return previous
	}

	previousRegistered := previous != nil && !previous.ModifierOnly()

	if c.ModifierOnly() {
		if m.kbHook == 0 {
			req.reply <- errors.New("a modifier-only hotkey needs the keyboard hook, which could not be installed")
			return previous
		}
		if previousRegistered {
			procUnregisterHotKey.Call(0, hotkeyID)
		}
		m.setTarget(c.Modifiers)
		req.reply <- nil
		return &c
	}

	if previousRegistered {
		procUnregisterHotKey.Call(0, hotkeyID)
	}
	r, _, err := procRegisterHotKey.Call(0, hotkeyID,
		uintptr(c.RegisterFlags()), uintptr(c.VirtualKey))
	if r == 0 {
		// Put the previous hotkey back so the user is not left without one.
		if previousRegistered {
			procRegisterHotKey.Call(0, hotkeyID,
				uintptr(previous.RegisterFlags()), uintptr(previous.VirtualKey))
		}
		if errors.Is(err, errHotkeyInUse) {
			req.reply <- fmt.Errorf("%s is already used by Windows or another app. Choose another combination.", c)
		} else {
			req.reply <- fmt.Errorf("Windows refused %s: %v", c, err)
		}
		return previous
	}

	m.setTarget(0) // a normal hotkey replaces any modifier-only one
	req.reply <- nil
	return &c
}
