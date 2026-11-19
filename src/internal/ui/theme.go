package ui

import "image/color"

// Theme holds the window's colors.
type Theme struct {
	Bg, Card, Border, Hover, Text, Muted color.RGBA
	Accent, AccentHover, AccentText      color.RGBA
	AccentSoft                           color.RGBA // banner background
	Error, SwitchOff, Knob               color.RGBA
	PicBg                                color.RGBA // behind row pictures (tray icons are drawn for dark panels); zero: none
}

func rgb(v uint32) color.RGBA {
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xFF}
}

// Dark and Light are the two themes; DefaultTheme picks one from the desktop setting.
var (
	Dark = Theme{
		Bg: rgb(0x1E1F22), Card: rgb(0x2A2B2F), Border: rgb(0x3A3B41), Hover: rgb(0x34353A),
		Text: rgb(0xE8E8EA), Muted: rgb(0x9C9EA6),
		Accent: rgb(0x3D8BFD), AccentHover: rgb(0x5A9DFF), AccentText: rgb(0xFFFFFF),
		AccentSoft: rgb(0x22344F),
		Error:      rgb(0xFF6B6B), SwitchOff: rgb(0x55575E), Knob: rgb(0xFFFFFF),
	}
	Light = Theme{
		Bg: rgb(0xF2F2F5), Card: rgb(0xFFFFFF), Border: rgb(0xDADAE0), Hover: rgb(0xEDEDF2),
		Text: rgb(0x1D1D1F), Muted: rgb(0x6E6E76),
		Accent: rgb(0x2470E0), AccentHover: rgb(0x3580EE), AccentText: rgb(0xFFFFFF),
		AccentSoft: rgb(0xDDE9FB),
		Error:      rgb(0xD12F2F), SwitchOff: rgb(0xC5C5CC), Knob: rgb(0xFFFFFF),
		PicBg: rgb(0x3A3B41),
	}
)

// DefaultTheme follows the desktop's light/dark preference.
func DefaultTheme() Theme {
	if PrefersDark() {
		return Dark
	}
	return Light
}
