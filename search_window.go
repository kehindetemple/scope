//go:build windows

package main

import (
	"math"
	"os/exec"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"go-file-search/internal/answer"
	"go-file-search/internal/index"
)

const (
	maxRows       = 6                      // rows visible at once; longer lists scroll
	firstLoad     = 100                    // results fetched for a new query
	maxResults    = 20000                  // the most results ever loaded for one query
	scrollStep    = 3.0                    // rows moved by one notch of the mouse wheel
	scrollTimerID = 1                      // drives the smooth-scroll animation
	focusGrace    = 400 * time.Millisecond // ignore focus loss right after showing
	editID        = 1001

	searchClassName = "GoFileSearchSearchBox"
	placeholderText = "Search files, folders and apps"
)

// search window state. Everything here is touched only on the UI thread,
// except iconsPosted, which the icon loader thread also uses.
type searchState struct {
	hwnd        uintptr
	edit        uintptr
	origEdit    uintptr
	placeholder uintptr
	inputBrush  uintptr // paints the text box background
	dwmRounded  bool    // Windows rounds the corners (Windows 11)

	visible  bool
	hasQuery bool
	shownAt  time.Time
	originX  int32
	originY  int32

	results  []index.Hit

	webMode   bool          // the text starts with ~: a question, not a file search
	webState  int           // webIdle, webLoading, webDone or webFailed
	webQuery  string        // the question, without the ~
	webAnswer answer.Result // the finished answer
	webError  string        // why the lookup failed, shown in the panel

	selected int    // index into results (not into the visible rows)
	query    string // the text the results are for
	limit    int    // how many results were asked for; more load on scroll

	scrollTarget float64 // the first visible row, where scrolling is heading
	scrollPos    float64 // the first visible row, as drawn right now
	animating    bool
	wheelAcc     int

	icons       *iconCache
	iconsPosted int32
}

var search searchState

func registerSearchWindowClass(instance uintptr, proc uintptr) error {
	cursor, _, _ := pLoadCursorW.Call(0, idcArrow)
	bigIcon, smallIcon := appIcons(instance)
	cls := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		Style:     csHRedraw | csVRedraw | csDropShadow,
		WndProc:   proc,
		Instance:  instance,
		Cursor:    cursor,
		Icon:      bigIcon,
		IconSm:    smallIcon,
		ClassName: utf16(searchClassName),
	}
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&cls))); r == 0 {
		return err
	}
	return nil
}

// createSearchWindow builds the hidden popup, its text box and the placeholder.
func createSearchWindow(instance uintptr, wndProc uintptr) error {
	// WS_CLIPCHILDREN keeps the popup's own painting off the text box. Without
	// it, every repaint of the popup wiped the text out of the box.
	hwnd, _, err := pCreateWindowExW.Call(
		wsExToolWindow|wsExTopmost,
		uintptr(unsafe.Pointer(utf16(searchClassName))),
		uintptr(unsafe.Pointer(utf16("Search"))),
		wsPopup|wsClipChildren,
		0, 0, uintptr(searchWidth), uintptr(inputHeight+1),
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return err
	}
	search.hwnd = hwnd

	// Windows 11 rounds the corners smoothly. Older versions get a cut-out
	// shape instead (see resizeSearch).
	search.dwmRounded = dwmSet(hwnd, dwmwaCornerPreference, 2) // DWMWCP_ROUND

	search.icons = newIconCache()
	go search.icons.run(hwnd, &search.iconsPosted)

	editY := (inputHeight - editHeight) / 2
	editW := searchWidth - editLeft - padX

	edit, _, err := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16("EDIT"))),
		0,
		wsChild|wsVisible|esAutoHScroll,
		uintptr(editLeft), uintptr(editY), uintptr(editW), uintptr(editHeight),
		hwnd, editID, instance, 0,
	)
	if edit == 0 {
		return err
	}
	search.edit = edit
	pSendMessageW.Call(edit, wmSetFont, fonts.input, 1)

	// Placeholder: a transparent static label drawn over the empty text box.
	// WS_EX_TRANSPARENT lets clicks reach the text box underneath.
	placeholder, _, err := pCreateWindowExW.Call(
		wsExTransparent,
		uintptr(unsafe.Pointer(utf16("STATIC"))),
		uintptr(unsafe.Pointer(utf16(placeholderText))),
		wsChild|wsVisible,
		uintptr(editLeft), uintptr(editY), uintptr(editW), uintptr(editHeight),
		hwnd, 0, instance, 0,
	)
	if placeholder == 0 {
		return err
	}
	search.placeholder = placeholder
	pSendMessageW.Call(placeholder, wmSetFont, fonts.input, 1)

	// Subclass the text box so arrow keys, Enter and Escape reach our code.
	search.origEdit, _, _ = pSetWindowLongPtrW.Call(edit, gwlpWndProcArg, syscall.NewCallback(editProc))
	return nil
}

