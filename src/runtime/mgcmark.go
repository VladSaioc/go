// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Garbage collector: marking and scanning

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/goexperiment"
	"runtime/internal/atomic"
	"runtime/internal/sys"
	"unsafe"
)

const (
	fixedRootFinalizers = iota
	fixedRootFreeGStacks
	fixedRootCount

	// rootBlockBytes is the number of bytes to scan per data or
	// BSS root.
	rootBlockBytes = 256 << 10

	// maxObletBytes is the maximum bytes of an object to scan at
	// once. Larger objects will be split up into "oblets" of at
	// most this size. Since we can scan 1–2 MB/ms, 128 KB bounds
	// scan preemption at ~100 µs.
	//
	// This must be > _MaxSmallSize so that the object base is the
	// span base.
	maxObletBytes = 128 << 10

	// drainCheckThreshold specifies how many units of work to do
	// between self-preemption checks in gcDrain. Assuming a scan
	// rate of 1 MB/ms, this is ~100 µs. Lower values have higher
	// overhead in the scan loop (the scheduler check may perform
	// a syscall, so its overhead is nontrivial). Higher values
	// make the system less responsive to incoming work.
	drainCheckThreshold = 100000

	// pagesPerSpanRoot indicates how many pages to scan from a span root
	// at a time. Used by special root marking.
	//
	// Higher values improve throughput by increasing locality, but
	// increase the minimum latency of a marking operation.
	//
	// Must be a multiple of the pageInUse bitmap element size and
	// must also evenly divide pagesPerArena.
	pagesPerSpanRoot = 512

	// ANGE XXX
	GC_SKIP_MASK      = uintptr(0x7000000000000000)
	GC_UNDO_SKIP_MASK = ^GC_SKIP_MASK // this flips every bit in GC_SKIP_MASK of uinptr width
)

// Stack returns a formatted stack trace of the goroutine that calls it.
// It calls runtime.Stack with a large enough buffer to capture the entire trace.
func getStack() []byte {
	buf := make([]byte, 1024)
	for {
		n := Stack(buf, false)
		if n < len(buf) {
			return buf[:n]
		}
		buf = make([]byte, 2*len(buf))
	}
}

func gc_ptr_is_masked(p unsafe.Pointer) bool {
	return (uintptr(p) & GC_SKIP_MASK) == GC_SKIP_MASK
}

func gc_mask_ptr(p unsafe.Pointer) unsafe.Pointer {
	if !(debug.gcdetectdeadlocks != 0) { // FIXME: deploy change: negation
		return unsafe.Pointer(uintptr(p) | GC_SKIP_MASK)
	}
	return p
}

func gc_undo_mask_ptr(p unsafe.Pointer) unsafe.Pointer {
	return unsafe.Pointer(uintptr(p) & GC_UNDO_SKIP_MASK)
}

// ANGE XXX: added helper
// return true if the reason is a non-blocking waitReason
// (i.e., the runtime will eventually reschedule this goroutine
// even though the goroutine is currently parked)
func unblockingWaitReason(reason waitReason) bool {
	return reason != waitReasonChanReceive &&
		reason != waitReasonSyncWaitGroupWait &&
		reason != waitReasonChanSend &&
		reason != waitReasonChanReceiveNilChan &&
		reason != waitReasonChanSendNilChan &&
		reason != waitReasonSelect &&
		reason != waitReasonSelectNoCases &&
		reason != waitReasonSyncMutexLock &&
		reason != waitReasonSyncRWMutexRLock &&
		reason != waitReasonSyncRWMutexLock &&
		reason != waitReasonSyncCondWait
}

// ANGE XXX: newly added for deadlock detection
// The world must be stopped or allglock must be held.
// go through the snapshot of allgs, putting them into an arrays,
// separated by index, where [0:blockedIndex] contains only running Gs
// allGs[blockedIndex:] contain only blocking Gs
// To avoid GC from marking and scanning the blocked Gs by scanning
// the returned array (which is heap allocated), we mask the highest
// bit of the pointers to Gs with GC_SKIP_MASK
func allGsSnapshotSortedForGC() ([]unsafe.Pointer, int) {
	assertWorldStoppedOrLockHeld(&allglock)

	allgsSorted := make([]unsafe.Pointer, len(allgs))

	var currIndex = 0                       // next index for where a non-waiting g should go
	var blockedIndex = len(allgsSorted) - 1 // next index for where a waiting g should go
	if gcddtrace(1) || gcddtrace(2) {
		println("\t~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\n" +
			"\t[[[ Performing all Gs snapshot ]]]\n" +
			"\t===================================")
	}
	for i, p := range allgs {
		var gp *g = (*g)(gc_undo_mask_ptr(p))
		// not sure if we need atomic load because we are stopping the world,
		// but do it just to be safe for now
		var status uint32 = readgstatus(gp)
		if status == _Gunreachable {
			// shouldn't encounter Gunreachable at this point, unless
			// we have overlap GCs, which shouldn't happen since we only
			// perform deadlock detection with STW GC
			println("Unreachable goroutine:", i, "\n"+
				fullGoroutineReport(gp))
			throw("Found unreachable goroutines in allgs snapshot!")
		}
		if status != _Gwaiting || unblockingWaitReason(gp.waitreason) {
			allgsSorted[currIndex] = unsafe.Pointer(gp)
			currIndex++
		} else {
			allgsSorted[blockedIndex] = gc_mask_ptr(unsafe.Pointer(gp))
			blockedIndex--
		}
	}
	if gcddtrace(1) || gcddtrace(2) {
		println("\t=============================\n" +
			"\t[[[ Done all Gs snapshot ]]]\n" +
			"\t~~~~~~~~~~~~~~~~~~~~~~~~~~~~~")
	}
	if currIndex != (blockedIndex + 1) {
		panic("currIndex and blockedIndex don't match up")
	}

	// Because the world is stopped or allglock is held, allgadd
	// cannot happen concurrently with this. allgs grows
	// monotonically and existing entries never change, so we can
	// simply return a copy of the slice header. For added safety,
	// we trim everything past len because that can still change.
	return allgsSorted, blockedIndex + 1
}

// gcMarkRootPrepare queues root scanning jobs (stacks, globals, and
// some miscellany) and initializes scanning-related state.
//
// The world must be stopped.
func gcMarkRootPrepare() {
	assertWorldStopped()
	if gcddtrace(1) || gcddtrace(2) {
		println("≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠\n"+
			"[[[[[[[[[[[[[[[[ GC:", work.cycles.Load(), ":: I. INITIAL MARKING PHASE ]]]]]]]]]]]]]]]]\n"+
			"≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠≠")
	}

	// Compute how many data and BSS root blocks there are.
	nBlocks := func(bytes uintptr) int {
		return int(divRoundUp(bytes, rootBlockBytes))
	}

	work.nDataRoots = 0
	work.nBSSRoots = 0

	// Scan globals.
	for _, datap := range activeModules() {
		nDataRoots := nBlocks(datap.edata - datap.data)
		if nDataRoots > work.nDataRoots {
			work.nDataRoots = nDataRoots
		}

		nBSSRoots := nBlocks(datap.ebss - datap.bss)
		if nBSSRoots > work.nBSSRoots {
			work.nBSSRoots = nBSSRoots
		}
	}

	// Scan span roots for finalizer specials.
	//
	// We depend on addfinalizer to mark objects that get
	// finalizers after root marking.
	//
	// We're going to scan the whole heap (that was available at the time the
	// mark phase started, i.e. markArenas) for in-use spans which have specials.
	//
	// Break up the work into arenas, and further into chunks.
	//
	// Snapshot allArenas as markArenas. This snapshot is safe because allArenas
	// is append-only.
	mheap_.markArenas = mheap_.allArenas[:len(mheap_.allArenas):len(mheap_.allArenas)]
	work.nSpanRoots = len(mheap_.markArenas) * (pagesPerArena / pagesPerSpanRoot)

	// Scan stacks.
	//
	// Gs may be created after this point, but it's okay that we
	// ignore them because they begin life without any roots, so
	// there's nothing to scan, and any roots they create during
	// the concurrent phase will be caught by the write barrier.
	// ANGE XXX: FIXME --- add a new flag later
	// ANGE XXX:  instead of getting allGs,  just get all Gs that are running
	// work.stackRoots = allGsSnapshot()
	var allgsSorted []unsafe.Pointer
	var blockedIndex int
	if !(debug.gcdetectdeadlocks == 0) { // FIXME: deploy change: negation
		// regular GC --- scan every go routine
		allgsSorted = allGsSnapshot()
		blockedIndex = len(allgsSorted)
	} else {
		allgsSorted, blockedIndex = allGsSnapshotSortedForGC()
		if gcddtrace(1) {
			for i, p := range allgsSorted {
				nomask := gc_undo_mask_ptr(p)
				var gp *g = (*g)(nomask)
				println("\t\tallgsSorted[", i, "] = ", nomask, "status", gStatusStrings[gp.atomicstatus.Load()], "name:",
					getGoroutineName(gp), "wait:", gp.waitreason.String())
				// printIfMarked(unsafe.Pointer(gp))
			}
		}
	}
	work.stackRoots = allgsSorted
	work.nStackRoots = len(work.stackRoots)
	work.nValidStackRoots = blockedIndex

	work.markrootNext = 0
	work.markrootJobs = uint32(fixedRootCount + work.nDataRoots + work.nBSSRoots + work.nSpanRoots + work.nValidStackRoots)

	// Calculate base indexes of each root type
	work.baseData = uint32(fixedRootCount)
	work.baseBSS = work.baseData + uint32(work.nDataRoots)
	work.baseSpans = work.baseBSS + uint32(work.nBSSRoots)
	work.baseStacks = work.baseSpans + uint32(work.nSpanRoots)
	work.baseEnd = work.baseStacks + uint32(work.nStackRoots)
}

