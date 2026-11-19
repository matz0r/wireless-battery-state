package ui

import "golang.org/x/sys/windows/registry"

// PrefersDark reads the "Choose your app mode" setting (AppsUseLightTheme = 0 is dark).
func PrefersDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err == nil && v == 0
}
