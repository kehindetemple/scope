//go:build windows

package main

import (
	"strings"
	"syscall"
	"unsafe"
)

// The AI answers window: where the answer source, the model provider, the
// model name and the API key are chosen. It opens from the dashboard.

const aiClassName = "GoFileSearchAI"

// Window styles and control styles not defined elsewhere in the app.
const (
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsGroup        = 0x00020000
	wsBorder       = 0x00800000
	bsAutoRadio    = 0x0009
	bsDefPushButton = 0x0001
	esPassword     = 0x0020
	bmGetCheck     = 0x00F0
	bmSetCheck     = 0x00F1
	bstChecked     = 1
	cwUseDefault   = 0x80000000
)

const (
	aiW = 500 // client size, in 100%-scale pixels
	aiH = 500
)

// control ids of the source radio buttons; the provider radio buttons follow from 3011
const (
	aiIDSourceAI     = 3001
	aiIDSourceWiki   = 3002
	aiIDSourceGoogle = 3003
	aiIDProviderBase = 3011
)

// The setting value each source radio button stands for, in order.
var (
	aiSourceIDs    = []int{aiIDSourceAI, aiIDSourceWiki, aiIDSourceGoogle}
	aiSourceValues = []string{srcAI, srcWikipedia, srcGoogle}
)

// The providers, in the order they appear in the window. Each has a radio
// button; its label is shown on the button.
var aiProviders = []struct {
	value string
	label string
}{
	{"anthropic", "Claude (Anthropic)"},
	{"openai", "OpenAI"},
	{"gemini", "Gemini (Google)"},
	{"grok", "Grok (xAI)"},
	{"kimi", "Kimi (Moonshot)"},
	{"deepseek", "DeepSeek"},
	{"mistral", "Mistral"},
	{"groq", "Groq (open models)"},
	{"openrouter", "OpenRouter (many models)"},
	{"ollama", "Ollama (on this PC, free)"},
}

// aiProviderIDs and aiProviderVals are the radio ids and their setting values, in order.
var aiProviderIDs, aiProviderVals = func() ([]int, []string) {
	ids := make([]int, len(aiProviders))
	vals := make([]string, len(aiProviders))
	for i, p := range aiProviders {
		ids[i] = aiIDProviderBase + i
		vals[i] = p.value
	}
	return ids, vals
}()

const (
	aiIDModel = 3101
	aiIDKey   = 3102
	aiIDSave  = 3111
	aiIDClose = 3112
)

var aiWin struct {
	hwnd       uintptr
	registered bool
	ctl        map[int]uintptr // control handles by id
	status     uintptr
}

// showAIWindow opens the AI answers window, creating it the first time.
func showAIWindow() {
	if aiWin.hwnd == 0 {
		if err := createAIWindow(); err != nil {
			logf("create AI answers window: %v", err)
			return
		}
	}
	loadAIControls()
	pShowWindow.Call(aiWin.hwnd, swShow)
	bringToFront(aiWin.hwnd)
}

func createAIWindow() error {
	instance, _, _ := pGetModuleHandle.Call(0)
	if !aiWin.registered {
		cursor, _, _ := pLoadCursorW.Call(0, idcArrow)
		bigIcon, smallIcon := appIcons(instance)
		cls := wndClassEx{
			Size:       uint32(unsafe.Sizeof(wndClassEx{})),
			Style:      csHRedraw | csVRedraw,
			WndProc:    syscall.NewCallback(aiProc),
			Instance:   instance,
			Cursor:     cursor,
			Icon:       bigIcon,
			IconSm:     smallIcon,
			Background: 16, // COLOR_BTNFACE + 1: the standard window grey
			ClassName:  utf16(aiClassName),
		}
		if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&cls))); r == 0 {
			return err
		}
		aiWin.registered = true
	}

	style := uintptr(wsCaption | wsSysMenu | wsClipChildren)
	frame := rect{0, 0, px(aiW), px(aiH)}
	pAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&frame)), style, 0, 0)

	hwnd, _, err := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16(aiClassName))),
		uintptr(unsafe.Pointer(utf16("AI answers"))),
		style,
		cwUseDefault, cwUseDefault, uintptr(frame.width()), uintptr(frame.height()),
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return err
	}
	aiWin.hwnd = hwnd
	aiWin.ctl = map[int]uintptr{}
	createAIControls(instance)
	return nil
}

// aiChild creates one control inside the AI window.
func aiChild(instance uintptr, class, text string, style uintptr, x, y, w, h int, id int) uintptr {
	hwnd, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16(class))),
		uintptr(unsafe.Pointer(utf16(text))),
		wsChild|wsVisible|style,
		uintptr(px(x)), uintptr(px(y)), uintptr(px(w)), uintptr(px(h)),
		aiWin.hwnd, uintptr(id), instance, 0,
	)
	pSendMessageW.Call(hwnd, wmSetFont, fonts.body, 1)
	aiWin.ctl[id] = hwnd
	return hwnd
}

