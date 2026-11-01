//go:build windows

package main

import (
	"image"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// How icons stay sharp: the shell is asked for a big version of each icon
// (128 pixels or more), and this app shrinks it once to the exact size it is
// drawn at, averaging pixels. The bitmap is then drawn 1:1, so nothing is
// stretched at paint time. Stretching a small icon up is what makes icons
// look blurry.

// iidShellItemImageFactory is the interface Windows Explorer uses for icons
// and thumbnails.
var iidShellItemImageFactory = windows.GUID{
	Data1: 0xBCC18B79,
	Data2: 0xBA16,
	Data3: 0x442F,
	Data4: [8]byte{0x80, 0xC4, 0x8A, 0x59, 0xC3, 0x0C, 0x46, 0x3B},
}

// SIIGBF flags for IShellItemImageFactory::GetImage.
const (
	siigbfBiggerSizeOK = 0x01
	siigbfIconOnly     = 0x04
)

// mediaExts are file types that get a real preview instead of their icon.
var mediaExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".bmp": true,
	".webp": true, ".heic": true, ".mp4": true, ".mov": true, ".mkv": true,
	".avi": true, ".webm": true, ".wmv": true, ".m4v": true,
}

// drawable is what the row painter has for one item.
type drawable struct {
	bmp  uintptr // a bitmap of iconSize x iconSize, drawn 1:1 with AlphaBlend
	icon uintptr // an icon handle, only used when the shell gives no image
}

type loadRequest struct {
	key  string
	path string
	dir  bool
}

// maxCachedIcons is how many icons are kept before the cache starts over.
const maxCachedIcons = 1500

// maxQueuedIcons is how many icon requests wait for the loader. When
// scrolling is faster than loading, the oldest requests are dropped.
const maxQueuedIcons = 96

// iconCache keeps one entry per file type, or per file when the icon depends
// on the file itself (applications, images, videos).
//
// Icons are loaded on a background thread, so scrolling never waits for the
// shell. A row whose icon is not ready yet is drawn without one, and the
// window is repainted when the icon arrives.
type iconCache struct {
	mu      sync.Mutex
	items   map[string]drawable
	pending map[string]bool
	queue   []loadRequest // the newest request is last, and is loaded first
	wake    chan struct{}
}

func newIconCache() *iconCache {
	return &iconCache{
		items:   make(map[string]drawable),
		pending: make(map[string]bool),
		wake:    make(chan struct{}, 1),
	}
}

// get returns the icon for an item, or an empty drawable if it is still
// loading. It is called on the UI thread.
func (c *iconCache) get(path string, dir bool) drawable {
	key := cacheKey(path, dir)

	c.mu.Lock()
	if d, ok := c.items[key]; ok {
		c.mu.Unlock()
		return d
	}
	if len(c.items) >= maxCachedIcons {
		// Scrolling through thousands of images must not fill memory. Only
		// the UI thread frees icons, and it uses each one right after get.
		c.freeLocked()
	}
	if !c.pending[key] {
		c.pending[key] = true
		c.queue = append(c.queue, loadRequest{key: key, path: path, dir: dir})
		if len(c.queue) > maxQueuedIcons {
			delete(c.pending, c.queue[0].key)
			c.queue = c.queue[1:]
		}
	}
	c.mu.Unlock()

	select {
	case c.wake <- struct{}{}:
	default:
	}
	return drawable{}
}

func (c *iconCache) freeLocked() {
	for _, d := range c.items {
		if d.bmp != 0 {
			pDeleteObject.Call(d.bmp)
		}
		if d.icon != 0 {
			pDestroyIcon.Call(d.icon)
		}
	}
	c.items = make(map[string]drawable)
}

func (c *iconCache) pop() (loadRequest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.queue)
	if n == 0 {
		return loadRequest{}, false
	}
	req := c.queue[n-1]
	c.queue = c.queue[:n-1]
	return req, true
}

func (c *iconCache) put(key string, d drawable) {
	c.mu.Lock()
	c.items[key] = d
	delete(c.pending, key)
	c.mu.Unlock()
}

// run is the loader thread. After loading it tells the window at hwnd to
// repaint; posted makes sure only one such message is waiting at a time.
func (c *iconCache) run(hwnd uintptr, posted *int32) {
	runtime.LockOSThread()
	pCoInitializeEx.Call(0, 0) // COINIT_MULTITHREADED

	for range c.wake {
		for {
			req, ok := c.pop()
			if !ok {
				break
			}
			c.put(req.key, loadDrawable(req.path, req.dir))
			if atomic.CompareAndSwapInt32(posted, 0, 1) {
				pPostMessageW.Call(hwnd, wmAppIcons, 0, 0)
			}
		}
	}
}

