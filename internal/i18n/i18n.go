// Package i18n provides lightweight internationalization for covo-agent.
//
// It follows a "minimal translation" philosophy:
// only static user-facing UI strings are translated. Agent output,
// log lines, tool outputs, and LLM prompts stay in English.
//
// Translations are stored as YAML files in the repo's locales/ directory.
// Each file is a nested YAML structure that is flattened to dotted keys at load time.
//
// Language resolution order:
//  1. Explicit set via SetLanguage()
//  2. COVO_LANGUAGE environment variable
//  3. display.language in config.yaml (via InitFromConfig)
//  4. Default "en"
//
// Usage:
//
//	import "github.com/covoyage/covo-agent/internal/i18n"
//
//	msg := i18n.T("approval.choose", "description", "rm -rf /")
//	// → "⚠️  DANGEROUS COMMAND: rm -rf /"
package i18n

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Language represents a supported language code.
type Language string

const (
	LangEN     Language = "en"
	LangZH     Language = "zh"
	LangZHHant Language = "zh-hant"
	LangJA     Language = "ja"
	LangKO     Language = "ko"
	LangDE     Language = "de"
	LangFR     Language = "fr"
	LangES     Language = "es"
	LangPT     Language = "pt"
	LangIT     Language = "it"
	LangRU     Language = "ru"
	LangTR     Language = "tr"
	LangAR     Language = "ar"
	LangID     Language = "id"
	LangHI     Language = "hi"
	LangVI     Language = "vi"
	LangTH     Language = "th"
	LangPL     Language = "pl"
	LangUK     Language = "uk"
	LangNL     Language = "nl"
	LangSV     Language = "sv"
	LangFA     Language = "fa"
	LangHE     Language = "he"

	DefaultLanguage = LangEN
)

// supportedLanguages is the canonical list of supported languages.
var supportedLanguages = []Language{
	LangEN, LangZH, LangZHHant, LangJA, LangKO, LangDE, LangFR, LangES,
	LangPT, LangIT, LangRU, LangTR, LangAR, LangID,
	LangHI, LangVI, LangTH, LangPL, LangUK, LangNL, LangSV, LangFA, LangHE,
}

// languageAliases maps alternate codes and natural names to canonical codes.
var languageAliases = map[string]Language{
	"english":             LangEN,
	"en-us":               LangEN,
	"en_us":               LangEN,
	"chinese":             LangZH,
	"zh-cn":               LangZH,
	"zh_cn":               LangZH,
	"zh-hans":             LangZH,
	"zh_hans":             LangZH,
	"simplified-chinese":  LangZH,
	"simplified chinese":  LangZH,
	"traditional-chinese": LangZHHant,
	"traditional chinese": LangZHHant,
	"zh-tw":               LangZHHant,
	"zh_tw":               LangZHHant,
	"zh-hk":               LangZHHant,
	"zh_hk":               LangZHHant,
	"japanese":            LangJA,
	"ja-jp":               LangJA,
	"ja_jp":               LangJA,
	"jp":                  LangJA,
	"korean":              LangKO,
	"ko-kr":               LangKO,
	"ko_kr":               LangKO,
	"german":              LangDE,
	"deutsch":             LangDE,
	"de-de":               LangDE,
	"de_de":               LangDE,
	"french":              LangFR,
	"francais":            LangFR,
	"français":            LangFR,
	"fr-fr":               LangFR,
	"fr_fr":               LangFR,
	"spanish":             LangES,
	"espanol":             LangES,
	"español":             LangES,
	"es-es":               LangES,
	"es_es":               LangES,
	"es-mx":               LangES,
	"es_mx":               LangES,
	"portuguese":          LangPT,
	"português":           LangPT,
	"portugues":           LangPT,
	"pt-br":               LangPT,
	"pt_br":               LangPT,
	"pt-pt":               LangPT,
	"pt_pt":               LangPT,
	"italian":             LangIT,
	"italiano":            LangIT,
	"it-it":               LangIT,
	"it_it":               LangIT,
	"russian":             LangRU,
	"русский":             LangRU,
	"ru-ru":               LangRU,
	"ru_ru":               LangRU,
	"turkish":             LangTR,
	"türkçe":              LangTR,
	"turkce":              LangTR,
	"tr-tr":               LangTR,
	"tr_tr":               LangTR,
	"arabic":              LangAR,
	"العربية":             LangAR,
	"ar-sa":               LangAR,
	"ar_sa":               LangAR,
	"indonesian":          LangID,
	"bahasa":              LangID,
	"bahasa indonesia":    LangID,
	"id-id":               LangID,
	"id_id":               LangID,
	"hindi":               LangHI,
	"हिन्दी":              LangHI,
	"hi-in":               LangHI,
	"vietnamese":          LangVI,
	"tiếng việt":          LangVI,
	"vi-vn":               LangVI,
	"thai":                LangTH,
	"ไทย":                 LangTH,
	"th-th":               LangTH,
	"polish":              LangPL,
	"polski":              LangPL,
	"pl-pl":               LangPL,
	"ukrainian":           LangUK,
	"українська":          LangUK,
	"uk-ua":               LangUK,
	"dutch":               LangNL,
	"nederlands":          LangNL,
	"nl-nl":               LangNL,
	"swedish":             LangSV,
	"svenska":             LangSV,
	"sv-se":               LangSV,
	"persian":             LangFA,
	"farsi":               LangFA,
	"فارسی":               LangFA,
	"fa-ir":               LangFA,
	"hebrew":              LangHE,
	"עברית":               LangHE,
	"he-il":               LangHE,
}

