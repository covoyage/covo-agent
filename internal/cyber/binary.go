package cyber

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"os"
	"runtime"
)

type BinaryDiagnostic struct {
	Path          string   `json:"path"`
	Platform      string   `json:"platform"`
	Format        string   `json:"format"`
	Architectures []string `json:"architectures,omitempty"`
	Compatibility string   `json:"compatibility"`
}

func InspectBinary(path string) BinaryDiagnostic {
	return inspectBinaryFor(path, runtime.GOOS, runtime.GOARCH)
}

func inspectBinaryFor(path, goos, goarch string) BinaryDiagnostic {
	result := BinaryDiagnostic{Path: path, Platform: goos + "/" + goarch, Format: "unknown", Compatibility: "unknown"}
	file, err := os.Open(path)
	if err != nil {
		return result
	}
	defer file.Close()
	var header [4]byte
	if _, err := file.ReadAt(header[:], 0); err != nil {
		return result
	}
	if header[0] == '#' && header[1] == '!' {
		result.Format, result.Compatibility = "script", "interpreter-dependent"
		return result
	}
	switch {
	case string(header[:]) == "\x7fELF":
		binary, err := elf.NewFile(file)
		if err != nil {
			return result
		}
		result.Format = "ELF"
		arch := map[elf.Machine]string{elf.EM_X86_64: "amd64", elf.EM_386: "386", elf.EM_AARCH64: "arm64", elf.EM_ARM: "arm", elf.EM_RISCV: "riscv64"}[binary.Machine]
		if arch != "" {
			result.Architectures = []string{arch}
		}
	case header[0] == 'M' && header[1] == 'Z':
		binary, err := pe.NewFile(file)
		if err != nil {
			return result
		}
		result.Format = "PE"
		arch := map[uint16]string{pe.IMAGE_FILE_MACHINE_AMD64: "amd64", pe.IMAGE_FILE_MACHINE_I386: "386", pe.IMAGE_FILE_MACHINE_ARM64: "arm64", pe.IMAGE_FILE_MACHINE_ARMNT: "arm"}[binary.Machine]
		if arch != "" {
			result.Architectures = []string{arch}
		}
	default:
		if binary, err := macho.NewFatFile(file); err == nil {
			result.Format = "Mach-O"
			for _, arch := range binary.Arches {
				if name := machoArchitecture(arch.Cpu); name != "" {
					result.Architectures = append(result.Architectures, name)
				}
			}
		} else if binary, err := macho.NewFile(file); err == nil {
			result.Format = "Mach-O"
			if arch := machoArchitecture(binary.Cpu); arch != "" {
				result.Architectures = []string{arch}
			}
		}
	}
	expectedFormat := map[string]string{"linux": "ELF", "android": "ELF", "freebsd": "ELF", "openbsd": "ELF", "netbsd": "ELF", "darwin": "Mach-O", "windows": "PE"}[goos]
	if result.Format != "unknown" && expectedFormat != "" && result.Format != expectedFormat {
		result.Compatibility = "foreign-format"
		return result
	}
	if expectedFormat == "" || len(result.Architectures) == 0 {
		return result
	}
	result.Compatibility = "translation-required"
	for _, arch := range result.Architectures {
		if arch == goarch {
			result.Compatibility = "native"
			break
		}
	}
	return result
}

func machoArchitecture(cpu macho.Cpu) string {
	return map[macho.Cpu]string{macho.CpuAmd64: "amd64", macho.Cpu386: "386", macho.CpuArm64: "arm64", macho.CpuArm: "arm", macho.CpuPpc: "ppc", macho.CpuPpc64: "ppc64"}[cpu]
}