// showPlaceholder shows the placeholder only while the text box is empty.
func showPlaceholder(show bool) {
	cmd := uintptr(swHide)
	if show {
		cmd = swShow
	}
	pShowWindow.Call(search.placeholder, cmd)
}

// toggleSearch shows the box, or hides it if it is already open.
func toggleSearch() {
	if search.visible {
		hideSearch()
		return
	}
	showSearch()
}

func showSearch() {
	x, y := searchOrigin()
	search.originX, search.originY = x, y

	setWindowTextValue(search.edit, "")
	showPlaceholder(true)
	resetResults()
	search.visible = true
	search.shownAt = time.Now()

	resizeSearch()
	bringToFront(search.hwnd)
	pSetFocus.Call(search.edit)
}

func hideSearch() {
	search.visible = false
	stopScrollAnimation()
	pKillTimer.Call(search.hwnd, webTimerID)
	pShowWindow.Call(search.hwnd, swHide)
}

// refreshSearchTheme repaints the search box after a color change.
func refreshSearchTheme() {
	if search.hwnd == 0 {
		return
	}
	pInvalidateRect.Call(search.hwnd, 0, 0)
	pInvalidateRect.Call(search.edit, 0, 1)
	pInvalidateRect.Call(search.placeholder, 0, 1)
}

// searchOrigin places the box near the top of the monitor under the mouse.
func searchOrigin() (int32, int32) {
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	mon, _, _ := pMonitorFromPoint.Call(uintptr(uint32(pt.X))|uintptr(uint32(pt.Y))<<32, 2) // MONITOR_DEFAULTTONEAREST
	var info monitorInfo
	info.Size = uint32(unsafe.Sizeof(info))
	pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&info)))

	work := info.Work
	x := work.Left + (work.width()-searchWidth)/2
	y := work.Top + work.height()/6
	return x, y
}

// rowsTop is the y coordinate of the first result row.
func rowsTop() int32 {
	return inputHeight + 1 + sectionHeight
}

// shownRows is how many result rows are on screen.
func shownRows() int {
	return min(len(search.results), maxRows)
}

// popupHeight is the height the popup needs for the current state.
func popupHeight() int32 {
	h := inputHeight + 1
	switch {
	case search.webMode:
		h += webBodyHeight()
	case shownRows() > 0:
		h += sectionHeight + int32(shownRows())*rowHeight + footerHeight
	case search.hasQuery:
		h += messageHeight
	}
	return h
}

// resizeSearch grows or shrinks the popup to fit the current results.
func resizeSearch() {
	h := popupHeight()
	pSetWindowPos.Call(search.hwnd, hwndTopmost, uintptr(search.originX), uintptr(search.originY),
		uintptr(searchWidth), uintptr(h), swpNoActivate|swpShowWin)

	if !search.dwmRounded {
		// Without Windows 11's rounded corners, cut the corners out instead.
		rgn, _, _ := pCreateRoundRectRgn.Call(0, 0, uintptr(searchWidth+1), uintptr(h+1),
			uintptr(cornerRadius*2), uintptr(cornerRadius*2))
		pSetWindowRgn.Call(search.hwnd, rgn, 1) // the window now owns rgn
	}
}

// resetResults clears the list and puts the scroll position back at the top.
func resetResults() {
	search.webMode = false
	search.results = nil
	search.query = ""
	search.hasQuery = false
	search.selected = 0
	search.scrollTarget = 0
	search.scrollPos = 0
	stopScrollAnimation()
}

// refreshResults runs a search for the current text. It runs on the UI thread,
// which is fast because the index is in memory. Only the first batch is
// loaded; growResults fetches more as the list is scrolled.
func refreshResults() {
	q := getWindowTextValue(search.edit)
	if isWebQuery(q) {
		startWebMode(q)
		return
	}
	search.hasQuery = q != ""
	showPlaceholder(!search.hasQuery)

	resetResults()
	search.query = q
	search.hasQuery = q != ""
	search.limit = firstLoad
	if q != "" {
		search.results = searchAll(q, search.limit)
	}
	resizeSearch()
	invalidate(search.hwnd)
}

// growResults loads more results when the view is close to the end of the
// ones loaded so far, so the whole list can be scrolled through.
func growResults() {
	if search.query == "" || len(search.results) < search.limit || search.limit >= maxResults {
		return // nothing more to load
	}
	if int(search.scrollTarget)+maxRows+20 < len(search.results) {
		return // still plenty ahead
	}
	search.limit = min(search.limit*4, maxResults)
	search.results = searchAll(search.query, search.limit)
}

