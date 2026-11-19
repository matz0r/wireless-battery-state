package tray

import (
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/matz0r/wireless-battery-state/src/internal/hidraw"
	"github.com/matz0r/wireless-battery-state/src/internal/icon"
	"github.com/matz0r/wireless-battery-state/src/internal/notify"
)

// Windows backend: one notification-area icon (Shell_NotifyIcon) per device, all owned by one
// hidden window whose message loop runs on a locked OS thread. Other goroutines hand work to
// that thread through do(). Menu actions run on their own goroutines, so the window thread
// never waits for the app.

var (
	user32                         = windows.NewLazySystemDLL("user32.dll")
	shell32                        = windows.NewLazySystemDLL("shell32.dll")
	gdi32                          = windows.NewLazySystemDLL("gdi32.dll")
	kernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW           = user32.NewProc("RegisterClassExW")
	procCreateWindowExW            = user32.NewProc("CreateWindowExW")
	procDefWindowProcW             = user32.NewProc("DefWindowProcW")
	procDestroyWindow              = user32.NewProc("DestroyWindow")
	procGetMessageW                = user32.NewProc("GetMessageW")
	procTranslateMessage           = user32.NewProc("TranslateMessage")
	procDispatchMessageW           = user32.NewProc("DispatchMessageW")
	procPostMessageW               = user32.NewProc("PostMessageW")
	procPostQuitMessage            = user32.NewProc("PostQuitMessage")
	procRegisterWindowMessageW     = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu            = user32.NewProc("CreatePopupMenu")
	procAppendMenuW                = user32.NewProc("AppendMenuW")
	procTrackPopupMenu             = user32.NewProc("TrackPopupMenu")
	procDestroyMenu                = user32.NewProc("DestroyMenu")
	procSetForegroundWindow        = user32.NewProc("SetForegroundWindow")
	procCreateIconIndirect         = user32.NewProc("CreateIconIndirect")
	procDestroyIcon                = user32.NewProc("DestroyIcon")
	procGetSystemMetrics           = user32.NewProc("GetSystemMetrics")
	procSetProcessDPIAware         = user32.NewProc("SetProcessDPIAware")
	procRegisterDeviceNotification = user32.NewProc("RegisterDeviceNotificationW")
	procUnregisterDeviceNotif      = user32.NewProc("UnregisterDeviceNotification")
	procShellNotifyIconW           = shell32.NewProc("Shell_NotifyIconW")
	procCreateDIBSection           = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap               = gdi32.NewProc("CreateBitmap")
	procDeleteObject               = gdi32.NewProc("DeleteObject")
	procGetModuleHandleW           = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmNull          = 0x0000
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmQueryEndSess  = 0x0011
	wmEndSession    = 0x0016
	wmContextMenu   = 0x007B
	wmDeviceChange  = 0x0219
	wmApp           = 0x8000
	wmRun           = wmApp + 1 // run queued do() calls
	wmTrayCallback  = wmApp + 2 // notification-area events
	ninSelect       = 0x0400
	ninKeySelect    = 0x0401
	nimAdd          = 0
	nimModify       = 1
	nimDelete       = 2
	nimSetVersion   = 4
	nifMessage      = 0x01
	nifIcon         = 0x02
	nifTip          = 0x04
	nifInfo         = 0x10
	nifShowTip      = 0x80
	niifUser        = 0x04
	niifLargeIcon   = 0x20
	notifyIconV4    = 4
	mfString        = 0x0000
	mfGrayed        = 0x0001
	mfDisabled      = 0x0002
	mfChecked       = 0x0008
	mfSeparator     = 0x0800
	tpmRightButton  = 0x0002
	tpmNoNotify     = 0x0080
	tpmReturnCmd    = 0x0100
	smCxIcon        = 11
	smCxSmIcon      = 49
	dbtDevArrival   = 0x8000
	dbtDevRemoved   = 0x8004
	dbtDevTypeIface = 5
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

type notifyIconData struct {
	Size            uint32
	Wnd             windows.HWND
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            windows.Handle
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32 // union with uTimeout
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        windows.GUID
	BalloonIcon     windows.Handle
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

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  windows.Handle
	HbmColor windows.Handle
}

type devBroadcastDeviceInterface struct {
	Size       uint32
	DeviceType uint32
	Reserved   uint32
	ClassGUID  windows.GUID
	Name       [1]uint16
}

type winPlatform struct {
	hwnd           windows.HWND
	calls          chan func()
	loopDone       chan struct{}
	quit           func()
	taskbarCreated uint32
	iconSize       int
	devNotify      uintptr

	mu       sync.Mutex
	menu     *menuState
	changed  func() // hotplug callback
	debounce *time.Timer

	// Window thread only.
	items   map[uint32]*winItem
	appIcon windows.Handle
}

type winItem struct {
	p        *winPlatform
	id       uint32
	look     appearance
	hicon    windows.Handle
	added    bool
	activate func() // left click; nil shows the menu
}

// The window procedure is a single callback for the process, so it finds the platform here.
var (
	thePlatform *winPlatform
	wndProcCB   = windows.NewCallback(wndProc)
	dpiOnce     sync.Once
)

func newPlatform(quit func()) (platform, error) {
	if thePlatform != nil {
		return nil, errors.New("tray already started")
	}
	dpiOnce.Do(func() { procSetProcessDPIAware.Call() })
	p := &winPlatform{
		calls:    make(chan func(), 64),
		loopDone: make(chan struct{}),
		quit:     quit,
		items:    map[uint32]*winItem{},
	}
	ready := make(chan error, 1)
	go p.loop(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	notify.SetHandler(p.balloon)
	return p, nil
}

// loop creates the window and runs its message loop on one OS thread.
func (p *winPlatform) loop(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(p.loopDone)

	inst, _, _ := procGetModuleHandleW.Call(0)
	className := windows.StringToUTF16Ptr("BatteryStateTray")
	wc := wndClassEx{WndProc: wndProcCB, Instance: windows.Handle(inst), ClassName: className}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		ready <- fmt.Errorf("RegisterClassEx: %v", err)
		return
	}
	thePlatform = p
	title := windows.StringToUTF16Ptr("Battery State")
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, 0, 0, inst, 0)
	if hwnd == 0 {
		thePlatform = nil
		ready <- fmt.Errorf("CreateWindowEx: %v", err)
		return
	}
	p.hwnd = windows.HWND(hwnd)
	tc, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("TaskbarCreated"))))
	p.taskbarCreated = uint32(tc)
	if s, _, _ := procGetSystemMetrics.Call(smCxSmIcon); s > 0 {
		p.iconSize = int(s)
	} else {
		p.iconSize = 16
	}
	// Ask for HID arrivals and removals, so a plugged-in receiver shows up within seconds.
	f := devBroadcastDeviceInterface{DeviceType: dbtDevTypeIface, ClassGUID: hidraw.GUIDHID}
	f.Size = uint32(unsafe.Sizeof(f))
	p.devNotify, _, _ = procRegisterDeviceNotification.Call(hwnd, uintptr(unsafe.Pointer(&f)), 0)
	ready <- nil

	var msg winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// do runs f on the window thread and waits for it. It must not be called from that thread.
