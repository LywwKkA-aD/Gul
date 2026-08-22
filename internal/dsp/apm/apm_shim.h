/* Plain-C surface over webrtc::AudioProcessing for cgo. The library exports a
   C++-only API (AudioProcessingBuilder, nested Config structs, a refcounted
   AudioProcessing), none of which cgo can name; everything below is therefore
   POD and free functions. Reference shape: livekit/rust-sdks
   webrtc-sys/src/apm.cpp.

   The instance is bound to the project audio grid at creation time:
   48000 Hz, 1 channel, 10 ms frames. Nothing here is thread safe. */
#ifndef GUL_APM_SHIM_H
#define GUL_APM_SHIM_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define GUL_APM_SAMPLE_RATE 48000
#define GUL_APM_CHANNELS 1
#define GUL_APM_FRAME_SAMPLES 480

/* Shim-level failures. Every other non-zero result is a webrtc
   AudioProcessing::Error, which occupies -1 .. -12. */
#define GUL_APM_ERR_NULL (-1000)
#define GUL_APM_ERR_CREATE (-1001)
#define GUL_APM_ERR_FRAME_SIZE (-1002)
#define GUL_APM_ERR_NS_LEVEL (-1003)

/* Noise suppression levels, mapped onto
   AudioProcessing::Config::NoiseSuppression::Level. Off disables the
   submodule; kVeryHigh is deliberately not exposed. */
#define GUL_APM_NS_OFF 0
#define GUL_APM_NS_LOW 1
#define GUL_APM_NS_MODERATE 2
#define GUL_APM_NS_HIGH 3

typedef struct gul_apm gul_apm;

typedef struct {
  int echo_cancel; /* AEC3 in desktop mode */
  int ns_level;    /* one of GUL_APM_NS_* */
} gul_apm_config;

/* Returns NULL when the configuration is rejected or the library reports a
   frame size other than GUL_APM_FRAME_SAMPLES for our grid, storing the
   reason in *err (0 on success). err may not be NULL. */
gul_apm *gul_apm_create(const gul_apm_config *cfg, int *err);

/* Both process calls run in place: src and dest are the same buffer, which
   webrtc explicitly supports. frame must hold GUL_APM_FRAME_SAMPLES samples. */
int gul_apm_process_stream(gul_apm *p, int16_t *frame);
int gul_apm_process_reverse_stream(gul_apm *p, int16_t *frame);

void gul_apm_set_stream_delay_ms(gul_apm *p, int ms);

void gul_apm_destroy(gul_apm *p);

#ifdef __cplusplus
}
#endif

#endif /* GUL_APM_SHIM_H */
