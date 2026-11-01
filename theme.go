//go:build windows

package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---- screen scale ---------------------------------------------------------

var (
	dwmapi                 = windows.NewLazySystemDLL("dwmapi.dll")
	pDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	pGetDpiForSystem       = user32.NewProc("GetDpiForSystem")
)

// dpiScale is the screen's scaling: 1.0 at 100%, 1.5 at 150%. The app is
// DPI-aware, so every size is multiplied by it and Windows never stretches
// (and blurs) the window.
var dpiScale = 1.0

func initDPI() {
	if pGetDpiForSystem.Find() != nil {
		return
	}
	if d, _, _ := pGetDpiForSystem.Call(); d >= 96 {
		dpiScale = float64(d) / 96
	}
}

// px converts a size in 100%-scale pixels to real pixels.
func px(n int) int32 { return int32(math.Round(float64(n) * dpiScale)) }

// Layout of the search box, in real pixels. Set by initLayout.
var (
	searchWidth, inputHeight, sectionHeight, rowHeight int32
	footerHeight, messageHeight, cornerRadius          int32
	iconSize, padX, editLeft, editHeight               int32
)

func initLayout() {
	searchWidth = px(720)
	inputHeight = px(52)
	sectionHeight = px(32)
	rowHeight = px(50)
	footerHeight = px(44)
	messageHeight = px(64)
	cornerRadius = px(16)
	iconSize = px(32)
	padX = px(20)
	editLeft = px(60)
	editHeight = px(30)
}

// ---- fonts ------------------------------------------------------------------

var fonts struct {
	input, name, path, small          uintptr // the search box
	title, head, body, bodyBold, key  uintptr // the dashboard
	glyph                             uintptr // icon font for the magnifier
}

func initFonts() {
	const face = "Segoe UI"
	fonts.input = createFont(-px(20), 400, face)
	fonts.name = createFont(-px(15), 400, face)
	fonts.path = createFont(-px(12), 400, face)
	fonts.small = createFont(-px(12), 400, face)
	fonts.title = createFont(-px(22), 600, face)
	fonts.head = createFont(-px(15), 600, face)
	fonts.body = createFont(-px(13), 400, face)
	fonts.bodyBold = createFont(-px(13), 600, face)
	fonts.key = createFont(-px(13), 600, face)
	fonts.glyph = createFont(-px(18), 400, "Segoe MDL2 Assets")
}

// ---- colors -----------------------------------------------------------------

// Colors are COLORREF values (0x00BBGGRR), the form GDI uses.

func splitRGB(c uintptr) (r, g, b int) {
	return int(c & 0xFF), int((c >> 8) & 0xFF), int((c >> 16) & 0xFF)
}

func clamp255(v float64) uint8 {
	return uint8(math.Max(0, math.Min(255, math.Round(v))))
}

// mix blends a toward b: t = 0 gives a, t = 1 gives b.
func mix(a, b uintptr, t float64) uintptr {
	ar, ag, ab := splitRGB(a)
	br, bg, bb := splitRGB(b)
	f := func(x, y int) uint8 { return clamp255(float64(x) + (float64(y)-float64(x))*t) }
	return rgb(f(ar, br), f(ag, bg), f(ab, bb))
}

func luminance(c uintptr) float64 {
	r, g, b := splitRGB(c)
	return 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
}

func isLight(c uintptr) bool { return luminance(c) > 150 }

// textColors picks readable text and muted text colors for a background.
func textColors(bg uintptr) (text, muted uintptr) {
	if isLight(bg) {
		return rgb(0x1B, 0x1B, 0x20), rgb(0x6B, 0x6B, 0x78)
	}
	return rgb(0xF2, 0xF2, 0xF5), rgb(0x9C, 0x9C, 0xA8)
}

func parseHexColor(s string) (uintptr, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, false
	}
	return rgb(uint8(v>>16), uint8(v>>8), uint8(v)), true
}