// gcMarkRootCheck checks that all roots have been scanned. It is
// purely for debugging.
func gcMarkRootCheck() {
	rootNext := atomic.Load(&work.markrootNext)
	rootJobs := atomic.Load(&work.markrootJobs)
	if rootNext < rootJobs {
		print(rootNext, " of ", rootJobs, " markroot jobs done\n")
		throw("left over markroot jobs")
	}

	// Check that stacks have been scanned.
	//
	// We only check the first nStackRoots Gs that we should have scanned.
	// Since we don't care about newer Gs (see comment in
	// gcMarkRootPrepare), no locking is required.
	i := 0
	forEachGRace(func(gp *g) {
		if i >= work.nStackRoots {
			return
		}

		if !gp.gcscandone {
			println("gp", gp, "goid", gp.goid,
				"status", readgstatus(gp),
				"gcscandone", gp.gcscandone,
				"gname",
			)
			throw("scan missed a g")
		}

		i++
	})
}

// ptrmask for an allocation containing a single pointer.
var oneptrmask = [...]uint8{1}

// Dequeue the deadlocked goroutine from the semaphore treap.
func gc_sema_dequeue(gp *g, addr *uint32) *sudog {
	// Get semaphore root from the semtable.
	var root *semaRoot = semtable.rootFor(addr)
	lockWithRank(&root.lock, lockRankRoot)

	// Get the sudog's parent in the treap.
	ps := &root.treap
	s := *ps
	// Can be assumed that the sema address is masked, because only
	// a goroutine blocked on a masked sema can be flagged as deadlocked
	// and reclaimed by the run-time.
	var masked_addr unsafe.Pointer = gc_mask_ptr(unsafe.Pointer(addr))
	// Cycle through the treap to find the right sudog.
	for ; s != nil; s = *ps {
		if s.elem == masked_addr {
			goto Found
		}
		if uintptr(masked_addr) < uintptr(s.elem) {
			ps = &s.prev
		} else {
			ps = &s.next
		}
	}
	// we can only fall through to this point if addr not found
	throw("Sema addr not found in the semTable!")

Found:
	// ANGE XXX Notes on the semTable treap structure
	// a sudog's parent / prev / next maintains the binary tree structure
	// for the treap, and the weitlink forms the linked list of sudogs
	// waiting on the same addr; the ticket field is used to maintain the
	// balanced tree property
	// In the linked list, only the sudog at the head of the list is
	// in the treap (i.e., maintains non-nil parent / prev / next)
	target := s
	if t := s.waitlink; t != nil {
		var targetPred *sudog = nil
		// Cycle through the linked list to find the right sudog.
		for ; target != nil && target.g != gp; target, targetPred = target.waitlink, target {
		}

		if target == nil {
			throw("The unreachable goroutine not found in the semTable!")
		}
		if target.g != gp {
			println("Wanted:", unsafe.Pointer(gp), "Got:", unsafe.Pointer(target.g))
			throw("Targetted wrong sudog node.")
		}

		if target == s {
			// we are actually removing s
			// Substitute t, also waiting on addr, for s in root tree of unique addrs.
			*ps = t
			t.ticket = s.ticket
			t.parent = s.parent
			t.prev = s.prev
			if t.prev != nil {
				t.prev.parent = t
			}
			t.next = s.next
			if t.next != nil {
				t.next.parent = t
			}
			if t.waitlink != nil {
				t.waittail = s.waittail
			} else {
				t.waittail = nil
			}
		} else {
			// unlinking target that is not s
			targetPred.waitlink = target.waitlink
			if targetPred.waitlink == nil {
				s.waittail = targetPred
			}
		}
		target.waitlink = nil
		target.waittail = nil
	} else {
		if target.g != gp {
			throw("Unreachable g not found in the semTable")
		}
		// Rotate s down to be leaf of tree for removal, respecting priorities.
		for target.next != nil || target.prev != nil {
			if target.next == nil || target.prev != nil && target.prev.ticket < target.next.ticket {
				root.rotateRight(target)
			} else {
				root.rotateLeft(target)
			}
		}
		// Remove target, now a leaf.
		if target.parent != nil {
			if target.parent.prev == target {
				target.parent.prev = nil
			} else {
				target.parent.next = nil
			}
		} else {
			root.treap = nil
		}
	}
	target.parent = nil
	target.elem = nil
	target.next = nil
	target.prev = nil
	target.ticket = 0

	root.nwait.Add(-1)
	unlock(&root.lock)

	return target
}

// similar to goexit0 in panic.go, except that we invoke this on the
// unreachable goroutines found during GC deadlock detection, and the
// goroutine running it is not g0 but the gcBgMarkWorker
func gc_goexit0(gp *g) {
	mp := getg().m
	if mp.lockedInt != 0 {
		print("invalid m->lockedInt = ", mp.lockedInt, "\n")
		throw("internal lockOSThread error")
	}
	pp := mp.p.ptr()

	if readgstatus(gp) != _Gunreachable {
		throw("Unreachable goroutine changed status!")
	}
	casgstatus(gp, _Gunreachable, _Gdead)
	gcController.addScannableStack(pp, -int64(gp.stack.hi-gp.stack.lo))
	if isSystemGoroutine(gp, false) {
		sched.ngsys.Add(-1)
	}
	if gp.m != nil {
		throw("Unrechable goroutine has a non-nil m!")
	}
	if gp.lockedm != 0 {
		throw("Unreachable goroutine has locked a thread!")
	}
	if mp.lockedg != 0 {
		throw("Not sure what to do here!")
	}

	// Dequeue deadlocked goroutine from semaphore
	if gp.waiting_sema != nil {
		var addr *uint32 = (*uint32)(gc_undo_mask_ptr(gp.waiting_sema))
		var s *sudog = gc_sema_dequeue(gp, addr)
		if s.g != gp {
			throw("Targetted wrong sudog!")
		}
		s.g = nil
		releaseSudog(s) // return sudog to the cache
	}

	// Remove deadlocked goroutines from the notifier.
	if gp.waiting_notifier != nil {
		notifier := (*notifyList)(gc_undo_mask_ptr(gp.waiting_notifier))
		var s *sudog = gc_notifyListNotifyOne(notifier, gp)
		if s == nil || s.g != gp {
			throw("Targetted wrong sudog!")
		}
		s.g = nil
		releaseSudog(s) // return sudog to the cache
	}

	// Make sure we properly blank slate the G of a deadlocked goroutine.
	gp.lockedm = 0
	gp.gcscandone = false
	gp.preempt = false
	gp.preemptStop = false
	gp.paniconfault = false
	gp.waitreason = waitReasonZero
	gp.asyncSafePoint = false
	gp.parkingOnChan.Store(false)
	gp.activeStackChans = false
	gp._defer = nil // should be true already but just in case.
	gp._panic = nil // non-nil for Goexit during panic. points at stack-allocated data.
	gp.writebuf = nil
	gp.waiting_sema = nil
	gp.waiting_notifier = nil
	gp.param = nil
	gp.labels = nil
	gp.timer = nil

	// Clear all elem before unlinking from gp.waiting. Release sudog to cache.
	for sg, nextsg := gp.waiting, (*sudog)(nil); sg != nil; sg = nextsg {
		nextsg = sg.waitlink
		sg.isSelect = false
		sg.elem = nil
		sg.c = nil
		sg.waitlink = nil
		sg.next = nil
		sg.prev = nil
		releaseSudog(sg)
	}
	gp.waiting = nil

	if gcBlackenEnabled != 0 && gp.gcAssistBytes > 0 {
		// Flush assist credit to the global pool. This gives
		// better information to pacing if the application is
		// rapidly creating an exiting goroutines.
		assistWorkPerByte := gcController.assistWorkPerByte.Load()
		scanCredit := int64(assistWorkPerByte * float64(gp.gcAssistBytes))
		gcController.bgScanCredit.Add(scanCredit)
		gp.gcAssistBytes = 0
	}

	if GOARCH == "wasm" { // no threads yet on wasm
		gfput(pp, gp)
		return
	}
	gfput(pp, gp)
}

// ANGE XXX: added function
// This is very similar to the cleanup process in Goexit in panic.go but invoked on an unreachable g
func gcGoexit(gp *g) {
	// Create a panic object for Goexit, so we can recognize when it might be
	// bypassed by a recover().
	var p _panic
	p.goexit = true
	p.link = gp._panic
	gp._panic = (*_panic)(noescape(unsafe.Pointer(&p)))

	for {
		if gp._defer == nil {
			break
		} else {
			popDefer(gp)
			continue
		}
	}
	gc_goexit0(gp)
}

