package ui

// The Windows window: a plain Win32 window that shows the frame drawn by builder.go and
// canvas.go with StretchDIBits. Each window runs its own message loop on a locked OS thread;
// other goroutines talk to it through PostMessage.

import (
	"fmt"
	"image"
	"math"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procIsIconic            = user32.NewProc("IsIconic")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procBeginPaint          = user32.NewProc("BeginPaint")
	procEndPaint            = user32.NewProc("EndPaint")
	procInvalidateRect      = user32.NewProc("InvalidateRect")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procAdjustWindowRectEx  = user32.NewProc("AdjustWindowRectEx")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procSetCursor           = user32.NewProc("SetCursor")
	procTrackMouseEvent     = user32.NewProc("TrackMouseEvent")
	procSetCapture          = user32.NewProc("SetCapture")
	procReleaseCapture      = user32.NewProc("ReleaseCapture")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procSetProcessDPIAware  = user32.NewProc("SetProcessDPIAware")
	procCreateIconIndirect  = user32.NewProc("CreateIconIndirect")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procStretchDIBits       = gdi32.NewProc("StretchDIBits")
	procGetDeviceCaps       = gdi32.NewProc("GetDeviceCaps")
	procCreateDIBSection    = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap        = gdi32.NewProc("CreateBitmap")
	procDeleteObject        = gdi32.NewProc("DeleteObject")
	procDwmSetWindowAttr    = dwmapi.NewProc("DwmSetWindowAttribute")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy     = 0x0002
	wmPaint       = 0x000F
	wmClose       = 0x0010
	wmEraseBkgnd  = 0x0014
	wmSetCursor   = 0x0020
	wmSetIcon     = 0x0080
	wmKeyDown     = 0x0100
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmMouseLeave  = 0x02A3
	wmApp         = 0x8000
	wmRefresh     = wmApp + 1
	wmRaise       = wmApp + 2
	wmCloseReq    = wmApp + 3

	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	cwUseDefault  = 0x80000000
	swShow        = 5
	swRestore     = 9
	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
	idcArrow      = 32512
	idcHand       = 32649
	htClient      = 1
	tmeLeave      = 0x0002
	vkEscape      = 0x1B
	logPixelsX    = 88
	srcCopy       = 0x00CC0020
	iconSmall     = 0
	iconBig       = 1
	smCxIcon      = 11
	smCxSmIcon    = 49
	dwmDarkMode   = 20 // DWMWA_USE_IMMERSIVE_DARK_MODE, Windows 10 20H1 and later
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type winMsg struct {
	Hwnd     windows.HWND
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       struct{ X, Y int32 }
	LPrivate uint32
}

type winRect struct{ Left, Top, Right, Bottom int32 }

type paintStruct struct {
	Hdc       windows.Handle
	Erase     int32
	Paint     winRect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type bitmapInfoHeader struct {
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

type trackMouseEvent struct {
	Size      uint32
	Flags     uint32
	Hwnd      windows.HWND
	HoverTime uint32
}

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  windows.Handle
	HbmColor windows.Handle
}

const className = "BatteryStateSettings"

var (
	classOnce sync.Once
	classErr  error
	wndProcCB = windows.NewCallback(wndProc)
	dpiOnce   sync.Once

	windowsMu sync.Mutex
	byHwnd    = map[windows.HWND]*Window{}
)

// Window is an open window. Its methods may be called from any goroutine.
type Window struct {
	opts  Options
	hwnd  windows.HWND
	scale float64
	style uintptr

	// Window thread only.
	w, h     int // client size in pixels
	frame    []byte
	hits     []hit
	mouse    image.Point
	pressed  int
	tracking bool
	icons    []windows.Handle

	done chan struct{}
}

// Open creates and shows the window.
func Open(opts Options) (*Window, error) {
	w := &Window{opts: opts, pressed: -1, mouse: image.Pt(-1, -1), done: make(chan struct{})}
	ready := make(chan error, 1)
	go w.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return w, nil
}

// Refresh rebuilds and redraws the contents.
func (w *Window) Refresh() { w.post(wmRefresh) }

// Raise brings the window to the front.
func (w *Window) Raise() { w.post(wmRaise) }

// Close closes the window.
func (w *Window) Close() { w.post(wmCloseReq) }

// Done is closed when the window is gone.
func (w *Window) Done() <-chan struct{} { return w.done }

func (w *Window) post(msg uintptr) {
	select {
	case <-w.done:
	default:
		procPostMessageW.Call(uintptr(w.hwnd), msg, 0, 0)
	}
}

func (w *Window) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(w.done)

	dpiOnce.Do(func() { procSetProcessDPIAware.Call() })
	inst, _, _ := procGetModuleHandleW.Call(0)
	cls := windows.StringToUTF16Ptr(className)
	classOnce.Do(func() {
		cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
		wc := wndClassEx{WndProc: wndProcCB, Instance: windows.Handle(inst), ClassName: cls, Cursor: windows.Handle(cursor)}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			classErr = fmt.Errorf("RegisterClassEx: %v", err)
		}
	})
	if classErr != nil {
		ready <- classErr
		return
	}

	w.scale = systemScale()
	w.w = int(math.Round(float64(w.opts.Width) * w.scale))
	w.h = layout(w.opts, w.scale, w.w, w.mouse).y
	w.style = wsCaption | wsSysMenu | wsMinimizeBox
	ow, oh := w.outerSize()
	title := windows.StringToUTF16Ptr(w.opts.Title)
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
		w.style, cwUseDefault, cwUseDefault, uintptr(ow), uintptr(oh), 0, 0, inst, 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("CreateWindowEx: %v", err)
		return
	}
	w.hwnd = windows.HWND(hwnd)
	windowsMu.Lock()
	byHwnd[w.hwnd] = w
	windowsMu.Unlock()
	defer func() {
		windowsMu.Lock()
		delete(byHwnd, w.hwnd)
		windowsMu.Unlock()
		for _, h := range w.icons {
			procDestroyIcon.Call(uintptr(h))
		}
	}()

	if isDark(w.opts.Theme) {
		on := int32(1)
		if procDwmSetWindowAttr.Find() == nil {
			procDwmSetWindowAttr.Call(hwnd, dwmDarkMode, uintptr(unsafe.Pointer(&on)), 4)
		}
	}
	if w.opts.Icon != nil {
		for _, kind := range []struct{ metric, which uintptr }{{smCxSmIcon, iconSmall}, {smCxIcon, iconBig}} {
			size, _, _ := procGetSystemMetrics.Call(kind.metric)
			if size == 0 {
				size = 32
			}
			if h := createIcon(w.opts.Icon(int(size))); h != 0 {
				w.icons = append(w.icons, h)
				procSendMessageW.Call(hwnd, wmSetIcon, kind.which, uintptr(h))
			}
		}
	}
	w.render()
	procShowWindow.Call(hwnd, swShow)
	procSetForegroundWindow.Call(hwnd)
	ready <- nil

	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// systemScale is the display scale for a system-DPI-aware process (the tray sets that).
