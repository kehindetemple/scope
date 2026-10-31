//go:build windows

package main

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"go-file-search/internal/hotkey"
)

// The dashboard is drawn entirely by the app instead of using standard
// buttons, so it can follow the light and dark theme and look modern.

const settingsClassName = "GoFileSearchSettings"

const (
	dashW = 560 // client size in 100%-scale pixels
	dashH = 640

	captureTimerID = 2
	captureTimeout = 20000 // ms
)

// What a clickable area does.
const (
	hitChange = iota + 1
	hitReset
	hitThemeLight
	hitThemeDark
	hitInputAuto
	hitInputSwatch
	hitInputCustom
	hitResultAuto
	hitResultSwatch
	hitResultCustom
	hitAIAnswers
	hitQuit
)

// Message colors.
const (
	msgInfo = iota
	msgError
	msgOK
)

// swatchColors are the ready-made colors offered for the search field and the
// results box.
var swatchColors = []string{
	"#FFFFFF", "#F3F3F6", "#DCE3F5",
	"#2B2B31", "#1E1E22", "#0F0F12",
	"#1B2A4A", "#2A1F3D", "#173B2F",
}

type hitArea struct {
	r   rect
	id  int
	arg int
}

func (h hitArea) key() int { return h.id<<8 | h.arg }

type dashState struct {
	hwnd uintptr

	hits     []hitArea
	hover    int // key of the area under the mouse, or 0
	press    int // key of the area the mouse went down on
	tracking bool

	listening bool   // recording a new shortcut
	held      uint32 // modifiers held while recording

	msg     string
	msgKind int
}

var dash dashState

var (
	comdlg32     = windows.NewLazySystemDLL("comdlg32.dll")
	pChooseColor = comdlg32.NewProc("ChooseColorW")
)

func registerSettingsWindowClass(instance uintptr, proc uintptr) error {
	cursor, _, _ := pLoadCursorW.Call(0, idcArrow)
	bigIcon, smallIcon := appIcons(instance)
	cls := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		Style:     csHRedraw | csVRedraw,
		WndProc:   proc,
		Instance:  instance,
		Cursor:    cursor,
		Icon:      bigIcon,
		IconSm:    smallIcon,
		ClassName: utf16(settingsClassName),
	}
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&cls))); r == 0 {
		return err
	}
	return nil
}

func createSettingsWindow(instance uintptr, wndProc uintptr) error {
	// Size the window so its drawing area is exactly dashW x dashH.
	frame := rect{0, 0, px(dashW), px(dashH)}
	pAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&frame)), wsDashboard, 0, 0)
	ww, wh := frame.width(), frame.height()

	// Centre it on the main screen, and never taller than the work area.
	mon, _, _ := pMonitorFromPoint.Call(0, 1) // MONITOR_DEFAULTTOPRIMARY
	var info monitorInfo
	info.Size = uint32(unsafe.Sizeof(info))
	pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&info)))
	work := info.Work
	wh = min(wh, work.height())
	x := work.Left + (work.width()-ww)/2
	y := work.Top + (work.height()-wh)/2

	hwnd, _, err := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16(settingsClassName))),
		uintptr(unsafe.Pointer(utf16("Scope"))),
		wsDashboard,
		uintptr(x), uintptr(y), uintptr(ww), uintptr(wh),
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return err
	}
	dash.hwnd = hwnd
	return nil
}

func showSettings() {
	pShowWindow.Call(dash.hwnd, swShow)
	bringToFront(dash.hwnd)
	pSetFocus.Call(dash.hwnd)
}

func invalidateDash() {
	if dash.hwnd != 0 {
		pInvalidateRect.Call(dash.hwnd, 0, 0)
	}
}

func setMessage(text string, kind int) {
	dash.msg = text
	dash.msgKind = kind
	invalidateDash()
}

// ---- messages -----------------------------------------------------------------