// markroot scans the i'th root.
//
// Preemption must be disabled (because this uses a gcWork).
//
// Returns the amount of GC work credit produced by the operation.
// If flushBgCredit is true, then that credit is also flushed
// to the background credit pool.
//
// nowritebarrier is only advisory here.
//
//go:nowritebarrier
func markroot(gcw *gcWork, i uint32, flags gcDrainFlags) int64 {
	flushBgCredit := flags&gcDrainFlushBgCredit != 0
	drainPartialDeadlocks := flags&gcDrainPartialDeadlock != 0
	// Note: if you add a case here, please also update heapdump.go:dumproots.
	var workDone int64
	var workCounter *atomic.Int64
	switch {
	case work.baseData <= i && i < work.baseBSS:
		workCounter = &gcController.globalsScanWork
		for _, datap := range activeModules() {
			workDone += markrootBlock(datap.data, datap.edata-datap.data, datap.gcdatamask.bytedata, gcw, int(i-work.baseData))
		}

	case work.baseBSS <= i && i < work.baseSpans:
		workCounter = &gcController.globalsScanWork
		for _, datap := range activeModules() {
			workDone += markrootBlock(datap.bss, datap.ebss-datap.bss, datap.gcbssmask.bytedata, gcw, int(i-work.baseBSS))
		}

	case i == fixedRootFinalizers:
		for fb := allfin; fb != nil; fb = fb.alllink {
			cnt := uintptr(atomic.Load(&fb.cnt))
			scanblock(uintptr(unsafe.Pointer(&fb.fin[0])), cnt*unsafe.Sizeof(fb.fin[0]), &finptrmask[0], gcw, nil)
		}

	case i == fixedRootFreeGStacks:
		// Switch to the system stack so we can call
		// stackfree.
		systemstack(markrootFreeGStacks)

	case work.baseSpans <= i && i < work.baseStacks:
		// mark mspan.specials
		markrootSpans(gcw, int(i-work.baseSpans))

	default:
		// the rest is scanning goroutine stacks
		workCounter = &gcController.stackScanWork
		if i < work.baseStacks || work.baseEnd <= i {
			printlock()
			print("runtime: markroot index ", i, " not in stack roots range [", work.baseStacks, ", ", work.baseEnd, ")\n")
			throw("markroot: bad index")
		}
		gp := (*g)(work.stackRoots[i-work.baseStacks])

		// remember when we've first observed the G blocked
		// needed only to output in traceback
		status := readgstatus(gp) // We are not in a scan state
		if (status == _Gwaiting || status == _Gsyscall) && gp.waitsince == 0 {
			gp.waitsince = work.tstart
		}

		// scanstack must be done on the system stack in case
		// we're trying to scan our own stack.
		systemstack(func() {
			// Draining as part of partial deadlock detection.
			if status == _Gunreachable {
				switch debug.gcdetectdeadlocks {
				case 0: // FIXME: deploy change: case 1:
					gcGoexit(gp)
				case 2:
					casgstatus(gp, _Gunreachable, _Gdeadlocked)
				default:
					throw("unreachable goroutine found during regular GC")
				}
			}

			// If this is a self-scan, put the user G in
			// _Gwaiting to prevent self-deadlock. It may
			// already be in _Gwaiting if this is a mark
			// worker or we're in mark termination.
			userG := getg().m.curg
			selfScan := gp == userG && readgstatus(userG) == _Grunning
			if selfScan {
				casGToWaiting(userG, _Grunning, waitReasonGarbageCollectionScan)
			}

			// TODO: suspendG blocks (and spins) until gp
			// stops, which may take a while for
			// running goroutines. Consider doing this in
			// two phases where the first is non-blocking:
			// we scan the stacks we can and ask running
			// goroutines to scan themselves; and the
			// second blocks.
			stopped := suspendG(gp, drainPartialDeadlocks)
			if stopped.dead {
				gp.gcscandone = true
				return
			}

			if gp.gcscandone {
				throw("g already scanned")
			}
			workDone += scanstack(gp, gcw)
			gp.gcscandone = true
			resumeG(stopped)

			if selfScan {
				casgstatus(userG, _Gwaiting, _Grunning)
			}
		})
	}
	if workCounter != nil && workDone != 0 {
		workCounter.Add(workDone)
		if flushBgCredit {
			gcFlushBgCredit(workDone)
		}
	}
	return workDone
}

// markrootBlock scans the shard'th shard of the block of memory [b0,
// b0+n0), with the given pointer mask.
//
// Returns the amount of work done.
//
//go:nowritebarrier
func markrootBlock(b0, n0 uintptr, ptrmask0 *uint8, gcw *gcWork, shard int) int64 {
	if rootBlockBytes%(8*goarch.PtrSize) != 0 {
		// This is necessary to pick byte offsets in ptrmask0.
		throw("rootBlockBytes must be a multiple of 8*ptrSize")
	}

	// Note that if b0 is toward the end of the address space,
	// then b0 + rootBlockBytes might wrap around.
	// These tests are written to avoid any possible overflow.
	off := uintptr(shard) * rootBlockBytes
	if off >= n0 {
		return 0
	}
	b := b0 + off
	ptrmask := (*uint8)(add(unsafe.Pointer(ptrmask0), uintptr(shard)*(rootBlockBytes/(8*goarch.PtrSize))))
	n := uintptr(rootBlockBytes)
	if off+n > n0 {
		n = n0 - off
	}

	// Scan this shard.
	scanblock(b, n, ptrmask, gcw, nil)
	return int64(n)
}

// markrootFreeGStacks frees stacks of dead Gs.
//
// This does not free stacks of dead Gs cached on Ps, but having a few
// cached stacks around isn't a problem.
func markrootFreeGStacks() {
	// Take list of dead Gs with stacks.
	lock(&sched.gFree.lock)
	list := sched.gFree.stack
	sched.gFree.stack = gList{}
	unlock(&sched.gFree.lock)
	if list.empty() {
		return
	}

	// Free stacks.
	q := gQueue{list.head, list.head}
	for gp := list.head.ptr(); gp != nil; gp = gp.schedlink.ptr() {
		stackfree(gp.stack)
		gp.stack.lo = 0
		gp.stack.hi = 0
		// Manipulate the queue directly since the Gs are
		// already all linked the right way.
		q.tail.set(gp)
	}

	// Put Gs back on the free list.
	lock(&sched.gFree.lock)
	sched.gFree.noStack.pushAll(q)
	unlock(&sched.gFree.lock)
}

// markrootSpans marks roots for one shard of markArenas.
//
//go:nowritebarrier
func markrootSpans(gcw *gcWork, shard int) {
	// Objects with finalizers have two GC-related invariants:
	//
	// 1) Everything reachable from the object must be marked.
	// This ensures that when we pass the object to its finalizer,
	// everything the finalizer can reach will be retained.
	//
	// 2) Finalizer specials (which are not in the garbage
	// collected heap) are roots. In practice, this means the fn
	// field must be scanned.
	sg := mheap_.sweepgen

	// Find the arena and page index into that arena for this shard.
	ai := mheap_.markArenas[shard/(pagesPerArena/pagesPerSpanRoot)]
	ha := mheap_.arenas[ai.l1()][ai.l2()]
	arenaPage := uint(uintptr(shard) * pagesPerSpanRoot % pagesPerArena)

	// Construct slice of bitmap which we'll iterate over.
	specialsbits := ha.pageSpecials[arenaPage/8:]
	specialsbits = specialsbits[:pagesPerSpanRoot/8]
	for i := range specialsbits {
		// Find set bits, which correspond to spans with specials.
		specials := atomic.Load8(&specialsbits[i])
		if specials == 0 {
			continue
		}
		for j := uint(0); j < 8; j++ {
			if specials&(1<<j) == 0 {
				continue
			}
			// Find the span for this bit.
			//
			// This value is guaranteed to be non-nil because having
			// specials implies that the span is in-use, and since we're
			// currently marking we can be sure that we don't have to worry
			// about the span being freed and re-used.
			s := ha.spans[arenaPage+uint(i)*8+j]

			// The state must be mSpanInUse if the specials bit is set, so
			// sanity check that.
			if state := s.state.get(); state != mSpanInUse {
				print("s.state = ", state, "\n")
				throw("non in-use span found with specials bit set")
			}
			// Check that this span was swept (it may be cached or uncached).
			if !useCheckmark && !(s.sweepgen == sg || s.sweepgen == sg+3) {
				// sweepgen was updated (+2) during non-checkmark GC pass
				print("sweep ", s.sweepgen, " ", sg, "\n")
				throw("gc: unswept span")
			}

			// Lock the specials to prevent a special from being
			// removed from the list while we're traversing it.
			lock(&s.speciallock)
			for sp := s.specials; sp != nil; sp = sp.next {
				if sp.kind != _KindSpecialFinalizer {
					continue
				}
				// don't mark finalized object, but scan it so we
				// retain everything it points to.
				spf := (*specialfinalizer)(unsafe.Pointer(sp))
				// A finalizer can be set for an inner byte of an object, find object beginning.
				p := s.base() + uintptr(spf.special.offset)/s.elemsize*s.elemsize

				// Mark everything that can be reached from
				// the object (but *not* the object itself or
				// we'll never collect it).
				if !s.spanclass.noscan() {
					scanobject(p, gcw)
				}

				// The special itself is a root.
				scanblock(uintptr(unsafe.Pointer(&spf.fn)), goarch.PtrSize, &oneptrmask[0], gcw, nil)
			}
			unlock(&s.speciallock)
		}
	}
}

