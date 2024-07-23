// Code generated by mklockrank.go; DO NOT EDIT.

package runtime

type lockRank int

// Constants representing the ranks of all non-leaf runtime locks, in rank order.
// Locks with lower rank must be taken before locks with higher rank,
// in addition to satisfying the partial order in lockPartialOrder.
// A few ranks allow self-cycles, which are specified in lockPartialOrder.
const (
	lockRankUnknown lockRank = iota

	lockRankSysmon
	lockRankScavenge
	lockRankForcegc
	lockRankDefer
	lockRankSweepWaiters
	lockRankAssistQueue
	lockRankSweep
	lockRankMark
	lockRankTestR
	lockRankTestW
	lockRankAllocmW
	lockRankExecW
	lockRankCpuprof
	lockRankPollDesc
	lockRankWakeableSleep
	// SCHED
	lockRankAllocmR
	lockRankExecR
	lockRankSched
	lockRankAllg
	lockRankAllp
	lockRankTimers
	lockRankNetpollInit
	lockRankHchan
	lockRankNotifyList
	lockRankSudog
	lockRankRoot
	lockRankItab
	lockRankReflectOffs
	lockRankUserArenaState
	// TRACEGLOBAL
	lockRankTraceBuf
	lockRankTraceStrings
	// MALLOC
	lockRankFin
	lockRankSpanSetSpine
	lockRankMspanSpecial
	// MPROF
	lockRankGcBitsArenas
	lockRankProfInsert
	lockRankProfBlock
	lockRankProfMemActive
	lockRankProfMemFuture
	// STACKGROW
	lockRankGscan
	lockRankStackpool
	lockRankStackLarge
	lockRankHchanLeaf
	// WB
	lockRankWbufSpans
	lockRankMheap
	lockRankMheapSpecial
	lockRankGlobalAlloc
	// TRACE
	lockRankTrace
	lockRankTraceStackTab
	lockRankPanic
	lockRankDeadlock
	lockRankRaceFini
	lockRankAllocmRInternal
	lockRankExecRInternal
	lockRankTestRInternal
)

// lockRankLeafRank is the rank of lock that does not have a declared rank,
// and hence is a leaf lock.
const lockRankLeafRank lockRank = 1000

// lockNames gives the names associated with each of the above ranks.
var lockNames = []string{
	lockRankSysmon:          "sysmon",
	lockRankScavenge:        "scavenge",
	lockRankForcegc:         "forcegc",
	lockRankDefer:           "defer",
	lockRankSweepWaiters:    "sweepWaiters",
	lockRankAssistQueue:     "assistQueue",
	lockRankSweep:           "sweep",
	lockRankTestR:           "testR",
	lockRankTestW:           "testW",
	lockRankAllocmW:         "allocmW",
	lockRankExecW:           "execW",
	lockRankCpuprof:         "cpuprof",
	lockRankPollDesc:        "pollDesc",
	lockRankWakeableSleep:   "wakeableSleep",
	lockRankAllocmR:         "allocmR",
	lockRankExecR:           "execR",
	lockRankSched:           "sched",
	lockRankAllg:            "allg",
	lockRankAllp:            "allp",
	lockRankTimers:          "timers",
	lockRankNetpollInit:     "netpollInit",
	lockRankHchan:           "hchan",
	lockRankNotifyList:      "notifyList",
	lockRankSudog:           "sudog",
	lockRankRoot:            "root",
	lockRankItab:            "itab",
	lockRankReflectOffs:     "reflectOffs",
	lockRankUserArenaState:  "userArenaState",
	lockRankTraceBuf:        "traceBuf",
	lockRankTraceStrings:    "traceStrings",
	lockRankFin:             "fin",
	lockRankSpanSetSpine:    "spanSetSpine",
	lockRankMspanSpecial:    "mspanSpecial",
	lockRankGcBitsArenas:    "gcBitsArenas",
	lockRankProfInsert:      "profInsert",
	lockRankProfBlock:       "profBlock",
	lockRankProfMemActive:   "profMemActive",
	lockRankProfMemFuture:   "profMemFuture",
	lockRankGscan:           "gscan",
	lockRankStackpool:       "stackpool",
	lockRankStackLarge:      "stackLarge",
	lockRankHchanLeaf:       "hchanLeaf",
	lockRankWbufSpans:       "wbufSpans",
	lockRankMheap:           "mheap",
	lockRankMheapSpecial:    "mheapSpecial",
	lockRankGlobalAlloc:     "globalAlloc",
	lockRankTrace:           "trace",
	lockRankTraceStackTab:   "traceStackTab",
	lockRankPanic:           "panic",
	lockRankDeadlock:        "deadlock",
	lockRankRaceFini:        "raceFini",
	lockRankAllocmRInternal: "allocmRInternal",
	lockRankExecRInternal:   "execRInternal",
	lockRankTestRInternal:   "testRInternal",
}

