// Package hotkey turns user-chosen keys into global hotkeys. This file is
// platform-independent so it can be tested anywhere: it holds the key names,
// the checks for combinations Windows keeps for itself, and the Tracker that
// follows raw keyboard events (used for modifier-only hotkeys such as the
// Windows key on its own, and for recording a new shortcut).
package hotkey

import (
	"errors"
	"fmt"
	"strings"
)

// Win32 modifier flags, as used by RegisterHotKey.
const (
	modAlt   uint32 = 0x0001
	modCtrl  uint32 = 0x0002
	modShift uint32 = 0x0004
	modWin   uint32 = 0x0008
	modNoRep uint32 = 0x4000 // don't repeat while the key is held
)

// The same flags for callers outside this package.
const (
	ModAlt   = modAlt
	ModCtrl  = modCtrl
	ModShift = modShift
	ModWin   = modWin
)

// Combo is a parsed hotkey. VirtualKey is 0 for a modifier-only hotkey, such
// as the Windows key on its own.
type Combo struct {
	Modifiers  uint32
	VirtualKey uint32
}

// ModifierOnly reports whether the hotkey has no key besides its modifiers.
func (c Combo) ModifierOnly() bool { return c.VirtualKey == 0 }

// String shows the combo the way people write it, like "Ctrl + Space".
func (c Combo) String() string {
	var parts []string
	for _, m := range modifierOrder {
		if c.Modifiers&m.flag != 0 {
			parts = append(parts, m.label)
		}
	}
	if c.VirtualKey != 0 {
		if name, ok := KeyName(c.VirtualKey); ok {
			parts = append(parts, name)
		} else {
			parts = append(parts, fmt.Sprintf("key %#x", c.VirtualKey))
		}
	}
	return strings.Join(parts, " + ")
}

// RegisterFlags returns the flags to pass to RegisterHotKey.
func (c Combo) RegisterFlags() uint32 {
	return c.Modifiers | modNoRep
}

var modifierOrder = []struct {
	name  string
	label string
	flag  uint32
}{
	{"ctrl", "Ctrl", modCtrl},
	{"alt", "Alt", modAlt},
	{"shift", "Shift", modShift},
	{"win", "Win", modWin},
}

// Spec is the user-facing form, as stored in settings.
type Spec struct {
	Modifiers []string `json:"modifiers"` // any of: ctrl, alt, shift, win
	Key       string   `json:"key"`       // empty for modifier-only, such as Win on its own
}

// ErrNeedsModifier is returned for a key pressed without Ctrl, Alt, Shift or Win.
var ErrNeedsModifier = errors.New("a hotkey needs Ctrl, Alt, Shift or Win (only function keys can be used on their own)")

// Parse validates a Spec and converts it to a Combo.
func Parse(s Spec) (Combo, error) {
	var c Combo

	for _, m := range s.Modifiers {
		found := false
		for _, def := range modifierOrder {
			if strings.EqualFold(m, def.name) {
				c.Modifiers |= def.flag
				found = true
			}
		}
		if !found {
			return Combo{}, fmt.Errorf("unknown modifier %q", m)
		}
	}

	key := strings.TrimSpace(s.Key)
	if key == "" {
		if c.Modifiers == 0 {
			return Combo{}, ErrNeedsModifier
		}
		return c, nil // modifier-only, for example the Windows key
	}

	vk, err := virtualKey(key)
	if err != nil {
		return Combo{}, err
	}
	c.VirtualKey = vk
	if c.Modifiers == 0 && !IsFunctionKey(vk) {
		return Combo{}, ErrNeedsModifier
	}
	return c, nil
}

// SpecFrom builds a Spec from modifier flags and a virtual-key code, as
// recorded by the capture mode. vk may be 0 for a modifier-only hotkey.
func SpecFrom(mods, vk uint32) (Spec, bool) {
	var s Spec
	for _, m := range modifierOrder {
		if mods&m.flag != 0 {
			s.Modifiers = append(s.Modifiers, m.name)
		}
	}
	if vk != 0 {
		name, ok := KeyName(vk)
		if !ok {
			return Spec{}, false
		}
		s.Key = name
	}
	return s, true
}

// Parts lists the pieces of a hotkey in display order: modifiers, then the
// key. For example {"Ctrl", "Space"} or {"Win"}.
func Parts(s Spec) []string {
	var parts []string
	for _, m := range modifierOrder {
		for _, have := range s.Modifiers {
			if strings.EqualFold(have, m.name) {
				parts = append(parts, m.label)
				break
			}
		}
	}
	if k := strings.TrimSpace(s.Key); k != "" {
		parts = append(parts, k)
	}
	return parts
}

// Format shows a Spec as text, like "Ctrl + Space".
func Format(s Spec) string { return strings.Join(Parts(s), " + ") }

