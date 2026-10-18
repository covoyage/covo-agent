package cyber

import (
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectBinaryArchitecture(t *testing.T) {
	elfHeader := make([]byte, 64)
	copy(elfHeader, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(elfHeader[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(elfHeader[18:], uint16(elf.EM_X86_64))
	binary.LittleEndian.PutUint32(elfHeader[20:], 1)
	binary.LittleEndian.PutUint16(elfHeader[52:], 64)
	binary.LittleEndian.PutUint16(elfHeader[54:], 56)
	binary.LittleEndian.PutUint16(elfHeader[58:], 64)
	machoHeader := make([]byte, 32)
	binary.LittleEndian.PutUint32(machoHeader, macho.Magic64)
	binary.LittleEndian.PutUint32(machoHeader[4:], uint32(macho.CpuArm64))
	binary.LittleEndian.PutUint32(machoHeader[12:], uint32(macho.TypeExec))
	for _, test := range []struct {
		name, goos, goarch, expected string
		data                         []byte
	}{
		{"native ELF", "linux", "amd64", "native", elfHeader},
		{"ARM needs translation", "linux", "arm64", "translation-required", elfHeader},
		{"native Mach-O", "darwin", "arm64", "native", machoHeader},
		{"wrong format", "linux", "arm64", "foreign-format", machoHeader},
		{"script", "linux", "arm64", "interpreter-dependent", []byte("#!/bin/sh\nexit 99\n")},
		{"unknown", "linux", "arm64", "unknown", []byte("not an executable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scanner")
			if err := os.WriteFile(path, test.data, 0o700); err != nil {
				t.Fatal(err)
			}
			result := inspectBinaryFor(path, test.goos, test.goarch)
			if result.Compatibility != test.expected || result.Platform != test.goos+"/"+test.goarch {
				t.Fatalf("incorrect offline diagnostic: %+v", result)
			}
		})
	}
}
