package battery

// Windows has no /sys/class/power_supply; Logitech receivers are read over HID++ directly.
var platformDrivers = []Driver{hidppDriver}