// maxScroll is the largest first-visible-row value.
func maxScroll() float64 {
	return math.Max(0, float64(len(search.results)-maxRows))
}

func clampScroll() {
	search.scrollTarget = math.Max(0, math.Min(search.scrollTarget, maxScroll()))
}

// keepSelectedVisible scrolls just enough to show the highlighted row.
func keepSelectedVisible() {
	sel := float64(search.selected)
	if sel < search.scrollTarget {
		search.scrollTarget = sel
	}
	if sel > search.scrollTarget+float64(maxRows-1) {
		search.scrollTarget = sel - float64(maxRows-1)
	}
	clampScroll()
	startScrollAnimation()
}

// moveSelection moves the highlight by delta rows, scrolling as needed.
func moveSelection(delta int) {
	if len(search.results) == 0 {
		return
	}
	search.selected = max(0, min(search.selected+delta, len(search.results)-1))
	keepSelectedVisible()
	growResults()
	invalidate(search.hwnd)
}

// scrollBy scrolls the list by rows (negative is up). The highlight stays on
// screen, moving with the list when it would scroll out of view.
func scrollBy(rows float64) {
	if len(search.results) <= maxRows || rows == 0 {
		return
	}
	search.scrollTarget += rows
	clampScroll()

	lo := int(math.Round(search.scrollTarget))
	hi := min(lo+maxRows-1, len(search.results)-1)
	search.selected = max(lo, min(search.selected, hi))

	growResults()
	startScrollAnimation()
}

// handleWheel scrolls by the mouse wheel. wp holds the wheel movement in its
// high word: 120 per notch, positive when the wheel is turned away from the user.
func handleWheel(wp uintptr) {
	delta := float64(int16(hiword(wp)))
	scrollBy(-delta / 120 * scrollStep)
}

// startScrollAnimation eases the drawn scroll position toward its target, so
// the list glides instead of jumping.
func startScrollAnimation() {
	if search.animating {
		return
	}
	search.animating = true
	pSetTimer.Call(search.hwnd, scrollTimerID, 15, 0)
}

func stopScrollAnimation() {
	if search.animating && search.hwnd != 0 {
		pKillTimer.Call(search.hwnd, scrollTimerID)
	}
	search.animating = false
}

// stepScroll runs on every animation tick.
func stepScroll() {
	d := search.scrollTarget - search.scrollPos
	if math.Abs(d) < 0.01 {
		search.scrollPos = search.scrollTarget
		stopScrollAnimation()
	} else {
		search.scrollPos += d * 0.35
	}
	invalidate(search.hwnd)
}

// openSelected opens the highlighted item with its default action and hides
// the box. Explorer does the opening, so the item doesn't run with this app's
// administrator rights.
func openSelected() {
	if openWebAnswer() {
		return
	}
	if search.selected < 0 || search.selected >= len(search.results) {
		return
	}
	path := search.results[search.selected].Path
	hideSearch()
	if err := exec.Command("explorer.exe", path).Start(); err != nil {
		logf("could not open %s: %v", path, err)
	}
}

func invalidate(hwnd uintptr) {
	pInvalidateRect.Call(hwnd, 0, 0)
}

// rowAt returns the index in results of the row under a y coordinate, or -1.
func rowAt(y int32) int {
	top := rowsTop()
	if y < top || y >= top+int32(shownRows())*rowHeight {
		return -1
	}
	off := int32(search.scrollPos * float64(rowHeight))
	i := int((y - top + off) / rowHeight)
	if i < 0 || i >= len(search.results) {
		return -1
	}
	return i
}

