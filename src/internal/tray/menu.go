package tray

import (
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

// com.canonical.dbusmenu: the menu a tray host shows for an item. All tray icons share one
// menuState; each icon's connection exports its own menuObject on top of it.

const menuIface = "com.canonical.dbusmenu"

type menuItem struct {
	Label     string
	Enabled   bool
	Separator bool
	Checkbox  bool
	Checked   bool
	Action    func()
}

type menuState struct {
	mu       sync.Mutex
	revision uint32
	items    []menuItem // ids are index+1; 0 is the root
	objects  []*menuObject
}

// set replaces the menu and tells every host to reload it.
func (m *menuState) set(items []menuItem) {
	m.mu.Lock()
	m.items = items
	m.revision++
	rev := m.revision
	objs := append([]*menuObject(nil), m.objects...)
	m.mu.Unlock()
	for _, o := range objs {
		_ = o.conn.Emit(menuPath, menuIface+".LayoutUpdated", rev, int32(0))
	}
}

func (m *menuState) add(o *menuObject) {
	m.mu.Lock()
	m.objects = append(m.objects, o)
	m.mu.Unlock()
}

func (m *menuState) remove(o *menuObject) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, x := range m.objects {
		if x == o {
			m.objects = append(m.objects[:i], m.objects[i+1:]...)
			return
		}
	}
}

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

type layout struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []dbus.Variant
}

type groupProps struct {
	ID    int32
	Props map[string]dbus.Variant
}

type menuEvent struct {
	ID        int32
	EventID   string
	Data      dbus.Variant
	Timestamp uint32
}

func itemProps(it menuItem) map[string]dbus.Variant {
	p := map[string]dbus.Variant{}
	if it.Separator {
		p["type"] = dbus.MakeVariant("separator")
		return p
	}
	p["label"] = dbus.MakeVariant(it.Label)
	p["enabled"] = dbus.MakeVariant(it.Enabled)
	if it.Checkbox {
		p["toggle-type"] = dbus.MakeVariant("checkmark")
		state := int32(0)
		if it.Checked {
			state = 1
		}
		p["toggle-state"] = dbus.MakeVariant(state)
	}
	return p
}

type menuObject struct {
	state *menuState
	conn  *dbus.Conn
}

func (o *menuObject) export() error {
	if err := o.conn.Export(o, menuPath, menuIface); err != nil {
		return err
	}
	props, err := prop.Export(o.conn, menuPath, prop.Map{menuIface: {
		"Version":       {Value: uint32(3), Emit: prop.EmitFalse},
		"TextDirection": {Value: "ltr", Emit: prop.EmitFalse},
		"Status":        {Value: "normal", Emit: prop.EmitFalse},
		"IconThemePath": {Value: []string{}, Emit: prop.EmitFalse},
	}})
	if err != nil {
		return err
	}
	node := &introspect.Node{
		Name: string(menuPath),
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{
				Name:       menuIface,
				Methods:    introspect.Methods(o),
				Properties: props.Introspection(menuIface),
				Signals: []introspect.Signal{
					{Name: "LayoutUpdated", Args: []introspect.Arg{{Name: "revision", Type: "u"}, {Name: "parent", Type: "i"}}},
					{Name: "ItemsPropertiesUpdated", Args: []introspect.Arg{{Name: "updatedProps", Type: "a(ia{sv})"}, {Name: "removedProps", Type: "a(ias)"}}},
				},
			},
		},
	}
	if err := o.conn.Export(introspect.NewIntrospectable(node), menuPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return err
	}
	o.state.add(o)
	return nil
}

func (o *menuObject) GetLayout(parentID int32, depth int32, names []string) (uint32, layout, *dbus.Error) {
	m := o.state
	m.mu.Lock()
	defer m.mu.Unlock()
	if parentID != 0 {
		if parentID < 1 || int(parentID) > len(m.items) {
			return m.revision, layout{}, dbus.MakeFailedError(errNoItem)
		}
		return m.revision, layout{ID: parentID, Props: itemProps(m.items[parentID-1]), Children: []dbus.Variant{}}, nil
	}
	root := layout{ID: 0, Props: map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")}, Children: []dbus.Variant{}}
	if depth != 0 {
		for i, it := range m.items {
			root.Children = append(root.Children, dbus.MakeVariant(layout{ID: int32(i + 1), Props: itemProps(it), Children: []dbus.Variant{}}))
		}
	}
	return m.revision, root, nil
}

func (o *menuObject) GetGroupProperties(ids []int32, names []string) ([]groupProps, *dbus.Error) {
	m := o.state
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []groupProps
	for _, id := range ids {
		if id == 0 {
			out = append(out, groupProps{ID: 0, Props: map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")}})
		} else if id >= 1 && int(id) <= len(m.items) {
			out = append(out, groupProps{ID: id, Props: itemProps(m.items[id-1])})
		}
	}
	return out, nil
}

func (o *menuObject) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	m := o.state
	m.mu.Lock()
	defer m.mu.Unlock()
	if id >= 1 && int(id) <= len(m.items) {
		if v, ok := itemProps(m.items[id-1])[name]; ok {
			return v, nil
		}
	}
	return dbus.MakeVariant(""), dbus.MakeFailedError(errNoItem)
}

func (o *menuObject) Event(id int32, eventID string, data dbus.Variant, timestamp uint32) *dbus.Error {
	if eventID == "clicked" {
		if f := o.state.action(id); f != nil {
			go f()
		}
	}
	return nil
}

func (o *menuObject) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	for _, e := range events {
		_ = o.Event(e.ID, e.EventID, e.Data, e.Timestamp)
	}
	return []int32{}, nil
}

func (o *menuObject) AboutToShow(id int32) (bool, *dbus.Error) { return false, nil }

func (o *menuObject) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	return []int32{}, []int32{}, nil
}

type menuError string

func (e menuError) Error() string { return string(e) }

const errNoItem = menuError("no such menu item")