func systemScale() float64 {
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return 1
	}
	defer procReleaseDC.Call(0, hdc)
	dpi, _, _ := procGetDeviceCaps.Call(hdc, logPixelsX)
	if dpi < 48 {
		return 1
	}
	return min(float64(dpi)/96, 4)
}

func isDark(t Theme) bool {
	return int(t.Bg.R)+int(t.Bg.G)+int(t.Bg.B) < 3*128
}

// outerSize is the window size for the client size w.w × w.h.
func (w *Window) outerSize() (int32, int32) {
	r := winRect{Right: int32(w.w), Bottom: int32(w.h)}
	procAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&r)), w.style, 0, 0)
	return r.Right - r.Left, r.Bottom - r.Top
}

// render lays out and draws the contents and schedules a repaint. Window thread only.
func (w *Window) render() {
	b := layout(w.opts, w.scale, w.w, w.mouse)
	if b.y != w.h {
		w.h = b.y
		if w.hwnd != 0 {
			ow, oh := w.outerSize()
			procSetWindowPos.Call(uintptr(w.hwnd), 0, 0, 0, uintptr(ow), uintptr(oh), swpNoMove|swpNoZOrder|swpNoActivate)
		}
	}
	img := paint(w.opts.Theme, b, w.w, w.h)
	if len(w.frame) != len(img.Pix) {
		w.frame = make([]byte, len(img.Pix))
	}
	for i := 0; i < len(img.Pix); i += 4 {
		w.frame[i], w.frame[i+1], w.frame[i+2], w.frame[i+3] = img.Pix[i+2], img.Pix[i+1], img.Pix[i], 0xFF // BGRA
	}
	w.hits = b.hits
	if w.hwnd != 0 {
		procInvalidateRect.Call(uintptr(w.hwnd), 0, 0)
	}
}