// searchProc handles messages for the search popup.
func searchProc(hwnd, m, wp, lp uintptr) uintptr {
	switch uint32(m) {
	case wmPaint:
		paintSearch(hwnd)
		return 0
	case wmEraseBkgnd:
		return 1 // painting is double-buffered, so skip the flicker-prone erase
	case wmCommand:
		if hiword(wp) == enChange && loword(wp) == editID {
			refreshResults()
		}
		return 0
	case wmCtlColorEdit:
		pSetTextColor.Call(wp, pal.inputText)
		pSetBkColor.Call(wp, pal.inputBg)
		return search.inputBrush
	case wmCtlColorStat:
		pSetTextColor.Call(wp, pal.inputMuted)
		pSetBkColor.Call(wp, pal.inputBg)
		return search.inputBrush
	case wmActivate:
		// Hide when the user clicks somewhere else. The grace period stops
		// Windows' own focus changes right after showing from closing the box.
		if loword(wp) == waInactive && search.visible && time.Since(search.shownAt) > focusGrace {
			hideSearch()
		}
		return 0
	case wmMouseWheel:
		handleWheel(wp)
		return 0
	case wmMouseMove:
		if i := rowAt(signedY(lp)); i >= 0 && i != search.selected {
			search.selected = i
			invalidate(hwnd)
		}
		return 0
	case wmLButtonUp:
		if search.webMode {
			openWebAnswer()
			return 0
		}
		if i := rowAt(signedY(lp)); i >= 0 {
			search.selected = i
			openSelected()
		}
		return 0
	case wmTimer:
		if wp == scrollTimerID {
			stepScroll()
		}
		if wp == webTimerID {
			onWebTimer()
		}
		return 0
	case wmAppWeb:
		onWebResult()
		return 0
	case wmAppIcons:
		atomic.StoreInt32(&search.iconsPosted, 0)
		if search.visible {
			invalidate(hwnd)
		}
		return 0
	case wmAppToggle:
		toggleSearch()
		return 0
	case wmClose:
		hideSearch()
		return 0
	}
	ret, _, _ := pDefWindowProcW.Call(hwnd, uintptr(m), wp, lp)
	return ret
}

// editProc sees keys typed in the text box before the edit control does.
func editProc(hwnd, m, wp, lp uintptr) uintptr {
	if uint32(m) == wmMouseWheel {
		handleWheel(wp)
		return 0
	}
	if uint32(m) == wmKeyDown {
		switch wp {
		case vkPrior:
			moveSelection(-maxRows)
			return 0
		case vkNext:
			moveSelection(maxRows)
			return 0
		case vkDown:
			moveSelection(1)
			return 0
		case vkUp:
			moveSelection(-1)
			return 0
		case vkReturn:
			openSelected()
			return 0
		case vkEscape:
			hideSearch()
			return 0
		case vkTab:
			return 0
		}
	}
	if uint32(m) == wmChar && (wp == vkReturn || wp == vkEscape || wp == vkTab) {
		return 0 // no beep for keys we handled above
	}
	ret, _, _ := pCallWindowProcW.Call(search.origEdit, hwnd, m, wp, lp)
	return ret
}

// typeLabel is the grey text shown on the right of each row.
func typeLabel(h index.Hit) string {
	switch {
	case h.Label != "":
		return h.Label
	case h.Kind == index.KindApp:
		return "Application"
	case h.Dir:
		return "Folder"
	default:
		return "File"
	}
}

// paintSearch draws the popup into an off-screen bitmap first, then copies it
// to the window, so it doesn't flicker.
func paintSearch(hwnd uintptr) {
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

	cv := newCanvas(memDC)
	paintSearchContents(cv, memDC, w, h)
	cv.close()

	// The popup clips its child controls out of the paint, so copying the
	// whole bitmap never covers the text box.
	pBitBlt.Call(hdc, 0, 0, uintptr(w), uintptr(h), memDC, 0, 0, srcCopy)
	pSelectObject.Call(memDC, old)
	pDeleteObject.Call(bmp)
	pDeleteDC.Call(memDC)
}

func paintSearchContents(cv *canvas, dc uintptr, w, h int32) {
	cv.fillRect(rect{0, 0, w, h}, pal.resultBg)
	cv.fillRect(rect{0, 0, w, inputHeight}, pal.inputBg)

	// The magnifier is a glyph from Windows' icon font, so it is crisp.
	drawText(dc, fonts.glyph, pal.inputMuted, "\uE721",
		rect{padX, 0, padX + px(28), inputHeight}, dtCenter|dtSingleLine|dtVCenter)

	if search.webMode {
		paintWebContents(cv, dc, w, h)
		return
	}

	n := shownRows()
	if n == 0 {
		if search.hasQuery {
			cv.fillRect(rect{0, inputHeight, w, inputHeight + 1}, pal.separator)
			drawText(dc, fonts.name, pal.resultMuted, "No results",
				rect{padX, inputHeight + 1, w - padX, h}, dtCenter|dtSingleLine|dtVCenter)
		}
		return
	}

	cv.fillRect(rect{0, inputHeight, w, inputHeight + 1}, pal.separator)
	top := rowsTop()
	drawText(dc, fonts.small, pal.resultMuted, "Results",
		rect{padX, inputHeight + 1, w, top}, dtLeft|dtSingleLine|dtVCenter)

	// Rows are drawn at a pixel offset while scrolling, and clipped to the list.
	pSaveDC.Call(dc)
	pIntersectClipRect.Call(dc, 0, uintptr(top), uintptr(w), uintptr(top+int32(n)*rowHeight))
	off := int32(search.scrollPos * float64(rowHeight))
	first := int(off / rowHeight)
	yOff := off % rowHeight
	for i := 0; i <= n; i++ {
		if i == n && yOff == 0 {
			break // nothing of the extra row would show
		}
		idx := first + i
		if idx >= len(search.results) {
			break
		}
		drawRow(cv, dc, search.results[idx], idx == search.selected, top-yOff+int32(i)*rowHeight, w)
	}
	pRestoreDC.Call(dc, ^uintptr(0))

	drawScrollbar(cv, w, n)
	drawFooter(cv, dc, top+int32(n)*rowHeight, w)
}

