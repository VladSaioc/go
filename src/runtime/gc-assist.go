package runtime

import (
	"runtime/internal/atomic"
	"unsafe"
)

//go:nosplit
func gcddtrace(lvl int32) bool {
	return debug.gcddtrace == lvl
}

func hexString(v hex) string {
	const dig = "0123456789abcdef"
	var buf string
	for {
		buf = string(dig[v%16]) + buf
		if v < 16 {
			break
		}
		v /= 16
	}
	return "0x" + buf
}

func stringFromValues(args ...any) string {

	msg := ""
	for i := 0; i < len(args); i++ {
		buf := make([]byte, 100)
		switch a := args[i].(type) {
		case bool:
			if a {
				msg += "true "
			} else {
				msg += "false "
			}
		case []byte:
			msg += string(a) + " "
		case string:
			msg += a + " "
		case int32:
			msg += string(itoa(buf, uint64(a))) + " "
		case int64:
			msg += string(itoa(buf, uint64(a))) + " "
		case uint32:
			msg += string(itoa(buf, uint64(a))) + " "
		case uint64:
			msg += string(itoa(buf, uint64(a))) + " "
		case *atomic.Uint64:
			msg += string(itoa(buf, uint64(a.Load()))) + " "
		case *atomic.Uint32:
			msg += string(itoa(buf, uint64(a.Load()))) + " "
		case hex:
			msg += hexString(a) + " "
		case int:
			msg += string(itoa(buf, uint64(a))) + " "
		case uint:
			msg += string(itoa(buf, uint64(a))) + " "
		case uintptr:
			msg += hexString(hex(uintptr(a))) + " "
		case unsafe.Pointer:
			msg += hexString(hex(uintptr(a))) + " "
		case *g:
			msg += hexString(hex(uintptr(unsafe.Pointer(a)))) + " "
		case *p:
			msg += hexString(hex(uintptr(unsafe.Pointer(a)))) + " "
		case *hchan:
			msg += hexString(hex(uintptr(unsafe.Pointer(a)))) + " "
		default:
			msg += "[Skipped value]"
		}
	}

	return msg
}