// gcAssistAlloc performs GC work to make gp's assist debt positive.
// gp must be the calling user goroutine.
//
// This must be called with preemption enabled.
func gcAssistAlloc(gp *g) {
	// Don't assist in non-preemptible contexts. These are
	// generally fragile and won't allow the assist to block.
	if getg() == gp.m.g0 {
		return
	}
	if mp := getg().m; mp.locks > 0 || mp.preemptoff != "" {
		return
	}

	// This extremely verbose boolean indicates whether we've
	// entered mark assist from the perspective of the tracer.
	//
	// In the old tracer, this is just before we call gcAssistAlloc1
	// *and* tracing is enabled. Because the old tracer doesn't
	// do any extra tracking, we need to be careful to not emit an
	// "end" event if there was no corresponding "begin" for the
	// mark assist.
	//
	// In the new tracer, this is just before we call gcAssistAlloc1
	// *regardless* of whether tracing is enabled. This is because
	// the new tracer allows for tracing to begin (and advance
	// generations) in the middle of a GC mark phase, so we need to
	// record some state so that the tracer can pick it up to ensure
	// a consistent trace result.
	//
	// TODO(mknyszek): Hide the details of inMarkAssist in tracer
	// functions and simplify all the state tracking. This is a lot.
	enteredMarkAssistForTracing := false
retry:
	if gcCPULimiter.limiting() {
		// If the CPU limiter is enabled, intentionally don't
		// assist to reduce the amount of CPU time spent in the GC.
		if enteredMarkAssistForTracing {
			trace := traceAcquire()
			if trace.ok() {
				trace.GCMarkAssistDone()
				// Set this *after* we trace the end to make sure
				// that we emit an in-progress event if this is
				// the first event for the goroutine in the trace
				// or trace generation. Also, do this between
				// acquire/release because this is part of the
				// goroutine's trace state, and it must be atomic
				// with respect to the tracer.
				gp.inMarkAssist = false
				traceRelease(trace)
			} else {
				// This state is tracked even if tracing isn't enabled.
				// It's only used by the new tracer.
				// See the comment on enteredMarkAssistForTracing.
				gp.inMarkAssist = false
			}
		}
		return
	}
	// Compute the amount of scan work we need to do to make the
	// balance positive. When the required amount of work is low,
	// we over-assist to build up credit for future allocations
	// and amortize the cost of assisting.
	assistWorkPerByte := gcController.assistWorkPerByte.Load()
	assistBytesPerWork := gcController.assistBytesPerWork.Load()
	debtBytes := -gp.gcAssistBytes
	scanWork := int64(assistWorkPerByte * float64(debtBytes))
	if scanWork < gcOverAssistWork {
		scanWork = gcOverAssistWork
		debtBytes = int64(assistBytesPerWork * float64(scanWork))
	}

	// Steal as much credit as we can from the background GC's
	// scan credit. This is racy and may drop the background
	// credit below 0 if two mutators steal at the same time. This
	// will just cause steals to fail until credit is accumulated
	// again, so in the long run it doesn't really matter, but we
	// do have to handle the negative credit case.
	bgScanCredit := gcController.bgScanCredit.Load()
	stolen := int64(0)
	if bgScanCredit > 0 {
		if bgScanCredit < scanWork {
			stolen = bgScanCredit
			gp.gcAssistBytes += 1 + int64(assistBytesPerWork*float64(stolen))
		} else {
			stolen = scanWork
			gp.gcAssistBytes += debtBytes
		}
		gcController.bgScanCredit.Add(-stolen)

		scanWork -= stolen

		if scanWork == 0 {
			// We were able to steal all of the credit we
			// needed.
			if enteredMarkAssistForTracing {
				trace := traceAcquire()
				if trace.ok() {
					trace.GCMarkAssistDone()
					// Set this *after* we trace the end to make sure
					// that we emit an in-progress event if this is
					// the first event for the goroutine in the trace
					// or trace generation. Also, do this between
					// acquire/release because this is part of the
					// goroutine's trace state, and it must be atomic
					// with respect to the tracer.
					gp.inMarkAssist = false
					traceRelease(trace)
				} else {
					// This state is tracked even if tracing isn't enabled.
					// It's only used by the new tracer.
					// See the comment on enteredMarkAssistForTracing.
					gp.inMarkAssist = false
				}
			}
			return
		}
	}
	if !enteredMarkAssistForTracing {
		trace := traceAcquire()
		if trace.ok() {
			if !goexperiment.ExecTracer2 {
				// In the old tracer, enter mark assist tracing only
				// if we actually traced an event. Otherwise a goroutine
				// waking up from mark assist post-GC might end up
				// writing a stray "end" event.
				//
				// This means inMarkAssist will not be meaningful
				// in the old tracer; that's OK, it's unused.
				//
				// See the comment on enteredMarkAssistForTracing.
				enteredMarkAssistForTracing = true
			}
			trace.GCMarkAssistStart()
			// Set this *after* we trace the start, otherwise we may
			// emit an in-progress event for an assist we're about to start.
			gp.inMarkAssist = true
			traceRelease(trace)
		} else {
			gp.inMarkAssist = true
		}
		if goexperiment.ExecTracer2 {
			// In the new tracer, set enter mark assist tracing if we
			// ever pass this point, because we must manage inMarkAssist
			// correctly.
			//
			// See the comment on enteredMarkAssistForTracing.
			enteredMarkAssistForTracing = true
		}
	}

	// Perform assist work
	systemstack(func() {
		gcAssistAlloc1(gp, scanWork)
		// The user stack may have moved, so this can't touch
		// anything on it until it returns from systemstack.
	})

	completed := gp.param != nil
	gp.param = nil
	if completed {
		gcMarkDone()
	}

	if gp.gcAssistBytes < 0 {
		// We were unable steal enough credit or perform
		// enough work to pay off the assist debt. We need to
		// do one of these before letting the mutator allocate
		// more to prevent over-allocation.
		//
		// If this is because we were preempted, reschedule
		// and try some more.
		if gp.preempt {
			Gosched()
			goto retry
		}

		// Add this G to an assist queue and park. When the GC
		// has more background credit, it will satisfy queued
		// assists before flushing to the global credit pool.
		//
		// Note that this does *not* get woken up when more
		// work is added to the work list. The theory is that
		// there wasn't enough work to do anyway, so we might
		// as well let background marking take care of the
		// work that is available.
		if !gcParkAssist() {
			goto retry
		}

		// At this point either background GC has satisfied
		// this G's assist debt, or the GC cycle is over.
	}
	if enteredMarkAssistForTracing {
		trace := traceAcquire()
		if trace.ok() {
			trace.GCMarkAssistDone()
			// Set this *after* we trace the end to make sure
			// that we emit an in-progress event if this is
			// the first event for the goroutine in the trace
			// or trace generation. Also, do this between
			// acquire/release because this is part of the
			// goroutine's trace state, and it must be atomic
			// with respect to the tracer.
			gp.inMarkAssist = false
			traceRelease(trace)
		} else {
			// This state is tracked even if tracing isn't enabled.
			// It's only used by the new tracer.
			// See the comment on enteredMarkAssistForTracing.
			gp.inMarkAssist = false
		}
	}
}

// gcAssistAlloc1 is the part of gcAssistAlloc that runs on the system
// stack. This is a separate function to make it easier to see that
// we're not capturing anything from the user stack, since the user
// stack may move while we're in this function.
//
// gcAssistAlloc1 indicates whether this assist completed the mark
// phase by setting gp.param to non-nil. This can't be communicated on
// the stack since it may move.
//
//go:systemstack
func gcAssistAlloc1(gp *g, scanWork int64) {
	// Clear the flag indicating that this assist completed the
	// mark phase.
	gp.param = nil

	if atomic.Load(&gcBlackenEnabled) == 0 {
		// The gcBlackenEnabled check in malloc races with the
		// store that clears it but an atomic check in every malloc
		// would be a performance hit.
		// Instead we recheck it here on the non-preemptible system
		// stack to determine if we should perform an assist.

		// GC is done, so ignore any remaining debt.
		gp.gcAssistBytes = 0
		return
	}
	// Track time spent in this assist. Since we're on the
	// system stack, this is non-preemptible, so we can
	// just measure start and end time.
	//
	// Limiter event tracking might be disabled if we end up here
	// while on a mark worker.
	startTime := nanotime()
	trackLimiterEvent := gp.m.p.ptr().limiterEvent.start(limiterEventMarkAssist, startTime)

	decnwait := atomic.Xadd(&work.nwait, -1)
	if decnwait == work.nproc {
		println("runtime: work.nwait =", decnwait, "work.nproc=", work.nproc)
		throw("nwait > work.nprocs")
	}

	// gcDrainN requires the caller to be preemptible.
	casGToWaiting(gp, _Grunning, waitReasonGCAssistMarking)

	// drain own cached work first in the hopes that it
	// will be more cache friendly.
	gcw := &getg().m.p.ptr().gcw
	workDone := gcDrainN(gcw, scanWork)

	casgstatus(gp, _Gwaiting, _Grunning)

	// Record that we did this much scan work.
	//
	// Back out the number of bytes of assist credit that
	// this scan work counts for. The "1+" is a poor man's
	// round-up, to ensure this adds credit even if
	// assistBytesPerWork is very low.
	assistBytesPerWork := gcController.assistBytesPerWork.Load()
	gp.gcAssistBytes += 1 + int64(assistBytesPerWork*float64(workDone))

	// If this is the last worker and we ran out of work,
	// signal a completion point.
	incnwait := atomic.Xadd(&work.nwait, +1)
	if incnwait > work.nproc {
		println("runtime: work.nwait=", incnwait,
			"work.nproc=", work.nproc)
		throw("work.nwait > work.nproc")
	}

	if incnwait == work.nproc && !gcMarkWorkAvailable(nil) {
		// This has reached a background completion point. Set
		// gp.param to a non-nil value to indicate this. It
		// doesn't matter what we set it to (it just has to be
		// a valid pointer).
		gp.param = unsafe.Pointer(gp)
	}
	now := nanotime()
	duration := now - startTime
	pp := gp.m.p.ptr()
	pp.gcAssistTime += duration
	if trackLimiterEvent {
		pp.limiterEvent.stop(limiterEventMarkAssist, now)
	}
	if pp.gcAssistTime > gcAssistTimeSlack {
		gcController.assistTime.Add(pp.gcAssistTime)
		gcCPULimiter.update(now)
		pp.gcAssistTime = 0
	}
}