// drawRow draws one result: icon, name, path and type.
func drawRow(cv *canvas, dc uintptr, hit index.Hit, selected bool, top, w int32) {
	if selected {
		cv.fillRound(rect{px(8), top + px(2), w - px(8), top + rowHeight - px(2)}, px(12), pal.selected)
	}

	d := search.icons.get(hit.Path, hit.Dir)
	drawItemIcon(dc, d, padX, top+(rowHeight-iconSize)/2)

	textX := padX + iconSize + px(14)
	textRight := w - px(170)
	if isShellItem(hit.Path) {
		// Apps and Settings pages have no useful path, so the name is centred.
		drawText(dc, fonts.name, pal.resultText, hit.Name,
			rect{textX, top, textRight, top + rowHeight}, dtLeft|dtSingleLine|dtEndEllipsis|dtNoPrefix|dtVCenter)
	} else {
		drawText(dc, fonts.name, pal.resultText, hit.Name,
			rect{textX, top + px(9), textRight, top + px(30)}, dtLeft|dtSingleLine|dtEndEllipsis|dtNoPrefix)
		drawText(dc, fonts.path, pal.resultMuted, hit.Path,
			rect{textX, top + px(30), textRight, top + px(48)}, dtLeft|dtSingleLine|dtEndEllipsis|dtNoPrefix)
	}
	drawText(dc, fonts.small, pal.resultMuted, typeLabel(hit),
		rect{w - px(166), top, w - px(24), top + rowHeight}, dtRight|dtSingleLine|dtVCenter)
}

// drawFooter shows the app's name on the left and key hints on the right,
// like "Enter Open" and "Esc Close", with the keys in small keycaps.
func drawFooter(cv *canvas, dc uintptr, top, w int32) {
	if g := microscopeBitmap(int(px(18))); g != 0 {
		size := px(18)
		drawBitmap(dc, g, padX, top+(footerHeight-size)/2, size, size, size, size)
	}
	drawText(dc, fonts.small, pal.resultMuted, "Scope",
		rect{padX + px(26), top, w / 2, top + footerHeight}, dtLeft|dtSingleLine|dtVCenter)

	x := w - padX
	x = drawKeyHint(cv, dc, top, x, "Esc", "Close")
	drawKeyHint(cv, dc, top, x-px(16), "Enter", "Open")
}

// drawKeyHint draws "label" ending at right, with a keycap to its left. It
// returns the x where the keycap starts.
func drawKeyHint(cv *canvas, dc uintptr, top, right int32, key, label string) int32 {
	labelW := measureText(dc, fonts.small, label)
	drawText(dc, fonts.small, pal.resultMuted, label,
		rect{right - labelW, top, right + 1, top + footerHeight}, dtLeft|dtSingleLine|dtVCenter)

	x := right - labelW - px(8)
	keyW := measureText(dc, fonts.small, key) + px(14)
	keyRect := rect{x - keyW, top + px(11), x, top + footerHeight - px(11)}
	cv.fillRound(keyRect, px(6), pal.keycap)
	drawText(dc, fonts.small, pal.resultText, key, keyRect, dtCenter|dtSingleLine|dtVCenter)
	return keyRect.Left
}

// drawScrollbar draws a thin scroll thumb at the right edge when there are
// more results than rows on screen.
func drawScrollbar(cv *canvas, w int32, rows int) {
	total := len(search.results)
	if total <= maxRows {
		return
	}
	trackH := int32(rows) * rowHeight
	thumbH := max(px(28), trackH*int32(maxRows)/int32(total))
	frac := math.Max(0, math.Min(1, search.scrollPos/maxScroll()))
	thumbTop := rowsTop() + int32(float64(trackH-thumbH)*frac)
	cv.fillRound(rect{w - px(8), thumbTop + 2, w - px(4), thumbTop + thumbH - 2}, px(2),
		mix(pal.resultBg, pal.resultText, 0.28))
}
