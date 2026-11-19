//go:build !windows

package ui

// The Linux window speaks the X11 protocol in pure Go (github.com/jezek/xgb), so the binary
// stays static; on Wayland it runs through XWayland.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Window is an open window. Its methods may be called from any goroutine.
type Window struct {
	opts   Options
	conn   *xgb.Conn
	screen *xproto.ScreenInfo
	win    xproto.Window
	gc     xproto.Gcontext
	scale  float64
	atoms  map[string]xproto.Atom
	escape xproto.Keycode
	hand   xproto.Cursor

	w, h    int // pixels
	frame   *image.RGBA
	hits    []hit
	mouse   image.Point
	pressed int // index into hits of the button press, -1 if none
	handOn  bool
	time    xproto.Timestamp // latest X server time seen, for focus requests
	raising bool

	wake      chan struct{}
	raise     chan struct{}
	closeReq  chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// Open creates and shows the window.
func Open(opts Options) (*Window, error) {
	if os.Getenv("DISPLAY") == "" {
		return nil, errors.New("no X11 display (the settings window needs X11 or XWayland)")
	}
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("can't connect to the X server (the settings window needs X11 or XWayland): %w", err)
	}
	w := &Window{
		opts:     opts,
		conn:     conn,
		pressed:  -1,
		mouse:    image.Pt(-1, -1),
		wake:     make(chan struct{}, 1),
		raise:    make(chan struct{}, 1),
		closeReq: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	if err := w.create(); err != nil {
		conn.Close()
		return nil, err
	}
	go w.loop()
	return w, nil
}

// Refresh rebuilds and redraws the contents.
func (w *Window) Refresh() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Raise brings the window to the front.
func (w *Window) Raise() {
	select {
	case w.raise <- struct{}{}:
	default:
	}
}

// Close closes the window.
func (w *Window) Close() {
	select {
	case w.closeReq <- struct{}{}:
	default:
	}
}

// Done is closed when the window is gone.
func (w *Window) Done() <-chan struct{} { return w.done }

func (w *Window) atom(name string) xproto.Atom {
	if a, ok := w.atoms[name]; ok {
		return a
	}
	r, err := xproto.InternAtom(w.conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0
	}
	w.atoms[name] = r.Atom
	return r.Atom
}

func (w *Window) create() error {
	setup := xproto.Setup(w.conn)
	w.screen = setup.DefaultScreen(w.conn)
	w.atoms = map[string]xproto.Atom{}
	if w.screen.RootDepth != 24 && w.screen.RootDepth != 32 {
		return fmt.Errorf("unsupported X display depth %d", w.screen.RootDepth)
	}
	for _, f := range setup.PixmapFormats {
		if f.Depth == w.screen.RootDepth && f.BitsPerPixel != 32 {
			return fmt.Errorf("unsupported X pixel format (%d bits per pixel)", f.BitsPerPixel)
		}
	}
	w.scale = w.readScale()
	w.w = int(float64(w.opts.Width)*w.scale + 0.5)
	w.h = w.w // corrected after the first layout

	var err error
	if w.win, err = xproto.NewWindowId(w.conn); err != nil {
		return err
	}
	bg := w.opts.Theme.Bg
	mask := uint32(xproto.EventMaskExposure | xproto.EventMaskButtonPress | xproto.EventMaskButtonRelease |
		xproto.EventMaskPointerMotion | xproto.EventMaskLeaveWindow | xproto.EventMaskKeyPress |
		xproto.EventMaskStructureNotify | xproto.EventMaskPropertyChange)
	err = xproto.CreateWindowChecked(w.conn, w.screen.RootDepth, w.win, w.screen.Root, 0, 0,
		uint16(w.w), uint16(w.h), 0, xproto.WindowClassInputOutput, w.screen.RootVisual,
		xproto.CwBackPixel|xproto.CwEventMask,
		[]uint32{uint32(bg.R)<<16 | uint32(bg.G)<<8 | uint32(bg.B), mask}).Check()
	if err != nil {
		return err
	}
	if w.gc, err = xproto.NewGcontextId(w.conn); err != nil {
		return err
	}
	xproto.CreateGC(w.conn, w.gc, xproto.Drawable(w.win), 0, nil)

	w.setProperties()
	w.escape = w.keycode(0xff1b)
	w.hand = w.cursor(60) // XC_hand2

	w.render() // sets the final height before the window appears
	// Window managers with focus-stealing prevention (Muffin, Mutter, KWin) only focus a new
	// window whose _NET_WM_USER_TIME is current; it is activated on MapNotify, since they
	// ignore requests for windows they don't manage yet.
	w.time = w.serverTime()
	w.setCardinals("_NET_WM_USER_TIME", "CARDINAL", []uint32{uint32(w.time)})
	xproto.MapWindow(w.conn, w.win)
	return nil
}

func (w *Window) setString(prop, typ string, s string) {
	xproto.ChangeProperty(w.conn, xproto.PropModeReplace, w.win, w.atom(prop), w.atom(typ), 8, uint32(len(s)), []byte(s))
}

func (w *Window) setCardinals(prop, typ string, vals []uint32) {
	data := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(data[4*i:], v) // xgb sends in the client's (little-endian) order
	}
	xproto.ChangeProperty(w.conn, xproto.PropModeReplace, w.win, w.atom(prop), w.atom(typ), 32, uint32(len(vals)), data)
}

