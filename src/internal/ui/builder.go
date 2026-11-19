package ui

import (
	"image"
	"image/color"
	"math"
)

// Builder lays out the window from top to bottom. The window calls the build function on
// every redraw (after a click, a hover change or Refresh), immediate-mode style: widgets
// record drawing operations and click areas, then the window renders them at the final
// height. Sizes are logical pixels, multiplied by the display scale.
type Builder struct {
	t     Theme
	s     float64
	w     int // window width in pixels
	y     int
	mouse image.Point
	ops   []func(*canvas)
	hits  []hit
	card  *cardState
}

type hit struct {
	r image.Rectangle
	f func()
}

type cardState struct {
	top  int
	op   int // index of the background operation, filled in by Card
	rows int
}

// Button is a push button. A button without OnClick or with Disabled is drawn greyed out.
type Button struct {
	Label    string
	Primary  bool
	Disabled bool
	OnClick  func()
}

// Logical layout sizes.
const (
	pad       = 18.0
	rowPad    = 14.0
	textSize  = 13.5
	smallSize = 12.0
	radius    = 9.0
)

func (b *Builder) px(v float64) int { return int(math.Round(v * b.s)) }

func (b *Builder) op(f func(*canvas)) { b.ops = append(b.ops, f) }

func (b *Builder) click(r image.Rectangle, f func()) {
	if f != nil {
		b.hits = append(b.hits, hit{r, f})
	}
}

func (b *Builder) hovered(r image.Rectangle) bool { return b.mouse.In(r) }

// space adds vertical space.
func (b *Builder) space(v float64) { b.y += b.px(v) }

// Theme returns the window's theme; Scale the pixels per logical pixel.
func (b *Builder) Theme() Theme   { return b.t }
func (b *Builder) Scale() float64 { return b.s }

// label draws one line of text in r, its capitals centered at fraction fy of r's height.
func (b *Builder) label(r image.Rectangle, fy float64, s string, isBold bool, size float64, col color.Color, alignRight bool) {
	f := face(isBold, size*b.s)
	s = ellipsize(f, s, r.Dx())
	y := r.Min.Y + int(math.Round(fy*float64(r.Dy()))) + capHeight(f)/2
	x := r.Min.X
	if alignRight {
		x = r.Max.X - measure(f, s)
	}
	b.op(func(c *canvas) { c.text(f, s, x, y, col) })
}

// twoLines draws a title and, if given, a smaller detail line below it.
func (b *Builder) twoLines(r image.Rectangle, title, detail string, titleCol color.Color) {
	if detail == "" {
		b.label(r, 0.5, title, false, textSize, titleCol, false)
		return
	}
	b.label(r, 0.34, title, false, textSize, titleCol, false)
	b.label(r, 0.69, detail, false, smallSize, b.t.Muted, false)
}

// Header shows the logo, the app name and a subtitle.
func (b *Builder) Header(logo func(px int) image.Image, title, subtitle string) {
	b.space(pad)
	size := b.px(40)
	x := b.px(pad)
	top := b.y
	if logo != nil {
		img := logo(size)
		at := image.Pt(x, top)
		b.op(func(c *canvas) { c.image(img, at) })
		x += size + b.px(12)
	}
	r := image.Rect(x, top, b.w-b.px(pad), top+size)
	b.label(r, 0.3, title, true, 17, b.t.Text, false)
	b.label(r, 0.76, subtitle, false, smallSize, b.t.Muted, false)
	b.y = top + size
}

// Banner shows a highlighted line with an optional button, e.g. an available update.
func (b *Builder) Banner(text string, btn *Button) {
	b.space(14)
	h := b.px(48)
	r := image.Rect(b.px(pad), b.y, b.w-b.px(pad), b.y+h)
	t := b.t
	b.op(func(c *canvas) { c.roundRect(r, radius*b.s, t.AccentSoft) })
	inner := image.Rect(r.Min.X+b.px(rowPad), r.Min.Y, r.Max.X-b.px(rowPad), r.Max.Y)
	if btn != nil {
		br := b.buttonRect(*btn, inner.Max.X, inner.Min.Y+(h-b.px(30))/2)
		b.button(br, *btn)
		inner.Max.X = br.Min.X - b.px(10)
	}
	b.label(inner, 0.5, text, true, textSize, t.Text, false)
	b.y += h
}

