package icon

import (
	"image"
	"math"

	"golang.org/x/image/vector"
)

// Glyph is the small device pictogram drawn above the number.
type Glyph int

const (
	NoGlyph Glyph = iota
	MouseGlyph
	KeyboardGlyph
	HeadsetGlyph
)

// aspect is the glyph's width divided by its height.
func (g Glyph) aspect() float64 {
	switch g {
	case MouseGlyph:
		return 1.6
	case KeyboardGlyph:
		return 1.75
	case HeadsetGlyph:
		return 1.15
	}
	return 1
}

// shape builds a coverage mask from filled shapes minus holes, in pixel coordinates.
type shape struct {
	w, h  int
	fill  *vector.Rasterizer
	holes *vector.Rasterizer
}

func newShape(w, h int) *shape {
	return &shape{w: w, h: h, fill: vector.NewRasterizer(w, h), holes: vector.NewRasterizer(w, h)}
}

// roundRect adds a rounded rectangle path.
func roundRect(r *vector.Rasterizer, x0, y0, x1, y1, rad float64) {
	rad = math.Min(rad, math.Min((x1-x0)/2, (y1-y0)/2))
	const k = 0.5523 // cubic Bézier circle approximation
	r.MoveTo(float32(x0+rad), float32(y0))
	r.LineTo(float32(x1-rad), float32(y0))
	r.CubeTo(float32(x1-rad+rad*k), float32(y0), float32(x1), float32(y0+rad-rad*k), float32(x1), float32(y0+rad))
	r.LineTo(float32(x1), float32(y1-rad))
	r.CubeTo(float32(x1), float32(y1-rad+rad*k), float32(x1-rad+rad*k), float32(y1), float32(x1-rad), float32(y1))
	r.LineTo(float32(x0+rad), float32(y1))
	r.CubeTo(float32(x0+rad-rad*k), float32(y1), float32(x0), float32(y1-rad+rad*k), float32(x0), float32(y1-rad))
	r.LineTo(float32(x0), float32(y0+rad))
	r.CubeTo(float32(x0), float32(y0+rad-rad*k), float32(x0+rad-rad*k), float32(y0), float32(x0+rad), float32(y0))
	r.ClosePath()
}

// ringArc adds a thick arc (part of a ring) from angle a0 to a1 (radians, screen coordinates).
func ringArc(r *vector.Rasterizer, cx, cy, outer, inner, a0, a1 float64) {
	const steps = 24
	pt := func(rad, a float64) (float32, float32) {
		return float32(cx + rad*math.Cos(a)), float32(cy + rad*math.Sin(a))
	}
	x, y := pt(outer, a0)
	r.MoveTo(x, y)
	for i := 1; i <= steps; i++ {
		x, y = pt(outer, a0+(a1-a0)*float64(i)/steps)
		r.LineTo(x, y)
	}
	for i := steps; i >= 0; i-- {
		x, y = pt(inner, a0+(a1-a0)*float64(i)/steps)
		r.LineTo(x, y)
	}
	r.ClosePath()
}

func (s *shape) mask() *image.Alpha {
	m := image.NewAlpha(image.Rect(0, 0, s.w, s.h))
	s.fill.Draw(m, m.Bounds(), image.Opaque, image.Point{})
	h := image.NewAlpha(m.Bounds())
	s.holes.Draw(h, h.Bounds(), image.Opaque, image.Point{})
	for i := range m.Pix {
		m.Pix[i] = uint8(int(m.Pix[i]) * (255 - int(h.Pix[i])) / 255)
	}
	return m
}

// glyphMask draws the pictogram into a w×h coverage mask.
func glyphMask(g Glyph, w, h int) *image.Alpha {
	s := newShape(w, h)
	W, H := float64(w), float64(h)
	line := math.Max(1, H*0.11) // width of the cut-out details
	switch g {
	case MouseGlyph:
		// Lying on its side, buttons to the left, so it is wide enough to read.
		roundRect(s.fill, 0, 0, W, H, H/2)
		split := W * 0.4
		roundRect(s.holes, 0, H/2-line/2, split, H/2+line/2, 0) // between the buttons
		roundRect(s.holes, split-line/2, 0, split+line/2, H, 0) // behind the buttons
	case KeyboardGlyph:
		roundRect(s.fill, 0, 0, W, H, H*0.18)
		// One row of keys and a space bar, cut out.
		in := H * 0.2
		keyH := H * 0.22
		n := 4
		gap := line
		keyW := (W - 2*in - float64(n-1)*gap) / float64(n)
		for i := 0; i < n; i++ {
			x := in + float64(i)*(keyW+gap)
			roundRect(s.holes, x, in, x+keyW, in+keyH, 0)
		}
		roundRect(s.holes, W*0.26, H-in-keyH, W*0.74, H-in, 0)
	case HeadsetGlyph:
		band := math.Max(1.2, W*0.13)
		cupW := W * 0.27
		cy := H * 0.55
		ringArc(s.fill, W/2, cy, W/2, W/2-band, math.Pi, 2*math.Pi)
		roundRect(s.fill, 0, cy-band*0.2, band, cy+band, 0) // band ends down into the cups
		roundRect(s.fill, W-band, cy-band*0.2, W, cy+band, 0)
		roundRect(s.fill, 0, H*0.5, cupW, H, cupW*0.35)
		roundRect(s.fill, W-cupW, H*0.5, W, H, cupW*0.35)
	}
	return s.mask()
}
