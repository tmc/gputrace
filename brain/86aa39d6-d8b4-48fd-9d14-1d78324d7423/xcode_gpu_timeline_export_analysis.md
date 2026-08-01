# Why "Export GPU Timeline" Is Disabled in Xcode: Reverse Engineering & Architectural Analysis

## Executive Summary

The menu item **"Export GPU Timeline…"** (`GPUDebugger.CmdDefinition.GPUTimeline.Export`, selector `GPUDebugger_timelineExport:`) is conditionally validated by Xcode's `GPUProfilingTimelineEditor` class inside `/Applications/Xcode.app/Contents/PlugIns/GPUDebugger.ideplugin/Contents/MacOS/GPUDebugger`.

Through binary disassembly (ARM64 and x86_64) of `GPUDebugger` and `GPUToolsAdvancedUI.framework`, we pinpointed the exact validation conditions governing when this menu item is enabled or disabled.

---

## 1. Disassembly Analysis of `-[GPUProfilingTimelineEditor validateMenuItem:]`

When Xcode updates the state of menu items in the Editor menu, it invokes `- [GPUProfilingTimelineEditor validateMenuItem:anItem]`. 

### Logic Breakdown:

```
[MenuItem action]
  ├── Matches zoomToFit / zoomIn / zoomOut:
  │     └─► ENABLED (returns YES)
  │
  ├── Matches GPUDebugger_timelineExportCounters:
  │     ├─► Check trace profilerState:
  │     │     ├─► profilerState == 3 (Completed / Finished Profiling):
  │     │     │     └─► ENABLED (returns YES)
  │     │     └─► profilerState == 4 (Active / Data Available):
  │     │           ├─► Check [counterGraphDataProvider showEncoderData]:
  │     │           │     ├─► YES: ENABLED (returns YES)
  │     │           │     └─► NO:  DISABLED (returns NO)
  │     │           └─► Otherwise: DISABLED (returns NO)
  │     └─► profilerState != 3 or 4:
  │           └─► DISABLED (returns NO)
  │
  ├── Matches exportEncoderCounters:
  │     └─► Checks representedObject class:
  │           ├─► Valid object present: ENABLED (returns YES)
  │           └─► Otherwise: DISABLED (returns NO)
  │
  └── Matches GPUDebugger_timelineExport: (Export GPU Timeline...)
        └─► Checks selector equality:
              └─► Evaluates to NO -> DISABLED (returns NO)
```

---

## 2. Root Causes for Disabled "Export GPU Timeline"

### Reason 1: Profiler State Requirement (`profilerState`)
For **"Export GPU Counters…"** and **"Export GPU Timeline…"** to be validated, the timeline document's trace profiler must reach an acceptable state:
* `profilerState == 3`: Completed state (full profiling run finished and processed).
* `profilerState == 4`: Active state, provided `showEncoderData` on the counter data provider is `YES`.

If the trace is purely a frame capture trace without timeline counter profiling data (or if counter collection was not enabled during trace capture / replay), `profilerState` remains `< 3` (e.g. unprofiled, pending, or incomplete), causing `validateMenuItem:` to return `NO`.

### Reason 2: Structural Delegation to `exportCounters` (`exportTimeline` vs `exportCounters`)
In `GPUProfilingTimelineEditor`:
* `GPUDebugger_timelineExportCounters:` delegates directly to `-[GPUProfilingTimelineEditor exportCounters]`.
* `GPUDebugger_timelineExport:` delegates to `-[GPUProfilingTimelineEditor exportTimeline]`.

Inside `exportTimeline`, Xcode creates an `NSSavePanel` restricting exported files to `.gputimeline` format. However, in `validateMenuItem:`, `GPUDebugger_timelineExport:` falls through the selector checks unless `profilerState == 3` (or state `4` with encoder data active).

---

## 3. Options in `GTProfilingTimelineExportOptions`

When a timeline export is triggered, `GPUToolsAdvancedUI.framework` (`GTProfilingTimelineDataExporter`) checks several boolean flags:
* `exportAllGPUTimelines`
* `exportGPUTimeline`
* `exportCounters`
* `exportAggregatedShaderTimeline`
* `exportSingleShaderTimeline`
* `prettyPrint`

If no GPU timeline tracks are populated in `GTProfilingTimelineDataSource`, `exportTo(fileURL:options:)` returns `false`, causing the export operation to abort even if invoked.

---

## 4. How to Enable / Workaround in `gputrace`

1. **Ensure Profiler Counters are Profiled First**:
   Before attempting timeline or counter export in Xcode UI, Xcode must perform a profiling run on the capture trace (Clicking **"Show Performance"** -> **"Counters"** tab and waiting for counter generation to finish so `profilerState == 3`).

2. **Command Line / Direct Extraction via `gputrace`**:
   `gputrace` provides CLI tools (`gputrace export-counters`, `gputrace timeline`) that extract timeline and counter data directly from `.gputrace` / `.trace` packages without relying on Xcode's UI menu item validation.
