package ui

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// The desktop's sans-serif font (through fc-match) if it can be read, the Go fonts otherwise.

var (
	fontsOnce     sync.Once
	regular, bold *sfnt.Font
	faceMu        sync.Mutex
	faceCache     = map[faceKey]font.Face{}
)

type faceKey struct {
	bold bool
	px   float64
}

func loadFonts() {
	regular = systemFont("sans-serif")
	bold = systemFont("sans-serif:bold")
	if regular == nil || bold == nil {
		regular, _ = opentype.Parse(goregular.TTF)
		bold, _ = opentype.Parse(gobold.TTF)
	}
}

func systemFont(pattern string) *sfnt.Font {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "fc-match", "-f", "%{file}", pattern).Output()
	if err != nil {
		return nil
	}
	path := strings.TrimSpace(string(out))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if strings.HasSuffix(strings.ToLower(path), ".ttc") {
		c, err := opentype.ParseCollection(data)
		if err != nil {
			return nil
		}
		f, err := c.Font(0)
		if err != nil {
			return nil
		}
		return f
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return nil
	}
	// The font must have the glyphs we draw.
	var buf sfnt.Buffer
	for _, r := range "Ag…%" {
		if i, err := f.GlyphIndex(&buf, r); err != nil || i == 0 {
			return nil
		}
	}
	return f
}

// face returns a font face with the given pixel size.
func face(isBold bool, px float64) font.Face {
	fontsOnce.Do(loadFonts)
	k := faceKey{isBold, px}
	faceMu.Lock()
	defer faceMu.Unlock()
	if f, ok := faceCache[k]; ok {
		return f
	}
	src := regular
	if isBold {
		src = bold
	}
	f, err := opentype.NewFace(src, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err) // a parsed font with a valid size; can't fail
	}
	faceCache[k] = f
	return f
}

func measure(f font.Face, s string) int { return font.MeasureString(f, s).Ceil() }

// capHeight is the height of capital letters, for centering text vertically.
func capHeight(f font.Face) int {
	if b, _, ok := f.GlyphBounds('H'); ok {
		return (-b.Min.Y).Round()
	}
	return f.Metrics().Ascent.Round() * 7 / 10
}

// ellipsize shortens s with "…" until it fits into w pixels.
func ellipsize(f font.Face, s string, w int) string {
	if measure(f, s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 {
		r = r[:len(r)-1]
		t := strings.TrimRight(string(r), " ") + "…"
		if measure(f, t) <= w {
			return t
		}
	}
	return "…"
}

// wrap breaks s into lines of at most w pixels at spaces.
func wrap(f font.Face, s string, w int) []string {
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			try := word
			if line != "" {
				try = line + " " + word
			}
			if line != "" && measure(f, try) > w {
				lines = append(lines, line)
				line = word
			} else {
				line = try
			}
		}
		lines = append(lines, ellipsize(f, line, w))
	}
	return lines
}
