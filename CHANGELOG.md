# Changelog

User-facing notes for each release. The release workflow publishes the section
that matches the tag as the GitHub release description, so write these for
players, not for developers.

## [Unreleased]

**Core**
- Updated to the latest upstream ssm core (kvarenzn/ssm).
- If the screen connection fails to start, or an unexpected error happens while a song is loading, the page now shows the error instead of hanging or closing the program.
- Slide notes with position jitter now move smoothly between points.
- Command-line mode now behaves exactly like upstream ssm.

## [3.6.1] - 2026-06-03

**Playback**
- The **Stop** button is now **Restart** (↻): it re-arms the current song so you can press Start again — no need to re-load the song.
- The page now tells you when the connection to the backend drops and when it reconnects, instead of looking frozen.

**Song selection & search**
- The search box shows a **"Loading song list…"** state on first use, and a clear error if the list can't be fetched.
- Typing a **Song ID** directly now shows the song's **real title** and only the **difficulties it actually has**; clearing the field clears the selection.

**Stability (crash fixes)**
- Fixed a crash when starting **HID** with a device that has no saved resolution.
- Fixed a crash on charts with no playable notes.
- Fixed a resource leak when stopping/closing the screen connection repeatedly.
- Fixed a possible crash from editing devices in Settings while a song is loading.

**Docs**
- Quick Start now includes the **Extract Assets** step (pull game data → Extract).

> Internally this release also unified the GUI/CLI playback code and added a frontend test suite. If anything seems off with adb/HID playback, please open an issue.