// gcWakeAllAssists wakes all currently blocked assists. This is used
// at the end of a GC cycle. gcBlackenEnabled must be false to prevent
// new assists from going to sleep after this point.
func gcWakeAllAssists() {
	lock(&work.assistQueue.lock)
	list := work.assistQueue.q.popList()
	injectglist(&list)
	unlock(&work.assistQueue.lock)
}

// gcParkAssist puts the current goroutine on the assist queue and parks.
//
// gcParkAssist reports whether the assist is now satisfied. If it
// returns false, the caller must retry the assist.
func gcParkAssist() bool {
	lock(&work.assistQueue.lock)
	// If the GC cycle finished while we were getting the lock,
	// exit the assist. The cycle can't finish while we hold the
	// lock.
	if atomic.Load(&gcBlackenEnabled) == 0 {
		unlock(&work.assistQueue.lock)
		return true
	}

	gp := getg()
	oldList := work.assistQueue.q
	work.assistQueue.q.pushBack(gp)

	// Recheck for background credit now that this G is in
	// the queue, but can still back out. This avoids a
	// race in case background marking has flushed more
	// credit since we checked above.
	if gcController.bgScanCredit.Load() > 0 {
		work.assistQueue.q = oldList
		if oldList.tail != 0 {
			oldList.tail.ptr().schedlink.set(nil)
		}
		unlock(&work.assistQueue.lock)
		return false
	}
	// Park.
	goparkunlock(&work.assistQueue.lock, waitReasonGCAssistWait, traceBlockGCMarkAssist, 2)
	return true
}

// gcFlushBgCredit flushes scanWork units of background scan work
// credit. This first satisfies blocked assists on the
// work.assistQueue and then flushes any remaining credit to
// gcController.bgScanCredit.
//
// Write barriers are disallowed because this is used by gcDrain after
// it has ensured that all work is drained and this must preserve that
// condition.
//
//go:nowritebarrierrec
func gcFlushBgCredit(scanWork int64) {
	if work.assistQueue.q.empty() {
		// Fast path; there are no blocked assists. There's a
		// small window here where an assist may add itself to
		// the blocked queue and park. If that happens, we'll
		// just get it on the next flush.
		gcController.bgScanCredit.Add(scanWork)
		return
	}

	assistBytesPerWork := gcController.assistBytesPerWork.Load()
	scanBytes := int64(float64(scanWork) * assistBytesPerWork)

	lock(&work.assistQueue.lock)
	for !work.assistQueue.q.empty() && scanBytes > 0 {
		gp := work.assistQueue.q.pop()
		// Note that gp.gcAssistBytes is negative because gp
		// is in debt. Think carefully about the signs below.
		if scanBytes+gp.gcAssistBytes >= 0 {
			// Satisfy this entire assist debt.
			scanBytes += gp.gcAssistBytes
			gp.gcAssistBytes = 0
			// It's important that we *not* put gp in
			// runnext. Otherwise, it's possible for user
			// code to exploit the GC worker's high
			// scheduler priority to get itself always run
			// before other goroutines and always in the
			// fresh quantum started by GC.
			ready(gp, 0, false)
		} else {
			// Partially satisfy this assist.
			gp.gcAssistBytes += scanBytes
			scanBytes = 0
			// As a heuristic, we move this assist to the
			// back of the queue so that large assists
			// can't clog up the assist queue and
			// substantially delay small assists.
			work.assistQueue.q.pushBack(gp)
			break
		}
	}

	if scanBytes > 0 {
		// Convert from scan bytes back to work.
		assistWorkPerByte := gcController.assistWorkPerByte.Load()
		scanWork = int64(float64(scanBytes) * assistWorkPerByte)
		gcController.bgScanCredit.Add(scanWork)
	}
	unlock(&work.assistQueue.lock)
}

// scanstack scans gp's stack, greying all pointers found on the stack.
//
// Returns the amount of scan work performed, but doesn't update
// gcController.stackScanWork or flush any credit. Any background credit produced
// by this function should be flushed by its caller. scanstack itself can't
// safely flush because it may result in trying to wake up a goroutine that
// was just scanned, resulting in a self-deadlock.
//
// scanstack will also shrink the stack if it is safe to do so. If it
// is not, it schedules a stack shrink for the next synchronous safe
// point.
//
// scanstack is marked go:systemstack because it must not be preempted
// while using a workbuf.
//
//go:nowritebarrier
//go:systemstack
func scanstack(gp *g, gcw *gcWork) int64 {
	if readgstatus(gp)&_Gscan == 0 {
		print("runtime:scanstack: gp=", gp, ", goid=", gp.goid, ", gp->atomicstatus=", hex(readgstatus(gp)), "\n")
		throw("scanstack - bad status")
	}

	switch readgstatus(gp) &^ _Gscan {
	default:
		print("runtime: gp=", gp, ", goid=", gp.goid, ", gp->atomicstatus=", readgstatus(gp), "\n")
		throw("mark - bad status")
	case _Gdead:
		return 0
	case _Grunning:
		print("runtime: gp=", gp, ", goid=", gp.goid, ", gp->atomicstatus=", readgstatus(gp), "\n")
		throw("scanstack: goroutine not stopped")
	case _Grunnable, _Gsyscall, _Gwaiting, _Gdeadlocked:
		// ok
	}

	if gp == getg() {
		throw("can't scan our own stack")
	}

	// scannedSize is the amount of work we'll be reporting.
	//
	// It is less than the allocated size (which is hi-lo).
	var sp uintptr
	if gp.syscallsp != 0 {
		sp = gp.syscallsp // If in a system call this is the stack pointer (gp.sched.sp can be 0 in this case on Windows).
	} else {
		sp = gp.sched.sp
	}
	scannedSize := gp.stack.hi - sp

	// Keep statistics for initial stack size calculation.
	// Note that this accumulates the scanned size, not the allocated size.
	p := getg().m.p.ptr()
	p.scannedStackSize += uint64(scannedSize)
	p.scannedStacks++

	if isShrinkStackSafe(gp) {
		// Shrink the stack if not much of it is being used.
		shrinkstack(gp)
	} else {
		// Otherwise, shrink the stack at the next sync safe point.
		gp.preemptShrink = true
	}

	var state stackScanState
	state.stack = gp.stack

	if stackTraceDebug {
		println("stack trace goroutine", gp.goid)
	}

	if debugScanConservative && gp.asyncSafePoint {
		print("scanning async preempted goroutine ", gp.goid, " stack [", hex(gp.stack.lo), ",", hex(gp.stack.hi), ")\n")
	}

	// Scan the saved context register. This is effectively a live
	// register that gets moved back and forth between the
	// register and sched.ctxt without a write barrier.
	if gp.sched.ctxt != nil {
		scanblock(uintptr(unsafe.Pointer(&gp.sched.ctxt)), goarch.PtrSize, &oneptrmask[0], gcw, &state)
	}

	// Scan the stack. Accumulate a list of stack objects.
	var u unwinder
	for u.init(gp, 0); u.valid(); u.next() {
		scanframeworker(&u.frame, &state, gcw)
	}

	// Find additional pointers that point into the stack from the heap.
	// Currently this includes defers and panics. See also function copystack.

	// Find and trace other pointers in defer records.
	for d := gp._defer; d != nil; d = d.link {
		if d.fn != nil {
			// Scan the func value, which could be a stack allocated closure.
			// See issue 30453.
			scanblock(uintptr(unsafe.Pointer(&d.fn)), goarch.PtrSize, &oneptrmask[0], gcw, &state)
		}
		if d.link != nil {
			// The link field of a stack-allocated defer record might point
			// to a heap-allocated defer record. Keep that heap record live.
			scanblock(uintptr(unsafe.Pointer(&d.link)), goarch.PtrSize, &oneptrmask[0], gcw, &state)
		}
		// Retain defers records themselves.
		// Defer records might not be reachable from the G through regular heap
		// tracing because the defer linked list might weave between the stack and the heap.
		if d.heap {
			scanblock(uintptr(unsafe.Pointer(&d)), goarch.PtrSize, &oneptrmask[0], gcw, &state)
		}
	}
	if gp._panic != nil {
		// Panics are always stack allocated.
		state.putPtr(uintptr(unsafe.Pointer(gp._panic)), false)
	}

	// Find and scan all reachable stack objects.
	//
	// The state's pointer queue prioritizes precise pointers over
	// conservative pointers so that we'll prefer scanning stack
	// objects precisely.
	state.buildIndex()
	for {
		p, conservative := state.getPtr()
		if p == 0 {
			break
		}
		obj := state.findObject(p)
		if obj == nil {
			continue
		}
		r := obj.r
		if r == nil {
			// We've already scanned this object.
			continue
		}
		obj.setRecord(nil) // Don't scan it again.
		if stackTraceDebug {
			printlock()
			print("  live stkobj at", hex(state.stack.lo+uintptr(obj.off)), "of size", obj.size)
			if conservative {
				print(" (conservative)")
			}
			println()
			printunlock()
		}
		gcdata := r.gcdata()
		var s *mspan
		if r.useGCProg() {
			// This path is pretty unlikely, an object large enough
			// to have a GC program allocated on the stack.
			// We need some space to unpack the program into a straight
			// bitmask, which we allocate/free here.
			// TODO: it would be nice if there were a way to run a GC
			// program without having to store all its bits. We'd have
			// to change from a Lempel-Ziv style program to something else.
			// Or we can forbid putting objects on stacks if they require
			// a gc program (see issue 27447).
			s = materializeGCProg(r.ptrdata(), gcdata)
			gcdata = (*byte)(unsafe.Pointer(s.startAddr))
		}

		b := state.stack.lo + uintptr(obj.off)
		if conservative {
			scanConservative(b, r.ptrdata(), gcdata, gcw, &state)
		} else {
			scanblock(b, r.ptrdata(), gcdata, gcw, &state)
		}

		if s != nil {
			dematerializeGCProg(s)
		}
	}

	// Deallocate object buffers.
	// (Pointer buffers were all deallocated in the loop above.)
	for state.head != nil {
		x := state.head
		state.head = x.next
		if stackTraceDebug {
			for i := 0; i < x.nobj; i++ {
				obj := &x.obj[i]
				if obj.r == nil { // reachable
					continue
				}
				println("  dead stkobj at", hex(gp.stack.lo+uintptr(obj.off)), "of size", obj.r.size)
				// Note: not necessarily really dead - only reachable-from-ptr dead.
			}
		}
		x.nobj = 0
		putempty((*workbuf)(unsafe.Pointer(x)))
	}
	if state.buf != nil || state.cbuf != nil || state.freeBuf != nil {
		throw("remaining pointer buffers")
	}
	return int64(scannedSize)
}

