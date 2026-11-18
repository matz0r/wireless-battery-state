package hidraw

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows backend: every top-level collection of a HID device is its own device interface
// ("\\?\hid#vid_046d&pid_c547&mi_02&col01#..."), so each Info carries exactly one collection.
// Reads and writes go through overlapped ReadFile/WriteFile, feature reports through
// HidD_SetFeature/HidD_GetFeature. No cgo: hid.dll is called through syscalls.

var (
	modHid                 = windows.NewLazySystemDLL("hid.dll")
	procGetAttributes      = modHid.NewProc("HidD_GetAttributes")
	procGetPreparsedData   = modHid.NewProc("HidD_GetPreparsedData")
	procFreePreparsedData  = modHid.NewProc("HidD_FreePreparsedData")
	procGetCaps            = modHid.NewProc("HidP_GetCaps")
	procGetProductString   = modHid.NewProc("HidD_GetProductString")
	procSetFeature         = modHid.NewProc("HidD_SetFeature")
	procGetFeature         = modHid.NewProc("HidD_GetFeature")
	procFlushQueue         = modHid.NewProc("HidD_FlushQueue")
	hidStatusSuccess       = uintptr(0x00110000) // HIDP_STATUS_SUCCESS
	errNoReports           = errors.New("hidraw: device has no reports of this kind")
	errFeatureOnly         = errors.New("hidraw: device opened for feature reports only")
	errReadTimeoutInternal = errors.New("hidraw: read timed out")
)

// GUIDHID is the HID device interface class (HidD_GetHidGuid).
var GUIDHID = windows.GUID{Data1: 0x4D1E55B2, Data2: 0xF16F, Data3: 0x11CF, Data4: [8]byte{0x88, 0xCB, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30}}

type hiddAttributes struct {
	Size          uint32
	VendorID      uint16
	ProductID     uint16
	VersionNumber uint16
}

type hidpCaps struct {
	Usage                     uint16
	UsagePage                 uint16
	InputReportByteLength     uint16
	OutputReportByteLength    uint16
	FeatureReportByteLength   uint16
	Reserved                  [17]uint16
	NumberLinkCollectionNodes uint16
	Counts                    [9]uint16 // button/value caps and data indices, unused here
}

// hidBool calls a hid.dll function that returns BOOLEAN (one byte; the rest of the register is
// undefined).
func hidBool(p *windows.LazyProc, args ...uintptr) bool {
	r, _, _ := p.Call(args...)
	return byte(r) != 0
}

// Enumerate lists the HID collections that are present, sorted by path.
func Enumerate() ([]Info, error) {
	paths, err := windows.CM_Get_Device_Interface_List("", &GUIDHID, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, p := range paths {
		if p == "" {
			continue
		}
		info, ok := readInfo(p)
		if ok {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out, nil
}

// openHandle opens a device path. access 0 opens without read/write rights, which is enough for
// attributes, caps, strings and feature reports, even on mice and keyboards Windows holds
// exclusively.
func openHandle(path string, access uint32, flags uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, flags, 0)
}

func getCaps(h windows.Handle) (hidpCaps, bool) {
	var caps hidpCaps
	var pp uintptr
	if !hidBool(procGetPreparsedData, uintptr(h), uintptr(unsafe.Pointer(&pp))) {
		return caps, false
	}
	defer procFreePreparsedData.Call(pp)
	r, _, _ := procGetCaps.Call(pp, uintptr(unsafe.Pointer(&caps)))
	return caps, r == hidStatusSuccess
}

func readInfo(path string) (Info, bool) {
	info := Info{Path: path, Node: shortNode(path), Interface: -1, Phys: PhysicalKey(path)}
	h, err := openHandle(path, 0, 0)
	if err != nil {
		return info, false
	}
	defer windows.CloseHandle(h)
	attr := hiddAttributes{Size: uint32(unsafe.Sizeof(hiddAttributes{}))}
	if hidBool(procGetAttributes, uintptr(h), uintptr(unsafe.Pointer(&attr))) {
		info.VendorID, info.ProductID = attr.VendorID, attr.ProductID
	}
	if caps, ok := getCaps(h); ok {
		info.Descriptor = Descriptor{
			TopLevel:   []Usage{{Page: caps.UsagePage, ID: caps.Usage}},
			HasFeature: caps.FeatureReportByteLength > 0,
		}
		info.InputLen = int(caps.InputReportByteLength)
		info.OutputLen = int(caps.OutputReportByteLength)
		info.FeatureLen = int(caps.FeatureReportByteLength)
	}
	var name [128]uint16
	if hidBool(procGetProductString, uintptr(h), uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)-1)*2) {
		info.Name = strings.TrimSpace(windows.UTF16ToString(name[:]))
	}
	lower := strings.ToLower(path)
	if i := strings.Index(lower, "&mi_"); i >= 0 && len(lower) >= i+6 {
		if n, err := strconv.ParseUint(lower[i+4:i+6], 16, 8); err == nil {
			info.Interface = int(n)
		}
	}
	switch {
	case strings.Contains(lower, "{00001812-"), strings.Contains(lower, "{00001124-"):
		info.Bus = 0x0005 // Bluetooth (HID over GATT, classic HID)
	case strings.HasPrefix(lower, `\\?\hid#vid_`):
		info.Bus = 0x0003 // USB
	}
	return info, true
}

// Device is an open HID collection.
type Device struct {
	h          windows.Handle
	overlapped bool // opened read/write; false: feature reports only
	inLen      int
	outLen     int
	featLen    int
}