// settingsProc handles messages for the dashboard.
func settingsProc(hwnd, m, wp, lp uintptr) uintptr {
	switch uint32(m) {
	case wmPaint:
		paintDashboard(hwnd)
		return 0
	case wmEraseBkgnd:
		return 1
	case wmMouseMove:
		onDashMouseMove(signedX(lp), signedY(lp))
		return 0
	case wmMouseLeave:
		dash.tracking = false
		if dash.hover != 0 {
			dash.hover = 0
			invalidateDash()
		}
		return 0
	case wmLButtonDown:
		if h, ok := hitAt(signedX(lp), signedY(lp)); ok {
			dash.press = h.key()
		} else {
			dash.press = 0
		}
		return 0
	case wmLButtonUp:
		if h, ok := hitAt(signedX(lp), signedY(lp)); ok && h.key() == dash.press {
			activate(h)
		}
		dash.press = 0
		return 0
	case wmSetCursor:
		if loword(lp) == 1 && dash.hover != 0 { // HTCLIENT, over a button
			hand, _, _ := pLoadCursorW.Call(0, idcHand)
			pSetCursor.Call(hand)
			return 1
		}
	case wmTimer:
		if wp == captureTimerID {
			stopListening()
			setMessage("Timed out. Click Change to try again.", msgInfo)
		}
		return 0
	case wmActivate:
		if loword(wp) == waInactive && dash.listening {
			stopListening()
			setMessage("Cancelled.", msgInfo)
		}
		return 0
	case wmClose:
		// The X button hides the dashboard. The app keeps running in the background.
		if dash.listening {
			stopListening()
		}
		pShowWindow.Call(hwnd, swHide)
		return 0
	case wmAppSettings:
		showSettings()
		return 0
	case wmAppCapture:
		onCapture(wp, lp)
		return 0
	}
	ret, _, _ := pDefWindowProcW.Call(hwnd, uintptr(m), wp, lp)
	return ret
}

func signedX(lp uintptr) int32 { return int32(int16(loword(lp))) }

func hitAt(x, y int32) (hitArea, bool) {
	for i := len(dash.hits) - 1; i >= 0; i-- {
		h := dash.hits[i]
		if x >= h.r.Left && x < h.r.Right && y >= h.r.Top && y < h.r.Bottom {
			return h, true
		}
	}
	return hitArea{}, false
}

func onDashMouseMove(x, y int32) {
	if !dash.tracking {
		// Ask Windows to tell us when the mouse leaves, so the highlight clears.
		tme := struct {
			Size      uint32
			Flags     uint32
			Track     uintptr
			HoverTime uint32
		}{Size: 24, Flags: 2 /* TME_LEAVE */, Track: dash.hwnd}
		pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
		dash.tracking = true
	}
	key := 0
	if h, ok := hitAt(x, y); ok {
		key = h.key()
	}
	if key != dash.hover {
		dash.hover = key
		invalidateDash()
	}
}

// activate runs the action of a clicked area.
func activate(h hitArea) {
	switch h.id {
	case hitChange:
		if dash.listening {
			stopListening()
			setMessage("Cancelled.", msgInfo)
		} else {
			startListening()
		}
	case hitReset:
		applyHotkey(defaultSpec())
	case hitThemeLight:
		changeAppearance(func(s *appSettings) { s.Theme = "light" })
	case hitThemeDark:
		changeAppearance(func(s *appSettings) { s.Theme = "dark" })
	case hitInputAuto:
		changeAppearance(func(s *appSettings) { s.InputColor = "" })
	case hitInputSwatch:
		changeAppearance(func(s *appSettings) { s.InputColor = swatchColors[h.arg] })
	case hitInputCustom:
		if hex, ok := chooseColor(settings.app.InputColor, defaultInputColor()); ok {
			changeAppearance(func(s *appSettings) { s.InputColor = hex })
		}
	case hitResultAuto:
		changeAppearance(func(s *appSettings) { s.ResultColor = "" })
	case hitResultSwatch:
		changeAppearance(func(s *appSettings) { s.ResultColor = swatchColors[h.arg] })
	case hitResultCustom:
		if hex, ok := chooseColor(settings.app.ResultColor, defaultResultColor()); ok {
			changeAppearance(func(s *appSettings) { s.ResultColor = hex })
		}
	case hitAIAnswers:
		showAIWindow()
	case hitQuit:
		pPostQuitMessage.Call(0)
	}
}