// catalog stores a flat dotted-key → translated string map.
type catalog map[string]string

var (
	mu             sync.RWMutex
	currentLang    Language
	catalogs       = map[Language]catalog{}
	localesDir     string
	localesDirOnce sync.Once
)

// normalize canonicalizes a language identifier.
// Returns the Language and whether it is supported.
func normalize(identifier string) (Language, bool) {
	clean := strings.TrimSpace(strings.ToLower(identifier))
	if clean == "" {
		return "", false
	}
	// Direct canonical match
	for _, lang := range supportedLanguages {
		if string(lang) == clean {
			return lang, true
		}
	}
	// Alias lookup
	if canon, ok := languageAliases[clean]; ok {
		return canon, true
	}
	return "", false
}

// IsSupported checks if a language identifier is supported.
func IsSupported(identifier string) bool {
	_, ok := normalize(identifier)
	return ok
}

// SupportedLanguages returns the list of supported language codes.
func SupportedLanguages() []Language {
	return append([]Language(nil), supportedLanguages...)
}

// resolveLocalesDir finds the locales/ directory relative to the binary or source.
// Resolution order:
//  1. COVO_LOCALES_DIR environment variable
//  2. Walk up from current directory looking for locales/
//  3. Look alongside the executable
func resolveLocalesDir() string {
	if dir := os.Getenv("COVO_LOCALES_DIR"); dir != "" {
		return dir
	}

	// Look from CWD upward (for development)
	dir, err := os.Getwd()
	if err == nil {
		for {
			for _, candidate := range []string{
				filepath.Join(dir, "internal", "i18n", "locales"),
				filepath.Join(dir, "locales"),
			} {
				if info, err := os.Stat(candidate); err == nil && info.IsDir() {
					return candidate
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// Look alongside the executable (for installed binaries)
	if exe, err := os.Executable(); err == nil {
		for _, candidate := range []string{
			filepath.Join(filepath.Dir(exe), "internal", "i18n", "locales"),
			filepath.Join(filepath.Dir(exe), "locales"),
		} {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate
			}
		}
	}

	return ""
}

// localesBaseDir returns the locales directory, resolving it once.
func localesBaseDir() string {
	localesDirOnce.Do(func() {
		localesDir = resolveLocalesDir()
	})
	return localesDir
}

// loadCatalog reads a YAML locale file and flattens it into a dotted-key map.
// It first tries the embedded filesystem (bundled with the binary), then falls back to disk.
func loadCatalog(lang Language) (catalog, error) {
	data, err := readLocaleFile(string(lang))
	if err != nil {
		return nil, err
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s.yaml: %w", lang, err)
	}

	flat := make(catalog, len(raw)*8) // rough estimate
	flatten(raw, "", flat)
	return flat, nil
}

// readLocaleFile reads a locale YAML file from the embedded filesystem first,
// then falls back to the on-disk locales directory (for development override).
func readLocaleFile(lang string) ([]byte, error) {
	// 1. Try embedded FS (always available in production builds)
	if data, err := embeddedLocales.ReadFile("locales/" + lang + ".yaml"); err == nil {
		return data, nil
	}

	// 2. Fall back to on-disk locales directory
	base := localesBaseDir()
	if base == "" {
		return nil, fmt.Errorf("locales directory not found")
	}
	path := filepath.Join(base, lang+".yaml")
	return os.ReadFile(path)
}

// flatten recursively walks a nested map and writes dotted keys into the catalog.
func flatten(m map[string]any, prefix string, cat catalog) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case string:
			cat[key] = val
		case map[string]any:
			flatten(val, key, cat)
		}
	}
}