func (w *Window) setProperties() {
	w.setString("WM_NAME", "STRING", w.opts.Title)
	w.setString("_NET_WM_NAME", "UTF8_STRING", w.opts.Title)
	w.setString("WM_CLASS", "STRING", w.opts.Class+"\x00"+w.opts.Title+"\x00")
	w.setCardinals("WM_PROTOCOLS", "ATOM", []uint32{uint32(w.atom("WM_DELETE_WINDOW"))})
	w.setCardinals("_NET_WM_PID", "CARDINAL", []uint32{uint32(os.Getpid())})
	w.setCardinals("WM_HINTS", "WM_HINTS", []uint32{1, 1, 0, 0, 0, 0, 0, 0, 0}) // InputHint: takes focus
	w.setCardinals("_NET_WM_WINDOW_TYPE", "ATOM", []uint32{uint32(w.atom("_NET_WM_WINDOW_TYPE_NORMAL"))})
	if host, err := os.Hostname(); err == nil {
		w.setString("WM_CLIENT_MACHINE", "STRING", host)
	}
	if w.opts.Icon != nil {
		var icon []uint32
		for _, size := range []int{16, 32, 48, 64, 128} {
			img := w.opts.Icon(size)
			icon = append(icon, uint32(size), uint32(size))
			for i := 0; i < len(img.Pix); i += 4 {
				p := img.Pix[i : i+4]
				icon = append(icon, uint32(p[3])<<24|uint32(p[0])<<16|uint32(p[1])<<8|uint32(p[2]))
			}
		}
		w.setCardinals("_NET_WM_ICON", "CARDINAL", icon)
	}
}

// setSize fixes the window size: the contents decide it, the user can't resize.
func (w *Window) setSize(width, height int) {
	hints := make([]uint32, 18)
	hints[0] = 16 | 32 // PMinSize | PMaxSize
	hints[5], hints[6] = uint32(width), uint32(height)
	hints[7], hints[8] = uint32(width), uint32(height)
	w.setCardinals("WM_NORMAL_HINTS", "WM_SIZE_HINTS", hints)
	xproto.ConfigureWindow(w.conn, w.win, xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(width), uint32(height)})
}

// readScale derives the UI scale from Xft.dpi, which desktops set for HiDPI screens.
func (w *Window) readScale() float64 {
	r, err := xproto.GetProperty(w.conn, false, w.screen.Root, w.atom("RESOURCE_MANAGER"),
		xproto.AtomString, 0, 1<<16).Reply()
	if err != nil {
		return 1
	}
	for _, line := range strings.Split(string(r.Value), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == "Xft.dpi" {
			if dpi, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && dpi >= 48 {
				return min(dpi/96, 4)
			}
		}
	}
	return 1
}

func (w *Window) keycode(keysym xproto.Keysym) xproto.Keycode {
	setup := xproto.Setup(w.conn)
	n := byte(setup.MaxKeycode - setup.MinKeycode + 1)
	r, err := xproto.GetKeyboardMapping(w.conn, setup.MinKeycode, n).Reply()
	if err != nil || r.KeysymsPerKeycode == 0 {
		return 0
	}
	per := int(r.KeysymsPerKeycode)
	for i, ks := range r.Keysyms {
		if ks == keysym {
			return setup.MinKeycode + xproto.Keycode(i/per)
		}
	}
	return 0
}

func (w *Window) cursor(glyph uint16) xproto.Cursor {
	f, err := xproto.NewFontId(w.conn)
	if err != nil {
		return 0
	}
	if xproto.OpenFontChecked(w.conn, f, 6, "cursor").Check() != nil {
		return 0
	}
	c, err := xproto.NewCursorId(w.conn)
	if err != nil {
		return 0
	}
	xproto.CreateGlyphCursor(w.conn, c, f, f, glyph, glyph+1, 0, 0, 0, 0xffff, 0xffff, 0xffff)
	xproto.CloseFont(w.conn, f)
	return c
}

// serverTime gets the current X server time from the PropertyNotify of an empty change.
// Only for use before the event loop runs.
func (w *Window) serverTime() xproto.Timestamp {
	w.touchTime()
	for {
		ev, err := w.conn.WaitForEvent()
		if ev == nil && err == nil {
			return 0
		}
		if p, ok := ev.(xproto.PropertyNotifyEvent); ok && p.Atom == w.atom("_BATTERY_STATE_TIME") {
			return p.Time
		}
	}
}

// touchTime makes the server send a PropertyNotify carrying the current time.
func (w *Window) touchTime() {
	xproto.ChangeProperty(w.conn, xproto.PropModeAppend, w.win, w.atom("_BATTERY_STATE_TIME"),
		xproto.AtomString, 8, 0, nil)
}

