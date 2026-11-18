// Package icon draws the tray icons: the battery percentage as a number with a level bar.
package icon

import (
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// State is what an icon shows.
type State struct {
	Percent  int  // 0..100, -1 unknown
	Charging bool // blue bar
	Asleep   bool // dimmed (last known value)
	NoAccess bool // "?"
	Low      int  // red at or below this level
}

// Sizes are the pixmap sizes handed to the tray; it picks the best fit.
var Sizes = []int{16, 22, 24, 32, 48, 64}

var (
	textColor   = color.NRGBA{0xF4, 0xF4, 0xF4, 0xFF}
	lowColor    = color.NRGBA{0xFF, 0x4D, 0x4D, 0xFF}
	outline     = color.NRGBA{0x00, 0x00, 0x00, 0xC0}
	barBack     = color.NRGBA{0x00, 0x00, 0x00, 0x90}
	barGood     = color.NRGBA{0x4C, 0xC3, 0x5A, 0xFF}
	barMid      = color.NRGBA{0xF0, 0xB4, 0x29, 0xFF}
	barLow      = color.NRGBA{0xFF, 0x4D, 0x4D, 0xFF}
	barCharging = color.NRGBA{0x3D, 0xA5, 0xFF, 0xFF}
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

// Render draws a size×size icon.
func Render(s State, size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	// Level bar along the bottom.
	barH := max(size/6, 2)
	barTop := size - barH
	if s.Percent >= 0 && !s.NoAccess {
		draw.Draw(img, image.Rect(0, barTop, size, size), image.NewUniform(barBack), image.Point{}, draw.Src)
		fill := barGood
		switch {
		case s.Charging:
			fill = barCharging
		case s.Percent <= s.Low:
			fill = barLow
		case s.Percent <= 50:
			fill = barMid
		}
		w := (size - 2) * s.Percent / 100
		if s.Percent > 0 {
			w = max(w, 1)
		}
		draw.Draw(img, image.Rect(1, barTop+1, 1+w, size-1), image.NewUniform(fill), image.Point{}, draw.Src)
	}

	// Number, as large as fits above the bar.
	text := Text(s)
	col := textColor
	if s.Percent >= 0 && s.Percent <= s.Low && !s.Charging && !s.NoAccess {
		col = lowColor
	}
	avail := barTop - 1
	f, cx, cy := fit(text, float64(size), float64(avail))
	x := fixed.I(size)/2 - cx
	y := fixed.I(avail)/2 - cy
	pad := 1
	if size < 20 {
		pad = 0
	}
	drawText(img, f, text, x, y, col, pad)

	if s.Asleep {
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = uint8(int(img.Pix[i]) * 45 / 100)
		}
	}
	return img
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

// AppIcon draws the application icon (a battery) for the desktop entry.
func AppIcon(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	u := float64(size) / 64
	rect := func(x0, y0, x1, y1 float64, c color.NRGBA) {
		draw.Draw(img, image.Rect(int(x0*u), int(y0*u), int(x1*u), int(y1*u)), image.NewUniform(c), image.Point{}, draw.Over)
	}
	body := color.NRGBA{0x2B, 0x2F, 0x36, 0xFF}
	rect(6, 18, 54, 46, color.NRGBA{0xE6, 0xE6, 0xE6, 0xFF}) // shell
	rect(54, 26, 60, 38, color.NRGBA{0xE6, 0xE6, 0xE6, 0xFF})
	rect(10, 22, 50, 42, body)
	rect(12, 24, 40, 40, barGood) // charge
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
