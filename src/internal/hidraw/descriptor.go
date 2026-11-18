package hidraw

// Usage is a HID usage page and usage id, e.g. 0x0001:0x0002 for a mouse.
type Usage struct {
	Page uint16
	ID   uint16
}

// Descriptor holds what the drivers need to know about a HID report descriptor.
type Descriptor struct {
	// TopLevel lists the application collections in order.
	TopLevel []Usage
	// HasFeature is true if the descriptor declares at least one feature report.
	HasFeature bool
	// HasReportIDs is true if reports are numbered.
	HasReportIDs bool
}

// Has reports whether one of the top-level collections is page:id.
func (d Descriptor) Has(page, id uint16) bool {
	for _, u := range d.TopLevel {
		if u.Page == page && u.ID == id {
			return true
		}
	}
	return false
}

// VendorDefined reports whether a top-level collection is on a vendor page (0xFF00-0xFFFF).
func (d Descriptor) VendorDefined() bool {
	for _, u := range d.TopLevel {
		if u.Page >= 0xFF00 {
			return true
		}
	}
	return false
}

// ParseDescriptor walks the short items of a HID report descriptor. It only tracks usage
// pages, usages, collections, report ids and feature items; everything else is skipped.
func ParseDescriptor(b []byte) Descriptor {
	var d Descriptor
	var page uint16
	var usages []uint32
	depth := 0
	for i := 0; i < len(b); {
		prefix := b[i]
		if prefix == 0xFE { // long item: 0xFE, size, tag, data...
			if i+1 >= len(b) {
				break
			}
			i += 3 + int(b[i+1])
			continue
		}
		size := int(prefix & 0x03)
		if size == 3 {
			size = 4
		}
		if i+1+size > len(b) {
			break
		}
		var v uint32
		for k := 0; k < size; k++ {
			v |= uint32(b[i+1+k]) << (8 * k)
		}
		typ := (prefix >> 2) & 0x03
		tag := prefix >> 4
		i += 1 + size

		switch typ {
		case 0: // main
			switch tag {
			case 0x0A: // collection
				if depth == 0 && v == 0x01 { // application
					u := Usage{Page: page}
					if len(usages) > 0 {
						u = usageOf(page, usages[0])
					}
					d.TopLevel = append(d.TopLevel, u)
				}
				depth++
			case 0x0C: // end collection
				if depth > 0 {
					depth--
				}
			case 0x0B: // feature
				d.HasFeature = true
			}
			usages = usages[:0]
		case 1: // global
			switch tag {
			case 0x00:
				page = uint16(v)
			case 0x08:
				d.HasReportIDs = true
			}
		case 2: // local
			if tag == 0x00 {
				// A 4-byte usage carries its own page in the upper 16 bits (see usageOf).
				usages = append(usages, v)
			}
		}
	}
	return d
}

func usageOf(page uint16, v uint32) Usage {
	if v > 0xFFFF {
		return Usage{Page: uint16(v >> 16), ID: uint16(v)}
	}
	return Usage{Page: page, ID: uint16(v)}
}
