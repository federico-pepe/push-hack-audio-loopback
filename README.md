# push-hack-audio-loopback

A [push-hack](https://github.com/federico-pepe/ableton-push-hack) module for
Ableton Push 3. Gives Live a selectable virtual audio device ("Push Hack
Virtual Audio"), backed by the standard Linux ALSA Loopback driver
(`snd-aloop`), so another process can send audio to Live or read audio from
it — without touching the real, exclusively-locked hardware audio device.

Install via [Push Hack Catalog](https://github.com/federico-pepe/ableton-push-hack/tree/main/catalog),
the on-device installer built into every push-hack setup.

## What it does

- Loads the Loopback kernel module on every boot and keeps checking it
  stays loaded — no manual steps after a reboot.
- Ships a prebuilt module for the stock Push 3 firmware kernel
  (`ko/5.15.48-intel-pk-preempt-rt/snd-aloop.ko`), so it works right after
  install with no build step of your own.
- If your kernel doesn't match any bundled module, it logs one clear line
  and does not load anything — never forces a mismatched module.

## Two devices, one for you

The Loopback driver always creates two PCM devices per card, cross-wired
to each other (device 0's playback arrives on device 1's capture, and
back). That pairing is the whole mechanism — a single device can't loop
audio to itself. Only device 0 ("PCM") is meant for you to pick in Live's
own audio preferences; device 1 ("Loopback PCM") is meant to be opened
directly, by `hw:` address, by whatever process feeds it (e.g.
[push-hack-braids](https://github.com/federico-pepe/push-hack-braids)).
Both devices still show up in Live's own device list — that list comes
from the driver's ALSA-level enumeration data, not from naming or
filesystem permissions, so there is no way to hide device 1 from it.

## Audio bus mixer

ALSA lets only one process write to a loopback stream. Without help, two audio hacks
(for example Braids and MachineModule) cannot play at the same time: the second one
gets "Device or resource busy".

This hack solves that. It owns the write side of the card and runs a mixer. An audio
hack does not open `hw:PHVAudio,1,0` any more. It connects to the mixer
(`core/audiobus`) and sends its audio. The mixer adds the audio of all hacks and writes
one stream, which Live reads.

- The mixer follows the format that Live negotiated, and reopens the card when it changes.
- Each hack plays on its own channel pair. The mixer remembers the pair of each hack in
  `audio-bus-pairs.json`, next to `hack.json`.
- A hack that finds no mixer writes to the card directly, as before. Only one hack can do
  this at a time.
- The web page of this hack (see "Web UI" in the Push Hack menu) shows the mixer state
  and the connected hacks, with their pair, level and underruns.
- You still need an audio track in Live that listens to the pair of the hack. The hack's
  own web page can create it.

The design is in
[docs/audio-bus.md](https://github.com/federico-pepe/ableton-push-hack/blob/main/docs/audio-bus.md)
in the main repository.

## Link Audio mode

The web page has a switch with two values: **Virtual Audio Interface** and **Link**. The
default is the Virtual Audio Interface, which is the mixer that this page describes above.
The choice is saved in `output-mode.json`, next to `hack.json`.

In **Link** mode the hack does not write the loopback card. It joins Ableton Link as the
peer `PushHack`. Each audio hack that connects to the bus gets its own Link Audio channel,
named after the hack. Live keeps the sound card of the Push as its output at all times.

- In Live, turn on Link and Link Audio. Give an audio track the input type `PushHack`, pick
  the channel of the hack, and set Monitor to In.
- The hacks do not change. They use the same bus as before. The format is fixed:
  stereo, 44100 Hz, 128 frames per block.
- The page shows `Link: N peers`, as Live does, and tells you if Live listens to each channel.
- Switching the mode drops the hacks for a moment. They reconnect by themselves.
- The kernel module still loads in both modes, so the virtual card stays in the device list of Live.
- Known issue: after a restart of the service, hacks can drift against the clock of Live.
  Press play in Live. The cause is in the hacks (see the plan in the main repository).

Each channel is stamped with a sample counter that starts 40 ms after "now". Live ignores
buffers that carry its own timestamps, and it plays a clock-stamped stream with glitches.
The code is in `src/linkshim.cpp` and `src/link.go`.

**Licence note.** The Link SDK (`src/third_party/link`, pinned to tag `Link-4.1.1`) is
GPL v2 or later, or proprietary from Ableton. A binary that links it is GPL v2 or later.
This repository has no licence file yet. This must be decided before a release.

The plan is `plans/2026-11-05-link-audio-for-hacks.md` in the main repository
([ableton-push-hack](https://github.com/federico-pepe/ableton-push-hack)).

## Building

The mixer needs cgo and libasound, and the Link mode needs a C++17 compiler. Clone with
`--recurse-submodules` (Link and its asio submodule are in `src/third_party/link`). Build in
Docker (like push-braids):

```bash
make build       # build/push-audio-loopback, linux/amd64, via Docker
make test        # go vet and go test -race in the same container
make test-local  # the portable tests on this machine (ALSA code is stubbed)
```

## Building a module for a different kernel

Needs the GPL source Ableton publishes for Push 3's firmware (request
your own copy from Ableton if you don't have it).

```bash
GPL_TGZ=push3-242-gpl-sources.tgz
KVER=linux-push3-5.15.48+gitAUTOINC+4ec79de9ce-r0   # match your device's `uname -r`

mkdir -p /tmp/push3-ksrc && cd /tmp/push3-ksrc
tar -xzf "$OLDPWD/$GPL_TGZ" \
  "sources/x86_64-oe-linux/$KVER/kernel.config" \
  "sources/x86_64-oe-linux/$KVER/$KVER-patched.tar.xz"
cd "sources/x86_64-oe-linux/$KVER"
mkdir ksrc && tar -xJf "$KVER-patched.tar.xz" -C ksrc
cp kernel.config ksrc/kernel-source/.config

patch -p1 -d ksrc/kernel-source < /path/to/aloop-rename.patch

docker run --rm --platform linux/amd64 -v "$PWD/ksrc/kernel-source":/work -w /work debian:bullseye \
  sh -c "apt-get update -qq && apt-get install -qq -y build-essential bc bison flex libssl-dev libelf-dev kmod rsync >/dev/null && \
         make olddefconfig ARCH=x86_64 && \
         make modules_prepare ARCH=x86_64 && \
         make M=sound/drivers ARCH=x86_64 modules"

# Vermagic must match your device's uname -r exactly, or the module load
# is refused:
strings ksrc/kernel-source/sound/drivers/snd-aloop.ko | grep ^vermagic=
```

Add the built `.ko` to `ko/<uname -r>/snd-aloop.ko`, commit, and cut a new
release (see below) — or copy it directly onto an installed device at
`/data/push-hack/hacks/push-audio-loopback/ko/<uname -r>/snd-aloop.ko` for
a quick local test.

## Releasing

```bash
# bump hack.json's "version", commit, then:
git tag v0.1.1
git push origin v0.1.1
```

`.github/workflows/release.yml` builds, packages, publishes the GitHub
Release, and updates `release.json` — Push Hack Catalog reads that file
live, so no further step is needed for the catalog to pick up a new
version.