// changeAppearance applies a change to the theme or colors everywhere and saves it.
func changeAppearance(change func(*appSettings)) {
	change(&settings.app)
	rebuildPalette()
	applyWindowTheme()
	invalidateDash()
	refreshSearchTheme()
	if err := saveSettingsFile(settings.path, settings.app); err != nil {
		logf("could not save settings: %v", err)
	}
}

func defaultInputColor() uintptr {
	if isDarkTheme() {
		return rgb(0x26, 0x26, 0x2C)
	}
	return rgb(0xFF, 0xFF, 0xFF)
}

func defaultResultColor() uintptr {
	if isDarkTheme() {
		return rgb(0x1D, 0x1D, 0x22)
	}
	return rgb(0xF4, 0xF4, 0xF7)
}

// ---- color picker -------------------------------------------------------------

// chooseColorW mirrors the Win32 CHOOSECOLOR struct.
type chooseColorW struct {
	StructSize   uint32
	Owner        uintptr
	Instance     uintptr
	RGBResult    uint32
	CustColors   *[16]uint32
	Flags        uint32
	CustData     uintptr
	Hook         uintptr
	TemplateName *uint16
}

var customColors [16]uint32

// chooseColor opens Windows' color dialog, starting from the current color
// (or def when none is set). It reports false if the user cancels.
func chooseColor(current string, def uintptr) (string, bool) {
	cc := chooseColorW{
		Owner:      dash.hwnd,
		RGBResult:  uint32(colorOr(current, def)),
		CustColors: &customColors,
		Flags:      0x3, // CC_RGBINIT | CC_FULLOPEN
	}
	cc.StructSize = uint32(unsafe.Sizeof(cc))
	r, _, _ := pChooseColor.Call(uintptr(unsafe.Pointer(&cc)))
	if r == 0 {
		return "", false
	}
	return formatHexColor(uintptr(cc.RGBResult)), true
}

// ---- hotkey recording -----------------------------------------------------------

func startListening() {
	if settings.hotkeys == nil {
		setMessage("Hotkeys are not available on this system.", msgError)
		return
	}
	hwnd := dash.hwnd
	err := settings.hotkeys.StartCapture(func(ev hotkey.CaptureEvent) {
		// Runs inside the keyboard hook: only post a message, never block.
		pPostMessageW.Call(hwnd, wmAppCapture, uintptr(ev.Kind)|uintptr(ev.Modifiers)<<8, uintptr(ev.VirtualKey))
	})
	if err != nil {
		setMessage(err.Error(), msgError)
		return
	}
	dash.listening = true
	dash.held = 0
	pSetTimer.Call(dash.hwnd, captureTimerID, captureTimeout, 0)
	setMessage("Press the shortcut you want. Esc cancels.", msgInfo)
}

func stopListening() {
	if dash.listening && settings.hotkeys != nil {
		settings.hotkeys.StopCapture()
	}
	dash.listening = false
	dash.held = 0
	pKillTimer.Call(dash.hwnd, captureTimerID)
	invalidateDash()
}

// onCapture handles what the keyboard hook reports while recording.
func onCapture(wp, lp uintptr) {
	if !dash.listening {
		return
	}
	kind := hotkey.CaptureKind(wp & 0xFF)
	mods := uint32(wp >> 8)
	vk := uint32(lp)

	switch kind {
	case hotkey.CaptureHeld:
		dash.held = mods
		invalidateDash()
	case hotkey.CaptureCancel:
		stopListening()
		setMessage("Cancelled.", msgInfo)
	case hotkey.CaptureDone:
		stopListening()
		spec, ok := hotkey.SpecFrom(mods, vk)
		if !ok {
			setMessage("That key can't be used as a hotkey. Try another.", msgError)
			return
		}
		applyHotkey(spec)
	}
}

