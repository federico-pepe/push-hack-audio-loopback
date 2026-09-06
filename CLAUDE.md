# CLAUDE.md

Guidance for Claude Code when it works in this repository.

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
