/* Thin C core between miniaudio devices and Go: SPSC frame rings plus
   device wrappers whose realtime callbacks stay pure C - no Go calls, no
   locks, no allocations, only memcpy into/out of the rings (PLAN.md 4.6).
   The vendored miniaudio implementation is compiled by z_miniaudio.c. */
#ifndef GUL_MA_H
#define GUL_MA_H

#include <stdint.h>

#include "miniaudio.h"

/* Lock-free single-producer single-consumer ring of fixed-size int16
   frames. The audio callback is one side, the Go DSP goroutine the other. */
typedef struct gul_ring gul_ring;

gul_ring* gul_ring_new(int frame_samples, int slots);
void gul_ring_free(gul_ring* r);
/* Copies one frame in; wait-free. Returns 1, or 0 when the ring is full
   (the frame is dropped and the drop counter bumped). */
int gul_ring_push(gul_ring* r, const int16_t* frame);
/* Copies one frame out. Returns 1, or 0 when the ring is empty. */
int gul_ring_pop(gul_ring* r, int16_t* frame);
int gul_ring_available(gul_ring* r);
uint64_t gul_ring_dropped(gul_ring* r);

/* One miniaudio device (capture or playback) bridged to a ring. */
typedef struct gul_device gul_device;

enum { GUL_DEVICE_CAPTURE = 0, GUL_DEVICE_PLAYBACK = 1 };

/* Opens a device on the context. id may be NULL for the system default.
   Returns ma_result (MA_SUCCESS == 0). */
int gul_device_open(gul_device** out, ma_context* ctx, int type,
                    const ma_device_id* id, int sample_rate,
                    int frame_samples, int ring_slots);
int gul_device_start(gul_device* d);
int gul_device_stop(gul_device* d);
void gul_device_close(gul_device* d);

gul_ring* gul_device_ring(gul_device* d);
uint64_t gul_device_callback_frames(gul_device* d);
uint64_t gul_device_underruns(gul_device* d);
uint64_t gul_device_reroutes(gul_device* d);
int gul_device_stopped(gul_device* d);
uint32_t gul_device_internal_rate(gul_device* d);
uint32_t gul_device_internal_period(gul_device* d);

#endif
