// Package icon draws the tray icons: a device pictogram (with a lightning bolt while
// charging), the battery percentage and a level bar.
package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"
	"sync"

	"github.com/matz0r/wireless-battery-state/assets"
	xdraw "golang.org/x/image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// State is what an icon shows.
type State struct {
	Percent  int  // 0..100, -1 unknown
	Charging bool // lightning bolt next to the pictogram
	Asleep   bool // dimmed (last known value)
	NoAccess bool // "?"
	Low      int  // red at or below this level
	Glyph    Glyph
}

// Sizes are the pixmap sizes handed to the tray; it picks the best fit.
var Sizes = []int{16, 22, 24, 32, 48, 64}

var (
	textColor = color.NRGBA{0xF4, 0xF4, 0xF4, 0xFF}
	lowColor  = color.NRGBA{0xFF, 0x4D, 0x4D, 0xFF}
	outline   = color.NRGBA{0x00, 0x00, 0x00, 0xC0}
	barBack   = color.NRGBA{0x00, 0x00, 0x00, 0x90}
	barGood   = color.NRGBA{0x4C, 0xC3, 0x5A, 0xFF}
	barMid    = color.NRGBA{0xF0, 0xB4, 0x29, 0xFF}
	barLow    = color.NRGBA{0xFF, 0x4D, 0x4D, 0xFF}
	boltColor = color.NRGBA{0xFF, 0xD8, 0x3D, 0xFF}
)

var (
	fontOnce sync.Once
	fontData *opentype.Font
	faceMu   sync.Mutex
	faces    = map[float64]font.Face{}
)

func face(size float64) font.Face {
	fontOnce.Do(func() {
		f, err := opentype.Parse(gobold.TTF)
		if err != nil {
			panic(err) // embedded font; can't fail
		}
		fontData = f
	})
	faceMu.Lock()
	defer faceMu.Unlock()
	if f, ok := faces[size]; ok {
		return f
	}
	f, err := opentype.NewFace(fontData, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	faces[size] = f
	return f
}

// Text is the label drawn for a state.
func Text(s State) string {
	switch {
	case s.NoAccess:
		return "?"
	case s.Percent < 0:
		return "–"
	default:
		return strconv.Itoa(s.Percent)
	}
}

// Render draws a size×size icon: the device pictogram on top (if any), the number below it
// and the level bar along the bottom.
func Render(s State, size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	pad := 1 // outline width
	if size < 20 {
		pad = 0
	}

	// The layout is the same for every icon of a size, with or without pictogram, so the
	// numbers of all devices line up and have the same font size.
	barH := max(size/9, 2)
	barTop := size - barH
	if s.Percent >= 0 && !s.NoAccess {
		draw.Draw(img, image.Rect(0, barTop, size, size), image.NewUniform(barBack), image.Point{}, draw.Src)
		fill := barGood
		switch {
		case s.Percent <= s.Low:
			fill = barLow
		case s.Percent <= 50:
			fill = barMid
		}
		w := (size - 2) * s.Percent / 100
		if s.Percent > 0 {
			w = max(w, 1)
		}
		inset := 1
		if barH <= 2 {
			inset = 0
		}
		draw.Draw(img, image.Rect(1, barTop+inset, 1+w, size-inset), image.NewUniform(fill), image.Point{}, draw.Src)
	}

	// Top row: the pictogram, and a lightning bolt beside it while charging, centered together.
	gh := int(math.Round(float64(size) * 0.36))
	charging := s.Charging && !s.NoAccess
	if s.Glyph != NoGlyph || charging {
		h := gh - pad
		boltW, gap, glyphW := 0, 0, 0
		if charging {
			boltW = max(int(math.Round(float64(h)*boltAspect)), 3)
		}
		if s.Glyph != NoGlyph {
			if charging {
				gap = max(size/24, 1)
			}
			glyphW = min(int(math.Round(float64(h)*s.Glyph.aspect())), size-2*pad-boltW-gap)
		}
		x := (size - glyphW - gap - boltW) / 2
		if glyphW > 0 {
			drawMask(img, glyphMask(s.Glyph, glyphW, h), image.Pt(x, pad), textColor, pad)
		}
		if charging {
			drawMask(img, boltMask(boltW, h), image.Pt(x+glyphW+gap, pad), boltColor, pad)
		}
	}
	textTop := gh + max(size/16, 1)

	// Number: one font size per icon size, the largest at which "100" fits.
	text := Text(s)
	col := textColor
	if s.Percent >= 0 && s.Percent <= s.Low && !s.Charging && !s.NoAccess {
		col = lowColor
	}
	avail := barTop - 1 - textTop
	f, _, cy := fit("100", float64(size-2*pad), float64(avail-pad))
	b, _ := font.BoundString(f, text)
	x := fixed.I(size)/2 - (b.Min.X+b.Max.X)/2
	y := fixed.I(textTop) + fixed.I(avail)/2 - cy // same baseline for every text
	drawText(img, f, text, x, y, col, pad)

	if s.Asleep {
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = uint8(int(img.Pix[i]) * 45 / 100)
		}
	}
	return img
}