// applyHotkey registers a new hotkey and saves it. If Windows refuses it
// (it is reserved, or another app uses it), the old hotkey stays active and
// the reason is shown.
func applyHotkey(spec hotkey.Spec) {
	combo, err := hotkey.Parse(spec)
	if err != nil {
		setMessage(err.Error(), msgError)
		return
	}
	if settings.hotkeys == nil {
		setMessage("Hotkeys are not available on this system.", msgError)
		return
	}
	if err := settings.hotkeys.Set(combo); err != nil {
		setMessage(err.Error(), msgError)
		return
	}

	settings.app.setSpec(spec)
	if err := saveSettingsFile(settings.path, settings.app); err != nil {
		logf("could not save settings: %v", err)
	}
	setMessage("Hotkey set to "+hotkey.Format(spec)+".", msgOK)
}

// ---- painting ---------------------------------------------------------------------

func addHit(r rect, id, arg int) {
	dash.hits = append(dash.hits, hitArea{r: r, id: id, arg: arg})
}

func paintDashboard(hwnd uintptr) {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	defer pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))

	var rc rect
	pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	w, h := rc.width(), rc.height()
	if w <= 0 || h <= 0 {
		return
	}

	memDC, _, _ := pCreateCompatibleDC.Call(hdc)
	bmp, _, _ := pCreateCompatibleBitmap.Call(hdc, uintptr(w), uintptr(h))
	old, _, _ := pSelectObject.Call(memDC, bmp)
	pSetBkMode.Call(memDC, transparent)

	dash.hits = dash.hits[:0]
	cv := newCanvas(memDC)
	paintDashboardContents(cv, memDC, w, h)
	cv.close()

	pBitBlt.Call(hdc, 0, 0, uintptr(w), uintptr(h), memDC, 0, 0, srcCopy)
	pSelectObject.Call(memDC, old)
	pDeleteObject.Call(bmp)
	pDeleteDC.Call(memDC)
}

func paintDashboardContents(cv *canvas, dc uintptr, w, h int32) {
	cv.fillRect(rect{0, 0, w, h}, pal.dashBg)

	// Header
	if g := microscopeBitmap(int(px(56))); g != 0 {
		drawBitmap(dc, g, px(24), px(24), px(56), px(56), px(56), px(56))
	}
	drawText(dc, fonts.title, pal.dashText, "Scope",
		rect{px(96), px(26), px(536), px(56)}, dtLeft|dtSingleLine|dtVCenter)
	drawText(dc, fonts.body, pal.dashMuted, "Find any file, folder or app from anywhere.",
		rect{px(96), px(56), px(536), px(80)}, dtLeft|dtSingleLine|dtVCenter)
	drawButton(cv, dc, rect{px(396), px(30), px(536), px(62)}, "AI answers…", false, hitAIAnswers, 0)

	paintHotkeyCard(cv, dc)
	paintAppearanceCard(cv, dc)

	// Footer
	drawButton(cv, dc, rect{px(24), px(598), px(104), px(630)}, "Quit", false, hitQuit, 0)
	drawText(dc, fonts.small, pal.dashMuted, "Closing this window keeps the app running in the background.",
		rect{px(120), px(598), px(536), px(630)}, dtLeft|dtSingleLine|dtVCenter)
}

func paintHotkeyCard(cv *canvas, dc uintptr) {
	cv.outlineRound(rect{px(24), px(100), px(536), px(246)}, px(14), pal.cardBorder, pal.card)
	drawText(dc, fonts.head, pal.dashText, "Hotkey",
		rect{px(44), px(114), px(300), px(138)}, dtLeft|dtSingleLine|dtVCenter)
	drawText(dc, fonts.small, pal.dashMuted, "Press it from anywhere to open search. Windows key alone works too.",
		rect{px(44), px(138), px(516), px(158)}, dtLeft|dtSingleLine|dtVCenter)

	// The shortcut as keycaps, or a prompt while recording.
	chipY := px(168)
	if dash.listening {
		var parts []string
		if spec, ok := hotkey.SpecFrom(dash.held, 0); ok {
			parts = hotkey.Parts(spec)
		}
		if len(parts) == 0 {
			drawText(dc, fonts.body, pal.dashMuted, "Press your shortcut now…",
				rect{px(44), chipY, px(330), chipY + px(38)}, dtLeft|dtSingleLine|dtVCenter)
		} else {
			drawChips(cv, dc, px(44), chipY, parts)
		}
	} else {
		drawChips(cv, dc, px(44), chipY, hotkey.Parts(settings.app.spec()))
	}

	label := "Change"
	if dash.listening {
		label = "Cancel"
	} else {
		drawButton(cv, dc, rect{px(344), px(168), px(420), px(206)}, "Reset", false, hitReset, 0)
	}
	drawButton(cv, dc, rect{px(428), px(168), px(516), px(206)}, label, true, hitChange, 0)

	color := pal.dashMuted
	switch dash.msgKind {
	case msgError:
		color = pal.danger
	case msgOK:
		color = pal.success
	}
	drawText(dc, fonts.small, color, dash.msg,
		rect{px(44), px(212), px(516), px(238)}, dtLeft|dtEndEllipsis|dtNoPrefix)
}