// getOrLoadCatalog returns the catalog for a language, loading it if not cached.
func getOrLoadCatalog(lang Language) (catalog, error) {
	// Fast path: already loaded
	mu.RLock()
	cat, ok := catalogs[lang]
	mu.RUnlock()
	if ok {
		return cat, nil
	}

	// Slow path: load from disk
	mu.Lock()
	defer mu.Unlock()

	// Double-check after acquiring write lock
	if cat, ok = catalogs[lang]; ok {
		return cat, nil
	}

	cat, err := loadCatalog(lang)
	if err != nil {
		return nil, err
	}
	catalogs[lang] = cat
	return cat, nil
}

// activeLanguage returns the currently active language (thread-safe).
func activeLanguage() Language {
	mu.RLock()
	defer mu.RUnlock()

	if currentLang != "" {
		return currentLang
	}

	// Lazy init from env
	if env := os.Getenv("COVO_LANGUAGE"); env != "" {
		if lang, ok := normalize(env); ok {
			return lang
		}
	}

	// Auto-detect from system locale (LANG, LC_ALL, LC_MESSAGES)
	if detected := DetectSystemLanguage(); detected != "" {
		return detected
	}

	return DefaultLanguage
}

// DetectSystemLanguage probes Unix locale environment variables to guess the user's language.
// Checks LANG, LC_ALL, and LC_MESSAGES in order, extracting the language portion (e.g. "zh_CN.UTF-8" → "zh").
// Returns the canonical Language code if supported, or empty string if not detected.
func DetectSystemLanguage() Language {
	for _, env := range []string{"LANG", "LC_ALL", "LC_MESSAGES"} {
		val := os.Getenv(env)
		if val == "" {
			continue
		}
		// Extract language portion: "zh_CN.UTF-8" → "zh", "ja_JP.UTF-8" → "ja"
		langPart := extractLangFromLocale(val)
		if langPart == "" {
			continue
		}
		if lang, ok := normalize(langPart); ok {
			return lang
		}
	}
	return ""
}

// extractLangFromLocale strips encoding/charset from a POSIX locale string.
// "zh_CN.UTF-8" → "zh", "ja_JP.utf8" → "ja", "de_DE@euro" → "de", "C" → "", "en_US" → "en".
func extractLangFromLocale(locale string) string {
	if locale == "" || locale == "C" || locale == "POSIX" {
		return ""
	}
	// Strip @modifier
	if idx := strings.IndexByte(locale, '@'); idx >= 0 {
		locale = locale[:idx]
	}
	// Strip .encoding
	if idx := strings.IndexByte(locale, '.'); idx >= 0 {
		locale = locale[:idx]
	}
	// Get language part before _
	if idx := strings.IndexByte(locale, '_'); idx >= 0 {
		return locale[:idx]
	}
	return locale
}