// drawMask paints col through mask at offset, with a dark outline like the text.
func drawMask(img *image.NRGBA, mask *image.Alpha, at image.Point, col color.NRGBA, pad int) {
	r := mask.Bounds().Add(at)
	for dx := -pad; dx <= pad; dx++ {
		for dy := -pad; dy <= pad; dy++ {
			if dx != 0 || dy != 0 {
				draw.DrawMask(img, r.Add(image.Pt(dx, dy)), image.NewUniform(outline), image.Point{}, mask, image.Point{}, draw.Over)
			}
		}
	}
	draw.DrawMask(img, r, image.NewUniform(col), image.Point{}, mask, image.Point{}, draw.Over)
}

// fit picks the largest font size whose text is at most maxW wide and capH tall. It returns
// the face and the center of the text's ink relative to the drawing origin.
func fit(text string, maxW, capH float64) (font.Face, fixed.Int26_6, fixed.Int26_6) {
	for pt := capH * 1.6; ; pt -= 0.5 {
		f := face(pt)
		b, _ := font.BoundString(f, text)
		w, h := b.Max.X-b.Min.X, b.Max.Y-b.Min.Y
		if pt <= 4 || (float64(w)/64 <= maxW-1 && float64(h)/64 <= capH) {
			return f, (b.Min.X + b.Max.X) / 2, (b.Min.Y + b.Max.Y) / 2
		}
	}
}

// drawText draws text with a dark outline so it reads on light and dark panels.
func drawText(img *image.NRGBA, f font.Face, text string, x, y fixed.Int26_6, col color.NRGBA, pad int) {
	d := &font.Drawer{Dst: img, Face: f}
	if pad > 0 {
		d.Src = image.NewUniform(outline)
		for dx := -pad; dx <= pad; dx++ {
			for dy := -pad; dy <= pad; dy++ {
				if dx == 0 && dy == 0 {
					continue
				}
				d.Dot = fixed.Point26_6{X: x + fixed.I(dx), Y: y + fixed.I(dy)}
				d.DrawString(text)
			}
		}
	}
	d.Src = image.NewUniform(col)
	d.Dot = fixed.Point26_6{X: x, Y: y}
	d.DrawString(text)
}

// AppIcon returns the logo (assets/icon-512.png) scaled to size×size.
func AppIcon(size int) *image.NRGBA {
	src, err := png.Decode(bytes.NewReader(assets.Icon512))
	if err != nil {
		panic(err) // embedded at build time; can't fail
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(img, img.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return img
}

// ARGB32 converts an image to the big-endian ARGB bytes used by StatusNotifierItem pixmaps.
func ARGB32(img *image.NRGBA) []byte {
	out := make([]byte, 0, len(img.Pix))
	for i := 0; i < len(img.Pix); i += 4 {
		r, g, b, a := img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]
		out = append(out, a, r, g, b)
	}
	return out
}
