//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Smooth shapes. GDI draws rounded rectangles and circles with jagged edges,
// so shapes go through GDI+ with anti-aliasing when it is available, and fall
// back to plain GDI when it is not. Text is always drawn with GDI so that it
// keeps ClearType.

var (
	gdiplusDLL            = windows.NewLazySystemDLL("gdiplus.dll")
	pGdiplusStartup       = gdiplusDLL.NewProc("GdiplusStartup")
	pGdipCreateFromHDC    = gdiplusDLL.NewProc("GdipCreateFromHDC")
	pGdipDeleteGraphics   = gdiplusDLL.NewProc("GdipDeleteGraphics")
	pGdipSetSmoothingMode = gdiplusDLL.NewProc("GdipSetSmoothingMode")
	pGdipCreateSolidFill  = gdiplusDLL.NewProc("GdipCreateSolidFill")
	pGdipDeleteBrush      = gdiplusDLL.NewProc("GdipDeleteBrush")
	pGdipFillRectangleI   = gdiplusDLL.NewProc("GdipFillRectangleI")
	pGdipFillEllipseI     = gdiplusDLL.NewProc("GdipFillEllipseI")
	pGdipFlush            = gdiplusDLL.NewProc("GdipFlush")
)

var gdiplusOK bool

type gdiplusStartupInput struct {
	Version                 uint32
	DebugEventCallback      uintptr
	SuppressBackgroundThread int32
	SuppressExternalCodecs  int32
}

// initGraphics starts GDI+. Without it, shapes are drawn with plain GDI.
func initGraphics() {
	if gdiplusDLL.Load() != nil {
		return
	}
	in := gdiplusStartupInput{Version: 1}
	var token uintptr
	r, _, _ := pGdiplusStartup.Call(uintptr(unsafe.Pointer(&token)), uintptr(unsafe.Pointer(&in)), 0)
	gdiplusOK = r == 0
}

// argb converts a COLORREF (0x00BBGGRR) to the opaque ARGB value GDI+ uses.
func argb(c uintptr) uintptr {
	r, g, b := c&0xFF, (c>>8)&0xFF, (c>>16)&0xFF
	return 0xFF000000 | r<<16 | g<<8 | b
}

// canvas draws shapes onto a device context.
type canvas struct {
	dc uintptr
	g  uintptr // GDI+ graphics for dc, or 0
}

func newCanvas(dc uintptr) *canvas {
	c := &canvas{dc: dc}
	if gdiplusOK {
		var g uintptr
		r, _, _ := pGdipCreateFromHDC.Call(dc, uintptr(unsafe.Pointer(&g)))
		if r == 0 && g != 0 {
			pGdipSetSmoothingMode.Call(g, 4) // SmoothingModeAntiAlias
			c.g = g
		}
	}
	return c
}

func (c *canvas) close() {
	if c.g != 0 {
		pGdipDeleteGraphics.Call(c.g)
		c.g = 0
	}
}

func (c *canvas) gpBrush(color uintptr) uintptr {
	var b uintptr
	r, _, _ := pGdipCreateSolidFill.Call(argb(color), uintptr(unsafe.Pointer(&b)))
	if r != 0 {
		return 0
	}
	return b
}

// fillRect fills a plain rectangle.
func (c *canvas) fillRect(r rect, color uintptr) {
	brush, _, _ := pCreateSolidBrush.Call(color)
	fill(c.dc, r, brush)
	pDeleteObject.Call(brush)
}