// IsFunctionKey reports whether vk is F1 to F24.
func IsFunctionKey(vk uint32) bool { return vk >= 0x70 && vk <= 0x87 }

type keyDef struct {
	name string
	vk   uint32
}

// namedKeys are the keys that are not letters, digits or function keys.
var namedKeys = []keyDef{
	{"Space", 0x20}, {"Enter", 0x0D}, {"Tab", 0x09}, {"Backspace", 0x08}, {"Esc", 0x1B},
	{"Insert", 0x2D}, {"Delete", 0x2E}, {"Home", 0x24}, {"End", 0x23},
	{"PageUp", 0x21}, {"PageDown", 0x22},
	{"Left", 0x25}, {"Up", 0x26}, {"Right", 0x27}, {"Down", 0x28},
	{";", 0xBA}, {"=", 0xBB}, {",", 0xBC}, {"-", 0xBD}, {".", 0xBE}, {"/", 0xBF},
	{"`", 0xC0}, {"[", 0xDB}, {"\\", 0xDC}, {"]", 0xDD}, {"'", 0xDE},
}

func virtualKey(key string) (uint32, error) {
	k := strings.ToUpper(strings.TrimSpace(key))

	switch {
	case len(k) == 1 && k[0] >= 'A' && k[0] <= 'Z':
		return uint32(k[0]), nil // VK_A..VK_Z match ASCII
	case len(k) == 1 && k[0] >= '0' && k[0] <= '9':
		return uint32(k[0]), nil // VK_0..VK_9 match ASCII
	case strings.HasPrefix(k, "F"):
		var n int
		if _, err := fmt.Sscanf(k, "F%d", &n); err == nil && n >= 1 && n <= 24 {
			return uint32(0x70 + n - 1), nil // VK_F1 = 0x70
		}
	}

	for _, d := range namedKeys {
		if strings.EqualFold(d.name, k) {
			return d.vk, nil
		}
	}
	return 0, fmt.Errorf("key %q cannot be used as a hotkey", key)
}

// KeyName converts a virtual-key code into the name stored in settings.
func KeyName(vk uint32) (string, bool) {
	switch {
	case vk >= 0x41 && vk <= 0x5A, vk >= 0x30 && vk <= 0x39:
		return string(rune(vk)), true
	case IsFunctionKey(vk):
		return fmt.Sprintf("F%d", vk-0x70+1), true
	}
	for _, d := range namedKeys {
		if d.vk == vk {
			return d.name, true
		}
	}
	return "", false
}

// reserved lists combinations that Windows handles before any app sees them.
// The text finishes the sentence "<combo> is reserved by Windows (...)".
var reserved = map[Combo]string{
	{modCtrl | modAlt, 0x2E}: "it opens the security screen",
	{modCtrl | modShift, 0x1B}: "it opens Task Manager",
	{modCtrl, 0x1B}:          "it opens the Start menu",
	{modAlt, 0x09}:           "it switches between windows",
	{modAlt, 0x1B}:           "it cycles through windows",
	{modAlt, 0x73}:           "it closes the window",
	{modWin, 'L'}:            "it locks the computer",
	{modWin, 'D'}:            "it shows the desktop",
	{modWin, 'E'}:            "it opens File Explorer",
	{modWin, 'R'}:            "it opens Run",
	{modWin, 'M'}:            "it minimizes all windows",
	{modWin, 0x09}:          "it opens Task View",
	{modWin, 0x20}:          "it switches the input language",
}

// Reserved returns why Windows keeps a combination for itself, or "" if it is
// not one of the well-known reserved ones. Windows can still refuse other
// combinations, for example when another app already uses them.
func Reserved(c Combo) string {
	return reserved[c]
}

// ---- keyboard tracking -------------------------------------------------

// ModifierFor returns the modifier flag a virtual key stands for, or 0 if it
// is not a modifier key.
func ModifierFor(vk uint32) uint32 {
	switch vk {
	case 0x10, 0xA0, 0xA1:
		return modShift
	case 0x11, 0xA2, 0xA3:
		return modCtrl
	case 0x12, 0xA4, 0xA5:
		return modAlt
	case 0x5B, 0x5C:
		return modWin
	}
	return 0
}

// CaptureKind says what a CaptureEvent reports.
type CaptureKind int

const (
	CaptureHeld   CaptureKind = iota // the modifiers now held down (for a live preview)
	CaptureDone                      // a complete combination was pressed
	CaptureCancel                    // Esc on its own
)

// CaptureEvent is sent while recording a new shortcut.
type CaptureEvent struct {
	Kind       CaptureKind
	Modifiers  uint32
	VirtualKey uint32 // 0 when the shortcut is modifiers only
}

