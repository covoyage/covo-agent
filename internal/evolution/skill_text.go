package evolution

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	"golang.org/x/text/encoding/unicode"
)

func decodeSkillText(content []byte) (string, error) {
	if bytes.HasPrefix(content, []byte{0xff, 0xfe}) || bytes.HasPrefix(content, []byte{0xfe, 0xff}) {
		decoded, err := unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder().Bytes(content)
		if err != nil {
			return "", err
		}
		content = decoded
	}
	if !utf8.Valid(content) || bytes.ContainsRune(content, '\x00') {
		return "", fmt.Errorf("skill file cannot be audited as UTF-8 or BOM-tagged UTF-16")
	}
	return string(bytes.TrimPrefix(content, []byte{0xef, 0xbb, 0xbf})), nil
}
