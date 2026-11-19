package notify

import "sync"

// On Windows a notification is a balloon on one of the tray icons, which Windows 10 and 11 show
// as a toast. The tray registers how to show it; until then notifications are dropped.

var (
	mu      sync.Mutex
	handler func(summary, body string)
)

// SetHandler sets the function that shows notifications (the tray's balloon).
func SetHandler(h func(summary, body string)) {
	mu.Lock()
	handler = h
	mu.Unlock()
}

// Send shows a notification. It is dropped if the tray isn't running.
func Send(summary, body string) {
	mu.Lock()
	h := handler
	mu.Unlock()
	if h != nil {
		h(summary, body)
	}
}
