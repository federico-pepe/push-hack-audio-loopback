//go:build linux && cgo

// linkshim.cpp - Ableton Link Audio peer for push-audio-loopback.
// The Link SDK is GPL v2 or later (src/third_party/link). See linkshim.h.

#include "linkshim.h"

#include <ableton/LinkAudio.hpp>

#include <chrono>
#include <sys/random.h>
#include <cmath>
#include <memory>
#include <string>

struct pl_link {
  ableton::LinkAudio link;
  explicit pl_link(const std::string &name) : link(120., name) {}
};

struct pl_sink {
  pl_link *owner;
  ableton::LinkAudioSink sink;
  double beats = 0;  // stamp of the next buffer
  bool anchored = false;
  uint64_t reanchors = 0;
  pl_sink(pl_link *l, const std::string &name, size_t maxSamples)
      : owner(l), sink(l->link, name, maxSamples) {}
};

namespace {
constexpr double kQuantum = 4.;
constexpr double kDriftSeconds = 0.010;  // restart the counter beyond this
}  // namespace

extern "C" {

// Static libstdc++ 12 (Debian bookworm) calls arc4random, glibc 2.36. Push 3
// has glibc 2.35, so the loader refuses the binary. Own copy here: the linker
// takes it, no GLIBC_2.36 symbol is left. Source: getrandom(2).
uint32_t arc4random(void) {
  uint32_t v;
  for (;;) {
    if (getrandom(&v, sizeof v, 0) == static_cast<ssize_t>(sizeof v)) return v;
  }
}

pl_link *pl_link_new(const char *name) {
  try {
    auto *l = new pl_link(name ? name : "PushHack");
    l->link.enable(true);
    l->link.enableLinkAudio(true);
    return l;
  } catch (...) {
    return nullptr;
  }
}

void pl_link_free(pl_link *l) { delete l; }

int pl_link_peers(pl_link *l) { return l ? static_cast<int>(l->link.numPeers()) : 0; }

double pl_link_tempo(pl_link *l) {
  return l ? l->link.captureAppSessionState().tempo() : 0.;
}

pl_sink *pl_sink_new(pl_link *l, const char *name, int maxFrames) {
  if (!l || !name || maxFrames <= 0) return nullptr;
  try {
    return new pl_sink(l, name, static_cast<size_t>(maxFrames) * 2);
  } catch (...) {
    return nullptr;
  }
}

void pl_sink_free(pl_sink *s) { delete s; }

int pl_sink_write(pl_sink *s, const int16_t *samples, int frames, int rate, int leadMs) {
  if (!s || !samples || frames <= 0 || rate <= 0) return -1;
  auto buf = ableton::LinkAudioSink::BufferHandle(s->sink);
  if (!buf) {
    s->anchored = false;  // nobody listens: start fresh when someone does
    return 0;
  }
  if (static_cast<size_t>(frames) * 2 > buf.maxNumSamples) return -1;
  for (int i = 0; i < frames * 2; ++i) buf.samples[i] = samples[i];

  auto st = s->owner->link.captureAppSessionState();
  const double bps = st.tempo() / 60.0;
  const auto now = s->owner->link.clock().micros();
  const double want =
      st.beatAtTime(now + std::chrono::microseconds(static_cast<long>(leadMs) * 1000), kQuantum);
  if (!s->anchored || std::fabs(s->beats - want) > kDriftSeconds * bps) {
    s->beats = want;
    s->anchored = true;
    ++s->reanchors;
  }
  buf.commit(st, s->beats, kQuantum, static_cast<uint32_t>(frames), 2, static_cast<uint32_t>(rate));
  s->beats += static_cast<double>(frames) / rate * bps;
  return 1;
}

uint64_t pl_sink_reanchors(pl_sink *s) { return s ? s->reanchors : 0; }

}  // extern "C"