// fillRound fills a rectangle with rounded corners.
func (c *canvas) fillRound(r rect, radius int32, color uintptr) {
	w, h := r.width(), r.height()
	if w <= 0 || h <= 0 {
		return
	}
	radius = min(radius, w/2, h/2)
	if radius <= 1 {
		c.fillRect(r, color)
		return
	}

	if c.g != 0 {
		if brush := c.gpBrush(color); brush != 0 {
			d := radius * 2
			rectI := func(x, y, rw, rh int32) {
				pGdipFillRectangleI.Call(c.g, brush, uintptr(x), uintptr(y), uintptr(rw), uintptr(rh))
			}
			ellI := func(x, y int32) {
				pGdipFillEllipseI.Call(c.g, brush, uintptr(x), uintptr(y), uintptr(d), uintptr(d))
			}
			rectI(r.Left+radius, r.Top, w-d, h)
			rectI(r.Left, r.Top+radius, radius, h-d)
			rectI(r.Right-radius, r.Top+radius, radius, h-d)
			ellI(r.Left, r.Top)
			ellI(r.Right-d, r.Top)
			ellI(r.Left, r.Bottom-d)
			ellI(r.Right-d, r.Bottom-d)
			pGdipDeleteBrush.Call(brush)
			pGdipFlush.Call(c.g, 1)
			return
		}
	}

	brush, _, _ := pCreateSolidBrush.Call(color)
	roundFill(c.dc, r, radius, brush)
	pDeleteObject.Call(brush)
}

// fillCircle fills the ellipse inscribed in r.
func (c *canvas) fillCircle(r rect, color uintptr) {
	if r.width() <= 0 || r.height() <= 0 {
		return
	}
	if c.g != 0 {
		if brush := c.gpBrush(color); brush != 0 {
			pGdipFillEllipseI.Call(c.g, brush, uintptr(r.Left), uintptr(r.Top), uintptr(r.width()), uintptr(r.height()))
			pGdipDeleteBrush.Call(brush)
			pGdipFlush.Call(c.g, 1)
			return
		}
	}
	brush, _, _ := pCreateSolidBrush.Call(color)
	oldBrush, _, _ := pSelectObject.Call(c.dc, brush)
	nullPen, _, _ := pGetStockObject.Call(stockNullPen)
	oldPen, _, _ := pSelectObject.Call(c.dc, nullPen)
	pEllipse.Call(c.dc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom))
	pSelectObject.Call(c.dc, oldPen)
	pSelectObject.Call(c.dc, oldBrush)
	pDeleteObject.Call(brush)
}

// outlineRound fills a rounded rectangle with a 1 px border.
func (c *canvas) outlineRound(r rect, radius int32, border, fillColor uintptr) {
	c.fillRound(r, radius, border)
	c.fillRound(inset(r, 1), radius-1, fillColor)
}

// inset shrinks r by d on every side (a negative d grows it).
func inset(r rect, d int32) rect {
	return rect{r.Left + d, r.Top + d, r.Right - d, r.Bottom - d}
}

// roundFill fills a rounded rectangle with plain GDI. It is the fallback when
// GDI+ is not available.
func roundFill(dc uintptr, r rect, radius int32, brush uintptr) {
	oldBrush, _, _ := pSelectObject.Call(dc, brush)
	nullPen, _, _ := pGetStockObject.Call(stockNullPen)
	oldPen, _, _ := pSelectObject.Call(dc, nullPen)
	pRoundRect.Call(dc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right+1), uintptr(r.Bottom+1),
		uintptr(radius*2), uintptr(radius*2))
	pSelectObject.Call(dc, oldPen)
	pSelectObject.Call(dc, oldBrush)
}

// measureText returns the pixel width of a single line of text.
func measureText(dc, font uintptr, text string) int32 {
	old, _, _ := pSelectObject.Call(dc, font)
	defer pSelectObject.Call(dc, old)

	var r rect
	s := utf16(text)
	pDrawTextW.Call(dc, uintptr(unsafe.Pointer(s)), uintptr(0xFFFFFFFF),
		uintptr(unsafe.Pointer(&r)), dtCalcRect|dtSingleLine)
	return r.width()
}

func fill(dc uintptr, r rect, brush uintptr) {
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&r)), brush)
}

func drawText(dc, font uintptr, color uintptr, text string, r rect, flags uintptr) {
	old, _, _ := pSelectObject.Call(dc, font)
	pSetTextColor.Call(dc, color)
	s := utf16(text)
	pDrawTextW.Call(dc, uintptr(unsafe.Pointer(s)), uintptr(0xFFFFFFFF), uintptr(unsafe.Pointer(&r)), flags)
	pSelectObject.Call(dc, old)
}
