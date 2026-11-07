/* linkshim.h - C face of the Ableton Link Audio peer used by link_cgo.go.
 *
 * One peer ("PushHack") per process. One sink per audio hack: Live lists each
 * sink as a channel of that peer. The shim stamps every buffer with a sample
 * counter, not with the clock: stamps taken from the clock jitter and glitch
 * (seen on a Push 3, 2026-11-05). The counter starts at "now + lead" and
 * restarts only when it drifts more than 10 ms from that target.
 *
 * The peer never sets the tempo or the transport.
 */
#ifndef LINKSHIM_H
#define LINKSHIM_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct pl_link pl_link;
typedef struct pl_sink pl_sink;

/* Join Link as `name`, with Link and Link Audio on. NULL on failure. */
pl_link *pl_link_new(const char *name);
void pl_link_free(pl_link *l);

/* Number of other Link peers (devices and apps), as Live shows it. */
int pl_link_peers(pl_link *l);
double pl_link_tempo(pl_link *l);

/* A sink announces one stereo channel. maxFrames is the largest write. */
pl_sink *pl_sink_new(pl_link *l, const char *name, int maxFrames);
void pl_sink_free(pl_sink *s);

/* Write interleaved 16-bit stereo. Returns 1 when sent, 0 when no peer listens
 * (Link keeps no buffer then), -1 on bad arguments. */
int pl_sink_write(pl_sink *s, const int16_t *samples, int frames, int rate, int leadMs);

/* How often the stamp counter restarted. */
uint64_t pl_sink_reanchors(pl_sink *s);

/* ---- Sources: receive the channel of another peer (a Live track, Main, a hack). ----
 *
 * A source turns the buffers that Link delivers into one steady stereo float
 * stream. The Link thread writes into a ring. The caller reads from it, one
 * period at a time, and decides how full the ring must be (see jitter.go). */

typedef struct pl_source pl_source;

/* The channels of the session as JSON, for the matrix:
 *   [{"id":"<16 hex>","name":"1-Grand Piano","peer_id":"<16 hex>","peer":"Push"}, ...]
 * Writes at most len-1 bytes plus a NUL. Returns the full length, so a caller
 * that gets a value >= len must call again with a bigger buffer. */
int pl_link_channels(pl_link *l, char *buf, int len);

/* Subscribe to a channel by its ID (16 hex characters). Only `rate` Hz is
 * accepted: a buffer with another rate is dropped and counted. ringFrames
 * is rounded up to a power of two. NULL on a bad ID. */
pl_source *pl_source_new(pl_link *l, const char *hexId, int rate, int ringFrames);
void pl_source_free(pl_source *s);

/* Frames waiting in the ring. */
int pl_source_fill(pl_source *s);
/* Copy up to `frames` stereo frames (interleaved float) out. Never blocks.
 * Returns the number of frames copied. */
int pl_source_read(pl_source *s, float *out, int frames);
/* Throw away the oldest `frames` frames. Reader side only. */
void pl_source_skip(pl_source *s, int frames);

typedef struct {
  uint64_t buffers;     /* buffers received and accepted */
  uint64_t frames;      /* frames received and accepted */
  uint64_t bad_rate;    /* buffers dropped: sample rate is not `rate` */
  uint64_t bad_format;  /* buffers dropped: not 1 or 2 channels */
  uint64_t overruns;    /* buffers dropped: the ring was full */
  int rate_seen;        /* sample rate of the last buffer */
  int channels_seen;    /* channels of the last buffer */
  float peak;           /* peak of the last accepted buffer, 0..1 */
} pl_source_stats;

void pl_source_get_stats(pl_source *s, pl_source_stats *out);

#ifdef __cplusplus
}
#endif
#endif
