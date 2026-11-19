// Package ui is a small self-drawn window for the settings. Widgets are laid out and drawn
// into an image in plain Go (builder.go, canvas.go); a thin backend shows that image: X11 on
// Linux (x11.go), Win32 on Windows (win32_windows.go).
package ui

import "image"

// Options configure a window.
type Options struct {
	Title string
	Class string // WM_CLASS instance name, e.g. "battery-state"
	Width int    // logical pixels
	// Icon renders the window icon at a pixel size (for _NET_WM_ICON).
	Icon  func(px int) *image.NRGBA
	Theme Theme
	// Build lays out the contents; it runs on the window's goroutine on every redraw.
	Build func(b *Builder)
}

// layout runs the build function at the given scale and pixel width.
func layout(opts Options, scale float64, width int, mouse image.Point) *Builder {
	b := &Builder{t: opts.Theme, s: scale, w: width, mouse: mouse}
	if opts.Build != nil {
		opts.Build(b)
	}
	b.end()
	return b
}

// paint draws a laid-out window into a new image.
func paint(t Theme, b *Builder, width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	c := &canvas{img}
	c.fill(img.Bounds(), t.Bg)
	for _, op := range b.ops {
		if op != nil {
			op(c)
		}
	}
	return img
}

// hitAt returns the index of the click area at p, or -1.
func hitAt(hits []hit, p image.Point) int {
	for i, h := range hits {
		if p.In(h.r) {
			return i
		}
	}
	return -1
}

// Snapshot renders the window contents offscreen at the given scale, with the mouse at
// mouse (for tests and screenshots).
func Snapshot(opts Options, scale float64, mouse image.Point) *image.RGBA {
	w := int(float64(opts.Width)*scale + 0.5)
	b := layout(opts, scale, w, mouse)
	return paint(opts.Theme, b, w, b.y)
}
