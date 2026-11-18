package icon

import "testing"

func TestRenderSizes(t *testing.T) {
	states := []State{
		{Percent: 100, Low: 15},
		{Percent: 7, Low: 15},
		{Percent: 50, Charging: true, Low: 15},
		{Percent: -1, Asleep: true},
		{Percent: -1, NoAccess: true},
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