// loadDrawable picks the best icon source for an item, in this order:
//  1. Start menu apps and Settings pages: the icon the shell shows in Start
//  2. a Store app's logo from its package (the real icon, for WindowsApps .exe)
//  3. a real preview for images and videos
//  4. the file's own icon, taken from the shell at a large size
//  5. Windows' generic icon for that kind of item
//
// The app's own microscope icon is never used here: every result shows what Windows
// itself would show for it. It runs on the loader thread.
func loadDrawable(path string, dir bool) drawable {
	if isShellItem(path) {
		target := path
		if strings.HasPrefix(path, settingsURIPrefix) {
			target = settingsAppPath
		}
		if d, ok := shellDrawable(target, siigbfIconOnly|siigbfBiggerSizeOK); ok {
			return d
		}
		return genericDrawable("app.exe", fileAttrNormal)
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".exe" {
		if bmp := appxBitmap(path); bmp != 0 {
			return drawable{bmp: bmp}
		}
	}
	if !dir && mediaExts[ext] {
		if d, ok := shellDrawable(path, 0); ok { // a thumbnail when there is one
			return d
		}
	}
	if d, ok := shellDrawable(path, siigbfIconOnly|siigbfBiggerSizeOK); ok {
		return d
	}

	switch {
	case dir:
		return genericDrawable("folder", fileAttrDirectory)
	case ext == ".exe":
		return genericDrawable("app.exe", fileAttrNormal)
	default:
		return genericDrawable("file"+ext, fileAttrNormal)
	}
}

// shellDrawable asks the shell for a large image of the item and shrinks it
// to the icon size.
func shellDrawable(path string, flags uintptr) (drawable, bool) {
	box := int(iconSize)
	request := int32(min(256, max(128, box*4)))

	hb := shellImage(path, request, flags)
	if hb == 0 {
		return drawable{}, false
	}
	img := imageFromBitmap(hb)
	pDeleteObject.Call(hb)
	if img == nil {
		return drawable{}, false
	}
	bmp := iconBitmapFromImage(img, box)
	if bmp == 0 {
		return drawable{}, false
	}
	return drawable{bmp: bmp}, true
}

// genericDrawable returns Windows' standard icon for a kind of item (folder,
// program, or a file type) without needing the item to exist.
func genericDrawable(name string, attrs uintptr) drawable {
	var info shFileInfo
	r, _, _ := pSHGetFileInfoW.Call(
		uintptr(unsafe.Pointer(utf16(name))),
		attrs,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
		shgfiIcon|shgfiUseFileAttributes,
	)
	if r == 0 || info.Icon == 0 {
		return drawable{}
	}
	return drawable{icon: info.Icon}
}

// cacheKey decides how widely an icon can be shared. Folders share one icon,
// and most documents share one icon per extension. Applications, shortcuts,
// images and videos get their own entry per file.
func cacheKey(path string, dir bool) string {
	if dir {
		return "<folder>"
	}
	if strings.HasPrefix(path, settingsURIPrefix) {
		return "<settings-page>" // every Settings page shares the Settings icon
	}
	if isShellItem(path) {
		return path
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".exe", ".lnk", ".ico":
		return path
	}
	if mediaExts[ext] {
		return path
	}
	return "ext:" + ext
}

// bitmap mirrors the Win32 BITMAP struct, which GetObjectW fills in.
type bitmap struct {
	Type       int32
	Width      int32
	Height     int32
	WidthBytes int32
	Planes     uint16
	BitsPixel  uint16
	Bits       uintptr
}

// bitmapInfo mirrors BITMAPINFO for a 32-bit bitmap.
type bitmapInfo struct {
	Header bitmapHeader
	Colors [1]uint32
}

// imageFromBitmap copies a shell bitmap into an image with straight (not
// premultiplied) alpha. It returns nil if the bitmap cannot be read.
func imageFromBitmap(bmp uintptr) *image.NRGBA {
	var b bitmap
	if r, _, _ := pGetObjectW.Call(bmp, unsafe.Sizeof(b), uintptr(unsafe.Pointer(&b))); r == 0 {
		return nil
	}
	w, h := int(b.Width), int(b.Height)
	if h < 0 {
		h = -h
	}
	if w <= 0 || h <= 0 || w > 2048 || h > 2048 {
		return nil
	}

	info := bitmapInfo{Header: bitmapHeader{
		Size:     uint32(unsafe.Sizeof(bitmapHeader{})),
		Width:    int32(w),
		Height:   -int32(h), // top-down
		Planes:   1,
		BitCount: 32,
	}}
	buf := make([]byte, w*h*4)
	dc, _, _ := pGetDC.Call(0)
	n, _, _ := pGetDIBits.Call(dc, bmp, 0, uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&info)), 0)
	pReleaseDC.Call(0, dc)
	if n == 0 {
		return nil
	}

	// Some bitmaps carry no alpha at all (every alpha byte is 0): those are opaque.
	hasAlpha := false
	for i := 3; i < len(buf); i += 4 {
		if buf[i] != 0 {
			hasAlpha = true
			break
		}
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		bl, gr, re, al := buf[i*4], buf[i*4+1], buf[i*4+2], buf[i*4+3]
		switch {
		case !hasAlpha:
			al = 255
		case al == 0:
			bl, gr, re = 0, 0, 0
		case al < 255: // the shell's bitmaps are premultiplied
			bl, gr, re = unpremultiply(bl, al), unpremultiply(gr, al), unpremultiply(re, al)
		}
		img.Pix[i*4+0] = re
		img.Pix[i*4+1] = gr
		img.Pix[i*4+2] = bl
		img.Pix[i*4+3] = al
	}
	return img
}

