//go:build windows

package main

import (
	"os/exec"
	"strings"
	"sync"

	"go-file-search/internal/answer"
)

// A search that starts with ~ asks a question instead of searching files.
// The answer comes from the source chosen in "AI answers": the AI model,
// Wikipedia or Google (Serper).

const (
	webTimerID  = 2   // waits for typing to pause before a Wikipedia or Google lookup
	webPauseMs  = 500 // how long typing must pause before that lookup starts
	dtWordBreak = 0x0010
)

// answer sources
const (
	srcWikipedia = "wikipedia"
	srcGoogle    = "google"
	srcAI        = "ai"
)

// states of the web answer
const (
	webIdle    = iota // nothing to show yet: only "~", or waiting for Enter (AI)
	webLoading        // fetching the answer
	webDone           // an answer is shown
	webFailed         // the lookup failed; webError says why
)

// webOutcome is what the lookup goroutine hands back to the UI thread.
type webOutcome struct {
	query  string
	result answer.Result
	err    error
}

var (
	webMu     sync.Mutex // guards webLatest, which the lookup goroutine writes
	webLatest webOutcome
)

// currentSource is the answer source chosen in AI answers.
func currentSource() string {
	switch settings.app.AnswerSource {
	case srcAI, srcGoogle:
		return settings.app.AnswerSource
	}
	return srcWikipedia
}

// sourceLabel is the heading shown above a finished answer.
func sourceLabel() string {
	switch currentSource() {
	case srcAI:
		return "AI answer"
	case srcGoogle:
		return "Google answer"
	}
	return "Wikipedia"
}

// isWebQuery reports whether the search text asks a question.
func isWebQuery(q string) bool {
	return strings.HasPrefix(q, "~")
}

// startWebMode runs when the search box text starts with ~.
func startWebMode(text string) {
	q := strings.TrimSpace(strings.TrimPrefix(text, "~"))

	search.webMode = true
	search.hasQuery = true
	search.results = nil
	search.selected = 0
	search.scrollTarget = 0
	search.scrollPos = 0
	stopScrollAnimation()
	showPlaceholder(false)

	search.webQuery = q
	search.webAnswer = answer.Result{}
	search.webError = ""
	switch {
	case q == "":
		search.webState = webIdle
		pKillTimer.Call(search.hwnd, webTimerID)
	case currentSource() == srcAI:
		// The AI model is asked on Enter, so a pause in typing costs nothing.
		search.webState = webIdle
		pKillTimer.Call(search.hwnd, webTimerID)
	default:
		// Setting the timer again on every keystroke restarts the pause.
		search.webState = webLoading
		pSetTimer.Call(search.hwnd, webTimerID, webPauseMs, 0)
	}
	resizeSearch()
	invalidate(search.hwnd)
}

// onWebTimer starts a Wikipedia or Google lookup once typing has paused.
func onWebTimer() {
	pKillTimer.Call(search.hwnd, webTimerID)
	if !search.webMode || search.webQuery == "" {
		return
	}
	startLookup(search.webQuery)
}

// startLookup fetches the answer for q in the background.
func startLookup(q string) {
	search.webState = webLoading
	search.webAnswer = answer.Result{}
	search.webError = ""
	resizeSearch()
	invalidate(search.hwnd)

	// Read the settings here on the UI thread; the goroutine must not touch them.
	src := currentSource()
	key := settings.app.SearchAPIKey
	cfg := answer.AIConfig{
		Provider: settings.app.AIProvider,
		Model:    settings.app.AIModel,
		APIKey:   settings.app.AIAPIKey,
		BaseURL:  settings.app.AIBaseURL,
	}
	hwnd := search.hwnd
	go func() {
		var res answer.Result
		var err error
		switch src {
		case srcAI:
			res, err = answer.AskAI(q, cfg)
		case srcGoogle:
			res, err = answer.Fetch(q, key)
		default:
			res, err = answer.Fetch(q, "") // Wikipedia only, even if a Google key is set
		}
		if err != nil {
			logf("web answer (%s): %v", src, err) // the question itself is not logged
		}
		webMu.Lock()
		webLatest = webOutcome{query: q, result: res, err: err}
		webMu.Unlock()
		pPostMessageW.Call(hwnd, wmAppWeb, 0, 0)
	}()
}

// onWebResult shows a finished lookup, unless the text has changed since.
func onWebResult() {
	webMu.Lock()
	out := webLatest
	webMu.Unlock()
	if !search.webMode || out.query != search.webQuery {
		return
	}
	if out.err == nil {
		search.webState = webDone
		search.webAnswer = out.result
	} else {
		search.webState = webFailed
		search.webError = answer.Describe(out.err)
	}
	resizeSearch()
	invalidate(search.hwnd)
}

// webBodyHeight is the height of the web panel below the search box. A
// finished answer is as tall as the results list; the messages are short.
func webBodyHeight() int32 {
	if search.webState == webDone {
		return sectionHeight + int32(maxRows)*rowHeight + footerHeight
	}
	return messageHeight
}

// openWebAnswer handles Enter or a click in web mode. A finished Wikipedia or
// Google answer opens its page. With the AI source, Enter asks the model.
// It reports whether the box is in web mode, so the caller stops there.
func openWebAnswer() bool {
	if !search.webMode {
		return false
	}
	switch {
	case search.webState == webDone && search.webAnswer.URL != "":
		u := search.webAnswer.URL
		hideSearch()
		if err := exec.Command("explorer.exe", u).Start(); err != nil {
			logf("could not open web answer: %v", err)
		}
	case currentSource() == srcAI && search.webQuery != "" &&
		(search.webState == webIdle || search.webState == webFailed):
		startLookup(search.webQuery)
	}
	return true
}

// paintWebContents draws the web panel under the search box.
func paintWebContents(cv *canvas, dc uintptr, w, h int32) {
	cv.fillRect(rect{0, inputHeight, w, inputHeight + 1}, pal.separator)

	if search.webState != webDone {
		msg := "Enter something to be searched after the ~"
		switch search.webState {
		case webLoading:
			switch currentSource() {
			case srcAI:
				msg = "Asking the AI model…"
			case srcGoogle:
				msg = "Searching Google…"
			default:
				msg = "Searching Wikipedia…"
			}
		case webFailed:
			msg = search.webError
		case webIdle:
			if search.webQuery != "" && currentSource() == srcAI {
				msg = "Press Enter to ask the AI model"
			}
		}
		drawText(dc, fonts.name, pal.resultMuted, msg,
			rect{padX, inputHeight + 1, w - padX, h}, dtCenter|dtSingleLine|dtVCenter)
		return
	}

	top := rowsTop()
	bottom := top + int32(maxRows)*rowHeight
	drawText(dc, fonts.small, pal.resultMuted, sourceLabel(),
		rect{padX, inputHeight + 1, w, top}, dtLeft|dtSingleLine|dtVCenter)
	drawText(dc, fonts.head, pal.resultText, search.webAnswer.Title,
		rect{padX, top + px(8), w - padX, top + px(34)}, dtLeft|dtSingleLine|dtEndEllipsis|dtNoPrefix)
	drawText(dc, fonts.name, pal.resultText, search.webAnswer.Text,
		rect{padX, top + px(40), w - padX, bottom - px(8)}, dtLeft|dtWordBreak|dtNoPrefix)
	drawFooter(cv, dc, bottom, w)
}
