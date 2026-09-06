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