func (w *Window) paint() {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer procEndPaint.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&ps)))
	if len(w.frame) < w.w*w.h*4 || w.w == 0 || w.h == 0 {
		return
	}
	bi := bitmapInfoHeader{Width: int32(w.w), Height: -int32(w.h), Planes: 1, BitCount: 32} // top-down, BI_RGB
	bi.Size = uint32(unsafe.Sizeof(bi))
	procStretchDIBits.Call(hdc, 0, 0, uintptr(w.w), uintptr(w.h), 0, 0, uintptr(w.w), uintptr(w.h),
		uintptr(unsafe.Pointer(&w.frame[0])), uintptr(unsafe.Pointer(&bi)), 0, srcCopy)
}

func lparamPoint(lparam uintptr) image.Point {
	return image.Pt(int(int16(lparam&0xFFFF)), int(int16(lparam>>16&0xFFFF)))
}

func wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	windowsMu.Lock()
	w := byHwnd[windows.HWND(hwnd)]
	windowsMu.Unlock()
	if w == nil {
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
		return r
	}
	switch msg {
	case wmPaint:
		w.paint()
		return 0
	case wmEraseBkgnd:
		return 1 // the frame covers everything; skipping the erase avoids flicker
	case wmMouseMove:
		if !w.tracking {
			tme := trackMouseEvent{Flags: tmeLeave, Hwnd: windows.HWND(hwnd)}
			tme.Size = uint32(unsafe.Sizeof(tme))
			procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
			w.tracking = true
		}
		p := lparamPoint(lparam)
		before := hitAt(w.hits, w.mouse)
		w.mouse = p
		if hitAt(w.hits, p) != before {
			w.render()
		}
		return 0
	case wmMouseLeave:
		w.tracking = false
		w.mouse = image.Pt(-1, -1)
		w.render()
		return 0
	case wmLButtonDown:
		w.mouse = lparamPoint(lparam)
		w.pressed = hitAt(w.hits, w.mouse)
		procSetCapture.Call(hwnd)
		return 0
	case wmLButtonUp:
		procReleaseCapture.Call()
		w.mouse = lparamPoint(lparam)
		if i := hitAt(w.hits, w.mouse); i >= 0 && i == w.pressed {
			w.hits[i].f()
		}
		w.pressed = -1
		w.render()
		return 0
	case wmSetCursor:
		if lparam&0xFFFF == htClient {
			id := uintptr(idcArrow)
			if hitAt(w.hits, w.mouse) >= 0 {
				id = idcHand
			}
			c, _, _ := procLoadCursorW.Call(0, id)
			procSetCursor.Call(c)
			return 1
		}
	case wmKeyDown:
		if wparam == vkEscape {
			procDestroyWindow.Call(hwnd)
			return 0
		}
	case wmRefresh:
		w.render()
		return 0
	case wmRaise:
		if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
			procShowWindow.Call(hwnd, swRestore)
		}
		procSetForegroundWindow.Call(hwnd)
		return 0
	case wmCloseReq, wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0) // ends this window's message loop (its own thread)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

// createIcon turns an image into an HICON with per-pixel alpha.
func createIcon(img *image.NRGBA) windows.Handle {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	bi := bitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32} // top-down, BI_RGB
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	color, _, _ := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 || bits == nil {
		return 0
	}
	defer procDeleteObject.Call(color)
	dst := unsafe.Slice((*byte)(bits), w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s := img.PixOffset(x+img.Rect.Min.X, y+img.Rect.Min.Y)
			d := (y*w + x) * 4
			dst[d], dst[d+1], dst[d+2], dst[d+3] = img.Pix[s+2], img.Pix[s+1], img.Pix[s], img.Pix[s+3] // BGRA
		}
	}
	// The AND mask is unused with an alpha channel but must exist; all zero = opaque.
	mask := make([]byte, ((w+15)/16)*2*h)
	hmask, _, _ := procCreateBitmap.Call(uintptr(w), uintptr(h), 1, 1, uintptr(unsafe.Pointer(&mask[0])))
	if hmask == 0 {
		return 0
	}
	defer procDeleteObject.Call(hmask)
	ii := iconInfo{FIcon: 1, HbmMask: windows.Handle(hmask), HbmColor: windows.Handle(color)}
	hicon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	return windows.Handle(hicon)
}