func paintAppearanceCard(cv *canvas, dc uintptr) {
	cv.outlineRound(rect{px(24), px(262), px(536), px(582)}, px(14), pal.cardBorder, pal.card)
	drawText(dc, fonts.head, pal.dashText, "Appearance",
		rect{px(44), px(276), px(300), px(300)}, dtLeft|dtSingleLine|dtVCenter)

	// Theme: a two-part switch.
	drawText(dc, fonts.body, pal.dashText, "Theme",
		rect{px(44), px(312), px(200), px(344)}, dtLeft|dtSingleLine|dtVCenter)
	track := rect{px(324), px(312), px(516), px(344)}
	cv.fillRound(track, px(10), pal.button)
	light, dark := rect{px(324), px(312), px(420), px(344)}, rect{px(420), px(312), px(516), px(344)}
	chosen, other := dark, light
	chosenLabel, otherLabel := "Dark", "Light"
	if !isDarkTheme() {
		chosen, other = light, dark
		chosenLabel, otherLabel = "Light", "Dark"
	}
	cv.fillRound(inset(chosen, px(3)), px(8), pal.accent)
	drawText(dc, fonts.bodyBold, rgb(255, 255, 255), chosenLabel, chosen, dtCenter|dtSingleLine|dtVCenter)
	drawText(dc, fonts.body, pal.dashMuted, otherLabel, other, dtCenter|dtSingleLine|dtVCenter)
	addHit(light, hitThemeLight, 0)
	addHit(dark, hitThemeDark, 0)

	drawText(dc, fonts.body, pal.dashText, "Search field color",
		rect{px(44), px(362), px(300), px(386)}, dtLeft|dtSingleLine|dtVCenter)
	drawSwatchRow(cv, dc, px(392), settings.app.InputColor, hitInputAuto, hitInputSwatch, hitInputCustom)

	drawText(dc, fonts.body, pal.dashText, "Results color",
		rect{px(44), px(432), px(300), px(456)}, dtLeft|dtSingleLine|dtVCenter)
	drawSwatchRow(cv, dc, px(462), settings.app.ResultColor, hitResultAuto, hitResultSwatch, hitResultCustom)

	paintPreview(cv, dc, rect{px(44), px(502), px(516), px(566)})
}

// paintPreview shows a small copy of the search box in the chosen colors.
func paintPreview(cv *canvas, dc uintptr, r rect) {
	radius := px(12)
	inputH := px(34)
	cv.fillRound(r, radius, pal.resultBg)
	cv.fillRound(rect{r.Left, r.Top, r.Right, r.Top + inputH}, radius, pal.inputBg)
	cv.fillRect(rect{r.Left, r.Top + inputH - radius, r.Right, r.Top + inputH}, pal.inputBg)

	drawText(dc, fonts.glyph, pal.inputMuted, "\uE721",
		rect{r.Left + px(10), r.Top, r.Left + px(34), r.Top + inputH}, dtCenter|dtSingleLine|dtVCenter)
	drawText(dc, fonts.body, pal.inputMuted, "Search files, folders and apps",
		rect{r.Left + px(40), r.Top, r.Right - px(10), r.Top + inputH}, dtLeft|dtSingleLine|dtVCenter)

	row := rect{r.Left + px(6), r.Top + inputH + px(3), r.Right - px(6), r.Bottom - px(3)}
	cv.fillRound(row, px(8), pal.selected)
	drawText(dc, fonts.body, pal.resultText, "Notepad",
		rect{row.Left + px(10), row.Top, row.Right, row.Bottom}, dtLeft|dtSingleLine|dtVCenter)
	drawText(dc, fonts.small, pal.resultMuted, "Application",
		rect{row.Left, row.Top, row.Right - px(10), row.Bottom}, dtRight|dtSingleLine|dtVCenter)
}