// Scan a stack frame: local variables and function arguments/results.
//
//go:nowritebarrier
func scanframeworker(frame *stkframe, state *stackScanState, gcw *gcWork) {
	if _DebugGC > 1 && frame.continpc != 0 {
		print("scanframe ", funcname(frame.fn), "\n")
	}

	isAsyncPreempt := frame.fn.valid() && frame.fn.funcID == abi.FuncID_asyncPreempt
	isDebugCall := frame.fn.valid() && frame.fn.funcID == abi.FuncID_debugCallV2
	if state.conservative || isAsyncPreempt || isDebugCall {
		if debugScanConservative {
			println("conservatively scanning function", funcname(frame.fn), "at PC", hex(frame.continpc))
		}

		// Conservatively scan the frame. Unlike the precise
		// case, this includes the outgoing argument space
		// since we may have stopped while this function was
		// setting up a call.
		//
		// TODO: We could narrow this down if the compiler
		// produced a single map per function of stack slots
		// and registers that ever contain a pointer.
		if frame.varp != 0 {
			size := frame.varp - frame.sp
			if size > 0 {
				scanConservative(frame.sp, size, nil, gcw, state)
			}
		}

		// Scan arguments to this frame.
		if n := frame.argBytes(); n != 0 {
			// TODO: We could pass the entry argument map
			// to narrow this down further.
			scanConservative(frame.argp, n, nil, gcw, state)
		}

		if isAsyncPreempt || isDebugCall {
			// This function's frame contained the
			// registers for the asynchronously stopped
			// parent frame. Scan the parent
			// conservatively.
			state.conservative = true
		} else {
			// We only wanted to scan those two frames
			// conservatively. Clear the flag for future
			// frames.
			state.conservative = false
		}
		return
	}

	locals, args, objs := frame.getStackMap(false)

	// Scan local variables if stack frame has been allocated.
	if locals.n > 0 {
		size := uintptr(locals.n) * goarch.PtrSize
		scanblock(frame.varp-size, size, locals.bytedata, gcw, state)
	}

	// Scan arguments.
	if args.n > 0 {
		scanblock(frame.argp, uintptr(args.n)*goarch.PtrSize, args.bytedata, gcw, state)
	}

	// Add all stack objects to the stack object list.
	if frame.varp != 0 {
		// varp is 0 for defers, where there are no locals.
		// In that case, there can't be a pointer to its args, either.
		// (And all args would be scanned above anyway.)
		for i := range objs {
			obj := &objs[i]
			off := obj.off
			base := frame.varp // locals base pointer
			if off >= 0 {
				base = frame.argp // arguments and return values base pointer
			}
			ptr := base + uintptr(off)
			if ptr < frame.sp {
				// object hasn't been allocated in the frame yet.
				continue
			}
			if stackTraceDebug {
				println("stkobj at", hex(ptr), "of size", obj.size)
			}
			state.addObject(ptr, obj)
		}
	}
}

type gcDrainFlags int

const (
	gcDrainUntilPreempt gcDrainFlags = 1 << iota
	gcDrainFlushBgCredit
	gcDrainIdle
	gcDrainFractional
	gcDrainPartialDeadlock
)

// gcDrainMarkWorkerIdle is a wrapper for gcDrain that exists to better account
// mark time in profiles.
func gcDrainMarkWorkerIdle(gcw *gcWork) {
	gcDrain(gcw, gcDrainIdle|gcDrainUntilPreempt|gcDrainFlushBgCredit)
}

// gcDrainMarkWorkerDedicated is a wrapper for gcDrain that exists to better account
// mark time in profiles.
func gcDrainMarkWorkerDedicated(gcw *gcWork, untilPreempt bool) {
	flags := gcDrainFlushBgCredit
	if untilPreempt {
		flags |= gcDrainUntilPreempt
	}
	gcDrain(gcw, flags)
}

// gcDrainMarkWorkerDedicated is a wrapper for gcDrain that exists to better account
// mark time in profiles.
func gcDrainMarkWorkerPartialDeadlocks(gcw *gcWork) {
	gcDrain(gcw, gcDrainFlushBgCredit|gcDrainPartialDeadlock)
}

// gcDrainMarkWorkerFractional is a wrapper for gcDrain that exists to better account
// mark time in profiles.
func gcDrainMarkWorkerFractional(gcw *gcWork) {
	gcDrain(gcw, gcDrainFractional|gcDrainUntilPreempt|gcDrainFlushBgCredit)
}

func gcUpdateMarkrootNext() (uint32, bool) {
	var next uint32 = atomic.Load(&work.markrootNext)
	var success bool

	if next < atomic.Load(&work.markrootJobs) {
		// still work available at the moment
		for !success {
			success = atomic.Cas(&work.markrootNext, next, next+1)
			// We manage to snatch a root job. Return the root index.
			if success {
				return next, true
			}

			// Get the latest value of markrootNext.
			next = atomic.Load(&work.markrootNext)
			// We are out of markroot jobs.
			if next >= atomic.Load(&work.markrootJobs) {
				break
			}
		}
	}
	return 0, false
}

