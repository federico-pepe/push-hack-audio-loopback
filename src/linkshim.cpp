//go:build linux && cgo

// linkshim.cpp - Ableton Link Audio peer for push-audio-loopback.
// The Link SDK is GPL v2 or later (src/third_party/link). See linkshim.h.

#include "linkshim.h"

#include <ableton/LinkAudio.hpp>

#include <algorithm>
#include <array>
#include <atomic>
#include <chrono>
#include <cstdio>
#include <cstring>
#include <sys/random.h>
#include <vector>
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

std::string hexId(const std::array<std::uint8_t, 8> &id) {
  char b[17];
  for (int i = 0; i < 8; ++i) snprintf(b + 2 * i, 3, "%02x", id[i]);
  return std::string(b, 16);
}

bool parseHexId(const char *s, std::array<std::uint8_t, 8> &out) {
  if (!s || strlen(s) != 16) return false;
  for (int i = 0; i < 8; ++i) {
    unsigned v;
    if (sscanf(s + 2 * i, "%2x", &v) != 1) return false;
    out[i] = static_cast<std::uint8_t>(v);
  }
  return true;
}

void jsonString(std::string &out, const std::string &in) {
  out += '"';
  for (unsigned char c : in) {
    switch (c) {
      case '"': out += "\\\""; break;
      case '\\': out += "\\\\"; break;
      default:
        if (c < 0x20) {
          char b[8];
          snprintf(b, sizeof b, "\\u%04x", c);
          out += b;
        } else {
          out += static_cast<char>(c);
        }
    }
  }
  out += '"';
}

constexpr double kQuantum = 4.;
constexpr double kDriftSeconds = 0.010;  // restart the counter beyond this
}  // namespace

// ---- Source: Link thread (producer) -> ring -> caller (consumer). ----

struct pl_source {
  std::vector<float> ring;  // interleaved stereo
  size_t mask = 0;          // frames - 1; frames is a power of two
  std::atomic<uint64_t> wpos{0}, rpos{0};  // in frames
  int rate;
  std::atomic<uint64_t> buffers{0}, frames{0}, badRate{0}, badFormat{0}, overruns{0};
  std::atomic<int> rateSeen{0}, channelsSeen{0};
  std::atomic<float> peak{0};
  std::unique_ptr<ableton::LinkAudioSource> src;

  // Runs on the Link thread. No locks, no allocation.
  void onBuffer(const ableton::LinkAudioSource::BufferHandle &h) {
    const size_t nf = h.info.numFrames, nc = h.info.numChannels;
    rateSeen.store(static_cast<int>(h.info.sampleRate), std::memory_order_relaxed);
    channelsSeen.store(static_cast<int>(nc), std::memory_order_relaxed);
    if (h.info.sampleRate != static_cast<uint32_t>(rate)) { badRate.fetch_add(1); return; }
    if (nc != 1 && nc != 2) { badFormat.fetch_add(1); return; }
    const uint64_t w = wpos.load(std::memory_order_relaxed);
    const uint64_t r = rpos.load(std::memory_order_acquire);
    const size_t cap = mask + 1;
    if (nf > cap - static_cast<size_t>(w - r)) { overruns.fetch_add(1); return; }
    float pk = 0.f;
    for (size_t i = 0; i < nf; ++i) {
      const float l = h.samples[i * nc] / 32768.f;
      const float rr = nc == 2 ? h.samples[i * nc + 1] / 32768.f : l;
      const size_t at = static_cast<size_t>((w + i) & mask) * 2;
      ring[at] = l;
      ring[at + 1] = rr;
      pk = std::max(pk, std::max(std::fabs(l), std::fabs(rr)));
    }
    wpos.store(w + nf, std::memory_order_release);
    buffers.fetch_add(1);
    frames.fetch_add(nf);
    peak.store(pk, std::memory_order_relaxed);
  }
};

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

int pl_link_channels(pl_link *l, char *buf, int len) {
  std::string out = "[";
  if (l) {
    bool first = true;
    for (const auto &c : l->link.channels()) {
      if (!first) out += ',';
      first = false;
      out += "{\"id\":\"" + hexId(c.id) + "\",\"name\":";
      jsonString(out, c.name);
      out += ",\"peer_id\":\"" + hexId(c.peerId) + "\",\"peer\":";
      jsonString(out, c.peerName);
      out += '}';
    }
  }
  out += ']';
  if (buf && len > 0) {
    const size_t n = std::min(out.size(), static_cast<size_t>(len - 1));
    memcpy(buf, out.data(), n);
    buf[n] = 0;
  }
  return static_cast<int>(out.size());
}

pl_source *pl_source_new(pl_link *l, const char *hex, int rate, int ringFrames) {
  std::array<std::uint8_t, 8> raw;
  if (!l || rate <= 0 || ringFrames <= 0 || !parseHexId(hex, raw)) return nullptr;
  try {
    auto *s = new pl_source();
    size_t cap = 256;
    while (cap < static_cast<size_t>(ringFrames)) cap <<= 1;
    s->ring.assign(cap * 2, 0.f);
    s->mask = cap - 1;
    s->rate = rate;
    s->src = std::make_unique<ableton::LinkAudioSource>(
        l->link, ableton::link::NodeId(raw),
        [s](ableton::LinkAudioSource::BufferHandle h) { s->onBuffer(h); });
    return s;
  } catch (...) {
    return nullptr;
  }
}

void pl_source_free(pl_source *s) {
  if (!s) return;
  s->src.reset();  // stops the callback before the ring goes away
  delete s;
}

int pl_source_fill(pl_source *s) {
  if (!s) return 0;
  return static_cast<int>(s->wpos.load(std::memory_order_acquire) - s->rpos.load(std::memory_order_relaxed));
}

int pl_source_read(pl_source *s, float *out, int frames) {
  if (!s || !out || frames <= 0) return 0;
  const uint64_t r = s->rpos.load(std::memory_order_relaxed);
  const uint64_t w = s->wpos.load(std::memory_order_acquire);
  const int n = std::min<int>(frames, static_cast<int>(w - r));
  for (int i = 0; i < n; ++i) {
    const size_t at = static_cast<size_t>((r + i) & s->mask) * 2;
    out[2 * i] = s->ring[at];
    out[2 * i + 1] = s->ring[at + 1];
  }
  s->rpos.store(r + n, std::memory_order_release);
  return n;
}

void pl_source_skip(pl_source *s, int frames) {
  if (!s || frames <= 0) return;
  const uint64_t r = s->rpos.load(std::memory_order_relaxed);
  const uint64_t w = s->wpos.load(std::memory_order_acquire);
  const uint64_t n = std::min<uint64_t>(static_cast<uint64_t>(frames), w - r);
  s->rpos.store(r + n, std::memory_order_release);
}

void pl_source_get_stats(pl_source *s, pl_source_stats *out) {
  if (!s || !out) return;
  out->buffers = s->buffers.load();
  out->frames = s->frames.load();
  out->bad_rate = s->badRate.load();
  out->bad_format = s->badFormat.load();
  out->overruns = s->overruns.load();
  out->rate_seen = s->rateSeen.load();
  out->channels_seen = s->channelsSeen.load();
  out->peak = s->peak.load();
}

}  // extern "C"
