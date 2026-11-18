package setup

import "errors"

// Available reports whether this OS needs (and can do) access setup. On Windows the vendor
// HID collections are open to the logged-in user, so there is nothing to set up.
const Available = false

// Run does nothing on Windows.
func Run() error { return errors.New("access setup is only needed on Linux") }