func (p *winPlatform) do(f func()) {
	done := make(chan struct{})
	select {
	case p.calls <- func() { f(); close(done) }:
	case <-p.loopDone:
		return
	}
	procPostMessageW.Call(uintptr(p.hwnd), wmRun, 0, 0)
	select {
	case <-done:
	case <-p.loopDone:
	}
}

func wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	p := thePlatform
	if p == nil {
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
		return r
	}
	switch {
	case msg == wmRun:
		for {
			select {
			case f := <-p.calls:
				f()
			default:
				return 0
			}
		}
	case msg == wmTrayCallback:
		// NOTIFYICON_VERSION_4: event in the low word of lparam, icon id in the high word
		// (only its low 16 bits), the click position in wparam.
		x, y := int32(int16(wparam&0xFFFF)), int32(int16(wparam>>16&0xFFFF))
		switch lparam & 0xFFFF {
		case ninSelect, ninKeySelect: // left click, or Enter/Space on a focused icon
			if f := p.activateFor(uint16(lparam >> 16 & 0xFFFF)); f != nil {
				go f()
			} else {
				p.showMenu(x, y)
			}
		case wmContextMenu:
			p.showMenu(x, y)
		}
		return 0
	case msg == wmDeviceChange:
		if wparam == dbtDevArrival || wparam == dbtDevRemoved {
			p.hotplug()
		}
		return 1
	case p.taskbarCreated != 0 && uint32(msg) == p.taskbarCreated:
		// Explorer (re)started: icons have to be added again.
		for _, it := range p.items {
			it.added = false
			it.show()
		}
		return 0
	case msg == wmClose: // e.g. "taskkill /im battery-state.exe" without /f
		go p.quit()
		return 0
	case msg == wmQueryEndSess:
		return 1
	case msg == wmEndSession:
		if wparam != 0 {
			go p.quit()
		}
		return 0
	case msg == wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

func (p *winPlatform) stop() {
	notify.SetHandler(nil)
	p.do(func() {
		for _, it := range p.items {
			it.remove()
		}
		if p.appIcon != 0 {
			procDestroyIcon.Call(uintptr(p.appIcon))
		}
		if p.devNotify != 0 {
			procUnregisterDeviceNotif.Call(p.devNotify)
		}
		procDestroyWindow.Call(uintptr(p.hwnd))
	})
	<-p.loopDone
	thePlatform = nil
}

func (p *winPlatform) watchHotplug(changed func(), quit <-chan struct{}) {
	p.mu.Lock()
	p.changed = changed
	p.mu.Unlock()
}

// hotplug is called for every arriving or leaving HID collection; one device brings several,
// so the refresh waits until they have settled.
func (p *winPlatform) hotplug() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.changed == nil {
		return
	}
	if p.debounce != nil {
		p.debounce.Stop()
	}
	p.debounce = time.AfterFunc(1500*time.Millisecond, p.changed)
}

