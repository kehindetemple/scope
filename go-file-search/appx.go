//go:build windows

package main

import (
	"encoding/xml"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

// appxManifest holds the parts of AppxManifest.xml that name an app's logo.
// Store apps (Photos, Calculator, ...) live under WindowsApps, and their
// .exe has no usable icon of its own. The real logo is an image in the package.
type appxManifest struct {
	Applications struct {
		Application []struct {
			Executable string `xml:"Executable,attr"`
			Visual     struct {
				Logo150 string `xml:"Square150x150Logo,attr"`
				Logo44  string `xml:"Square44x44Logo,attr"`
			} `xml:"VisualElements"`
		} `xml:"Application"`
	} `xml:"Applications"`
}

// appxBitmap returns the logo of a Store app's .exe as a bitmap, or 0 if the
// file is not in WindowsApps or no logo can be read.
func appxBitmap(exe string) uintptr {
	img := appxLogo(exe)
	if img == nil {
		return 0
	}
	return bitmapFromImage(resizeBox(cropToContent(img), int(iconSize)))
}

// appxLogo finds the logo image for an .exe inside a WindowsApps package.
func appxLogo(exe string) image.Image {
	const marker = `\windowsapps\`
	i := strings.Index(strings.ToLower(exe), marker)
	if i < 0 {
		return nil
	}
	rest := exe[i+len(marker):]
	seg := strings.IndexByte(rest, '\\')
	if seg < 0 {
		return nil
	}
	pkgDir := exe[:i+len(marker)+seg]

	data, err := os.ReadFile(filepath.Join(pkgDir, "AppxManifest.xml"))
	if err != nil {
		return nil
	}
	var m appxManifest
	if err := xml.Unmarshal(data, &m); err != nil {
		return nil
	}

	name := filepath.Base(exe)
	for _, app := range m.Applications.Application {
		if !strings.EqualFold(app.Executable, name) {
			continue
		}
		for _, logo := range []string{app.Visual.Logo150, app.Visual.Logo44} {
			if img := loadLogo(pkgDir, logo); img != nil {
				return img
			}
		}
	}
	return nil
}

// loadLogo opens a logo file, preferring the highest-resolution scale variant
// (Logo.scale-400.png before Logo.scale-200.png, and so on).
func loadLogo(pkgDir, logo string) image.Image {
	if logo == "" {
		return nil
	}
	base := filepath.Join(pkgDir, logo)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for _, suffix := range []string{".scale-400", ".scale-200", ".scale-100", ""} {
		f, err := os.Open(stem + suffix + ext)
		if err != nil {
			continue
		}
		img, err := png.Decode(f)
		f.Close()
		if err == nil {
			return img
		}
	}
	return nil
}

// resizeBox scales an image to size x size by averaging the source pixels in
// each output pixel. Colors are averaged premultiplied by alpha, so transparent
// areas don't add dark edges.
func resizeBox(src image.Image, size int) *image.NRGBA {
	return resizeBoxWH(src, size, size)
}

// resizeBoxWH shrinks an image to dw x dh pixels by averaging the source
// pixels under each new pixel, which keeps edges clean. Colors are averaged
// weighted by alpha, so transparent pixels do not darken the edges.
func resizeBoxWH(src image.Image, dw, dh int) *image.NRGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	if sw <= 0 || sh <= 0 || dw <= 0 || dh <= 0 {
		return dst
	}
	fast, isNRGBA := src.(*image.NRGBA)

	for dy := 0; dy < dh; dy++ {
		y0, y1 := dy*sh/dh, (dy+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dw; dx++ {
			x0, x1 := dx*sw/dw, (dx+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}

			var pr, pg, pb, sa, n uint64
			for y := y0; y < y1 && y < sh; y++ {
				for x := x0; x < x1 && x < sw; x++ {
					var c color.NRGBA
					if isNRGBA {
						c = fast.NRGBAAt(b.Min.X+x, b.Min.Y+y)
					} else {
						c = color.NRGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
					}
					a := uint64(c.A)
					pr += uint64(c.R) * a / 255
					pg += uint64(c.G) * a / 255
					pb += uint64(c.B) * a / 255
					sa += a
					n++
				}
			}
			if n == 0 || sa == 0 {
				continue // leave the pixel transparent
			}
			o := dst.PixOffset(dx, dy)
			dst.Pix[o+0] = uint8(pr * 255 / sa)
			dst.Pix[o+1] = uint8(pg * 255 / sa)
			dst.Pix[o+2] = uint8(pb * 255 / sa)
			dst.Pix[o+3] = uint8(sa / n)
		}
	}
	return dst
}

// cropToContent trims the transparent margin around a logo and returns the
// result as a square, so a small glyph on a big empty canvas is not drawn tiny.
func cropToContent(src image.Image) image.Image {
	b := src.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := src.At(x, y).RGBA(); a>>8 > 8 {
				minX, maxX = min(minX, x), max(maxX, x)
				minY, maxY = min(minY, y), max(maxY, y)
			}
		}
	}
	if maxX < minX || maxY < minY {
		return src // fully transparent: leave it alone
	}

	side := max(maxX-minX+1, maxY-minY+1)
	left := (minX+maxX+1)/2 - side/2
	top := (minY+maxY+1)/2 - side/2
	out := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			sx, sy := left+x, top+y
			if sx >= b.Min.X && sx < b.Max.X && sy >= b.Min.Y && sy < b.Max.Y {
				out.Set(x, y, src.At(sx, sy))
			}
		}
	}
	return out
}