func (rank lockRank) String() string {
	if rank == 0 {
		return "UNKNOWN"
	}
	if rank == lockRankLeafRank {
		return "LEAF"
	}
	if rank < 0 || int(rank) >= len(lockNames) {
		return "BAD RANK"
	}
	return lockNames[rank]
}

// lockPartialOrder is the transitive closure of the lock rank graph.
// An entry for rank X lists all of the ranks that can already be held
// when rank X is acquired.
//
// Lock ranks that allow self-cycles list themselves.
var lockPartialOrder [][]lockRank = [][]lockRank{
	lockRankSysmon:          {},
	lockRankScavenge:        {lockRankSysmon},
	lockRankForcegc:         {lockRankSysmon},
	lockRankDefer:           {},
	lockRankSweepWaiters:    {},
	lockRankAssistQueue:     {},
	lockRankSweep:           {},
	lockRankMark:            {},
	lockRankTestR:           {},
	lockRankTestW:           {},
	lockRankAllocmW:         {},
	lockRankExecW:           {},
	lockRankCpuprof:         {},
	lockRankPollDesc:        {},
	lockRankWakeableSleep:   {},
	lockRankAllocmR:         {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep},
	lockRankExecR:           {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep},
	lockRankSched:           {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankMark},
	lockRankAllg:            {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched},
	lockRankAllp:            {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched},
	lockRankTimers:          {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllp, lockRankTimers},
	lockRankNetpollInit:     {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllp, lockRankTimers},
	lockRankHchan:           {lockRankSysmon, lockRankScavenge, lockRankSweep, lockRankTestR, lockRankWakeableSleep, lockRankHchan},
	lockRankNotifyList:      {},
	lockRankSudog:           {lockRankSysmon, lockRankScavenge, lockRankSweep, lockRankTestR, lockRankWakeableSleep, lockRankHchan, lockRankNotifyList},
	lockRankRoot:            {},
	lockRankItab:            {},
	lockRankReflectOffs:     {lockRankItab},
	lockRankUserArenaState:  {},
	lockRankTraceBuf:        {lockRankSysmon, lockRankScavenge},
	lockRankTraceStrings:    {lockRankSysmon, lockRankScavenge, lockRankTraceBuf},
	lockRankFin:             {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankHchan, lockRankNotifyList, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings},
	lockRankSpanSetSpine:    {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankHchan, lockRankNotifyList, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings},
	lockRankMspanSpecial:    {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankHchan, lockRankNotifyList, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings},
	lockRankGcBitsArenas:    {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankHchan, lockRankNotifyList, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings, lockRankMspanSpecial},
	lockRankProfInsert:      {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankHchan, lockRankNotifyList, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings},
	lockRankProfBlock:       {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankHchan, lockRankNotifyList, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings},
	lockRankProfMemActive:   {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankHchan, lockRankNotifyList, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings},
	lockRankProfMemFuture:   {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankHchan, lockRankNotifyList, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings, lockRankProfMemActive},
	lockRankGscan:           {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankNetpollInit, lockRankHchan, lockRankNotifyList, lockRankRoot, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings, lockRankFin, lockRankSpanSetSpine, lockRankMspanSpecial, lockRankGcBitsArenas, lockRankProfInsert, lockRankProfBlock, lockRankProfMemActive, lockRankProfMemFuture},
	lockRankStackpool:       {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankNetpollInit, lockRankHchan, lockRankNotifyList, lockRankRoot, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings, lockRankFin, lockRankSpanSetSpine, lockRankMspanSpecial, lockRankGcBitsArenas, lockRankProfInsert, lockRankProfBlock, lockRankProfMemActive, lockRankProfMemFuture, lockRankGscan},
	lockRankStackLarge:      {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankNetpollInit, lockRankHchan, lockRankNotifyList, lockRankRoot, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings, lockRankFin, lockRankSpanSetSpine, lockRankMspanSpecial, lockRankGcBitsArenas, lockRankProfInsert, lockRankProfBlock, lockRankProfMemActive, lockRankProfMemFuture, lockRankGscan},
	lockRankHchanLeaf:       {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankNetpollInit, lockRankHchan, lockRankNotifyList, lockRankRoot, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings, lockRankFin, lockRankSpanSetSpine, lockRankMspanSpecial, lockRankGcBitsArenas, lockRankProfInsert, lockRankProfBlock, lockRankProfMemActive, lockRankProfMemFuture, lockRankGscan, lockRankHchanLeaf},
	lockRankWbufSpans:       {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankDefer, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankNetpollInit, lockRankHchan, lockRankNotifyList, lockRankSudog, lockRankRoot, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings, lockRankFin, lockRankSpanSetSpine, lockRankMspanSpecial, lockRankGcBitsArenas, lockRankProfInsert, lockRankProfBlock, lockRankProfMemActive, lockRankProfMemFuture, lockRankGscan},
	lockRankMheap:           {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankDefer, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankNetpollInit, lockRankHchan, lockRankNotifyList, lockRankSudog, lockRankRoot, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings, lockRankFin, lockRankSpanSetSpine, lockRankMspanSpecial, lockRankGcBitsArenas, lockRankProfInsert, lockRankProfBlock, lockRankProfMemActive, lockRankProfMemFuture, lockRankGscan, lockRankStackpool, lockRankStackLarge, lockRankWbufSpans},
	lockRankMheapSpecial:    {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankDefer, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankNetpollInit, lockRankHchan, lockRankNotifyList, lockRankSudog, lockRankRoot, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings, lockRankFin, lockRankSpanSetSpine, lockRankMspanSpecial, lockRankGcBitsArenas, lockRankProfInsert, lockRankProfBlock, lockRankProfMemActive, lockRankProfMemFuture, lockRankGscan, lockRankStackpool, lockRankStackLarge, lockRankWbufSpans, lockRankMheap},
	lockRankGlobalAlloc:     {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankDefer, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankNetpollInit, lockRankHchan, lockRankNotifyList, lockRankSudog, lockRankRoot, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings, lockRankFin, lockRankSpanSetSpine, lockRankMspanSpecial, lockRankGcBitsArenas, lockRankProfInsert, lockRankProfBlock, lockRankProfMemActive, lockRankProfMemFuture, lockRankGscan, lockRankStackpool, lockRankStackLarge, lockRankWbufSpans, lockRankMheap, lockRankMheapSpecial},
	lockRankTrace:           {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankDefer, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankNetpollInit, lockRankHchan, lockRankNotifyList, lockRankSudog, lockRankRoot, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings, lockRankFin, lockRankSpanSetSpine, lockRankMspanSpecial, lockRankGcBitsArenas, lockRankProfInsert, lockRankProfBlock, lockRankProfMemActive, lockRankProfMemFuture, lockRankGscan, lockRankStackpool, lockRankStackLarge, lockRankWbufSpans, lockRankMheap},
	lockRankTraceStackTab:   {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankDefer, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR, lockRankExecR, lockRankSched, lockRankAllg, lockRankAllp, lockRankTimers, lockRankNetpollInit, lockRankHchan, lockRankNotifyList, lockRankSudog, lockRankRoot, lockRankItab, lockRankReflectOffs, lockRankUserArenaState, lockRankTraceBuf, lockRankTraceStrings, lockRankFin, lockRankSpanSetSpine, lockRankMspanSpecial, lockRankGcBitsArenas, lockRankProfInsert, lockRankProfBlock, lockRankProfMemActive, lockRankProfMemFuture, lockRankGscan, lockRankStackpool, lockRankStackLarge, lockRankWbufSpans, lockRankMheap, lockRankTrace},
	lockRankPanic:           {},
	lockRankDeadlock:        {lockRankPanic, lockRankDeadlock},
	lockRankRaceFini:        {lockRankPanic},
	lockRankAllocmRInternal: {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankAllocmW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankAllocmR},
	lockRankExecRInternal:   {lockRankSysmon, lockRankScavenge, lockRankForcegc, lockRankSweepWaiters, lockRankAssistQueue, lockRankSweep, lockRankTestR, lockRankExecW, lockRankCpuprof, lockRankPollDesc, lockRankWakeableSleep, lockRankExecR},
	lockRankTestRInternal:   {lockRankTestR, lockRankTestW},
}
