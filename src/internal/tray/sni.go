package tray

import (
	"fmt"
	"os"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

// One StatusNotifierItem (a tray icon) on its own D-Bus connection, so several icons can
// live in one process: each registers its own well-known name with the watcher, like
// libappindicator does. Works with KDE, Cinnamon, XFCE, MATE, LXQt, Budgie, Waybar and GNOME
// with the AppIndicator extension.

const (
	sniIface     = "org.kde.StatusNotifierItem"
	sniPath      = dbus.ObjectPath("/StatusNotifierItem")
	menuPath     = dbus.ObjectPath("/MenuBar")
	watcherName  = "org.kde.StatusNotifierWatcher"
	watcherPath  = dbus.ObjectPath("/StatusNotifierWatcher")
	watcherIface = "org.kde.StatusNotifierWatcher"
)

type pixmap struct {
	W, H int32
	Pix  []byte
}

type tooltip struct {
	Icon    string
	Pixmaps []pixmap
	Title   string
	Text    string
}

type item struct {
	conn  *dbus.Conn
	name  string
	props *prop.Properties
	menu  *menuObject
	mu    sync.Mutex
	done  chan struct{}
	// activate runs on a left click (the host shows the menu on a right click).
	activate func()
}

var itemSeq struct {
	sync.Mutex
	n int
}

// newItem connects to the session bus and registers a tray icon. activate runs when the
// icon is clicked.
func newItem(id, title string, icons []pixmap, tip tooltip, m *menuState, activate func()) (*item, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("session bus: %w", err)
	}
	itemSeq.Lock()
	itemSeq.n++
	name := fmt.Sprintf("org.kde.StatusNotifierItem-%d-%d", os.Getpid(), itemSeq.n)
	itemSeq.Unlock()

	it := &item{conn: conn, name: name, done: make(chan struct{}), activate: activate}
	if err := conn.Export(sniMethods{it}, sniPath, sniIface); err != nil {
		conn.Close()
		return nil, err
	}
	it.props, err = prop.Export(conn, sniPath, prop.Map{sniIface: {
		"Category":   {Value: "Hardware", Emit: prop.EmitFalse},
		"Id":         {Value: id, Emit: prop.EmitFalse},
		"Title":      {Value: title, Emit: prop.EmitFalse},
		"Status":     {Value: "Active", Emit: prop.EmitFalse},
		"WindowId":   {Value: int32(0), Emit: prop.EmitFalse},
		"IconName":   {Value: "", Emit: prop.EmitFalse},
		"IconPixmap": {Value: icons, Emit: prop.EmitFalse},
		"ToolTip":    {Value: tip, Emit: prop.EmitFalse},
		"ItemIsMenu": {Value: false, Emit: prop.EmitFalse},
		"Menu":       {Value: menuPath, Emit: prop.EmitFalse},
	}})
	if err != nil {
		conn.Close()
		return nil, err
	}
	it.menu = &menuObject{state: m, conn: conn}
	if err := it.menu.export(); err != nil {
		conn.Close()
		return nil, err
	}
	node := &introspect.Node{
		Name: string(sniPath),
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{
				Name:       sniIface,
				Methods:    introspect.Methods(sniMethods{}),
				Properties: it.props.Introspection(sniIface),
				Signals: []introspect.Signal{
					{Name: "NewTitle"}, {Name: "NewIcon"}, {Name: "NewToolTip"},
					{Name: "NewStatus", Args: []introspect.Arg{{Name: "status", Type: "s"}}},
				},
			},
		},
	}
	if err := conn.Export(introspect.NewIntrospectable(node), sniPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		conn.Close()
		return nil, err
	}
	if reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return nil, fmt.Errorf("can't own %s: %v", name, err)
	}

	// Register now, and again whenever the watcher (re)starts, e.g. after a panel restart.
	_ = conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, watcherName),
	)
	sig := make(chan *dbus.Signal, 8)
	conn.Signal(sig)
	go func() {
		for {
			select {
			case s, ok := <-sig:
				if !ok {
					return
				}
				if s.Name == "org.freedesktop.DBus.NameOwnerChanged" && len(s.Body) == 3 {
					if newOwner, _ := s.Body[2].(string); newOwner != "" {
						it.register()
					}
				}
			case <-it.done:
				return
			}
		}
	}()
	if err := it.register(); err != nil {
		conn.Close()
		return nil, err
	}
	return it, nil
}

func (it *item) register() error {
	call := it.conn.Object(watcherName, watcherPath).Call(watcherIface+".RegisterStatusNotifierItem", 0, it.name)
	if call.Err != nil {
		return fmt.Errorf("no system tray (StatusNotifierWatcher): %w", call.Err)
	}
	return nil
}

// update changes the icon, title and tooltip.
func (it *item) update(title string, icons []pixmap, tip tooltip) {
	it.mu.Lock()
	defer it.mu.Unlock()
	it.props.SetMust(sniIface, "Title", title)
	it.props.SetMust(sniIface, "IconPixmap", icons)
	it.props.SetMust(sniIface, "ToolTip", tip)
	_ = it.conn.Emit(sniPath, sniIface+".NewTitle")
	_ = it.conn.Emit(sniPath, sniIface+".NewIcon")
	_ = it.conn.Emit(sniPath, sniIface+".NewToolTip")
}

func (it *item) close() {
	it.menu.state.remove(it.menu)
	close(it.done)
	it.conn.Close() // dropping the name removes the icon from the tray
}

// sniMethods are the methods hosts call. ItemIsMenu is false, so a left click calls Activate
// (the settings window) and a right click opens the menu.
type sniMethods struct{ it *item }

func (m sniMethods) Activate(x, y int32) *dbus.Error {
	if m.it != nil && m.it.activate != nil {
		go m.it.activate()
	}
	return nil
}

func (sniMethods) SecondaryActivate(x, y int32) *dbus.Error { return nil }
func (sniMethods) ContextMenu(x, y int32) *dbus.Error       { return nil }
func (sniMethods) Scroll(delta int32, orientation string) *dbus.Error {
	return nil
}
