#pragma once

#include <algorithm>
#include <cmath>
#include <vector>

namespace gul_audio_test {
constexpr double pi = 3.14159265358979323846;
inline double amplitude(const std::vector<float>& pcm, unsigned channel, double frequency) {
  double sine = 0, cosine = 0, weights = 0;
  const std::size_t frames = pcm.size() / 2;
  if (frames < 2) return 0;
  for (std::size_t i = 0; i < frames; ++i) {
    const double phase = 2 * pi * frequency * static_cast<double>(i) / 48000;
    const double weight = 0.5 * (1 - std::cos(2 * pi * static_cast<double>(i) / static_cast<double>(frames - 1)));
    sine += weight * pcm[i * 2 + channel] * std::sin(phase);
    cosine += weight * pcm[i * 2 + channel] * std::cos(phase);
    weights += weight;
  }
  return weights > 0 ? 2 * std::sqrt(sine * sine + cosine * cosine) / weights : 0;
}
inline double peak_band(const std::vector<float>& pcm, unsigned channel, double frequency) {
  double peak = 0;
  for (int offset = -2; offset <= 2; ++offset)
    peak = std::max(peak, amplitude(pcm, channel, frequency + offset * 0.25));
  return peak;
}
struct Proof {
  double left, right, exclusion_db, separation_db, audible_voice;
  bool passes() const {
    return left > 0.04 && right > 0.04 && exclusion_db > 30 && separation_db > 30 && audible_voice > 0.04;
  }
};
inline Proof measure(const std::vector<float>& pcm, const std::vector<float>& hardware) {
  const double left = peak_band(pcm, 0, 440), right = peak_band(pcm, 1, 660);
  const double own = std::max(peak_band(pcm, 0, 880), peak_band(pcm, 1, 880));
  const double cross = std::max(peak_band(pcm, 0, 660), peak_band(pcm, 1, 440));
  return {left, right, 20 * std::log10(std::max(left, right) / std::max(own, 1e-9)),
          20 * std::log10(std::min(left, right) / std::max(cross, 1e-9)),
          std::max(peak_band(hardware, 0, 880), peak_band(hardware, 1, 880))};
}
}  // namespace gul_audio_test