// stableID gives each device the same icon id across restarts, so Windows remembers whether
// the user chose to show it on the taskbar or in the overflow area.
func (p *winPlatform) stableID(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	id := h.Sum32() & 0x7FFFFFFF
	for id == 0 || p.lowTaken(uint16(id)) {
		id++
	}
	return id
}

// lowTaken reports whether an icon's id already ends in low: click events only carry the low
// 16 bits of the id, so those must be unique too.
func (p *winPlatform) lowTaken(low uint16) bool {
	for id := range p.items {
		if uint16(id) == low {
			return true
		}
	}
	return false
}

func (p *winPlatform) newItem(key string, look appearance, m *menuState, activate func()) (trayItem, error) {
	p.mu.Lock()
	p.menu = m
	p.mu.Unlock()
	var it *winItem
	p.do(func() {
		it = &winItem{p: p, id: p.stableID(key), look: look, activate: activate}
		p.items[it.id] = it
		it.show() // if Explorer isn't up yet (early at login), TaskbarCreated adds it later
	})
	if it == nil {
		return nil, errors.New("tray window is gone")
	}
	return it, nil
}

func (it *winItem) update(look appearance) {
	it.p.do(func() {
		it.look = look
		it.show()
	})
}

func (it *winItem) close() {
	it.p.do(func() {
		it.remove()
		delete(it.p.items, it.id)
	})
}

func (it *winItem) nid() notifyIconData {
	d := notifyIconData{Wnd: it.p.hwnd, ID: it.id}
	d.Size = uint32(unsafe.Sizeof(d))
	return d
}

