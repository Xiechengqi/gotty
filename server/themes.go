package server

import (
	_ "embed"
	"encoding/json"
)

//go:embed terminaldata/themes.json
var builtinThemeJSON []byte

var builtinThemes = func() map[string]map[string]string {
	var themes map[string]map[string]string
	if err := json.Unmarshal(builtinThemeJSON, &themes); err != nil {
		panic(err)
	}
	return themes
}()

// resolveTheme returns a theme color map based on the user's config.
// If a named theme is specified and it exists, it's used as the base.
// If the theme is "custom" or unknown, an empty map is returned (no base).
// Individual color overrides are applied on top in buildPreferences.
func resolveTheme(theme string) map[string]string {
	if t, ok := builtinThemes[theme]; ok {
		result := make(map[string]string, len(t))
		for k, v := range t {
			result[k] = v
		}
		return result
	}
	return make(map[string]string)
}