// Result tells the caller what to do with one keyboard event.
type Result struct {
	Swallow bool          // do not pass the event on to other programs
	Trigger bool          // the modifier-only hotkey was pressed and released
	Mask    bool          // send a neutral key press now, so Windows does not open Start or a menu
	Event   *CaptureEvent // set while recording
}

// Tracker follows keyboard events. It has no operating-system calls, so its
// behaviour can be tested directly.
//
// A modifier-only hotkey fires when its modifiers are pressed together and
// released again without any other key or mouse button in between. So the
// Windows key on its own opens search, but Win + E still opens Explorer.
type Tracker struct {
	down      [256]bool
	chordMods uint32 // every modifier held since the chord began
	chordKey  bool   // a key or mouse button was used during the chord
	armed     bool   // the modifier-only hotkey's modifiers are held, and nothing else
	target    uint32 // modifier-only hotkey, or 0
	capturing bool
	draining  bool // after a capture: keep swallowing until every key is released
}

// SetTarget sets the modifier-only hotkey to watch for (0 for none).
func (t *Tracker) SetTarget(mods uint32) {
	t.target = mods
	t.armed = false
}

// StartCapture begins recording a shortcut. Events are swallowed until the
// shortcut is complete.
func (t *Tracker) StartCapture() {
	t.capturing = true
	t.draining = false
	t.chordMods = 0
	t.chordKey = false
	t.armed = false
}

// StopCapture ends recording without a result.
func (t *Tracker) StopCapture() {
	t.capturing = false
	t.draining = false
}

// Active reports whether recording is under way or still being wound down.
func (t *Tracker) Active() bool { return t.capturing || t.draining }

// Capturing reports whether recording is waiting for a shortcut.
func (t *Tracker) Capturing() bool { return t.capturing }

// IsDown reports whether the tracker believes vk is held down.
func (t *Tracker) IsDown(vk uint32) bool { return vk < 256 && t.down[vk] }

// Clear forgets that vk is held down. It is used when the real keyboard state
// shows the tracker missed a key release.
func (t *Tracker) Clear(vk uint32) {
	if vk < 256 {
		t.down[vk] = false
	}
}

// Other records a mouse click or similar: it cancels a pending modifier-only
// hotkey, so Ctrl + click does not open search.
func (t *Tracker) Other() {
	if t.capturing {
		return
	}
	t.chordKey = true
	t.armed = false
}

func (t *Tracker) held() uint32 {
	var mods uint32
	for vk := uint32(0); vk < 256; vk++ {
		if t.down[vk] {
			mods |= ModifierFor(vk)
		}
	}
	return mods
}

func (t *Tracker) anyDown() bool {
	for _, d := range t.down {
		if d {
			return true
		}
	}
	return false
}

// Key processes one key press (down) or release (!down) of virtual key vk.
func (t *Tracker) Key(vk uint32, down bool) Result {
	var res Result
	if vk >= 256 {
		return res
	}
	res.Swallow = t.Active()
	t.down[vk] = down
	held := t.held()

	if flag := ModifierFor(vk); flag != 0 {
		if down {
			t.chordMods |= held
			// Windows opens Start when the Win key is released, unless another
			// key went down while it was held. Send that neutral key now, on
			// press, so the release is never a lone Win press. Doing it on
			// release was a race and failed about half the time.
			if !t.capturing && t.target&(modWin|modAlt) != 0 {
				res.Mask = true
			}
			if t.capturing {
				res.Event = &CaptureEvent{Kind: CaptureHeld, Modifiers: held}
			} else if t.target != 0 {
				switch {
				case !t.chordKey && held == t.target:
					t.armed = true
				case held&^t.target != 0:
					t.armed = false
				}
			}
		} else {
			switch {
			case t.capturing && !t.chordKey && t.chordMods != 0:
				res.Event = &CaptureEvent{Kind: CaptureDone, Modifiers: t.chordMods}
				t.capturing = false
				t.draining = true
			case t.capturing:
				res.Event = &CaptureEvent{Kind: CaptureHeld, Modifiers: held}
			case t.armed:
				t.armed = false
				res.Trigger = true
			}
		}
	} else if down {
		if t.capturing {
			if vk == 0x1B && held == 0 {
				res.Event = &CaptureEvent{Kind: CaptureCancel}
			} else {
				res.Event = &CaptureEvent{Kind: CaptureDone, Modifiers: held, VirtualKey: vk}
			}
			t.capturing = false
			t.draining = true
		} else {
			t.chordKey = true
			t.armed = false
		}
	}

	if !down {
		if held == 0 {
			t.chordMods = 0
			t.chordKey = false
			t.armed = false
		}
		if t.draining && !t.capturing && !t.anyDown() {
			t.draining = false
		}
	}
	return res
}