// Message shows wrapped text, in the error color if isErr.
func (b *Builder) Message(text string, isErr bool) {
	b.space(12)
	col := b.t.Muted
	if isErr {
		col = b.t.Error
	}
	f := face(false, smallSize*b.s)
	lineH := b.px(smallSize * 1.45)
	x := b.px(pad + 2)
	for _, line := range wrap(f, text, b.w-2*b.px(pad+2)) {
		y := b.y + (lineH+capHeight(f))/2
		b.op(func(c *canvas) { c.text(f, line, x, y, col) })
		b.y += lineH
	}
}

// Section starts a group with a small heading.
func (b *Builder) Section(title string) {
	b.space(18)
	h := b.px(22)
	r := image.Rect(b.px(pad+2), b.y, b.w-b.px(pad), b.y+h)
	b.label(r, 0.45, title, true, smallSize, b.t.Muted, false)
	b.y += h
}

// Card groups the rows added by f in a rounded box with separators.
func (b *Builder) Card(f func()) {
	b.space(4)
	cs := &cardState{top: b.y, op: len(b.ops)}
	b.op(nil) // placeholder for the background
	b.card = cs
	f()
	b.card = nil
	r := image.Rect(b.px(pad), cs.top, b.w-b.px(pad), b.y)
	t := b.t
	bw := max(b.px(1), 1)
	b.ops[cs.op] = func(c *canvas) { c.framedRect(r, radius*b.s, bw, t.Border, t.Card) }
}

// row reserves a row of logical height h inside a card and returns its full rectangle.
func (b *Builder) row(h float64) image.Rectangle {
	if b.card != nil {
		if b.card.rows > 0 {
			y := b.y
			x0, x1 := b.px(pad+rowPad), b.w-b.px(pad)
			col := b.t.Border
			lw := max(b.px(1), 1)
			b.op(func(c *canvas) { c.fill(image.Rect(x0, y, x1, y+lw), col) })
		}
		b.card.rows++
	}
	r := image.Rect(b.px(pad), b.y, b.w-b.px(pad), b.y+b.px(h))
	b.y = r.Max.Y
	return r
}

// hoverBg highlights a clickable row under the mouse; the first and last row keep the
// card's rounded corners.
func (b *Builder) hoverBg(r image.Rectangle) {
	if !b.hovered(r) {
		return
	}
	in := max(b.px(1), 1)
	hr := image.Rect(r.Min.X+in, r.Min.Y, r.Max.X-in, r.Max.Y)
	if b.card != nil && b.card.rows == 1 {
		hr.Min.Y += in
	}
	col := b.t.Hover
	rad := (radius - 1) * b.s
	b.op(func(c *canvas) { c.roundRect(hr, rad, col) })
}

// TextRow is a row with a line of muted text.
func (b *Builder) TextRow(text string) {
	r := b.row(44)
	b.label(r.Inset(b.px(rowPad)), 0.5, text, false, textSize, b.t.Muted, false)
}

// CheckRow is a clickable row with a checkbox, an optional picture, a title and a detail.
func (b *Builder) CheckRow(pic func(px int) image.Image, title, detail string, checked bool, onClick func()) {
	r := b.row(54)
	b.hoverBg(r)
	b.click(r, onClick)
	t := b.t
	x := r.Min.X + b.px(rowPad)
	box := b.px(18)
	br := image.Rect(x, r.Min.Y+(r.Dy()-box)/2, x+box, r.Min.Y+(r.Dy()+box)/2)
	s := b.s
	b.op(func(c *canvas) {
		if checked {
			c.roundRect(br, 4*s, t.Accent)
			o := pt{float64(br.Min.X), float64(br.Min.Y)}
			c.polyline([]pt{{o.X + 4.5*s, o.Y + 9.5*s}, {o.X + 7.7*s, o.Y + 12.6*s}, {o.X + 13.5*s, o.Y + 5.6*s}}, 2*s, t.AccentText)
		} else {
			c.framedRect(br, 4*s, max(int(math.Round(1.5*s)), 1), t.Muted, t.Card)
		}
	})
	x = br.Max.X + b.px(12)
	if pic != nil {
		size := b.px(30)
		img := pic(size)
		at := image.Pt(x, r.Min.Y+(r.Dy()-size)/2)
		chip := image.Rectangle{at, at.Add(image.Pt(size, size))}.Inset(-b.px(3))
		b.op(func(c *canvas) {
			if t.PicBg.A != 0 {
				c.roundRect(chip, 6*s, t.PicBg)
			}
			c.image(img, at)
		})
		x += size + b.px(12)
	}
	col := t.Text
	if !checked {
		col = t.Muted
	}
	b.twoLines(image.Rect(x, r.Min.Y, r.Max.X-b.px(rowPad), r.Max.Y), title, detail, col)
}

