#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>

#include "gul_ma.h"

struct gul_ring {
    int frame_samples;
    int slots;
    _Atomic uint64_t write_idx; /* frames ever pushed */
    _Atomic uint64_t read_idx;  /* frames ever popped */
    _Atomic uint64_t dropped;
    int16_t* data;
};

gul_ring* gul_ring_new(int frame_samples, int slots) {
    if (frame_samples <= 0 || slots <= 0) {
        return NULL;
    }
    gul_ring* r = (gul_ring*)calloc(1, sizeof(gul_ring));
    if (r == NULL) {
        return NULL;
    }
    r->data = (int16_t*)calloc((size_t)frame_samples * (size_t)slots, sizeof(int16_t));
    if (r->data == NULL) {
        free(r);
        return NULL;
    }
    r->frame_samples = frame_samples;
    r->slots = slots;
    return r;
}

void gul_ring_free(gul_ring* r) {
    if (r != NULL) {
        free(r->data);
        free(r);
    }
}

int gul_ring_push(gul_ring* r, const int16_t* frame) {
    uint64_t w = atomic_load_explicit(&r->write_idx, memory_order_relaxed);
    uint64_t rd = atomic_load_explicit(&r->read_idx, memory_order_acquire);
    if (w - rd >= (uint64_t)r->slots) {
        atomic_fetch_add_explicit(&r->dropped, 1, memory_order_relaxed);
        return 0;
    }
    memcpy(r->data + (w % (uint64_t)r->slots) * (uint64_t)r->frame_samples,
           frame, (size_t)r->frame_samples * sizeof(int16_t));
    atomic_store_explicit(&r->write_idx, w + 1, memory_order_release);
    return 1;
}

int gul_ring_pop(gul_ring* r, int16_t* frame) {
    uint64_t rd = atomic_load_explicit(&r->read_idx, memory_order_relaxed);
    uint64_t w = atomic_load_explicit(&r->write_idx, memory_order_acquire);
    if (w == rd) {
        return 0;
    }
    memcpy(frame,
           r->data + (rd % (uint64_t)r->slots) * (uint64_t)r->frame_samples,
           (size_t)r->frame_samples * sizeof(int16_t));
    atomic_store_explicit(&r->read_idx, rd + 1, memory_order_release);
    return 1;
}

int gul_ring_available(gul_ring* r) {
    uint64_t w = atomic_load_explicit(&r->write_idx, memory_order_acquire);
    uint64_t rd = atomic_load_explicit(&r->read_idx, memory_order_acquire);
    return (int)(w - rd);
}

uint64_t gul_ring_dropped(gul_ring* r) {
    return atomic_load_explicit(&r->dropped, memory_order_relaxed);
}

struct gul_device {
    ma_device device;
    gul_ring* ring;
    /* Re-framing buffer: backends normally deliver exactly frame_samples
       (noFixedSizedCallback = 0), but the callbacks stay correct for any
       frameCount. Touched only by the audio thread. */
    int16_t* accum;
    int accum_pos; /* playback: consumed samples of the loaded frame */
    int accum_len; /* capture: fill level; playback: loaded frame length */
    int frame_samples;
    int type;
    _Atomic uint64_t cb_frames;
    _Atomic uint64_t underruns;
    _Atomic uint64_t reroutes;
    _Atomic int stopped;
};

static void gul_capture_cb(ma_device* pDevice, void* pOutput, const void* pInput, ma_uint32 frameCount) {
    gul_device* d = (gul_device*)pDevice->pUserData;
    const int16_t* in = (const int16_t*)pInput;
    ma_uint32 left = frameCount;
    (void)pOutput;
    if (in == NULL) {
        return;
    }
    while (left > 0) {
        int take = d->frame_samples - d->accum_len;
        if ((ma_uint32)take > left) {
            take = (int)left;
        }
        memcpy(d->accum + d->accum_len, in, (size_t)take * sizeof(int16_t));
        d->accum_len += take;
        in += take;
        left -= (ma_uint32)take;
        if (d->accum_len == d->frame_samples) {
            gul_ring_push(d->ring, d->accum); /* full ring: counted drop */
            d->accum_len = 0;
        }
    }
    atomic_fetch_add_explicit(&d->cb_frames, frameCount, memory_order_relaxed);
}

