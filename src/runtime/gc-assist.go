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

func fullGoroutineReport(gp *g) string {
	if gp == nil {
		return "\t\t\t<<<<GO WAS NIL>>>>"
	}
	var name string = "!unnamed"
	fn := findfunc(gp.startpc)
	if fn.valid() {
		name = funcname(fn)
	}

	gstatus := readgstatus(gp) &^ _Gscan
	status := stringFromValues("\t\t\t====[GOROUTINE:", name, "("+gStatusStrings[gstatus]+")]====")
	if gp.m != nil {
		status += stringFromValues("\n\t\t\t\tG0 (addr):", gp.m.g0, ", Stack:", gp.m.g0.stack.lo, "-", gp.m.g0.stack.hi)
	}
	status += stringFromValues("\n\t\t\t\tG (addr):", gp, ", Stack:", gp.stack.lo, "-", gp.stack.hi)
	var scanstatus = "UNSCANNED"
	if gp.gcscandone {
		scanstatus = "SCANNED"
	}
	status += stringFromValues("\n\t\t\t\tScan status:", scanstatus)
	status += stringFromValues("\n\t\t\t\tStart PC:", gp.startpc, "Go PC:", gp.gopc)
	if gp.waitreason != waitReasonZero {
		status += stringFromValues("\n\t\t\t\tWait Reason:", gp.waitreason.String())
	}
	for sg := gp.waiting; sg != nil; sg = sg.waitlink {
		if sg.c != nil {
			marked := "not marked"
			if isMarked(unsafe.Pointer(sg.c)) {
				marked = "marked"
			}
			status += stringFromValues("\n\t\t\t\tChannel:", sg.c, "("+marked+")")
		}
	}
	if gp.waiting_sema != nil {
		status += stringFromValues("\n\t\t\t\tSema:", gp.waiting_sema, ":: Marked:", isMarked(gc_undo_mask_ptr(gp.waiting_sema)))
	}
	status += "\n\t\t\t====[END GOROUTINE REPORT]===="
	return status

}

func userGoroutineReport(gp *g) string {
	if gp == nil {
		return "\t\t\t<<<<GO WAS NIL>>>>"
	}

	var name string = "unnamed"
	fn := findfunc(gp.startpc)
	if fn.valid() {
		name = funcname(fn)
	}

	// if len(name) > len("runtime.") && name[:len("runtime.")] == "runtime." {
	// 	return "INTERNAL GOROUTINE"
	// }

	gstatus := readgstatus(gp) &^ _Gscan
	status := stringFromValues("\t\t\t====[GOROUTINE:", name, "("+gStatusStrings[gstatus]+")]====")
	if gp.m != nil {
		status += stringFromValues("\n\t\t\t\tG0 (addr):", gp.m.g0, ", Stack:", gp.m.g0.stack.lo, "-", gp.m.g0.stack.hi)
	}
	status += stringFromValues("\n\t\t\t\tG (addr):", gp, ", Stack:", gp.stack.lo, "-", gp.stack.hi)
	var scanstatus = "UNSCANNED"
	if gp.gcscandone {
		scanstatus = "SCANNED"
	}
	status += stringFromValues("\n\t\t\t\tScan status:", scanstatus)
	status += stringFromValues("\n\t\t\t\tStart PC:", gp.startpc, "Go PC:", gp.gopc)
	if gp.waitreason != waitReasonZero {
		status += stringFromValues("\n\t\t\t\tWait Reason:", gp.waitreason.String())
	}
	for sg := gp.waiting; sg != nil; sg = sg.waitlink {
		if sg.c != nil {
			marked := "not marked"
			if isMarked(unsafe.Pointer(sg.c)) {
				marked = "marked"
			}
			status += stringFromValues("\n\t\t\t\tChannel:", sg.c, "("+marked+")")
		}
	}
	if gp.waiting_sema != nil {
		status += stringFromValues("\n\t\t\t\tSema:", gc_undo_mask_ptr(gp.waiting_sema), ":: Marked:", isMarked(gp.waiting_sema))
	} else {
		switch gp.waitreason {
		case waitReasonSemacquire:
			status += "(semacquire)"
		case waitReasonSyncMutexLock,
			waitReasonSyncRWMutexRLock,
			waitReasonSyncRWMutexLock,
			waitReasonSyncWaitGroupWait:
			status += "(nil sync sema?)"
		}
	}
	if gp.waitreason == waitReasonSyncCondWait {
		if gp.waiting_notifier != nil {
			status += stringFromValues("\n\t\t\t\tNotifier:", gc_undo_mask_ptr(gp.waiting_notifier), ":: Marked:", isMarked(gp.waiting_notifier))
		} else {
			status += "(nil notifier?)"
		}
	}
	return status
}

// go:systemstack
func goroutineHeader(gp *g) string {
	fn := findfunc(gp.startpc)
	if fn.valid() {
		return "====[GOROUTINE: " + funcname(fn) + " (" + hexString(hex(uintptr(unsafe.Pointer(gp)))) + ")]===="
	}
	return "unnamed goroutine"
}

// Use this to print in systemstack.
//
// go:systemstack
func printGoroutineReport(gp *g) {
	if gcddtrace(0) {
		return
	}
	if gp == nil {
		println("<<<<GO WAS NIL>>>>")
		return
	}

	var name string = "unnamed"
	fn := findfunc(gp.startpc)
	if fn.valid() {
		name = funcname(fn)
	}

	status := readgstatus(gp) &^ _Gscan
	println("\t\t\t====[GOROUTINE:", name, "[", gp, "] (", gStatusStrings[status]+")]====")
	if gp.m != nil {
		println("\t\t\t\tG0 (addr):", gp.m.g0, ", Stack:", gp.m.g0.stack.lo, "-", gp.m.g0.stack.hi)
	}
	println("\t\t\t\tStack:", hex(gp.stack.lo), "-", hex(gp.stack.hi), "\n"+
		"\t\t\t\tScan done:", gp.gcscandone, "; Marked", isMarked(unsafe.Pointer(gp)), "\n"+
		"\t\t\t\tStart PC:", hex(gp.startpc), "Go PC:", hex(gp.gopc), "\n"+
		"\t\t\t\tWait Reason:", gp.waitreason.String())
	for sg := gp.waiting; sg != nil; sg = sg.waitlink {
		if sg.c != nil {
			println("\t\t\t\tChannel:", sg.c, ":: Marked:", isMarked(unsafe.Pointer(sg.c)))
		}
	}
	if gp.waiting_sema != nil {
		sema := gc_undo_mask_ptr(gp.waiting_sema)
		println("\t\t\t\tSema:", sema, ":: Marked:", isMarked(sema))
	}
}