// SwitchRow is a clickable row with a title, a detail and an on/off switch.
func (b *Builder) SwitchRow(title, detail string, on bool, onClick func()) {
	h := 46.0
	if detail != "" {
		h = 56
	}
	r := b.row(h)
	b.hoverBg(r)
	b.click(r, onClick)
	t := b.t
	sw, sh := b.px(38), b.px(22)
	right := r.Max.X - b.px(rowPad)
	tr := image.Rect(right-sw, r.Min.Y+(r.Dy()-sh)/2, right, r.Min.Y+(r.Dy()+sh)/2)
	knob := sh - 2*b.px(3)
	b.op(func(c *canvas) {
		track, kx := t.SwitchOff, tr.Min.X+(sh-knob)/2
		if on {
			track, kx = t.Accent, tr.Max.X-(sh+knob)/2
		}
		c.roundRect(tr, float64(sh)/2, track)
		ky := tr.Min.Y + (sh-knob)/2
		c.roundRect(image.Rect(kx, ky, kx+knob, ky+knob), float64(knob)/2, t.Knob)
	})
	b.twoLines(image.Rect(r.Min.X+b.px(rowPad), r.Min.Y, tr.Min.X-b.px(12), r.Max.Y), title, detail, t.Text)
}

// InfoRow is a row with a title, a detail and an optional button on the right.
func (b *Builder) InfoRow(title, detail string, btn *Button) {
	h := 46.0
	if detail != "" {
		h = 56
	}
	r := b.row(h)
	inner := image.Rect(r.Min.X+b.px(rowPad), r.Min.Y, r.Max.X-b.px(rowPad), r.Max.Y)
	if btn != nil {
		br := b.buttonRect(*btn, inner.Max.X, r.Min.Y+(r.Dy()-b.px(30))/2)
		b.button(br, *btn)
		inner.Max.X = br.Min.X - b.px(12)
	}
	b.twoLines(inner, title, detail, b.t.Text)
}

// Buttons puts a row of buttons at the bottom: left ones aligned left, right ones right.
func (b *Builder) Buttons(left, right []Button) {
	b.space(18)
	top := b.y
	x := b.px(pad)
	for _, btn := range left {
		r := b.buttonRect(btn, 0, top)
		r = r.Add(image.Pt(x-r.Min.X, 0))
		b.button(r, btn)
		x = r.Max.X + b.px(8)
	}
	x = b.w - b.px(pad)
	for i := len(right) - 1; i >= 0; i-- {
		r := b.buttonRect(right[i], x, top)
		b.button(r, right[i])
		x = r.Min.X - b.px(8)
	}
	b.y = top + b.px(30)
}

// End adds the bottom margin. The window calls it after the build function.
func (b *Builder) end() { b.space(pad) }

// buttonRect sizes a button whose right edge is at right.
func (b *Builder) buttonRect(btn Button, right, top int) image.Rectangle {
	f := face(btn.Primary, 13*b.s)
	w := measure(f, btn.Label) + 2*b.px(14)
	return image.Rect(right-w, top, right, top+b.px(30))
}

func (b *Builder) button(r image.Rectangle, btn Button) {
	t := b.t
	enabled := btn.OnClick != nil && !btn.Disabled
	hover := enabled && b.hovered(r)
	if enabled {
		b.click(r, btn.OnClick)
	}
	f := face(btn.Primary, 13*b.s)
	tx := r.Min.X + (r.Dx()-measure(f, btn.Label))/2
	ty := r.Min.Y + (r.Dy()+capHeight(f))/2
	s := b.s
	bw := max(b.px(1), 1)
	b.op(func(c *canvas) {
		textCol := t.Text
		switch {
		case btn.Primary && enabled:
			bg := t.Accent
			if hover {
				bg = t.AccentHover
			}
			c.roundRect(r, 6*s, bg)
			textCol = t.AccentText
		default:
			bg := t.Card
			if hover {
				bg = t.Hover
			}
			c.framedRect(r, 6*s, bw, t.Border, bg)
			if !enabled {
				textCol = t.Muted
			}
		}
		c.text(f, btn.Label, tx, ty, textCol)
	})
}
