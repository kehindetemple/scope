//go:build windows

// Scope is a native Windows app: one .exe, no browser engine.
// Press the hotkey to open a search box over everything on your drives. The
// dashboard holds the hotkey, the light and dark theme and the colors.
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"go-file-search/internal/engine"
	"go-file-search/internal/hotkey"
)

const singleInstanceName = "GoFileSearchSingleInstance"

var (
	eng         *engine.Engine
	mutexHandle windows.Handle

	pCreateMutexW    = windows.NewLazySystemDLL("kernel32.dll").NewProc("CreateMutexW")
	pGetModuleHandle = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")
)

// logf writes to the log file. The app runs without a console window, so the
// log file is the only place errors are visible.
func logf(format string, args ...any) {
	log.Printf(format, args...)
}

func main() {
	runtime.LockOSThread()

	setupLog()

	if alreadyRunning() {
		// The app is already running: open its dashboard instead of starting a second copy.
		if hwnd := findSettingsWindow(); hwnd != 0 {
			pPostMessageW.Call(hwnd, wmAppSettings, 0, 0)
		}
		return
	}

	instance, _, _ := pGetModuleHandle.Call(0)
	pCoInitializeEx.Call(0, coinitApartmentThreaded)

	// Sizes, fonts and colors come first: the windows are built from them.
	initDPI()
	initLayout()
	initFonts()
	initGraphics()
	settings.path = settingsPath()
	settings.app = loadSettingsFile(settings.path)
	rebuildPalette()

	if err := registerSearchWindowClass(instance, syscall.NewCallback(searchProc)); err != nil {
		logf("register search window: %v", err)
		return
	}
	if err := registerSettingsWindowClass(instance, syscall.NewCallback(settingsProc)); err != nil {
		logf("register settings window: %v", err)
		return
	}
	if err := createSearchWindow(instance, 0); err != nil {
		logf("create search window: %v", err)
		return
	}
	if err := createSettingsWindow(instance, 0); err != nil {
		logf("create settings window: %v", err)
		return
	}
	applyWindowTheme()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eng = engine.New(engine.Options{
		SnapshotPath:     snapshotPath(),
		SnapshotInterval: 5 * time.Minute,
		PollInterval:     500 * time.Millisecond,
		WalkRefresh:      30 * time.Minute,
	})
	if err := eng.Start(ctx); err != nil {
		logf("indexing could not start: %v", err)
	}
	startCatalog(ctx) // Start menu apps and Settings pages, which the file index cannot see

	hk, err := hotkey.NewManager(func() {
		pPostMessageW.Call(search.hwnd, wmAppToggle, 0, 0)
	})
	if err != nil {
		logf("hotkeys unavailable: %v", err)
		setMessage("Hotkeys are not available: "+err.Error(), msgError)
	} else {
		settings.hotkeys = hk
		spec := settings.app.spec()
		combo, perr := hotkey.Parse(spec)
		if perr == nil {
			perr = hk.Set(combo)
		}
		if perr != nil {
			logf("could not register hotkey %s: %v", hotkey.Format(spec), perr)
			setMessage(perr.Error(), msgError)
		}
	}

	showSettings()

	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // WM_QUIT or error
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	cancel()
	if err := eng.Save(); err != nil {
		logf("could not save index: %v", err)
	}
}

// alreadyRunning reports whether another copy holds the single-instance mutex.
func alreadyRunning() bool {
	_, _, err := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(utf16(singleInstanceName))))
	return err == windows.ERROR_ALREADY_EXISTS
}

func findSettingsWindow() uintptr {
	hwnd, _, _ := pFindWindowW.Call(
		uintptr(unsafe.Pointer(utf16(settingsClassName))),
		0,
	)
	return hwnd
}

func setupLog() {
	dir := appDir(os.UserCacheDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "scope.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags)
}

func settingsPath() string {
	return filepath.Join(appDir(os.UserConfigDir), "settings.json")
}

func snapshotPath() string {
	return filepath.Join(appDir(os.UserCacheDir), "index.snap")
}

// appDir returns a folder named after the app inside the user's config or
// cache directory (%AppData% or %LocalAppData% on Windows).
func appDir(base func() (string, error)) string {
	dir, err := base()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "scope")
}