func createAIControls(instance uintptr) {
	label := func(text string, x, y, w int) {
		aiChild(instance, "STATIC", text, 0, x, y, w, 20, 0)
	}
	radio := func(text string, first bool, x, y, w, id int) {
		style := uintptr(bsAutoRadio | wsTabStop)
		if first {
			style |= wsGroup
		}
		aiChild(instance, "BUTTON", text, style, x, y, w, 22, id)
	}

	label("Answer source", 20, 16, 460)
	radio("AI model", true, 20, 38, 130, aiIDSourceAI)
	radio("Wikipedia", false, 160, 38, 120, aiIDSourceWiki)
	radio("Google (needs Serper key)", false, 290, 38, 190, aiIDSourceGoogle)

	label("Model provider", 20, 72, 460)
	// Providers: two columns of radio buttons.
	for i, p := range aiProviders {
		x := 20
		if i%2 == 1 {
			x = 250
		}
		y := 94 + (i/2)*26
		radio(p.label, i == 0, x, y, 230, aiIDProviderBase+i)
	}

	label("Model name", 20, 236, 460)
	aiChild(instance, "EDIT", "", esAutoHScroll|wsBorder|wsTabStop, 20, 256, 460, 24, aiIDModel)
	label("Leave empty for the provider's default. For Ollama, use the name from 'ollama list',", 20, 284, 460)
	label("for example llama3.2:latest.", 20, 300, 460)

	label("API key", 20, 328, 460)
	aiChild(instance, "EDIT", "", esAutoHScroll|esPassword|wsBorder|wsTabStop, 20, 348, 460, 24, aiIDKey)
	label("Stored in settings.json on this PC. Ollama doesn't need a key.", 20, 376, 460)

	aiWin.status = aiChild(instance, "STATIC", "", 0, 20, 402, 460, 40, 0)
	aiChild(instance, "BUTTON", "Save", bsDefPushButton|wsTabStop, 20, 448, 110, 30, aiIDSave)
	aiChild(instance, "BUTTON", "Close", wsTabStop, 140, 448, 110, 30, aiIDClose)
}

// loadAIControls fills the controls from the saved settings.
func loadAIControls() {
	checkChoice(aiSourceIDs, aiSourceValues, currentSource())
	checkChoice(aiProviderIDs, aiProviderVals, providerOrDefault())
	setWindowTextValue(aiWin.ctl[aiIDModel], settings.app.AIModel)
	setWindowTextValue(aiWin.ctl[aiIDKey], settings.app.AIAPIKey)
	setAIStatus("")
}

func providerOrDefault() string {
	for _, v := range aiProviderVals {
		if v == settings.app.AIProvider {
			return v
		}
	}
	return "anthropic"
}

// checkChoice ticks the radio button for value and clears the others.
func checkChoice(ids []int, values []string, value string) {
	for i, id := range ids {
		check := uintptr(0)
		if values[i] == value {
			check = bstChecked
		}
		pSendMessageW.Call(aiWin.ctl[id], bmSetCheck, check, 0)
	}
}

// pickedChoice returns the value of the ticked radio button.
func pickedChoice(ids []int, values []string, fallback string) string {
	for i, id := range ids {
		r, _, _ := pSendMessageW.Call(aiWin.ctl[id], bmGetCheck, 0, 0)
		if r == bstChecked {
			return values[i]
		}
	}
	return fallback
}

func setAIStatus(text string) {
	setWindowTextValue(aiWin.status, text)
}

// saveAIControls writes the window's choices to settings.json.
func saveAIControls() {
	settings.app.AnswerSource = pickedChoice(aiSourceIDs, aiSourceValues, srcWikipedia)
	settings.app.AIProvider = pickedChoice(aiProviderIDs, aiProviderVals, "anthropic")
	settings.app.AIModel = strings.TrimSpace(getWindowTextValue(aiWin.ctl[aiIDModel]))
	settings.app.AIAPIKey = strings.TrimSpace(getWindowTextValue(aiWin.ctl[aiIDKey]))

	if err := saveSettingsFile(settings.path, settings.app); err != nil {
		setAIStatus("Couldn't save the settings: " + err.Error())
		return
	}
	msg := "Saved. Questions starting with ~ now use " + sourceName(settings.app.AnswerSource) + "."
	if settings.app.AnswerSource == srcAI && settings.app.AIProvider == "ollama" {
		msg += " Ollama must be running for AI answers."
	}
	setAIStatus(msg)
}

func sourceName(src string) string {
	switch src {
	case srcAI:
		return "the AI model"
	case srcGoogle:
		return "Google"
	}
	return "Wikipedia"
}

// aiProc handles messages for the AI answers window.
func aiProc(hwnd, m, wp, lp uintptr) uintptr {
	switch uint32(m) {
	case wmCommand:
		if hiword(wp) == bnClicked {
			switch int(loword(wp)) {
			case aiIDSave:
				saveAIControls()
			case aiIDClose:
				pShowWindow.Call(hwnd, swHide)
			}
		}
		return 0
	case wmClose:
		pShowWindow.Call(hwnd, swHide)
		return 0
	}
	ret, _, _ := pDefWindowProcW.Call(hwnd, uintptr(m), wp, lp)
	return ret
}
