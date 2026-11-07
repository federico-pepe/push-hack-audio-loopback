# CLAUDE.md

Guidance for Claude Code when it works in this repository.

## Push family context

Shared facts for all Push repos (repo map, git identity, `core/` pinning, cross-repo hardware facts):

@~/.claude/push-family.md

## Building the `snd-aloop.ko` kernel module

The README's Docker build recipe can fail with an error like this:

```
E: Failed to fetch http://deb.debian.org/debian-security/pool/updates/main/g/gnupg2/gpgsm_2.2.27-2+deb11u3_amd64.deb  404  Not Found
```

Cause: `apt-get install` installs Recommends by default. One recommended
package pulled in a version that the mirror's package index still lists,
but the mirror's file pool no longer serves. This is a Debian mirror
index/pool desync, external to this repo. It is not caused by a change in
this repo, and a plain retry does not fix it.

Fix: add `--no-install-recommends` to the `apt-get install` line in the
Docker build command from the README:

```bash
apt-get install -qq -y --no-install-recommends \
  build-essential bc bison flex libssl-dev libelf-dev kmod rsync
```

If this still fails on a fresh mirror desync, check which package
actually 404s and confirm it only appears as a Recommends (not a hard
Depends) of one of the packages above before assuming a different fix is
needed.

## The Loopback pair (see also `aloop-rename.patch` and `src/main.go`)

`snd-aloop` always creates two cross-wired PCM devices per card. This
pairing is the loopback mechanism itself — a single PCM device cannot
loop audio to itself, so there is no way to reduce this to one device.

Device 1 (the feed side, meant to be opened by `hw:` address by whatever
process feeds it) always appears in Live's own audio device list next to
device 0, with no way to hide it. Live's device list comes from the
driver's ALSA-level enumeration data, not from device naming or
filesystem permissions. An earlier version of this hack tried both:
renaming device 1 to "PCM - feed, do not select", and `chmod 600`-locking
its raw device nodes. Neither hid it from Live's list — do not
re-attempt either approach without a way to change ALSA's own
enumeration behavior (which would need a change to ALSA core, not to
this driver).

## Audio bus mixer (`mixer.go`, `web.go`, `pcm_cgo.go`)

This service also owns the write side of `hw:PHVAudio,1,0`. `mixer.go` runs an `audiobus.Server`
(`core/audiobus`), follows Live's negotiated format with `alsapcm.ReadHWParams`, mixes every client
in float and writes one S16 block per period. Audio hacks (Braids, MachineModule) connect to it
instead of opening the card; without a mixer they write to the card themselves.

- The binary now needs cgo and libasound (`bridge.c`, `pcm_cgo.go`). `pcm_stub.go` keeps the package
  buildable and testable without them. Build and test with `make build` / `make test` (Docker).
- The release workflow builds in the same Docker image. A plain `CGO_ENABLED=0 go build` no longer works.
- `core/audiobus` comes from `core/v0.5.5` (pinned in `src/go.mod`).
- `web.go` serves `GET /api/status` and a status page. The port comes from the catalog (`hackcfg`), 7719 as a fallback.
- Pairs are saved in `audio-bus-pairs.json` next to `hack.json`.
- Design and plan: `docs/audio-bus.md` in ableton-push-hack.

## Link Audio mode (`link.go`, `linkshim.cpp`, `link_cgo.go`)

The web page switches the output between the loopback card ("card", `mixer.go`) and Ableton
Link Audio ("link", `link.go`). The mode is saved in `output-mode.json` next to `hack.json`.
Link is the default (a missing or bad `output-mode.json` means "link"). `runOutput` runs one of them and swaps them when `POST /api/mode` changes it.

- The hacks do not change. In Link mode the bus server has a fixed format (44100 Hz, period 128,
  16 channels = 8 pairs). `runLink` calls `Server.Mix`, reads each client's pair, and writes it to
  one Link sink named after the client. Nothing is mixed across hacks. No `core/` change was needed.
- Stamp every buffer with a sample counter (`pl_sink_write`), starting at "now + 40 ms", and restart
  it only when it drifts more than 10 ms. Live ignores buffers that carry its own timestamps. A stamp
  taken from the clock on each buffer plays with glitches. Seen on a Push 3 (2026-11-05).
- Never set the Link tempo or transport. The peer only follows the session.
- Link (`src/third_party/link`, tag `Link-4.1.1`) is a git submodule with its own asio submodule. It lives
  under `src/` because the release workflow mounts only `src/` into Docker. The checkout needs
  `submodules: recursive`.
- **glibc:** Push 3 has glibc 2.35, the Docker image has 2.36. A static libstdc++ from this image asks
  for `arc4random` (2.36) and the loader refuses the binary ("version GLIBC_2.36 not found"). The shim
  defines its own `arc4random`. `make build` and the release workflow fail on any glibc symbol above 2.35.
- The binary needs the loader path `/lib/ld-linux-x86-64.so.2` (the Push has no `/lib64`). The Makefile sets it.
- `link_stub.go` and the build tag in `linkshim.cpp` keep `go vet` and `go test` working off-device.
- Link is GPL v2 or later, so the binary is GPL v2 or later. The repository is GPL v2 or later (`LICENSE`).
- Plan and results: `plans/2026-11-05-link-audio-for-hacks.md` in ableton-push-hack.

## Link inputs (`router.go`, `jitter.go`, `linkshim.cpp`, `linkrec.go`)

Routes connect a Link channel to the input of a hack (`core/audiobus`: `OpenWithInputs`, `Server.Inputs`, `Input.Write`).

- `router.go`: `routeStore` saves routes in `link-routes.json`. `router.tick` runs once for each period inside `runLink`: it moves one period from each source into the input ring of its hack. Once a second `reconcile` makes the sources match the routes and the session, and takes the snapshot for the page.
- A route has no effect while its hack or channel is away. It stays in the file. It resolves a channel by ID first, then by peer and channel name.
- `router` talks to Link through `netAPI`/`srcAPI`/`busInput` interfaces, so `router_test.go` runs with fakes, off-device.
- **Never keep an `Input` from an earlier tick.** The server unmaps its ring about half a second after the hack leaves. `tick` takes the list again on every call and sets `a.in` from it. A saved pointer could write into unmapped memory.
- `jitter.go`: play starts at 4 periods, audio above 6 periods is dropped, an empty ring gives silence and a new fill. `pl_source_*` in `linkshim.cpp` is a ring that the Link thread writes (lock-free, no allocation). Only 44100 Hz is accepted.
- Channel names come from other devices on the network. `index.html` builds the matrix with `textContent` only. Keep it that way.
- `GET /api/matrix` and `POST /api/route` are in `web.go` (JSON only for the POST).
- `-linkrec` (in `linkrec.go`) is a test tool that records a Link channel to a WAV file. It joins Link as `PushHack rec`.
- The routes and inputs need `core` v0.5.5 (`OpenWithInputs`), pinned in `src/go.mod`.
- Plan and results: `plans/2026-11-07-link-inputs-matrix.md` in ableton-push-hack.