// drawSwatchRow draws "Auto", the ready-made colors and "Custom…" in a row.
// current is the stored color ("" means automatic).
func drawSwatchRow(cv *canvas, dc uintptr, y int32, current string, autoID, swatchID, customID int) {
	size := px(28)
	gap := px(8)
	x := px(44)

	pill := func(r rect, label string, selected bool, id int) {
		fillc := pal.button
		if dash.hover == (hitArea{id: id}).key() {
			fillc = mix(pal.button, pal.dashText, 0.10)
		}
		if selected {
			cv.outlineRound(r, size/2, pal.accent, fillc)
		} else {
			cv.fillRound(r, size/2, fillc)
		}
		drawText(dc, fonts.body, pal.dashText, label, r, dtCenter|dtSingleLine|dtVCenter)
		addHit(r, id, 0)
	}

	autoR := rect{x, y, x + px(48), y + size}
	pill(autoR, "Auto", current == "", autoID)
	x = autoR.Right + gap

	isPreset := false
	for i, hex := range swatchColors {
		r := rect{x, y, x + size, y + size}
		col, _ := parseHexColor(hex)
		selected := strings.EqualFold(current, hex)
		if selected {
			isPreset = true
			cv.fillCircle(inset(r, -px(3)), pal.accent)
			cv.fillCircle(inset(r, -px(1)), pal.card)
		} else if dash.hover == (hitArea{id: swatchID, arg: i}).key() {
			cv.fillCircle(inset(r, -px(2)), pal.dashMuted)
			cv.fillCircle(inset(r, -px(1)), pal.card)
		}
		cv.fillCircle(r, pal.cardBorder)
		cv.fillCircle(inset(r, 1), col)
		addHit(r, swatchID, i)
		x += size + gap
	}

	customR := rect{x, y, x + px(72), y + size}
	pill(customR, "Custom…", current != "" && !isPreset, customID)
}

// drawChips draws a shortcut as keycaps joined by "+". It returns where it ended.
func drawChips(cv *canvas, dc uintptr, x, y int32, parts []string) int32 {
	height := px(38)
	depth := px(3)
	for i, p := range parts {
		if i > 0 {
			drawText(dc, fonts.body, pal.dashMuted, "+",
				rect{x, y, x + px(18), y + height}, dtCenter|dtSingleLine|dtVCenter)
			x += px(18)
		}
		width := max(px(44), measureText(dc, fonts.key, p)+px(26))
		cv.fillRound(rect{x, y, x + width, y + height}, px(9), pal.cardBorder)                // the key's edge
		cv.fillRound(rect{x, y, x + width, y + height - depth}, px(9), pal.button)             // the key's face
		drawText(dc, fonts.key, pal.dashText, p,
			rect{x, y, x + width, y + height - depth}, dtCenter|dtSingleLine|dtVCenter)
		x += width
	}
	return x
}

// drawButton draws a rounded button and registers it as clickable.
func drawButton(cv *canvas, dc uintptr, r rect, label string, primary bool, id, arg int) {
	fillc, textc := pal.button, pal.dashText
	hover := dash.hover == (hitArea{id: id, arg: arg}).key()
	if primary {
		fillc, textc = pal.accent, rgb(255, 255, 255)
		if hover {
			fillc = mix(fillc, rgb(255, 255, 255), 0.14)
		}
	} else if hover {
		fillc = mix(fillc, textc, 0.10)
	}
	cv.fillRound(r, px(10), fillc)
	drawText(dc, fonts.bodyBold, textc, label, r, dtCenter|dtSingleLine|dtVCenter)
	addHit(r, id, arg)
}
