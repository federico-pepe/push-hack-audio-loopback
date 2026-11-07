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

#ifdef __cplusplus
}
#endif
#endif
