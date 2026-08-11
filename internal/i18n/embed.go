package i18n

import "embed"

//go:embed locales/*.yaml
var embeddedLocales embed.FS