// SetLanguage sets the active language for all subsequent T() calls.
func SetLanguage(lang Language) {
	mu.Lock()
	defer mu.Unlock()
	currentLang = lang
}

// InitFromConfig initializes the language from a config value.
// Call this after loading user config during startup.
func InitFromConfig(configLang string) {
	if configLang == "" {
		return
	}
	if lang, ok := normalize(configLang); ok {
		mu.Lock()
		currentLang = lang
		mu.Unlock()
	}
}

// ParseLanguage converts a language identifier to its canonical Language value.
// Supports aliases (e.g., "chinese" → "zh", "zh-CN" → "zh").
// Returns the Language and whether the identifier is valid.
func ParseLanguage(identifier string) (Language, bool) {
	return normalize(identifier)
}

// DisplayName returns the native display name for a language.
func DisplayName(lang Language) string {
	names := map[Language]string{
		LangEN:     "English",
		LangZH:     "中文",
		LangZHHant: "繁體中文",
		LangJA:     "日本語",
		LangKO:     "한국어",
		LangDE:     "Deutsch",
		LangFR:     "Français",
		LangES:     "Español",
		LangPT:     "Português",
		LangIT:     "Italiano",
		LangRU:     "Русский",
		LangTR:     "Türkçe",
		LangAR:     "العربية",
		LangID:     "Bahasa Indonesia",
		LangHI:     "हिन्दी",
		LangVI:     "Tiếng Việt",
		LangTH:     "ไทย",
		LangPL:     "Polski",
		LangUK:     "Українська",
		LangNL:     "Nederlands",
		LangSV:     "Svenska",
		LangFA:     "فارسی",
		LangHE:     "עברית",
	}
	if name, ok := names[lang]; ok {
		return name
	}
	return string(lang)
}

// T returns the translated string for the given key.
//
//	key: Dotted path into the catalog, e.g. "app.welcome"
//	args: Optional key-value pairs for {placeholder} substitution,
//	      followed by the format string style: T("key", pairs...)
//
// Fallback chain:
//  1. Active language catalog
//  2. English catalog
//  3. Bare key (never crashes)
//
// Key-value pair format: arguments are passed as pairs of (key, value) strings.
//
//	msg := i18n.T("approval.dangerous", "description", "rm -rf /")
func T(key string, args ...string) string {
	lang := activeLanguage()

	// Build replacement map from args
	replacements := make(map[string]string, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		replacements[args[i]] = args[i+1]
	}

	// Try target language first
	if cat, err := getOrLoadCatalog(lang); err == nil {
		if val, ok := cat[key]; ok {
			return expand(val, replacements)
		}
	}

	// Fall back to English
	if lang != LangEN {
		if cat, err := getOrLoadCatalog(LangEN); err == nil {
			if val, ok := cat[key]; ok {
				return expand(val, replacements)
			}
		}
	}

	// Last resort: bare key
	return key
}

// TF is like T but with fmt.Sprintf-style formatting.
// Use this when you need positional args (%s, %d, etc.) rather than named {placeholders}.
//
//	msg := i18n.TF("app.tokens", 100, 200)
func TF(key string, args ...interface{}) string {
	base := T(key)
	if len(args) > 0 {
		return fmt.Sprintf(base, args...)
	}
	return base
}

// expand replaces {placeholder} tokens with values from the map.
func expand(template string, replacements map[string]string) string {
	if len(replacements) == 0 {
		return template
	}
	result := template
	for k, v := range replacements {
		result = strings.ReplaceAll(result, "{"+k+"}", v)
	}
	return result
}

// ResetLocalesDir overrides the locales directory path. Used for testing.
func ResetLocalesDir() {
	localesDirOnce = sync.Once{}
	localesDir = ""
	mu.Lock()
	currentLang = ""
	catalogs = map[Language]catalog{}
	mu.Unlock()
}
