# Syncing with upstream (kvarenzn/ssm)

This fork keeps the GUI **beside** the upstream code instead of inside it, so
merging upstream stays conflict-free. The rule:

> Upstream files are left as upstream wrote them, except for a few one-line
> hooks. Everything GUI-specific lives in files upstream does not have.

## Where the GUI code lives

| File (fork-only) | What it holds |
|---|---|
| `main_gui.go` | GUI entry (`-gui`, `-port`, no-argument start), playback flow, backend selection |
| `scores/humanize.go` | Timing/position/hold-time jitter, exact Great count, generator fixes the GUI relies on |
| `controllers/scrcpy_gui.go` | Non-fatal send/close, `ResetTouch`, tolerant touch preprocessing |
| `controllers/scrcpy_frames.go` | Opt-in video decoding (`SSM_ENABLE_VIDEO_DECODE=1`) and frame capture |
| `controllers/hid_gui.go` | Non-fatal USB calls, packing pointers into the 10 HID contact slots |
| `config/config_gui.go` | Thread-safe device accessors used by the GUI server |
| `adb/device_reverse.go` | Per-device reverse-forward removal (works with several devices attached) |
| `gui/`, `openbrowser_*.go` | Web server, frontend, browser launcher |

## Hooks in upstream files

These are the only intentional differences from upstream. If a merge conflicts,
it will be here; keep upstream's version and re-apply the hook.

| Upstream file | Hook |
|---|---|
| `main.go` | `if guiRequested() { runGUI(); return }` after `log.ShowDebug` |
| `scores/generate.go` | `config.beforeEmit(stars, pointers)` right after pointer allocation |
| `scores/common.go` | `beforeEmit` field at the end of `VTEGenerateConfig` |
| `controllers/scrcpy.go` | `frames` field, `setupDecoder()` call, skip video payload when not decoding, `KillReverseForward`, non-fatal server start, low video bitrate, 500 ms settle delay |

The only other differences are small fixes kept in this fork
(`adb/client.go`, `decoders/av/av.go`).

## Merging

```bash
git remote add upstream https://github.com/kvarenzn/ssm.git   # once
git config rerere.enabled true                                  # once: reuse conflict resolutions
git fetch upstream
git merge upstream/main
go build ./... && go test ./...
```

`go test ./scores ./controllers` checks the invariants the GUI depends on
(well-formed touch strokes, exact Great count, jitter bounds, 10-contact HID
reports), so a merge that silently changes generator behavior shows up there.

The command-line mode (`ssm-gui -n … -d …`) is upstream's code path unchanged,
so it behaves exactly like upstream ssm.
