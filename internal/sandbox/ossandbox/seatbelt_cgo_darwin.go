//go:build darwin && cgo

package ossandbox

/*
#include <stdint.h>
#include <stdlib.h>

// Link against the sandbox private API via the system library.
// sandbox_init and sandbox_free_error are in libsystem_sandbox.dylib
// (part of /usr/lib/system on macOS).
//
// We declare them here so cgo can find them at link time.
// On modern macOS (11+), these symbols are available via the system
// library and do not require an explicit framework link.

#cgo LDFLAGS: -lsandbox

// sandbox_init: apply a Seatbelt profile to the current process.
// profile: NUL-terminated Seatbelt policy string (when flags = SANDBOX_NAMED)
//          or a built-in profile name (when flags = SANDBOX_NAMED_BUILTIN)
// flags: SANDBOX_NAMED (0x2) for custom profile strings
// errorbuf: receives error message on failure (caller must free with sandbox_free_error)
extern int sandbox_init(const char *profile, uint64_t flags, char **errorbuf);
extern void sandbox_free_error(char *errorbuf);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// SANDBOX_NAMED_EXTERNAL tells sandbox_init to interpret the profile
// parameter as a path to a file containing a Seatbelt policy.
// This is flag value 0x3 from <sandbox.h>.
const sandboxNamedExternal uint64 = 0x3

// sandboxInitInProcess applies a Seatbelt sandbox policy to the current
// process using the private sandbox_init() API, without re-executing.
// This is irreversible — once applied, restrictions cannot be removed.
//
// The policyPath must be a path to a file containing a Seatbelt policy.
func sandboxInitInProcess(policyPath string) error {
	cPath := C.CString(policyPath)
	defer C.free(unsafe.Pointer(cPath))

	var errBuf *C.char
	ret := C.sandbox_init(cPath, C.uint64_t(sandboxNamedExternal), &errBuf)
	if ret != 0 {
		var errMsg string
		if errBuf != nil {
			errMsg = C.GoString(errBuf)
			C.sandbox_free_error(errBuf)
		}
		return fmt.Errorf("sandbox_init failed (code %d): %s", int(ret), errMsg)
	}
	return nil
}
