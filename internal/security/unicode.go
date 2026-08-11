// Package security provides shared security primitives used by multiple
// internal packages. It avoids import cycles between packages like agent
// and evolution that both need the same threat-detection data.
package security

// InvisibleChars is the set of invisible/suspicious Unicode codepoints that
// can be used for steganographic prompt injection. Shared between the
// ThreatScanner (agent package) and the SkillsGuard (evolution package).
var InvisibleChars = map[rune]string{
	'\u200B': "U+200B zero-width space",
	'\u200C': "U+200C zero-width non-joiner",
	'\u200D': "U+200D zero-width joiner",
	'\u2060': "U+2060 word joiner",
	'\u2062': "U+2062 invisible times",
	'\u2063': "U+2063 invisible separator",
	'\u2064': "U+2064 invisible plus",
	'\uFEFF': "U+FEFF BOM / zero-width no-break space",
	'\u202A': "U+202A left-to-right embedding",
	'\u202B': "U+202B right-to-left embedding",
	'\u202C': "U+202C pop directional formatting",
	'\u202D': "U+202D left-to-right override",
	'\u202E': "U+202E right-to-left override",
	'\u2066': "U+2066 left-to-right isolate",
	'\u2067': "U+2067 right-to-left isolate",
	'\u2068': "U+2068 first strong isolate",
	'\u2069': "U+2069 pop directional isolate",
}
