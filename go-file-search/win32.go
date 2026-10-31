//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// gwlpWndProcArg is GWLP_WNDPROC (-4) as the uintptr the API expects.
var gwlpWndProcArg = ^uintptr(3)

// Windows API bindings. Only what the app uses is declared here.

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	msimg32  = windows.NewLazySystemDLL("msimg32.dll")

	pRegisterClassExW     = user32.NewProc("RegisterClassExW")
	pCreateWindowExW      = user32.NewProc("CreateWindowExW")
	pDefWindowProcW       = user32.NewProc("DefWindowProcW")
	pShowWindow           = user32.NewProc("ShowWindow")
	pSetWindowPos         = user32.NewProc("SetWindowPos")
	pPostQuitMessage      = user32.NewProc("PostQuitMessage")
	pGetMessageW          = user32.NewProc("GetMessageW")
	pTranslateMessage     = user32.NewProc("TranslateMessage")
	pDispatchMessageW     = user32.NewProc("DispatchMessageW")
	pPostMessageW         = user32.NewProc("PostMessageW")
	pSendMessageW         = user32.NewProc("SendMessageW")
	pSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	pGetForegroundWindow  = user32.NewProc("GetForegroundWindow")
	pGetWindowThreadPID   = user32.NewProc("GetWindowThreadProcessId")
	pAttachThreadInput    = user32.NewProc("AttachThreadInput")
	pSetFocus             = user32.NewProc("SetFocus")
	pGetCursorPos         = user32.NewProc("GetCursorPos")
	pMonitorFromPoint     = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfoW      = user32.NewProc("GetMonitorInfoW")
	pBeginPaint           = user32.NewProc("BeginPaint")
	pEndPaint             = user32.NewProc("EndPaint")
	pInvalidateRect       = user32.NewProc("InvalidateRect")
	pFillRect             = user32.NewProc("FillRect")
	pDrawTextW            = user32.NewProc("DrawTextW")
	pSetWindowLongPtrW    = user32.NewProc("SetWindowLongPtrW")
	pCallWindowProcW      = user32.NewProc("CallWindowProcW")
	pGetWindowTextW       = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	pSetWindowTextW       = user32.NewProc("SetWindowTextW")
	pLoadCursorW          = user32.NewProc("LoadCursorW")
	pFindWindowW          = user32.NewProc("FindWindowW")
	pGetKeyState          = user32.NewProc("GetKeyState")
	pEnableWindow         = user32.NewProc("EnableWindow")
	pGetClientRect        = user32.NewProc("GetClientRect")
	pSetLayeredWindowAttrs = user32.NewProc("SetLayeredWindowAttributes")
	pSetWindowRgn         = user32.NewProc("SetWindowRgn")
	pGetDC                = user32.NewProc("GetDC")
	pReleaseDC            = user32.NewProc("ReleaseDC")
	pGetSysColorBrush     = user32.NewProc("GetSysColorBrush")
	pDrawIconEx           = user32.NewProc("DrawIconEx")
	pDestroyIcon          = user32.NewProc("DestroyIcon")
	pSetTimer             = user32.NewProc("SetTimer")
	pKillTimer            = user32.NewProc("KillTimer")
	pAdjustWindowRectEx   = user32.NewProc("AdjustWindowRectEx")
	pTrackMouseEvent      = user32.NewProc("TrackMouseEvent")
	pSetCursor            = user32.NewProc("SetCursor")

	pCreateSolidBrush        = gdi32.NewProc("CreateSolidBrush")
	pCreateFontW             = gdi32.NewProc("CreateFontW")
	pGetStockObject          = gdi32.NewProc("GetStockObject")
	pSelectObject            = gdi32.NewProc("SelectObject")
	pDeleteObject            = gdi32.NewProc("DeleteObject")
	pCreateCompatibleDC      = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap  = gdi32.NewProc("CreateCompatibleBitmap")
	pDeleteDC                = gdi32.NewProc("DeleteDC")
	pBitBlt                  = gdi32.NewProc("BitBlt")
	pSetTextColor            = gdi32.NewProc("SetTextColor")
	pSetBkColor              = gdi32.NewProc("SetBkColor")
	pSetBkMode               = gdi32.NewProc("SetBkMode")
	pCreateRoundRectRgn      = gdi32.NewProc("CreateRoundRectRgn")
	pRoundRect               = gdi32.NewProc("RoundRect")
	pIntersectClipRect       = gdi32.NewProc("IntersectClipRect")
	pSaveDC                  = gdi32.NewProc("SaveDC")
	pRestoreDC               = gdi32.NewProc("RestoreDC")
	pGetDIBits               = gdi32.NewProc("GetDIBits")
	pEllipse                 = gdi32.NewProc("Ellipse")
	pMoveToEx                = gdi32.NewProc("MoveToEx")
	pLineTo                  = gdi32.NewProc("LineTo")
	pCreatePen               = gdi32.NewProc("CreatePen")
	pCreateDIBSection        = gdi32.NewProc("CreateDIBSection")
	pGetObjectW              = gdi32.NewProc("GetObjectW")
	pAlphaBlend              = msimg32.NewProc("AlphaBlend")

	pSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
	pSHGetFileInfoW              = shell32.NewProc("SHGetFileInfoW")
	pLoadImageW                  = user32.NewProc("LoadImageW")
	pCoInitializeEx              = ole32.NewProc("CoInitializeEx")
	pGetCurrentThreadId          = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentThreadId")
)

