//go:build windows

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"unsafe"

	_ "embed"
)

// microscopePNG is the app's own icon (assets/microscope.png), a 256 px image with a
// transparent background. It is used on the dashboard, and for results that
// have no icon of their own.
//
//go:embed assets/microscope.png
var microscopePNG []byte

var (
	microscopeImg  image.Image
	microscopeBmps = map[int]uintptr{}
)

// microscopeBitmap returns the app icon drawn at px x px pixels. Each size is
// made once from the full-resolution image and then kept, so drawing it at
// that size is 1:1 and stays sharp.
func microscopeBitmap(px int) uintptr {
	if b, ok := microscopeBmps[px]; ok {
		return b
	}
	if microscopeImg == nil {
		img, err := png.Decode(bytes.NewReader(microscopePNG))
		if err != nil {
			logf("app icon could not be decoded: %v", err)
			microscopeBmps[px] = 0
			return 0
		}
		microscopeImg = img
	}
	b := bitmapFromImage(resizeBox(microscopeImg, px))
	microscopeBmps[px] = b
	return b
}

// bitmapHeader mirrors BITMAPINFOHEADER, which CreateDIBSection reads.
type bitmapHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// bitmapFromImage copies an image into a top-down 32-bit DIB section, with
// premultiplied alpha as AlphaBlend requires.
func bitmapFromImage(img image.Image) uintptr {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	hdr := bitmapHeader{
		Size:     uint32(unsafe.Sizeof(bitmapHeader{})),
		Width:    int32(w),
		Height:   -int32(h), // negative height: rows go top to bottom
		Planes:   1,
		BitCount: 32,
	}

	var bits unsafe.Pointer
	hdc, _, _ := pCreateCompatibleDC.Call(0)
	defer pDeleteDC.Call(hdc)

	bmp, _, _ := pCreateDIBSection.Call(
		hdc,
		uintptr(unsafe.Pointer(&hdr)),
		0, // DIB_RGB_COLORS
		uintptr(unsafe.Pointer(&bits)),
		0, 0,
	)
	if bmp == 0 || bits == nil {
		return 0
	}

	px := unsafe.Slice((*byte)(bits), w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			o := (y*w + x) * 4
			px[o+0] = uint8(uint32(c.B) * uint32(c.A) / 255)
			px[o+1] = uint8(uint32(c.G) * uint32(c.A) / 255)
			px[o+2] = uint8(uint32(c.R) * uint32(c.A) / 255)
			px[o+3] = c.A
		}
	}
	return bmp
}

// appIcons loads the microscope icon embedded in the .exe (see the .syso file) at
// the sizes Windows uses for the title bar (small) and Alt+Tab / taskbar (big).
// Both are 0 if the resource is missing, and Windows then uses its default.
func appIcons(instance uintptr) (big, small uintptr) {
	load := func(px int) uintptr {
		h, _, _ := pLoadImageW.Call(instance, appIconResource, imageIcon, uintptr(px), uintptr(px), 0)
		return h
	}
	return load(32), load(16)
}
