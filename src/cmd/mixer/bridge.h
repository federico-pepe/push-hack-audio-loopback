/* bridge.h - ALSA PCM playback (the only C in the module). */
#ifndef MIXER_BRIDGE_H
#define MIXER_BRIDGE_H
#include <stdint.h>

typedef struct mixer_pcm mixer_pcm_t;

const char *mixer_last_error(void);

/* Opens a playback device with S16_LE interleaved. The channel count and rate
 * must match what the device already negotiated. */
mixer_pcm_t *mixer_pcm_open(const char *device, unsigned int channels, unsigned int rate,
                      unsigned int period_hint, unsigned int buffer_hint);
unsigned int mixer_pcm_period(mixer_pcm_t *p);
unsigned int mixer_pcm_channels(mixer_pcm_t *p);
/* Writes frames; recovers from an underrun once. Returns frames or a negative error. */
int mixer_pcm_writei(mixer_pcm_t *p, const int16_t *buf, unsigned int frames);
void mixer_pcm_close(mixer_pcm_t *p);

/* SCHED_FIFO for the calling thread. 0 on success. */
int mixer_set_realtime(int priority);
#endif
