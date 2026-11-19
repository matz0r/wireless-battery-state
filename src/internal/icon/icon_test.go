package icon

import "testing"

func TestRenderSizes(t *testing.T) {
	states := []State{
		{Percent: 100, Low: 15},
		{Percent: 7, Low: 15},
		{Percent: 50, Charging: true, Low: 15},
		{Percent: -1, Asleep: true},
		{Percent: -1, NoAccess: true},
		{Percent: 88, Low: 15, Glyph: MouseGlyph},
		{Percent: 100, Low: 15, Glyph: KeyboardGlyph},
		{Percent: 3, Charging: true, Low: 15, Glyph: HeadsetGlyph},
		{Percent: -1, NoAccess: true, Glyph: MouseGlyph},
	}
	for _, s := range states {
		for _, size := range Sizes {
			img := Render(s, size)
			if img.Bounds().Dx() != size || img.Bounds().Dy() != size {
				t.Fatalf("size %d: got %v", size, img.Bounds())
			}
			opaque := 0
			for i := 3; i < len(img.Pix); i += 4 {
				if img.Pix[i] > 0 {
					opaque++
				}
			}
			if opaque == 0 {
				t.Errorf("%+v at %d: empty icon", s, size)
			}
			if got := len(ARGB32(img)); got != size*size*4 {
				t.Errorf("ARGB32 length %d", got)
			}
		}
	}
}

func TestText(t *testing.T) {
	if Text(State{Percent: 42}) != "42" || Text(State{Percent: -1}) != "–" || Text(State{NoAccess: true}) != "?" {
		t.Fatal("unexpected labels")
	}
}

func TestGlyphsHaveInk(t *testing.T) {
	for _, g := range []Glyph{MouseGlyph, KeyboardGlyph, HeadsetGlyph} {
		for _, h := range []int{7, 8, 17} {
			w := int(float64(h) * g.aspect())
			m := glyphMask(g, w, h)
			ink := 0
			for _, a := range m.Pix {
				if a > 128 {
					ink++
				}
			}
			if ink < w*h/5 {
				t.Errorf("glyph %d at %dx%d: only %d inked pixels", g, w, h, ink)
			}
		}
	}
}

// Charging adds the bolt (yellow pixels) and keeps the level color on the bar.
func TestChargingBolt(t *testing.T) {
	yellow := func(s State) int {
		n := 0
		img := Render(s, 48)
		for i := 0; i < len(img.Pix); i += 4 {
			if img.Pix[i] > 0xE0 && img.Pix[i+1] > 0xC0 && img.Pix[i+2] < 0x80 && img.Pix[i+3] > 0xC0 {
				n++
			}
		}
		return n
	}
	for _, g := range []Glyph{NoGlyph, MouseGlyph, KeyboardGlyph, HeadsetGlyph} {
		on := State{Percent: 94, Charging: true, Low: 15, Glyph: g}
		off := on
		off.Charging = false
		if yellow(on) < 10 || yellow(off) != 0 {
			t.Errorf("glyph %d: bolt pixels charging %d, not charging %d", g, yellow(on), yellow(off))
		}
		bar := func(s State) [3]uint8 {
			c := Render(s, 48).NRGBAAt(24, 47)
			return [3]uint8{c.R, c.G, c.B}
		}
		if bar(on) != bar(off) {
			t.Errorf("glyph %d: charging changes the bar color", g)
		}
	}
}

func TestAppIcon(t *testing.T) {
	img := AppIcon(48)
	if img.Bounds().Dx() != 48 || img.NRGBAAt(24, 24).A == 0 {
		t.Fatal("logo not decoded/scaled")
	}
}