static void gul_playback_cb(ma_device* pDevice, void* pOutput, const void* pInput, ma_uint32 frameCount) {
    gul_device* d = (gul_device*)pDevice->pUserData;
    int16_t* out = (int16_t*)pOutput;
    ma_uint32 need = frameCount;
    (void)pInput;
    while (need > 0) {
        if (d->accum_pos == d->accum_len) {
            if (gul_ring_pop(d->ring, d->accum)) {
                d->accum_pos = 0;
                d->accum_len = d->frame_samples;
            } else {
                /* Starved: emit silence for the rest of the period. */
                memset(out, 0, (size_t)need * sizeof(int16_t));
                atomic_fetch_add_explicit(&d->underruns, 1, memory_order_relaxed);
                break;
            }
        }
        int take = d->accum_len - d->accum_pos;
        if ((ma_uint32)take > need) {
            take = (int)need;
        }
        memcpy(out, d->accum + d->accum_pos, (size_t)take * sizeof(int16_t));
        d->accum_pos += take;
        out += take;
        need -= (ma_uint32)take;
    }
    atomic_fetch_add_explicit(&d->cb_frames, frameCount, memory_order_relaxed);
}

static void gul_notification_cb(const ma_device_notification* pNotification) {
    gul_device* d = (gul_device*)pNotification->pDevice->pUserData;
    if (pNotification->type == ma_device_notification_type_stopped) {
        atomic_store_explicit(&d->stopped, 1, memory_order_relaxed);
    } else if (pNotification->type == ma_device_notification_type_rerouted) {
        atomic_fetch_add_explicit(&d->reroutes, 1, memory_order_relaxed);
    }
}

int gul_device_open(gul_device** out, ma_context* ctx, int type,
                    const ma_device_id* id, int sample_rate,
                    int frame_samples, int ring_slots) {
    gul_device* d = (gul_device*)calloc(1, sizeof(gul_device));
    if (d == NULL) {
        return MA_OUT_OF_MEMORY;
    }
    d->frame_samples = frame_samples;
    d->type = type;
    d->ring = gul_ring_new(frame_samples, ring_slots);
    d->accum = (int16_t*)calloc((size_t)frame_samples, sizeof(int16_t));
    if (d->ring == NULL || d->accum == NULL) {
        gul_device_close(d);
        return MA_OUT_OF_MEMORY;
    }

    ma_device_config cfg = ma_device_config_init(
        type == GUL_DEVICE_CAPTURE ? ma_device_type_capture : ma_device_type_playback);
    cfg.sampleRate = (ma_uint32)sample_rate;
    cfg.periodSizeInFrames = (ma_uint32)frame_samples;
    cfg.dataCallback = type == GUL_DEVICE_CAPTURE ? gul_capture_cb : gul_playback_cb;
    cfg.notificationCallback = gul_notification_cb;
    cfg.pUserData = d;
    if (type == GUL_DEVICE_CAPTURE) {
        cfg.capture.pDeviceID = id;
        cfg.capture.format = ma_format_s16;
        cfg.capture.channels = 1;
    } else {
        cfg.playback.pDeviceID = id;
        cfg.playback.format = ma_format_s16;
        cfg.playback.channels = 1;
    }

    ma_result res = ma_device_init(ctx, &cfg, &d->device);
    if (res != MA_SUCCESS) {
        gul_ring_free(d->ring);
        free(d->accum);
        free(d);
        return (int)res;
    }
    *out = d;
    return MA_SUCCESS;
}

int gul_device_start(gul_device* d) {
    atomic_store_explicit(&d->stopped, 0, memory_order_relaxed);
    return (int)ma_device_start(&d->device);
}

int gul_device_stop(gul_device* d) {
    return (int)ma_device_stop(&d->device);
}

void gul_device_close(gul_device* d) {
    if (d == NULL) {
        return;
    }
    if (d->ring != NULL || d->accum != NULL) {
        /* Only uninit a device that was actually initialized: open failures
           call this with ring/accum possibly set but device zeroed. */
        if (d->device.pContext != NULL) {
            ma_device_uninit(&d->device);
        }
    }
    gul_ring_free(d->ring);
    free(d->accum);
    free(d);
}

gul_ring* gul_device_ring(gul_device* d) { return d->ring; }

uint64_t gul_device_callback_frames(gul_device* d) {
    return atomic_load_explicit(&d->cb_frames, memory_order_relaxed);
}

uint64_t gul_device_underruns(gul_device* d) {
    return atomic_load_explicit(&d->underruns, memory_order_relaxed);
}

uint64_t gul_device_reroutes(gul_device* d) {
    return atomic_load_explicit(&d->reroutes, memory_order_relaxed);
}

int gul_device_stopped(gul_device* d) {
    return atomic_load_explicit(&d->stopped, memory_order_relaxed);
}

uint32_t gul_device_internal_rate(gul_device* d) {
    return d->type == GUL_DEVICE_CAPTURE
        ? d->device.capture.internalSampleRate
        : d->device.playback.internalSampleRate;
}

uint32_t gul_device_internal_period(gul_device* d) {
    return d->type == GUL_DEVICE_CAPTURE
        ? d->device.capture.internalPeriodSizeInFrames
        : d->device.playback.internalPeriodSizeInFrames;
}