// Open opens a HID collection for reading and writing. If Windows refuses read/write access
// (mice and keyboards are held exclusively by the system), it falls back to a handle that can
// only send and receive feature reports. Access errors match os.ErrPermission.
func Open(path string) (*Device, error) {
	h, err := openHandle(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_FLAG_OVERLAPPED)
	overlapped := err == nil
	if err != nil {
		h, err = openHandle(path, 0, 0)
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: path, Err: err}
		}
	}
	d := &Device{h: h, overlapped: overlapped}
	if caps, ok := getCaps(h); ok {
		d.inLen = int(caps.InputReportByteLength)
		d.outLen = int(caps.OutputReportByteLength)
		d.featLen = int(caps.FeatureReportByteLength)
	}
	return d, nil
}

// Close closes the device.
func (d *Device) Close() error { return windows.CloseHandle(d.h) }

// waitIO waits for an overlapped operation; on timeout it cancels it and waits for the cancel,
// so the buffer is no longer in use when it returns.
func (d *Device) waitIO(ov *windows.Overlapped, timeout time.Duration) (uint32, error) {
	ms := uint32(max(timeout.Milliseconds(), 0))
	ev, _ := windows.WaitForSingleObject(ov.HEvent, ms)
	var n uint32
	if ev != windows.WAIT_OBJECT_0 {
		windows.CancelIoEx(d.h, ov)
		if err := windows.GetOverlappedResult(d.h, ov, &n, true); err != nil {
			if err == windows.ERROR_OPERATION_ABORTED {
				return 0, errReadTimeoutInternal
			}
			return 0, err
		}
		return n, nil // it completed while we were cancelling
	}
	err := windows.GetOverlappedResult(d.h, ov, &n, false)
	return n, err
}

func newOverlapped() (*windows.Overlapped, error) {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	return &windows.Overlapped{HEvent: ev}, nil
}

// Write sends an output report. The first byte is the report id (0 if reports are unnumbered);
// the report is padded to the collection's output report length.
func (d *Device) Write(report []byte) (int, error) {
	if !d.overlapped {
		return 0, errFeatureOnly
	}
	if d.outLen == 0 {
		return 0, errNoReports
	}
	buf := make([]byte, d.outLen)
	copy(buf, report)
	ov, err := newOverlapped()
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(ov.HEvent)
	err = windows.WriteFile(d.h, buf, nil, ov)
	if err != nil && err != windows.ERROR_IO_PENDING {
		return 0, err
	}
	n, err := d.waitIO(ov, time.Second)
	runtime.KeepAlive(buf)
	if err != nil {
		return 0, err
	}
	return min(int(n), len(report)), nil
}

// Read waits up to timeout for one input report. It returns 0, nil on timeout. As with Linux
// hidraw, the report id byte is dropped for unnumbered reports (report id 0), so buf[0] is the
// report id only if the device numbers its reports.
func (d *Device) Read(buf []byte, timeout time.Duration) (int, error) {
	if !d.overlapped {
		return 0, errFeatureOnly
	}
	if d.inLen == 0 {
		return 0, errNoReports
	}
	tmp := make([]byte, d.inLen)
	ov, err := newOverlapped()
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(ov.HEvent)
	err = windows.ReadFile(d.h, tmp, nil, ov)
	if err != nil && err != windows.ERROR_IO_PENDING {
		return 0, err
	}
	n, err := d.waitIO(ov, timeout)
	runtime.KeepAlive(tmp)
	if err == errReadTimeoutInternal {
		return 0, nil
	}
	if err != nil {
		if err == windows.ERROR_DEVICE_NOT_CONNECTED {
			return 0, errors.New("hidraw: device gone")
		}
		return 0, err
	}
	data := tmp[:n]
	if len(data) > 0 && data[0] == 0 {
		data = data[1:]
	}
	return copy(buf, data), nil
}

// Flush drops input reports that are already queued.
func (d *Device) Flush() {
	procFlushQueue.Call(uintptr(d.h))
}

// SetFeature sends a feature report. report[0] is the report id (0 if unnumbered); the report
// is padded or cut to the collection's feature report length.
func (d *Device) SetFeature(report []byte) error {
	if len(report) == 0 {
		return errors.New("hidraw: empty feature report")
	}
	if d.featLen == 0 {
		return errNoReports
	}
	buf := make([]byte, d.featLen)
	copy(buf, report)
	r, _, e := procSetFeature.Call(uintptr(d.h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if byte(r) == 0 {
		return callErr("HidD_SetFeature", e)
	}
	return nil
}

// GetFeature reads a feature report into buf. buf[0] must hold the report id; on return it
// still does and the data follows, as with SetFeature.
func (d *Device) GetFeature(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, errors.New("hidraw: empty feature buffer")
	}
	if d.featLen == 0 {
		return 0, errNoReports
	}
	tmp := make([]byte, d.featLen)
	tmp[0] = buf[0]
	r, _, e := procGetFeature.Call(uintptr(d.h), uintptr(unsafe.Pointer(&tmp[0])), uintptr(len(tmp)))
	if byte(r) == 0 {
		return 0, callErr("HidD_GetFeature", e)
	}
	return copy(buf, tmp), nil
}

func callErr(fn string, e error) error {
	if errno, ok := e.(syscall.Errno); ok && errno != 0 {
		return fmt.Errorf("%s: %w", fn, errno)
	}
	return fmt.Errorf("%s failed", fn)
}
