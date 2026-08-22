/* Definitions for the AVX2 entry points webrtc dispatches to on x86.

   The AVX2 translation units need per-file -mavx2/-mfma, which cgo cannot
   express: its flags apply to every file in the package, and compiling the
   whole APM with -mavx2 would produce a binary that crashes on any CPU without
   AVX2. So the AVX2 sources are not vendored and WEBRTC_ENABLE_AVX2 is never
   defined, which makes GetCPUInfo(kAVX2) return 0 unconditionally
   (system_wrappers/source/cpu_features.cc) and every runtime dispatch below
   pick the SSE2 or C path instead.

   The call sites, however, are guarded by WEBRTC_ARCH_X86_FAMILY rather than
   by WEBRTC_ENABLE_AVX2, so the symbols must still resolve at link time. These
   bodies are unreachable; they abort rather than compute anything. They are
   written as out-of-line definitions of the upstream declarations, so a
   signature change upstream fails the build instead of going unnoticed.

   Cost of giving up AVX2: single-digit percent of one core on the hot paths
   (matched filter, FIR, sqrt/multiply/accumulate), measured for the same code
   in docs/research/alternatives/aec.md. */

#include <stdlib.h>

#include "common_audio/fir_filter_avx2.h"
#include "common_audio/resampler/sinc_resampler.h"
#include "modules/audio_processing/aec3/adaptive_fir_filter.h"
#include "modules/audio_processing/aec3/adaptive_fir_filter_erl.h"
#include "modules/audio_processing/aec3/fft_data.h"
#include "modules/audio_processing/aec3/matched_filter.h"
#include "modules/audio_processing/aec3/vector_math.h"
#include "modules/audio_processing/agc2/rnn_vad/vector_math.h"

namespace {

[[noreturn]] void Unreachable() {
  abort();
}

}  // namespace

namespace webrtc {

FIRFilterAVX2::FIRFilterAVX2(const float* /*coefficients*/,
                             size_t /*coefficients_length*/,
                             size_t /*max_input_length*/)
    : coefficients_length_(0), state_length_(0) {
  Unreachable();
}

FIRFilterAVX2::~FIRFilterAVX2() = default;

void FIRFilterAVX2::Filter(const float* /*in*/,
                           size_t /*length*/,
                           float* /*out*/) {
  Unreachable();
}

float SincResampler::Convolve_AVX2(const float* /*input_ptr*/,
                                   const float* /*k1*/,
                                   const float* /*k2*/,
                                   double /*kernel_interpolation_factor*/) {
  Unreachable();
}

void FftData::SpectrumAVX2(rtc::ArrayView<float> /*power_spectrum*/) const {
  Unreachable();
}

namespace aec3 {

void ComputeFrequencyResponse_Avx2(
    size_t /*num_partitions*/,
    const std::vector<std::vector<FftData>>& /*H*/,
    std::vector<std::array<float, kFftLengthBy2Plus1>>* /*H2*/) {
  Unreachable();
}

void AdaptPartitions_Avx2(const RenderBuffer& /*render_buffer*/,
                          const FftData& /*G*/,
                          size_t /*num_partitions*/,
                          std::vector<std::vector<FftData>>* /*H*/) {
  Unreachable();
}

void ApplyFilter_Avx2(const RenderBuffer& /*render_buffer*/,
                      size_t /*num_partitions*/,
                      const std::vector<std::vector<FftData>>& /*H*/,
                      FftData* /*S*/) {
  Unreachable();
}

void ErlComputer_AVX2(
    const std::vector<std::array<float, kFftLengthBy2Plus1>>& /*H2*/,
    rtc::ArrayView<float> /*erl*/) {
  Unreachable();
}

void MatchedFilterCore_AVX2(size_t /*x_start_index*/,
                            float /*x2_sum_threshold*/,
                            float /*smoothing*/,
                            rtc::ArrayView<const float> /*x*/,
                            rtc::ArrayView<const float> /*y*/,
                            rtc::ArrayView<float> /*h*/,
                            bool* /*filters_updated*/,
                            float* /*error_sum*/,
                            bool /*compute_accumulated_error*/,
                            rtc::ArrayView<float> /*accumulated_error*/,
                            rtc::ArrayView<float> /*scratch_memory*/) {
  Unreachable();
}

void VectorMath::SqrtAVX2(rtc::ArrayView<float> /*x*/) {
  Unreachable();
}

void VectorMath::MultiplyAVX2(rtc::ArrayView<const float> /*x*/,
                              rtc::ArrayView<const float> /*y*/,
                              rtc::ArrayView<float> /*z*/) {
  Unreachable();
}

void VectorMath::AccumulateAVX2(rtc::ArrayView<const float> /*x*/,
                                rtc::ArrayView<float> /*z*/) {
  Unreachable();
}

}  // namespace aec3

namespace rnn_vad {

float VectorMath::DotProductAvx2(rtc::ArrayView<const float> /*x*/,
                                 rtc::ArrayView<const float> /*y*/) const {
  Unreachable();
}

}  // namespace rnn_vad
}  // namespace webrtc
