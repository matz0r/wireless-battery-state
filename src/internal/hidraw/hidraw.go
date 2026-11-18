// Package hidraw enumerates and talks to Linux hidraw devices (/dev/hidraw*).
package hidraw

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// SysRoot is where hidraw nodes are listed; tests point it at a fake tree.
var SysRoot = "/sys/class/hidraw"

// DevRoot is where the device files live.
var DevRoot = "/dev"

// Info describes one hidraw node, read from sysfs without opening the device.
type Info struct {
	Node       string // "hidraw3"
	Path       string // "/dev/hidraw3"
	Bus        uint16 // 0x0003 = USB, 0x0005 = Bluetooth
	VendorID   uint16
	ProductID  uint16
	Name       string // HID_NAME, e.g. "Glorious Model D- Wireless"
	Phys       string // HID_PHYS, e.g. "usb-0000:0b:00.3-1/input2"
	Interface  int    // USB interface number from Phys, -1 if unknown
	Driver     string // kernel driver bound to the HID device (hid-generic, logitech-hidpp-device, ...)
	Descriptor Descriptor
}

func (i Info) String() string {
	var tl []string
	for _, u := range i.Descriptor.TopLevel {
		tl = append(tl, fmt.Sprintf("%04X:%04X", u.Page, u.ID))
	}
	return fmt.Sprintf("%s %04X:%04X if=%d %q driver=%s collections=[%s] feature=%v",
		i.Path, i.VendorID, i.ProductID, i.Interface, i.Name, i.Driver, strings.Join(tl, " "), i.Descriptor.HasFeature)
}

// Enumerate lists all hidraw nodes, sorted by node number.
func Enumerate() ([]Info, error) {
	entries, err := os.ReadDir(SysRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Info
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "hidraw") {
			continue
		}
		info, err := readInfo(e.Name())
		if err != nil {
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(a, b int) bool { return nodeNum(out[a].Node) < nodeNum(out[b].Node) })
	return out, nil
}

func nodeNum(n string) int {
	v, _ := strconv.Atoi(strings.TrimPrefix(n, "hidraw"))
	return v
}

func readInfo(node string) (Info, error) {
	dev := filepath.Join(SysRoot, node, "device")
	info := Info{Node: node, Path: filepath.Join(DevRoot, node), Interface: -1}
	f, err := os.Open(filepath.Join(dev, "uevent"))
	if err != nil {
		return info, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "HID_ID": // 0003:0000258A:00002025
			parts := strings.Split(v, ":")
			if len(parts) == 3 {
				info.Bus = uint16(parseHex(parts[0]))
				info.VendorID = uint16(parseHex(parts[1]))
				info.ProductID = uint16(parseHex(parts[2]))
			}
		case "HID_NAME":
			info.Name = v
		case "HID_PHYS":
			info.Phys = v
			if _, in, ok := strings.Cut(v, "/input"); ok {
				if n, err := strconv.Atoi(in); err == nil {
					info.Interface = n
				}
			}
		case "DRIVER":
			info.Driver = v
		}
	}
	if desc, err := os.ReadFile(filepath.Join(dev, "report_descriptor")); err == nil {
		info.Descriptor = ParseDescriptor(desc)
	}
	return info, nil
}

func parseHex(s string) uint64 {
	v, _ := strconv.ParseUint(s, 16, 32)
	return v
}

// Device is an open hidraw node.
type Device struct {
	f  *os.File
	fd int
}

// Open opens a hidraw node for reading and writing. A permission error is returned as is,
// so callers can tell it apart with errors.Is(err, os.ErrPermission).
func Open(path string) (*Device, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return &Device{f: os.NewFile(uintptr(fd), path), fd: fd}, nil
}

// Close closes the device.
func (d *Device) Close() error { return d.f.Close() }

// Write sends an output report. The first byte is the report id (0 if reports are unnumbered).
func (d *Device) Write(report []byte) (int, error) {
	return unix.Write(d.fd, report)
}

// Read waits up to timeout for one input report. It returns 0, nil on timeout.
func (d *Device) Read(buf []byte, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for {
		left := time.Until(deadline)
		if left < 0 {
			left = 0
		}
		fds := []unix.PollFd{{Fd: int32(d.fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, int(left.Milliseconds()))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, nil
		}
		if fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return 0, errors.New("hidraw: device gone")
		}
		r, err := unix.Read(d.fd, buf)
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		return r, err
	}
}

// Flush drops input reports that are already queued.
func (d *Device) Flush() {
	buf := make([]byte, 256)
	for {
		if n, err := unix.Read(d.fd, buf); n <= 0 || err != nil {
			return
		}
	}
}

// ioctl numbers from linux/hidraw.h: _IOC(_IOC_WRITE|_IOC_READ, 'H', nr, len).
func hidIOC(nr, size uintptr) uintptr {
	const iocRead, iocWrite = 2, 1
	return (iocRead|iocWrite)<<30 | size<<16 | uintptr('H')<<8 | nr
}

// SetFeature sends a feature report. report[0] is the report id (0 if unnumbered).
func (d *Device) SetFeature(report []byte) error {
	if len(report) == 0 {
		return errors.New("hidraw: empty feature report")
	}
	_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), hidIOC(0x06, uintptr(len(report))), uintptr(unsafe.Pointer(&report[0])))
	if e != 0 {
		return e
	}
	return nil
}

// GetFeature reads a feature report into buf. buf[0] must hold the report id; on return it
// still does and the data follows, as with SetFeature.
func (d *Device) GetFeature(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, errors.New("hidraw: empty feature buffer")
	}
	n, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), hidIOC(0x07, uintptr(len(buf))), uintptr(unsafe.Pointer(&buf[0])))
	if e != 0 {
		return 0, e
	}
	return int(n), nil
}