// show adds or refreshes the icon. Window thread only.
func (it *winItem) show() {
	hicon := createIcon(it.look.Render(it.p.iconSize))
	d := it.nid()
	d.Flags = nifMessage | nifIcon | nifTip | nifShowTip
	d.CallbackMessage = wmTrayCallback
	d.Icon = hicon
	tip := it.look.TipTitle
	if it.look.TipText != "" {
		tip += "\n" + it.look.TipText
	}
	copyUTF16(d.Tip[:], tip)
	op := uintptr(nimModify)
	if !it.added {
		op = nimAdd
	}
	r, _, _ := procShellNotifyIconW.Call(op, uintptr(unsafe.Pointer(&d)))
	if r == 0 && it.added { // Explorer lost it; add it again
		r, _, _ = procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&d)))
		it.added = false
	}
	if r != 0 && !it.added {
		it.added = true
		v := it.nid()
		v.Version = notifyIconV4
		procShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&v)))
	}
	if it.hicon != 0 {
		procDestroyIcon.Call(uintptr(it.hicon))
	}
	it.hicon = hicon
}

// remove deletes the icon. Window thread only.
func (it *winItem) remove() {
	d := it.nid()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&d)))
	it.added = false
	if it.hicon != 0 {
		procDestroyIcon.Call(uintptr(it.hicon))
		it.hicon = 0
	}
}

// activateFor returns the click action of the icon whose id ends in low. Window thread only.
func (p *winPlatform) activateFor(low uint16) func() {
	for id, it := range p.items {
		if uint16(id) == low {
			return it.activate
		}
	}
	return nil
}

// showMenu shows the shared menu at x, y and runs the chosen action. Window thread only.
func (p *winPlatform) showMenu(x, y int32) {
	p.mu.Lock()
	m := p.menu
	p.mu.Unlock()
	if m == nil {
		return
	}
	items := m.snapshot()
	h, _, _ := procCreatePopupMenu.Call()
	if h == 0 {
		return
	}
	defer procDestroyMenu.Call(h)
	for i, it := range items {
		if it.Separator {
			procAppendMenuW.Call(h, mfSeparator, 0, 0)
			continue
		}
		flags := uintptr(mfString)
		switch {
		case it.Action == nil:
			flags |= mfDisabled // information line: not clickable, but not greyed out either
		case !it.Enabled:
			flags |= mfGrayed
		}
		if it.Checked {
			flags |= mfChecked
		}
		label := strings.ReplaceAll(it.Label, "&", "&&") // & marks an access key
		procAppendMenuW.Call(h, flags, uintptr(i+1), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(label))))
	}
	// The menu only closes on an outside click if our window is in the foreground.
	procSetForegroundWindow.Call(uintptr(p.hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(h, tpmReturnCmd|tpmNoNotify|tpmRightButton, uintptr(x), uintptr(y), 0, uintptr(p.hwnd), 0)
	procPostMessageW.Call(uintptr(p.hwnd), wmNull, 0, 0)
	if i := int(cmd) - 1; i >= 0 && i < len(items) && items[i].Enabled && items[i].Action != nil {
		go items[i].Action()
	}
}

// balloon shows a notification on the first icon; Windows 10 and 11 turn it into a toast.
func (p *winPlatform) balloon(summary, body string) {
	p.do(func() {
		var target *winItem
		for _, it := range p.items {
			if it.added && (target == nil || it.id < target.id) {
				target = it
			}
		}
		if target == nil {
			return
		}
		if p.appIcon == 0 {
			size := 32
			if s, _, _ := procGetSystemMetrics.Call(smCxIcon); s > 0 {
				size = int(s)
			}
			p.appIcon = createIcon(icon.AppIcon(size))
		}
		d := target.nid()
		d.Flags = nifInfo
		copyUTF16(d.InfoTitle[:], summary)
		copyUTF16(d.Info[:], body)
		d.InfoFlags = niifUser | niifLargeIcon
		d.BalloonIcon = p.appIcon
		procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&d)))
	})
}

// copyUTF16 copies s into a fixed, NUL-terminated buffer, cutting it if needed.
func copyUTF16(dst []uint16, s string) {
	u := windows.StringToUTF16(s) // ends in NUL
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
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

// singleInstance makes sure only one tray runs per user session.
func singleInstance() (func(), error) {
	h, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(`Local\battery-state-tray`))
	if err == windows.ERROR_ALREADY_EXISTS {
		windows.CloseHandle(h)
		return nil, ErrRunning
	}
	if err != nil {
		return nil, err
	}
	return func() { windows.CloseHandle(h) }, nil
}
