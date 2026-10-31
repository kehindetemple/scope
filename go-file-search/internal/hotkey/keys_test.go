package hotkey

import (
	"errors"
	"testing"
)

func TestParseCtrlSpace(t *testing.T) {
	c, err := Parse(Spec{Modifiers: []string{"ctrl"}, Key: "Space"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Modifiers != modCtrl || c.VirtualKey != 0x20 {
		t.Fatalf("got %+v", c)
	}
}

func TestParseCombinesModifiersAndKeys(t *testing.T) {
	c, err := Parse(Spec{Modifiers: []string{"Ctrl", "Shift", "win"}, Key: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Modifiers != modCtrl|modShift|modWin || c.VirtualKey != 'K' {
		t.Fatalf("got %+v", c)
	}
}

func TestParseFunctionKeys(t *testing.T) {
	c, err := Parse(Spec{Modifiers: []string{"alt"}, Key: "F12"})
	if err != nil {
		t.Fatal(err)
	}
	if c.VirtualKey != 0x7B {
		t.Fatalf("F12 = %#x, want 0x7B", c.VirtualKey)
	}
	if c, err := Parse(Spec{Key: "F24"}); err != nil || c.VirtualKey != 0x87 {
		t.Fatalf("F24 on its own: %+v, %v", c, err)
	}
}

func TestParseRejectsKeyWithoutModifier(t *testing.T) {
	_, err := Parse(Spec{Key: "Space"})
	if !errors.Is(err, ErrNeedsModifier) {
		t.Fatalf("err = %v, want ErrNeedsModifier", err)
	}
	if _, err := Parse(Spec{}); !errors.Is(err, ErrNeedsModifier) {
		t.Fatalf("empty spec: err = %v, want ErrNeedsModifier", err)
	}
}

func TestParseAllowsModifierOnly(t *testing.T) {
	c, err := Parse(Spec{Modifiers: []string{"win"}})
	if err != nil {
		t.Fatal(err)
	}
	if !c.ModifierOnly() || c.Modifiers != modWin {
		t.Fatalf("got %+v", c)
	}
	if c.String() != "Win" {
		t.Fatalf("String = %q", c.String())
	}
}

func TestParseRejectsUnknownKey(t *testing.T) {
	if _, err := Parse(Spec{Modifiers: []string{"ctrl"}, Key: "Escape"}); err == nil {
		t.Fatal("expected error for unsupported key")
	}
}

func TestParseRejectsUnknownModifier(t *testing.T) {
	if _, err := Parse(Spec{Modifiers: []string{"meta"}, Key: "A"}); err == nil {
		t.Fatal("expected error for unknown modifier")
	}
}

func TestKeyNameRoundTrip(t *testing.T) {
	for _, name := range []string{"A", "Z", "0", "9", "F1", "F24", "Space", "Enter", "Esc", "PageDown", ";", "`", "\\"} {
		vk, err := virtualKey(name)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		got, ok := KeyName(vk)
		if !ok || got != name {
			t.Fatalf("%q -> %#x -> %q (%v)", name, vk, got, ok)
		}
	}
}

func TestSpecFromAndFormat(t *testing.T) {
	s, ok := SpecFrom(modCtrl|modWin, 'K')
	if !ok || Format(s) != "Ctrl + Win + K" {
		t.Fatalf("got %+v %q %v", s, Format(s), ok)
	}
	s, ok = SpecFrom(modWin, 0)
	if !ok || Format(s) != "Win" {
		t.Fatalf("got %+v %q %v", s, Format(s), ok)
	}
	if _, ok := SpecFrom(modCtrl, 0xFF); ok {
		t.Fatal("unknown key should not convert")
	}
}

func TestReserved(t *testing.T) {
	if Reserved(Combo{modWin, 'L'}) == "" {
		t.Fatal("Win+L should be reserved")
	}
	if Reserved(Combo{modCtrl, 0x20}) != "" {
		t.Fatal("Ctrl+Space should not be reserved")
	}
	if Reserved(Combo{modWin, 0}) != "" {
		t.Fatal("the Windows key on its own is allowed")
	}
}

// ---- Tracker ----

const (
	vkLWin   = 0x5B
	vkLCtrl  = 0xA2
	vkLShift = 0xA0
	vkE      = 'E'
	vkS      = 'S'
	vkEsc    = 0x1B
)

func press(t *Tracker, vks ...uint32) (last Result) {
	for _, vk := range vks {
		last = t.Key(vk, true)
	}
	return last
}

func release(t *Tracker, vks ...uint32) (triggers int) {
	for _, vk := range vks {
		if t.Key(vk, false).Trigger {
			triggers++
		}
	}
	return triggers
}

func TestWinAloneTriggersOnRelease(t *testing.T) {
	var tr Tracker
	tr.SetTarget(modWin)
	if r := press(&tr, vkLWin); r.Trigger || r.Swallow {
		t.Fatalf("pressing Win must not trigger or swallow: %+v", r)
	}
	r := tr.Key(vkLWin, false)
	if !r.Trigger || !r.Mask || r.Swallow {
		t.Fatalf("releasing Win should trigger with a mask: %+v", r)
	}
}

func TestWinWithAnotherKeyDoesNotTrigger(t *testing.T) {
	var tr Tracker
	tr.SetTarget(modWin)
	press(&tr, vkLWin, vkE)
	if n := release(&tr, vkE, vkLWin); n != 0 {
		t.Fatalf("Win+E must not trigger the Win-only hotkey (%d)", n)
	}
	// and the next lone Win press works again
	press(&tr, vkLWin)
	if n := release(&tr, vkLWin); n != 1 {
		t.Fatalf("lone Win after Win+E: triggers = %d", n)
	}
}

func TestWinWithShiftDoesNotTrigger(t *testing.T) {
	var tr Tracker
	tr.SetTarget(modWin)
	press(&tr, vkLWin, vkLShift, vkS) // Win+Shift+S
	if n := release(&tr, vkS, vkLShift, vkLWin); n != 0 {
		t.Fatalf("Win+Shift+S triggered %d times", n)
	}
	press(&tr, vkLWin, vkLShift) // modifiers only, but not the target set
	if n := release(&tr, vkLShift, vkLWin); n != 0 {
		t.Fatalf("Win+Shift alone triggered %d times", n)
	}
}

func TestMouseClickCancelsModifierOnly(t *testing.T) {
	var tr Tracker
	tr.SetTarget(modCtrl)
	press(&tr, vkLCtrl)
	tr.Other() // Ctrl + click
	if n := release(&tr, vkLCtrl); n != 0 {
		t.Fatalf("Ctrl+click triggered %d times", n)
	}
}

func TestTwoModifiersTrigger(t *testing.T) {
	var tr Tracker
	tr.SetTarget(modCtrl | modShift)
	press(&tr, vkLCtrl)
	if r := press(&tr, vkLShift); r.Trigger {
		t.Fatal("must wait for the release")
	}
	if n := release(&tr, vkLShift, vkLCtrl); n != 1 {
		t.Fatalf("Ctrl+Shift triggered %d times, want 1", n)
	}
}

func TestCtrlShiftLetterDoesNotTrigger(t *testing.T) {
	var tr Tracker
	tr.SetTarget(modCtrl | modShift)
	press(&tr, vkLCtrl, vkLShift, 'T')
	if n := release(&tr, 'T', vkLShift, vkLCtrl); n != 0 {
		t.Fatalf("Ctrl+Shift+T triggered %d times", n)
	}
}

func TestNoTargetNeverTriggers(t *testing.T) {
	var tr Tracker
	press(&tr, vkLWin)
	if n := release(&tr, vkLWin); n != 0 {
		t.Fatal("no modifier-only hotkey is set")
	}
}

func TestCaptureKeyedShortcut(t *testing.T) {
	var tr Tracker
	tr.StartCapture()
	r := tr.Key(vkLCtrl, true)
	if !r.Swallow || r.Event == nil || r.Event.Kind != CaptureHeld || r.Event.Modifiers != modCtrl {
		t.Fatalf("Ctrl down: %+v %+v", r, r.Event)
	}
	r = tr.Key(0x20, true) // Space
	if !r.Swallow || r.Event == nil || r.Event.Kind != CaptureDone ||
		r.Event.Modifiers != modCtrl || r.Event.VirtualKey != 0x20 {
		t.Fatalf("Space down: %+v %+v", r, r.Event)
	}
	if !tr.Active() || tr.Capturing() {
		t.Fatal("after the shortcut, capture is finished but still draining")
	}
	// the releases are still swallowed, then normal service resumes
	if !tr.Key(0x20, false).Swallow || !tr.Key(vkLCtrl, false).Swallow {
		t.Fatal("releases after a capture must be swallowed")
	}
	if tr.Active() {
		t.Fatal("capture should be fully over once every key is up")
	}
	if tr.Key('A', true).Swallow {
		t.Fatal("keys after the capture must pass through")
	}
}

func TestCaptureWinAlone(t *testing.T) {
	var tr Tracker
	tr.StartCapture()
	r := tr.Key(vkLWin, true)
	if !r.Swallow || r.Event.Kind != CaptureHeld || r.Event.Modifiers != modWin {
		t.Fatalf("Win down: %+v", r.Event)
	}
	r = tr.Key(vkLWin, false)
	if !r.Swallow || r.Event == nil || r.Event.Kind != CaptureDone ||
		r.Event.Modifiers != modWin || r.Event.VirtualKey != 0 {
		t.Fatalf("Win up: %+v", r.Event)
	}
	if tr.Active() {
		t.Fatal("nothing is held, so the capture is over")
	}
}

func TestCaptureTwoModifiersThenRelease(t *testing.T) {
	var tr Tracker
	tr.StartCapture()
	tr.Key(vkLCtrl, true)
	tr.Key(vkLShift, true)
	r := tr.Key(vkLShift, false)
	if r.Event == nil || r.Event.Kind != CaptureDone || r.Event.Modifiers != modCtrl|modShift {
		t.Fatalf("got %+v", r.Event)
	}
}

func TestCaptureEscCancels(t *testing.T) {
	var tr Tracker
	tr.StartCapture()
	r := tr.Key(vkEsc, true)
	if r.Event == nil || r.Event.Kind != CaptureCancel || !r.Swallow {
		t.Fatalf("got %+v", r.Event)
	}
	// Ctrl+Esc is a shortcut, not a cancel
	tr = Tracker{}
	tr.StartCapture()
	tr.Key(vkLCtrl, true)
	r = tr.Key(vkEsc, true)
	if r.Event == nil || r.Event.Kind != CaptureDone || r.Event.VirtualKey != vkEsc {
		t.Fatalf("got %+v", r.Event)
	}
}

func TestStopCaptureStopsSwallowing(t *testing.T) {
	var tr Tracker
	tr.StartCapture()
	tr.StopCapture()
	if tr.Key('A', true).Swallow {
		t.Fatal("keys must pass once capture is stopped")
	}
}
