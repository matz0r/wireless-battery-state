//go:build !windows

package battery

// On Linux the kernel reports Logitech HID++ devices (and Bluetooth devices, game
// controllers, ...) in /sys/class/power_supply, so no HID++ driver is needed here.
var platformDrivers = []Driver{powerSupplyDriver}