// Window messages and styles.
const (
	wmDestroy       = 0x0002
	wmPaint         = 0x000F
	wmClose         = 0x0010
	wmEraseBkgnd    = 0x0014
	wmActivate      = 0x0006
	wmKeyDown       = 0x0100
	wmChar          = 0x0102
	wmCommand       = 0x0111
	wmCtlColorEdit  = 0x0133
	wmCtlColorBtn   = 0x0135
	wmCtlColorStat  = 0x0138
	wmMouseMove     = 0x0200
	wmLButtonDown   = 0x0201
	wmMouseLeave    = 0x02A3
	wmTimer         = 0x0113
	wmSetCursor     = 0x0020
	wmMouseWheel    = 0x020A
	wmLButtonUp     = 0x0202
	wmSetFont       = 0x0030
	wmApp           = 0x8000
	wmAppToggle     = wmApp + 1 // show or hide the search box
	wmAppSettings   = wmApp + 2 // show the settings window
	wmAppIcons      = wmApp + 3 // the icon loader finished some icons
	wmAppCapture    = wmApp + 4 // the hotkey recorder has something to report
	wmAppWeb        = wmApp + 5 // a web answer for the ~ search is ready

	enChange = 0x0300
	bnClicked = 0

	waInactive = 0

	wsPopup         = 0x80000000
	wsChild         = 0x40000000
	wsVisible       = 0x10000000
	wsTabStop       = 0x00010000
	wsOverlappedWin = 0x00CF0000
	wsDashboard     = 0x00CA0000 // title bar, close and minimize buttons, fixed size
	wsClipChildren  = 0x02000000 // painting the parent must not paint over its child controls
	wsExToolWindow  = 0x00000080
	wsExTopmost     = 0x00000008
	wsExLayered     = 0x00080000

	lwaAlpha = 0x00000002
	emSetCueBanner = 0x1501 // EM_SETCUEBANNER: placeholder text in an edit box
	wsExTransparent = 0x00000020 // mouse clicks pass through to the window below
	diNormal        = 0x0003
	shgfiIcon       = 0x000000100
	shgfiUseFileAttributes = 0x00000010 // describe a file type without touching the disk
	fileAttrDirectory      = 0x10
	fileAttrNormal         = 0x80
	imageIcon       = 1 // LoadImageW type: icon
	appIconResource = 1 // id of the microscope icon group in the .syso resource
	psSolid         = 0
	stockNullBrush  = 5
	stockNullPen    = 8

	esAutoHScroll = 0x0080
	bsPushButton  = 0x00000000

	swHide = 0
	swShow = 5

	swpNoSize    = 0x0001
	swpNoMove    = 0x0002
	swpShowWin   = 0x0040
	swpNoActivate = 0x0010

	gwlpWndProc = -4

	csHRedraw = 0x0002
	csDropShadow = 0x00020000
	csVRedraw = 0x0001

	dtLeft         = 0x0000
	dtSingleLine   = 0x0020
	dtEndEllipsis  = 0x8000
	dtNoPrefix     = 0x0800
	dtVCenter      = 0x0004
	dtRight        = 0x0002
	dtCenter       = 0x0001
	dtCalcRect     = 0x0400

	transparent = 1
	srcCopy     = 0x00CC0020
	acSrcOver   = 0x00
	acSrcAlpha  = 0x01

	gwDefaultGuiFont = 17
	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12
	vkLWin    = 0x5B
	vkRWin    = 0x5C
	vkReturn  = 0x0D
	vkEscape  = 0x1B
	vkUp      = 0x26
	vkDown    = 0x28
	vkPrior   = 0x21 // Page Up
	vkNext    = 0x22 // Page Down
	vkTab     = 0x09

	hwndTopmost = ^uintptr(0) // (HWND)-1
	coinitApartmentThreaded = 0x2
	idcArrow = 32512
	idcHand  = 32649
)

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

