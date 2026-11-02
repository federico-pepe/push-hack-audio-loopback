//go:build linux && cgo

#include "bridge.h"

#include <alsa/asoundlib.h>
#include <errno.h>
#include <sched.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static char g_err[512];

static void set_error(const char *fmt, ...) {
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(g_err, sizeof(g_err), fmt, ap);
    va_end(ap);
}

const char *mixer_last_error(void) { return g_err; }

struct mixer_pcm {
    snd_pcm_t *pcm;
    unsigned int channels;
    snd_pcm_uframes_t period;
};

mixer_pcm_t *mixer_pcm_open(const char *device, unsigned int channels, unsigned int rate,
                      unsigned int period_hint, unsigned int buffer_hint) {
    snd_pcm_t *pcm = NULL;
    int err = snd_pcm_open(&pcm, device, SND_PCM_STREAM_PLAYBACK, 0);
    if (err < 0) {
        set_error("snd_pcm_open(%s): %s", device, snd_strerror(err));
        return NULL;
    }
    snd_pcm_hw_params_t *hw;
    snd_pcm_hw_params_alloca(&hw);
    snd_pcm_hw_params_any(pcm, hw);
    snd_pcm_hw_params_set_access(pcm, hw, SND_PCM_ACCESS_RW_INTERLEAVED);
    snd_pcm_hw_params_set_format(pcm, hw, SND_PCM_FORMAT_S16_LE);
    snd_pcm_hw_params_set_channels(pcm, hw, channels);
    unsigned int r = rate;
    snd_pcm_hw_params_set_rate_near(pcm, hw, &r, 0);
    snd_pcm_uframes_t period = period_hint;
    snd_pcm_hw_params_set_period_size_near(pcm, hw, &period, 0);
    snd_pcm_uframes_t buffer = buffer_hint;
    snd_pcm_hw_params_set_buffer_size_near(pcm, hw, &buffer);
    err = snd_pcm_hw_params(pcm, hw);
    if (err < 0) {
        set_error("snd_pcm_hw_params(%s): %s", device, snd_strerror(err));
        snd_pcm_close(pcm);
        return NULL;
    }
    snd_pcm_hw_params_get_period_size(hw, &period, 0);
    err = snd_pcm_prepare(pcm);
    if (err < 0) {
        set_error("snd_pcm_prepare: %s", snd_strerror(err));
        snd_pcm_close(pcm);
        return NULL;
    }
    mixer_pcm_t *o = (mixer_pcm_t *)malloc(sizeof(mixer_pcm_t));
    o->pcm = pcm;
    o->channels = channels;
    o->period = period;
    return o;
}

unsigned int mixer_pcm_period(mixer_pcm_t *p) { return p ? (unsigned int)p->period : 0; }
unsigned int mixer_pcm_channels(mixer_pcm_t *p) { return p ? p->channels : 0; }

int mixer_pcm_writei(mixer_pcm_t *p, const int16_t *buf, unsigned int frames) {
    if (!p) return -1;
    snd_pcm_sframes_t w = snd_pcm_writei(p->pcm, buf, frames);
    if (w == -EPIPE || w == -ESTRPIPE) {
        snd_pcm_recover(p->pcm, (int)w, 1);
        w = snd_pcm_writei(p->pcm, buf, frames);
    }
    return (int)w;
}

void mixer_pcm_close(mixer_pcm_t *p) {
    if (!p) return;
    snd_pcm_drain(p->pcm);
    snd_pcm_close(p->pcm);
    free(p);
}

int mixer_set_realtime(int priority) {
    struct sched_param sp;
    memset(&sp, 0, sizeof(sp));
    sp.sched_priority = priority;
    return sched_setscheduler(0, SCHED_FIFO, &sp);
}
