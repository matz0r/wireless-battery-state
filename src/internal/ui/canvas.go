package ui

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// canvas draws antialiased shapes and text into an RGBA image.
type canvas struct{ img *image.RGBA }

func (c *canvas) fill(r image.Rectangle, col color.Color) {
	draw.Draw(c.img, r, image.NewUniform(col), image.Point{}, draw.Over)
}

// roundRect fills r with corners of radius rad.
func (c *canvas) roundRect(r image.Rectangle, rad float64, col color.Color) {
	if r.Empty() {
		return
	}
	w, h := float32(r.Dx()), float32(r.Dy())
	k := float32(math.Min(rad, math.Min(float64(w), float64(h))/2))
	q := k * 0.4477 // 1 - 0.5523: control point distance for a quarter circle
	z := vector.NewRasterizer(r.Dx(), r.Dy())
	z.MoveTo(k, 0)
	z.LineTo(w-k, 0)
	z.CubeTo(w-q, 0, w, q, w, k)
	z.LineTo(w, h-k)
	z.CubeTo(w, h-q, w-q, h, w-k, h)
	z.LineTo(k, h)
	z.CubeTo(q, h, 0, h-q, 0, h-k)
	z.LineTo(0, k)
	z.CubeTo(0, q, q, 0, k, 0)
	z.ClosePath()
	z.Draw(c.img, r, image.NewUniform(col), image.Point{})
}

// framedRect draws a rounded rectangle with a border of width bw.
func (c *canvas) framedRect(r image.Rectangle, rad float64, bw int, border, fill color.Color) {
	c.roundRect(r, rad, border)
	c.roundRect(r.Inset(bw), math.Max(rad-float64(bw), 0), fill)
}

type pt struct{ X, Y float64 }

// polyline strokes the line through pts with width w and round joints.
func (c *canvas) polyline(pts []pt, w float64, col color.Color) {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		minX, minY = math.Min(minX, p.X), math.Min(minY, p.Y)
		maxX, maxY = math.Max(maxX, p.X), math.Max(maxY, p.Y)
	}
	r := image.Rect(int(minX-w), int(minY-w), int(maxX+w)+1, int(maxY+w)+1)
	z := vector.NewRasterizer(r.Dx(), r.Dy())
	ox, oy := float64(r.Min.X), float64(r.Min.Y)
	at := func(x, y float64) (float32, float32) { return float32(x - ox), float32(y - oy) }
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		dx, dy := b.X-a.X, b.Y-a.Y
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		nx, ny := -dy/l*w/2, dx/l*w/2
		z.MoveTo(at(a.X+nx, a.Y+ny))
		z.LineTo(at(b.X+nx, b.Y+ny))
		z.LineTo(at(b.X-nx, b.Y-ny))
		z.LineTo(at(a.X-nx, a.Y-ny))
		z.ClosePath()
	}
	// round joints and caps: a polygonal circle at every point
	for _, p := range pts {
		const n = 12
		for i := 0; i <= n; i++ {
			a := 2 * math.Pi * float64(i) / n
			x, y := at(p.X+math.Cos(a)*w/2, p.Y+math.Sin(a)*w/2)
			if i == 0 {
				z.MoveTo(x, y)
			} else {
				z.LineTo(x, y)
			}
		}
		z.ClosePath()
	}
	z.Draw(c.img, r, image.NewUniform(col), image.Point{})
}

func (c *canvas) image(img image.Image, at image.Point) {
	b := img.Bounds()
	draw.Draw(c.img, b.Sub(b.Min).Add(at), img, b.Min, draw.Over)
}

// text draws s with its baseline at y.
func (c *canvas) text(f font.Face, s string, x, y int, col color.Color) {
	d := &font.Drawer{Dst: c.img, Src: image.NewUniform(col), Face: f, Dot: fixed.P(x, y)}
	d.DrawString(s)
}