func unpremultiply(c, a uint8) uint8 {
	return uint8(min(255, int(c)*255/int(a)))
}

// iconBitmapFromImage shrinks an image to fit a box x box square, keeping its
// proportions and centering it, and turns it into a bitmap.
func iconBitmapFromImage(src image.Image, box int) uintptr {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || box <= 0 {
		return 0
	}
	tw, th := box, box
	switch {
	case w > h:
		th = max(1, h*box/w)
	case h > w:
		tw = max(1, w*box/h)
	}
	scaled := resizeBoxWH(src, tw, th)

	square := image.NewNRGBA(image.Rect(0, 0, box, box))
	ox, oy := (box-tw)/2, (box-th)/2
	for y := 0; y < th; y++ {
		from := scaled.Pix[y*scaled.Stride : y*scaled.Stride+tw*4]
		to := square.Pix[(y+oy)*square.Stride+ox*4:]
		copy(to, from)
	}
	return bitmapFromImage(square)
}

// shellImage asks the shell for an item's image at size x size pixels.
func shellImage(path string, size int32, flags uintptr) uintptr {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}

	var factory unsafe.Pointer
	hr, _, _ := pSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(p)),
		0,
		uintptr(unsafe.Pointer(&iidShellItemImageFactory)),
		uintptr(unsafe.Pointer(&factory)),
	)
	if int32(hr) < 0 || factory == nil {
		return 0
	}
	defer releaseCOM(factory)

	// IShellItemImageFactory vtable: QueryInterface, AddRef, Release, GetImage.
	vtbl := (*[4]uintptr)(*(*unsafe.Pointer)(factory))
	const methodGetImage = 3

	// SIZE is passed by value; on 64-bit Windows it fits in one register.
	sz := uintptr(uint32(size)) | uintptr(uint32(size))<<32

	var bmp uintptr
	hr, _, _ = syscall.SyscallN(
		vtbl[methodGetImage],
		uintptr(factory),
		sz,
		flags,
		uintptr(unsafe.Pointer(&bmp)),
	)
	if int32(hr) < 0 {
		return 0
	}
	return bmp
}

// shFileInfo mirrors the SHFILEINFOW struct used by SHGetFileInfoW.
type shFileInfo struct {
	Icon        uintptr
	IconIndex   int32
	Attributes  uint32
	DisplayName [260]uint16
	TypeName    [80]uint16
}

func releaseCOM(obj unsafe.Pointer) {
	vtbl := (*[3]uintptr)(*(*unsafe.Pointer)(obj))
	syscall.SyscallN(vtbl[2], uintptr(obj)) // IUnknown::Release
}

// drawBitmap paints a bitmap into dst at (x, y), keeping transparency. The
// bitmap is drawn at its own size (srcW x srcH scaled to w x h; pass the same
// values for a sharp 1:1 draw). It must have premultiplied alpha.
func drawBitmap(dstDC, bmp uintptr, x, y, w, h, srcW, srcH int32) {
	srcDC, _, _ := pCreateCompatibleDC.Call(dstDC)
	old, _, _ := pSelectObject.Call(srcDC, bmp)

	// BLENDFUNCTION: BlendOp, BlendFlags, SourceConstantAlpha, AlphaFormat.
	blend := uint32(acSrcOver) | uint32(0)<<8 | uint32(255)<<16 | uint32(acSrcAlpha)<<24
	pAlphaBlend.Call(
		dstDC, uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		srcDC, 0, 0, uintptr(srcW), uintptr(srcH),
		uintptr(blend),
	)

	pSelectObject.Call(srcDC, old)
	pDeleteDC.Call(srcDC)
}

// drawIcon paints an icon handle at (x, y) with size s x s.
func drawIcon(dc, icon uintptr, x, y, s int32) {
	pDrawIconEx.Call(dc, uintptr(x), uintptr(y), icon, uintptr(s), uintptr(s), 0, 0, diNormal)
}

// drawItemIcon draws a result's icon at the icon size. The app's own microscope icon is
// never used here: while an icon is still loading, or if Windows has none,
// nothing is drawn.
func drawItemIcon(dc uintptr, d drawable, x, y int32) {
	switch {
	case d.bmp != 0:
		drawBitmap(dc, d.bmp, x, y, iconSize, iconSize, iconSize, iconSize)
	case d.icon != 0:
		drawIcon(dc, d.icon, x, y, iconSize)
	}
}
