#include "apm_shim.h"

#include <new>

#include "api/audio/audio_processing.h"
#include "api/scoped_refptr.h"

namespace {

/* AudioProcessing is refcounted (RefCountInterface), so the owning handle has
   to be a scoped_refptr rather than a raw pointer: Create() hands out one
   reference and the submodules keep their own. */
struct Handle {
  rtc::scoped_refptr<webrtc::AudioProcessing> apm;
  webrtc::StreamConfig stream;

  Handle()
      : stream(GUL_APM_SAMPLE_RATE, static_cast<size_t>(GUL_APM_CHANNELS)) {}
};

bool ApplyNoiseSuppression(webrtc::AudioProcessing::Config *config,
                          int level) {
  using NS = webrtc::AudioProcessing::Config::NoiseSuppression;
  switch (level) {
    case GUL_APM_NS_OFF:
      config->noise_suppression.enabled = false;
      return true;
    case GUL_APM_NS_LOW:
      config->noise_suppression.level = NS::kLow;
      break;
    case GUL_APM_NS_MODERATE:
      config->noise_suppression.level = NS::kModerate;
      break;
    case GUL_APM_NS_HIGH:
      config->noise_suppression.level = NS::kHigh;
      break;
    default:
      return false;
  }
  config->noise_suppression.enabled = true;
  return true;
}

}  // namespace

extern "C" {

gul_apm *gul_apm_create(const gul_apm_config *cfg, int *err) {
  if (err == nullptr) {
    return nullptr;
  }
  *err = 0;
  if (cfg == nullptr) {
    *err = GUL_APM_ERR_NULL;
    return nullptr;
  }

  webrtc::AudioProcessing::Config config;

  /* Everything below the API is fixed by PLAN.md section 4.3: the capture
     chain is HPF -> AEC3 -> NS -> AGC2 and the order is not configurable. */
  config.pipeline.maximum_internal_processing_rate = GUL_APM_SAMPLE_RATE;
  config.pipeline.multi_channel_render = false;
  config.pipeline.multi_channel_capture = false;

  config.high_pass_filter.enabled = true;

  config.echo_canceller.enabled = cfg->echo_cancel != 0;
  config.echo_canceller.mobile_mode = false;
  config.echo_canceller.enforce_high_pass_filtering = true;

  if (!ApplyNoiseSuppression(&config, cfg->ns_level)) {
    *err = GUL_APM_ERR_NS_LEVEL;
    return nullptr;
  }

  /* AGC2 is the only gain control in the pipeline; AGC1 stays off so the two
     do not fight over the same signal. The analog (input volume) controller
     stays off as well: we never touch the OS mixer. */
  config.gain_controller1.enabled = false;
  config.gain_controller2.enabled = true;
  config.gain_controller2.input_volume_controller.enabled = false;
  config.gain_controller2.adaptive_digital.enabled = true;

  Handle *h = new (std::nothrow) Handle();
  if (h == nullptr) {
    *err = GUL_APM_ERR_CREATE;
    return nullptr;
  }
  h->apm = webrtc::AudioProcessingBuilder().SetConfig(config).Create();
  if (h->apm == nullptr) {
    *err = GUL_APM_ERR_CREATE;
    delete h;
    return nullptr;
  }
  if (h->stream.num_frames() != static_cast<size_t>(GUL_APM_FRAME_SAMPLES)) {
    *err = GUL_APM_ERR_FRAME_SIZE;
    delete h;
    return nullptr;
  }
  return reinterpret_cast<gul_apm *>(h);
}

int gul_apm_process_stream(gul_apm *p, int16_t *frame) {
  if (p == nullptr || frame == nullptr) {
    return GUL_APM_ERR_NULL;
  }
  Handle *h = reinterpret_cast<Handle *>(p);
  return h->apm->ProcessStream(frame, h->stream, h->stream, frame);
}

int gul_apm_process_reverse_stream(gul_apm *p, int16_t *frame) {
  if (p == nullptr || frame == nullptr) {
    return GUL_APM_ERR_NULL;
  }
  Handle *h = reinterpret_cast<Handle *>(p);
  return h->apm->ProcessReverseStream(frame, h->stream, h->stream, frame);
}

void gul_apm_set_stream_delay_ms(gul_apm *p, int ms) {
  if (p == nullptr) {
    return;
  }
  /* AEC3 treats this as a hint and keeps refining the delay itself, so a
     rejected value is not worth surfacing. */
  reinterpret_cast<Handle *>(p)->apm->set_stream_delay_ms(ms);
}

void gul_apm_destroy(gul_apm *p) {
  if (p == nullptr) {
    return;
  }
  delete reinterpret_cast<Handle *>(p);
}

}  // extern "C"