// gcDrain scans roots and objects in work buffers, blackening grey
// objects until it is unable to get more work. It may return before
// GC is done; it's the caller's responsibility to balance work from
// other Ps.
//
// If flags&gcDrainUntilPreempt != 0, gcDrain returns when g.preempt
// is set.
//
// If flags&gcDrainIdle != 0, gcDrain returns when there is other work
// to do.
//
// If flags&gcDrainFractional != 0, gcDrain self-preempts when
// pollFractionalWorkerExit() returns true. This implies
// gcDrainNoBlock.
//
// If flags&gcDrainFlushBgCredit != 0, gcDrain flushes scan work
// credit to gcController.bgScanCredit every gcCreditSlack units of
// scan work.
//
// gcDrain will always return if there is a pending STW or forEachP.
//
// Disabling write barriers is necessary to ensure that after we've
// confirmed that we've drained gcw, that we don't accidentally end
// up flipping that condition by immediately adding work in the form
// of a write barrier buffer flush.
//
// Don't set nowritebarrierrec because it's safe for some callees to
// have write barriers enabled.
//
//go:nowritebarrier
func gcDrain(gcw *gcWork, flags gcDrainFlags) {
	if !writeBarrier.enabled {
		throw("gcDrain phase incorrect")
	}

	// N.B. We must be running in a non-preemptible context, so it's
	// safe to hold a reference to our P here.
	gp := getg().m.curg
	pp := gp.m.p.ptr()
	preemptible := flags&gcDrainUntilPreempt != 0
	flushBgCredit := flags&gcDrainFlushBgCredit != 0
	drainingPartialDeadlocks := flags&gcDrainPartialDeadlock != 0
	idle := flags&gcDrainIdle != 0

	initScanWork := gcw.heapScanWork

	// checkWork is the scan work before performing the next
	// self-preempt check.
	checkWork := int64(1<<63 - 1)
	var check func() bool
	if flags&(gcDrainIdle|gcDrainFractional) != 0 {
		checkWork = initScanWork + drainCheckThreshold
		if idle {
			check = pollWork
		} else if flags&gcDrainFractional != 0 {
			check = pollFractionalWorkerExit
		}
	}

	// Drain root marking jobs.
	rootNext := atomic.Load(&work.markrootNext)
	rootJobs := atomic.Load(&work.markrootJobs)
	if rootNext < rootJobs {
		if gcddtrace(1) || gcddtrace(2) {
			validRoots := work.nValidStackRoots
			roots := work.nStackRoots

			println("\t\t===========================================================================\n"+
				"\t\t[[[[ DRAINING ROOT MARKING JOBS :: Root next", rootNext, ", Root jobs", rootJobs, "]]]]\n"+
				"\t\t[[[[ Valid Roots", validRoots, ", Roots", roots, "]]]]\n"+
				"\t\t[[[[ Drain flags:",
				"Until-preempt:", flags&gcDrainUntilPreempt != 0,
				"; Flush-BG-credit:", flags&gcDrainFlushBgCredit != 0,
				"; Idle:", flags&gcDrainIdle != 0,
				"; Fractional:", flags&gcDrainFractional != 0,
				"; Draining PDs:", flags&gcDrainPartialDeadlock != 0,
				"]]]]\n"+
					"\t\tWill enter mark loop:", drainingPartialDeadlocks || !(gp.preempt && (preemptible || sched.gcwaiting.Load() || pp.runSafePointFn != 0)),
				"\n"+
					"\t\tgp.preempt:", gp.preempt, "; preemptible:", preemptible, "; gcwaiting:", sched.gcwaiting.Load(), "; runSafePointFn:", pp.runSafePointFn,
				"\n"+
					"\t\t===========================================================================")
		}

		// Stop if we're preemptible, if someone wants to STW, or if
		// someone is calling forEachP.
		//
		// Continue unconditionally if we're draining partial deadlocks.
		for drainingPartialDeadlocks || !(gp.preempt && (preemptible || sched.gcwaiting.Load() || pp.runSafePointFn != 0)) {
			job, success := gcUpdateMarkrootNext()
			if !success {
				break
			}
			markroot(gcw, job, flags)
			if check != nil && check() {
				if drainingPartialDeadlocks {
					break
				}
				goto done
			}
		}
	}

	// Drain heap marking jobs.
	//
	// Stop if we're preemptible, if someone wants to STW, or if
	// someone is calling forEachP.
	//
	// TODO(mknyszek): Consider always checking gp.preempt instead
	// of having the preempt flag, and making an exception for certain
	// mark workers in retake. That might be simpler than trying to
	// enumerate all the reasons why we might want to preempt, even
	// if we're supposed to be mostly non-preemptible.
	for drainingPartialDeadlocks || !(gp.preempt && (preemptible || sched.gcwaiting.Load() || pp.runSafePointFn != 0)) {
		// Try to keep work available on the global queue. We used to
		// check if there were waiting workers, but it's better to
		// just keep work available than to make workers wait. In the
		// worst case, we'll do O(log(_WorkbufSize)) unnecessary
		// balances.
		if work.full == 0 {
			gcw.balance()
		}

		b := gcw.tryGetFast()
		if b == 0 {
			b = gcw.tryGet()
			if b == 0 {
				// Flush the write barrier
				// buffer; this may create
				// more work.
				wbBufFlush()
				b = gcw.tryGet()
			}
		}
		if b == 0 {
			// Unable to get work.
			break
		}
		scanobject(b, gcw)

		// Flush background scan work credit to the global
		// account if we've accumulated enough locally so
		// mutator assists can draw on it.
		if gcw.heapScanWork >= gcCreditSlack {
			gcController.heapScanWork.Add(gcw.heapScanWork)
			if flushBgCredit {
				gcFlushBgCredit(gcw.heapScanWork - initScanWork)
				initScanWork = 0
			}
			checkWork -= gcw.heapScanWork
			gcw.heapScanWork = 0

			if checkWork <= 0 {
				checkWork += drainCheckThreshold
				if check != nil && check() {
					break
				}
			}
		}
	}

done:
	// Flush remaining scan work credit.
	if gcw.heapScanWork > 0 {
		gcController.heapScanWork.Add(gcw.heapScanWork)
		if flushBgCredit {
			gcFlushBgCredit(gcw.heapScanWork - initScanWork)
		}
		gcw.heapScanWork = 0
	}
}

// gcDrainN blackens grey objects until it has performed roughly
// scanWork units of scan work or the G is preempted. This is
// best-effort, so it may perform less work if it fails to get a work
// buffer. Otherwise, it will perform at least n units of work, but
// may perform more because scanning is always done in whole object
// increments. It returns the amount of scan work performed.
//
// The caller goroutine must be in a preemptible state (e.g.,
// _Gwaiting) to prevent deadlocks during stack scanning. As a
// consequence, this must be called on the system stack.
//
//go:nowritebarrier
//go:systemstack
func gcDrainN(gcw *gcWork, scanWork int64) int64 {
	if !writeBarrier.enabled {
		throw("gcDrainN phase incorrect")
	}

	// There may already be scan work on the gcw, which we don't
	// want to claim was done by this call.
	workFlushed := -gcw.heapScanWork

	// In addition to backing out because of a preemption, back out
	// if the GC CPU limiter is enabled.
	gp := getg().m.curg
	for !gp.preempt && !gcCPULimiter.limiting() && workFlushed+gcw.heapScanWork < scanWork {
		// See gcDrain comment.
		if work.full == 0 {
			gcw.balance()
		}

		b := gcw.tryGetFast()
		if b == 0 {
			b = gcw.tryGet()
			if b == 0 {
				// Flush the write barrier buffer;
				// this may create more work.
				wbBufFlush()
				b = gcw.tryGet()
			}
		}

		if b == 0 {
			// Try to do a root job.
			rootNext := atomic.Load(&work.markrootNext)
			rootJobs := atomic.Load(&work.markrootJobs)
			if rootNext < rootJobs {
				job, success := gcUpdateMarkrootNext()
				if success {
					workFlushed += markroot(gcw, job, 0)
					continue
				}
			}
			// No heap or root jobs.
			break
		}

		scanobject(b, gcw)

		// Flush background scan work credit.
		if gcw.heapScanWork >= gcCreditSlack {
			gcController.heapScanWork.Add(gcw.heapScanWork)
			workFlushed += gcw.heapScanWork
			gcw.heapScanWork = 0
		}
	}

	// Unlike gcDrain, there's no need to flush remaining work
	// here because this never flushes to bgScanCredit and
	// gcw.dispose will flush any remaining work to scanWork.

	return workFlushed + gcw.heapScanWork
}

// scanblock scans b as scanobject would, but using an explicit
// pointer bitmap instead of the heap bitmap.
//
// This is used to scan non-heap roots, so it does not update
// gcw.bytesMarked or gcw.heapScanWork.
//
// If stk != nil, possible stack pointers are also reported to stk.putPtr.
//
//go:nowritebarrier
func scanblock(b0, n0 uintptr, ptrmask *uint8, gcw *gcWork, stk *stackScanState) {
	// Use local copies of original parameters, so that a stack trace
	// due to one of the throws below shows the original block
	// base and extent.
	b := b0
	n := n0

	for i := uintptr(0); i < n; {
		// Find bits for the next word.
		bits := uint32(*addb(ptrmask, i/(goarch.PtrSize*8)))
		if bits == 0 {
			i += goarch.PtrSize * 8
			continue
		}
		for j := 0; j < 8 && i < n; j++ {
			if bits&1 != 0 {
				// Same work as in scanobject; see comments there.
				p := *(*uintptr)(unsafe.Pointer(b + i))
				if p != 0 {
					if obj, span, objIndex := findObject(p, b, i); obj != 0 {
						greyobject(obj, b, i, span, gcw, objIndex)
					} else if stk != nil && p >= stk.stack.lo && p < stk.stack.hi {
						stk.putPtr(p, false)
					}
				}
			}
			bits >>= 1
			i += goarch.PtrSize
		}
	}
}

// scanobject scans the object starting at b, adding pointers to gcw.
// b must point to the beginning of a heap object or an oblet.
// scanobject consults the GC bitmap for the pointer mask and the
// spans for the size of the object.
//
//go:nowritebarrier
func scanobject(b uintptr, gcw *gcWork) {
	// Prefetch object before we scan it.
	//
	// This will overlap fetching the beginning of the object with initial
	// setup before we start scanning the object.
	sys.Prefetch(b)

	// Find the bits for b and the size of the object at b.
	//
	// b is either the beginning of an object, in which case this
	// is the size of the object to scan, or it points to an
	// oblet, in which case we compute the size to scan below.
	s := spanOfUnchecked(b)
	n := s.elemsize
	if n == 0 {
		throw("scanobject n == 0")
	}
	if s.spanclass.noscan() {
		// Correctness-wise this is ok, but it's inefficient
		// if noscan objects reach here.
		throw("scanobject of a noscan object")
	}

	var tp typePointers
	if n > maxObletBytes {
		// Large object. Break into oblets for better
		// parallelism and lower latency.
		if b == s.base() {
			// Enqueue the other oblets to scan later.
			// Some oblets may be in b's scalar tail, but
			// these will be marked as "no more pointers",
			// so we'll drop out immediately when we go to
			// scan those.
			for oblet := b + maxObletBytes; oblet < s.base()+s.elemsize; oblet += maxObletBytes {
				if !gcw.putFast(oblet) {
					gcw.put(oblet)
				}
			}
		}

		// Compute the size of the oblet. Since this object
		// must be a large object, s.base() is the beginning
		// of the object.
		n = s.base() + s.elemsize - b
		n = min(n, maxObletBytes)
		if goexperiment.AllocHeaders {
			tp = s.typePointersOfUnchecked(s.base())
			tp = tp.fastForward(b-tp.addr, b+n)
		}
	} else {
		if goexperiment.AllocHeaders {
			tp = s.typePointersOfUnchecked(b)
		}
	}

	var hbits heapBits
	if !goexperiment.AllocHeaders {
		hbits = heapBitsForAddr(b, n)
	}
	var scanSize uintptr
	for {
		var addr uintptr
		if goexperiment.AllocHeaders {
			if tp, addr = tp.nextFast(); addr == 0 {
				if tp, addr = tp.next(b + n); addr == 0 {
					break
				}
			}
		} else {
			if hbits, addr = hbits.nextFast(); addr == 0 {
				if hbits, addr = hbits.next(); addr == 0 {
					break
				}
			}
		}

		// Keep track of farthest pointer we found, so we can
		// update heapScanWork. TODO: is there a better metric,
		// now that we can skip scalar portions pretty efficiently?
		scanSize = addr - b + goarch.PtrSize

		// Work here is duplicated in scanblock and above.
		// If you make changes here, make changes there too.
		obj := *(*uintptr)(unsafe.Pointer(addr))

		// At this point we have extracted the next potential pointer.
		// Quickly filter out nil and pointers back to the current object.
		if obj != 0 && obj-b >= n {
			if gc_ptr_is_masked(unsafe.Pointer(obj)) {
				// Skip masked pointers.
				return
			}
			// Test if obj points into the Go heap and, if so,
			// mark the object.
			//
			// Note that it's possible for findObject to
			// fail if obj points to a just-allocated heap
			// object because of a race with growing the
			// heap. In this case, we know the object was
			// just allocated and hence will be marked by
			// allocation itself.
			if obj, span, objIndex := findObject(obj, b, addr-b); obj != 0 {
				greyobject(obj, b, addr-b, span, gcw, objIndex)
			}
		}
	}
	gcw.bytesMarked += uint64(n)
	gcw.heapScanWork += int64(scanSize)
}