func formatHexColor(c uintptr) string {
	r, g, b := splitRGB(c)
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

func colorOr(hex string, def uintptr) uintptr {
	if c, ok := parseHexColor(hex); ok {
		return c
	}
	return def
}

// ---- palette ----------------------------------------------------------------

// palette holds every color in use. It is rebuilt when the theme or a custom
// color changes.
type palette struct {
	// search box
	inputBg, resultBg       uintptr
	inputText, inputMuted   uintptr
	resultText, resultMuted uintptr
	selected, keycap        uintptr
	separator, accent       uintptr

	// dashboard
	dashBg, card, cardBorder uintptr
	dashText, dashMuted      uintptr
	button                   uintptr
	danger, success          uintptr
}

var pal palette

func isDarkTheme() bool { return settings.app.Theme != "light" }

// rebuildPalette works out all colors from the theme and the custom colors,
// and renews the brush the text box paints its background with.
func rebuildPalette() {
	var p palette

	inDef, resDef := rgb(0x26, 0x26, 0x2C), rgb(0x1D, 0x1D, 0x22)
	if !isDarkTheme() {
		inDef, resDef = rgb(0xFF, 0xFF, 0xFF), rgb(0xF4, 0xF4, 0xF7)
	}
	p.inputBg = colorOr(settings.app.InputColor, inDef)
	p.resultBg = colorOr(settings.app.ResultColor, resDef)
	p.inputText, p.inputMuted = textColors(p.inputBg)
	p.resultText, p.resultMuted = textColors(p.resultBg)
	p.accent = rgb(0x4C, 0x8D, 0xFF)
	p.selected = mix(p.resultBg, p.accent, 0.22)
	p.keycap = mix(p.resultBg, p.resultText, 0.12)
	p.separator = mix(p.resultBg, p.resultText, 0.10)

	if isDarkTheme() {
		p.dashBg = rgb(0x15, 0x15, 0x18)
		p.card = rgb(0x1E, 0x1E, 0x23)
		p.cardBorder = rgb(0x2D, 0x2D, 0x35)
		p.dashText = rgb(0xF2, 0xF2, 0xF5)
		p.dashMuted = rgb(0x9A, 0x9A, 0xA6)
		p.button = rgb(0x2A, 0x2A, 0x31)
		p.danger = rgb(0xF0, 0x61, 0x6D)
		p.success = rgb(0x3D, 0xBE, 0x8B)
	} else {
		p.dashBg = rgb(0xF2, 0xF2, 0xF5)
		p.card = rgb(0xFF, 0xFF, 0xFF)
		p.cardBorder = rgb(0xE0, 0xE0, 0xE7)
		p.dashText = rgb(0x1B, 0x1B, 0x1F)
		p.dashMuted = rgb(0x6B, 0x6B, 0x77)
		p.button = rgb(0xEC, 0xEC, 0xF1)
		p.danger = rgb(0xD6, 0x33, 0x3F)
		p.success = rgb(0x1E, 0x9B, 0x63)
	}
	pal = p

	if search.inputBrush != 0 {
		pDeleteObject.Call(search.inputBrush)
	}
	search.inputBrush, _, _ = pCreateSolidBrush.Call(p.inputBg)
}

// ---- window frame -----------------------------------------------------------

const (
	dwmwaImmersiveDarkOld = 19 // Windows 10 before version 2004
	dwmwaImmersiveDark    = 20
	dwmwaCornerPreference = 33 // Windows 11
	dwmwaBorderColor      = 34 // Windows 11
	dwmwaCaptionColor     = 35 // Windows 11
	dwmwaTextColor        = 36 // Windows 11
)

// dwmSet sets a window frame attribute. Older Windows versions do not know
// the newer ones, so it reports whether Windows accepted it.
func dwmSet(hwnd uintptr, attr uint32, value uint32) bool {
	if hwnd == 0 || pDwmSetWindowAttribute.Find() != nil {
		return false
	}
	r, _, _ := pDwmSetWindowAttribute.Call(hwnd, uintptr(attr), uintptr(unsafe.Pointer(&value)), 4)
	return int32(r) >= 0
}

// applyWindowTheme colors the parts of the windows Windows draws itself: the
// dashboard's title bar and the search box's border.
func applyWindowTheme() {
	dark := uint32(0)
	if isDarkTheme() {
		dark = 1
	}
	if dash.hwnd != 0 {
		dwmSet(dash.hwnd, dwmwaImmersiveDark, dark)
		dwmSet(dash.hwnd, dwmwaImmersiveDarkOld, dark)
		dwmSet(dash.hwnd, dwmwaCaptionColor, uint32(pal.dashBg))
		dwmSet(dash.hwnd, dwmwaTextColor, uint32(pal.dashText))
	}
	if search.hwnd != 0 {
		dwmSet(search.hwnd, dwmwaBorderColor, uint32(pal.separator))
	}
}