// activate asks the window manager to raise and focus the window.
func (w *Window) activate() {
	xproto.ConfigureWindow(w.conn, w.win, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove})
	ev := xproto.ClientMessageEvent{
		Format: 32,
		Window: w.win,
		Type:   w.atom("_NET_ACTIVE_WINDOW"),
		Data:   xproto.ClientMessageDataUnionData32New([]uint32{2, uint32(w.time), 0, 0, 0}), // 2: from a pager
	}
	xproto.SendEvent(w.conn, false, w.screen.Root,
		xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify, string(ev.Bytes()))
}

// render lays out the contents, draws them and copies them to the window.
func (w *Window) render() {
	b := layout(w.opts, w.scale, w.w, w.mouse)
	if b.y != w.h || w.frame == nil {
		w.h = b.y
		w.setSize(w.w, w.h)
	}
	w.frame = paint(w.opts.Theme, b, w.w, w.h)
	w.hits = b.hits
	w.updateCursor()
	w.present()
}

// present copies the frame to the window in strips that fit into one request.
func (w *Window) present() {
	img := w.frame
	if img == nil {
		return
	}
	maxBytes := int(xproto.Setup(w.conn).MaximumRequestLength)*4 - 32
	rows := max(maxBytes/(w.w*4), 1)
	buf := make([]byte, 0, rows*w.w*4)
	for y0 := 0; y0 < w.h; y0 += rows {
		y1 := min(y0+rows, w.h)
		buf = buf[:0]
		for y := y0; y < y1; y++ {
			line := img.Pix[y*img.Stride : y*img.Stride+w.w*4]
			for i := 0; i < len(line); i += 4 {
				buf = append(buf, line[i+2], line[i+1], line[i], 0xFF) // BGRX
			}
		}
		xproto.PutImage(w.conn, xproto.ImageFormatZPixmap, xproto.Drawable(w.win), w.gc,
			uint16(w.w), uint16(y1-y0), 0, int16(y0), 0, w.screen.RootDepth, buf)
	}
}

func (w *Window) hitAt(p image.Point) int { return hitAt(w.hits, p) }

func (w *Window) updateCursor() {
	on := w.hitAt(w.mouse) >= 0
	if on == w.handOn || w.hand == 0 {
		return
	}
	w.handOn = on
	c := uint32(w.hand)
	if !on {
		c = 0 // the parent's cursor
	}
	xproto.ChangeWindowAttributes(w.conn, w.win, xproto.CwCursor, []uint32{c})
}

func (w *Window) loop() {
	defer close(w.done)
	events := make(chan xgb.Event, 16)
	go func() {
		defer close(events)
		for {
			ev, err := w.conn.WaitForEvent()
			if ev == nil && err == nil {
				return // connection closed
			}
			if ev != nil {
				select {
				case events <- ev:
				case <-w.done:
					return
				}
			}
		}
	}()
	finish := func() {
		xproto.DestroyWindow(w.conn, w.win)
		w.conn.Sync()
		w.conn.Close()
	}
	for {
		select {
		case <-w.closeReq:
			finish()
			return
		case <-w.raise:
			xproto.MapWindow(w.conn, w.win)
			w.raising = true
			w.touchTime() // activated with a fresh time on the PropertyNotify
		case <-w.wake:
			w.render()
		case ev, ok := <-events:
			if !ok {
				return // the X server went away
			}
			if w.handle(ev) {
				finish()
				return
			}
		}
	}
}

// handle processes one X event and reports whether the window should close.
func (w *Window) handle(ev xgb.Event) bool {
	switch e := ev.(type) {
	case xproto.ExposeEvent:
		if e.Count == 0 {
			w.present()
		}
	case xproto.MotionNotifyEvent:
		p := image.Pt(int(e.EventX), int(e.EventY))
		before := w.hitAt(w.mouse)
		w.mouse = p
		if w.hitAt(p) != before {
			w.render()
		}
	case xproto.LeaveNotifyEvent:
		w.mouse = image.Pt(-1, -1)
		w.render()
	case xproto.ButtonPressEvent:
		w.time = e.Time
		if e.Detail == 1 {
			w.mouse = image.Pt(int(e.EventX), int(e.EventY))
			w.pressed = w.hitAt(w.mouse)
		}
	case xproto.ButtonReleaseEvent:
		if e.Detail == 1 {
			w.mouse = image.Pt(int(e.EventX), int(e.EventY))
			i := w.hitAt(w.mouse)
			if i >= 0 && i == w.pressed {
				w.hits[i].f()
			}
			w.pressed = -1
			w.render()
		}
	case xproto.KeyPressEvent:
		w.time = e.Time
		if w.escape != 0 && e.Detail == w.escape {
			return true
		}
	case xproto.ClientMessageEvent:
		if e.Type == w.atom("WM_PROTOCOLS") && xproto.Atom(e.Data.Data32[0]) == w.atom("WM_DELETE_WINDOW") {
			return true
		}
	case xproto.MapNotifyEvent:
		w.activate()
	case xproto.PropertyNotifyEvent:
		w.time = e.Time
		if w.raising && e.Atom == w.atom("_BATTERY_STATE_TIME") {
			w.raising = false
			w.activate()
		}
	case xproto.DestroyNotifyEvent:
		return true
	}
	return false
}