// scanConservative scans block [b, b+n) conservatively, treating any
// pointer-like value in the block as a pointer.
//
// If ptrmask != nil, only words that are marked in ptrmask are
// considered as potential pointers.
//
// If state != nil, it's assumed that [b, b+n) is a block in the stack
// and may contain pointers to stack objects.
func scanConservative(b, n uintptr, ptrmask *uint8, gcw *gcWork, state *stackScanState) {
	if debugScanConservative {
		printlock()
		print("conservatively scanning [", hex(b), ",", hex(b+n), ")\n")
		hexdumpWords(b, b+n, func(p uintptr) byte {
			if ptrmask != nil {
				word := (p - b) / goarch.PtrSize
				bits := *addb(ptrmask, word/8)
				if (bits>>(word%8))&1 == 0 {
					return '$'
				}
			}

			val := *(*uintptr)(unsafe.Pointer(p))
			if state != nil && state.stack.lo <= val && val < state.stack.hi {
				return '@'
			}

			span := spanOfHeap(val)
			if span == nil {
				return ' '
			}
			idx := span.objIndex(val)
			if span.isFree(idx) {
				return ' '
			}
			return '*'
		})
		printunlock()
	}

	for i := uintptr(0); i < n; i += goarch.PtrSize {
		if ptrmask != nil {
			word := i / goarch.PtrSize
			bits := *addb(ptrmask, word/8)
			if bits == 0 {
				// Skip 8 words (the loop increment will do the 8th)
				//
				// This must be the first time we've
				// seen this word of ptrmask, so i
				// must be 8-word-aligned, but check
				// our reasoning just in case.
				if i%(goarch.PtrSize*8) != 0 {
					throw("misaligned mask")
				}
				i += goarch.PtrSize*8 - goarch.PtrSize
				continue
			}
			if (bits>>(word%8))&1 == 0 {
				continue
			}
		}

		val := *(*uintptr)(unsafe.Pointer(b + i))

		// Check if val points into the stack.
		if state != nil && state.stack.lo <= val && val < state.stack.hi {
			// val may point to a stack object. This
			// object may be dead from last cycle and
			// hence may contain pointers to unallocated
			// objects, but unlike heap objects we can't
			// tell if it's already dead. Hence, if all
			// pointers to this object are from
			// conservative scanning, we have to scan it
			// defensively, too.
			state.putPtr(val, true)
			continue
		}

		// Check if val points to a heap span.
		span := spanOfHeap(val)
		if span == nil {
			continue
		}

		// Check if val points to an allocated object.
		idx := span.objIndex(val)
		if span.isFree(idx) {
			continue
		}

		// val points to an allocated object. Mark it.
		obj := span.base() + idx*span.elemsize
		greyobject(obj, b, i, span, gcw, idx)
	}
}

// Shade the object if it isn't already.
// The object is not nil and known to be in the heap.
// Preemption must be disabled.
//
//go:nowritebarrier
func shade(b uintptr) {
	if obj, span, objIndex := findObject(b, 0, 0); obj != 0 {
		gcw := &getg().m.p.ptr().gcw
		greyobject(obj, 0, 0, span, gcw, objIndex)
	}
}

// obj is the start of an object with mark mbits.
// If it isn't already marked, mark it and enqueue into gcw.
// base and off are for debugging only and could be removed.
//
// See also wbBufFlush1, which partially duplicates this logic.
//
//go:nowritebarrierrec
func greyobject(obj, base, off uintptr, span *mspan, gcw *gcWork, objIndex uintptr) {
	// obj should be start of allocation, and so must be at least pointer-aligned.
	if obj&(goarch.PtrSize-1) != 0 {
		throw("greyobject: obj not pointer-aligned")
	}
	mbits := span.markBitsForIndex(objIndex)

	if useCheckmark {
		if setCheckmark(obj, base, off, mbits) {
			// Already marked.
			return
		}
	} else {
		if debug.gccheckmark > 0 && span.isFree(objIndex) {
			print("runtime: marking free object ", hex(obj), " found at *(", hex(base), "+", hex(off), ")\n")
			gcDumpObject("base", base, off)
			gcDumpObject("obj", obj, ^uintptr(0))
			getg().m.traceback = 2
			throw("marking free object")
		}

		// If marked we have nothing to do.
		if mbits.isMarked() {
			return
		}
		mbits.setMarked()

		// Mark span.
		arena, pageIdx, pageMask := pageIndexOf(span.base())
		if arena.pageMarks[pageIdx]&pageMask == 0 {
			atomic.Or8(&arena.pageMarks[pageIdx], pageMask)
		}

		// If this is a noscan object, fast-track it to black
		// instead of greying it.
		if span.spanclass.noscan() {
			gcw.bytesMarked += uint64(span.elemsize)
			return
		}
	}

	// We're adding obj to P's local workbuf, so it's likely
	// this object will be processed soon by the same P.
	// Even if the workbuf gets flushed, there will likely still be
	// some benefit on platforms with inclusive shared caches.
	sys.Prefetch(obj)
	// Queue the obj for scanning.
	if !gcw.putFast(obj) {
		gcw.put(obj)
	}
}

// gcDumpObject dumps the contents of obj for debugging and marks the
// field at byte offset off in obj.
func gcDumpObject(label string, obj, off uintptr) {
	s := spanOf(obj)
	print(label, "=", hex(obj))
	if s == nil {
		print(" s=nil\n")
		return
	}
	print(" s.base()=", hex(s.base()), " s.limit=", hex(s.limit), " s.spanclass=", s.spanclass, " s.elemsize=", s.elemsize, " s.state=")
	if state := s.state.get(); 0 <= state && int(state) < len(mSpanStateNames) {
		print(mSpanStateNames[state], "\n")
	} else {
		print("unknown(", state, ")\n")
	}

	skipped := false
	size := s.elemsize
	if s.state.get() == mSpanManual && size == 0 {
		// We're printing something from a stack frame. We
		// don't know how big it is, so just show up to an
		// including off.
		size = off + goarch.PtrSize
	}
	for i := uintptr(0); i < size; i += goarch.PtrSize {
		// For big objects, just print the beginning (because
		// that usually hints at the object's type) and the
		// fields around off.
		if !(i < 128*goarch.PtrSize || off-16*goarch.PtrSize < i && i < off+16*goarch.PtrSize) {
			skipped = true
			continue
		}
		if skipped {
			print(" ...\n")
			skipped = false
		}
		print(" *(", label, "+", i, ") = ", hex(*(*uintptr)(unsafe.Pointer(obj + i))))
		if i == off {
			print(" <==")
		}
		print("\n")
	}
	if skipped {
		print(" ...\n")
	}
}

// gcmarknewobject marks a newly allocated object black. obj must
// not contain any non-nil pointers.
//
// This is nosplit so it can manipulate a gcWork without preemption.
//
//go:nowritebarrier
//go:nosplit
func gcmarknewobject(span *mspan, obj uintptr) {
	if useCheckmark { // The world should be stopped so this should not happen.
		throw("gcmarknewobject called while doing checkmark")
	}

	// Mark object.
	objIndex := span.objIndex(obj)
	span.markBitsForIndex(objIndex).setMarked()

	// Mark span.
	arena, pageIdx, pageMask := pageIndexOf(span.base())
	if arena.pageMarks[pageIdx]&pageMask == 0 {
		atomic.Or8(&arena.pageMarks[pageIdx], pageMask)
	}

	gcw := &getg().m.p.ptr().gcw
	gcw.bytesMarked += uint64(span.elemsize)
}

// gcMarkTinyAllocs greys all active tiny alloc blocks.
//
// The world must be stopped.
func gcMarkTinyAllocs() {
	assertWorldStopped()

	for _, p := range allp {
		c := p.mcache
		if c == nil || c.tiny == 0 {
			continue
		}
		_, span, objIndex := findObject(c.tiny, 0, 0)
		gcw := &p.gcw
		greyobject(c.tiny, 0, 0, span, gcw, objIndex)
	}
}