func (r rect) width() int32  { return r.Right - r.Left }
func (r rect) height() int32 { return r.Bottom - r.Top }

type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// paintStruct is large enough for the Win32 PAINTSTRUCT.
type paintStruct [72]byte

func utf16(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return windows.StringToUTF16Ptr("")
	}
	return p
}

// rgb builds a COLORREF value.
func rgb(r, g, b uint8) uintptr {
	return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16
}

// loword and hiword extract the packed fields of wParam and lParam.
func loword(v uintptr) uintptr { return v & 0xFFFF }
func hiword(v uintptr) uintptr { return (v >> 16) & 0xFFFF }

// signedY returns the signed y coordinate packed in a mouse message's lParam.
func signedY(lp uintptr) int32 { return int32(int16(hiword(lp))) }

func createFont(height int32, weight int32, face string) uintptr {
	f, _, _ := pCreateFontW.Call(
		uintptr(height), 0, 0, 0,
		uintptr(weight), 0, 0, 0,
		1,          // DEFAULT_CHARSET
		0, 0,       // precision
		5,          // CLEARTYPE_QUALITY
		0,          // pitch and family
		uintptr(unsafe.Pointer(utf16(face))),
	)
	return f
}

func setWindowTextValue(hwnd uintptr, s string) {
	pSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(utf16(s))))
}

func getWindowTextValue(hwnd uintptr) string {
	n, _, _ := pGetWindowTextLengthW.Call(hwnd)
	buf := make([]uint16, n+1)
	pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf)
}

func isKeyDown(vk uintptr) bool {
	s, _, _ := pGetKeyState.Call(vk)
	return int16(s) < 0
}

// bringToFront makes hwnd the foreground window. Windows only allows this from
// the foreground app's thread, so the input state is briefly shared with it.
func bringToFront(hwnd uintptr) {
	fg, _, _ := pGetForegroundWindow.Call()
	var pid uint32
	tid, _, _ := pGetWindowThreadPID.Call(fg, uintptr(unsafe.Pointer(&pid)))
	me, _, _ := pGetCurrentThreadId.Call()
	if tid != 0 && tid != me {
		pAttachThreadInput.Call(me, tid, 1)
		pSetForegroundWindow.Call(hwnd)
		pAttachThreadInput.Call(me, tid, 0)
	} else {
		pSetForegroundWindow.Call(hwnd)
	}
}

