package tray

import "sync"

// The menu shared by all tray icons: one list of items, rebuilt whenever the devices change.
// Backends either listen for changes (dbusmenu on Linux) or read a snapshot when the menu is
// opened (Windows).

type menuItem struct {
	Label     string
	Enabled   bool
	Separator bool
	Checkbox  bool
	Checked   bool
	Action    func()
}

type menuListener interface {
	layoutUpdated(revision uint32)
}

type menuState struct {
	mu        sync.Mutex
	revision  uint32
	items     []menuItem // ids are index+1; 0 is the root
	listeners []menuListener
}

// set replaces the menu and tells every listener.
func (m *menuState) set(items []menuItem) {
	m.mu.Lock()
	m.items = items
	m.revision++
	rev := m.revision
	ls := append([]menuListener(nil), m.listeners...)
	m.mu.Unlock()
	for _, l := range ls {
		l.layoutUpdated(rev)
	}
}

func (m *menuState) snapshot() []menuItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]menuItem(nil), m.items...)
}

func (m *menuState) add(l menuListener) {
	m.mu.Lock()
	m.listeners = append(m.listeners, l)
	m.mu.Unlock()
}

func (m *menuState) remove(l menuListener) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, x := range m.listeners {
		if x == l {
			m.listeners = append(m.listeners[:i], m.listeners[i+1:]...)
			return
		}
	}
}

// action returns the action of menu item id (1-based), or nil if it has none or is disabled.
func (m *menuState) action(id int32) func() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.items) {
		return nil
	}
	it := m.items[id-1]
	if !it.Enabled {
		return nil
	}
	return it.Action
}
