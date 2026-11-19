package hidraw

import "strings"

// Helpers for Windows device interface paths. They are plain string functions, so they are
// built and tested on every OS.

// shortNode shortens "\\?\hid#vid_046d&pid_c547&mi_02&col01#8&2a2b3c4d&0&0000#{guid}" to
// "vid_046d&pid_c547&mi_02&col01#8&2a2b3c4d&0&0000" for display.
func shortNode(path string) string {
	s := strings.TrimPrefix(strings.ToLower(path), `\\?\hid#`)
	if i := strings.LastIndex(s, "#{"); i >= 0 {
		s = s[:i]
	}
	return s
}

// PhysicalKey identifies the device interface a collection belongs to: the collections of one
// interface differ only in "&colNN" and in the last "&NNNN" of the instance part. Paths of
// another shape are returned lower-cased as they are.
func PhysicalKey(path string) string {
	p := strings.ToLower(path)
	parts := strings.Split(p, "#")
	if len(parts) < 4 {
		return p
	}
	hw, inst := parts[1], parts[2]
	if i := strings.Index(hw, "&col"); i >= 0 && len(hw) >= i+6 {
		hw = hw[:i] + hw[i+6:]
	}
	if i := strings.LastIndex(inst, "&"); i >= 0 {
		inst = inst[:i]
	}
	return hw + "#" + inst
}
