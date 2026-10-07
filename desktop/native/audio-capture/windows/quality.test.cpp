#include "quality.hpp"
#include <cassert>
#include <vector>

int main() {
  std::vector<float> game, hardware, leaked, mono;
  for (unsigned i = 0; i < 48000; ++i) {
    const auto left = static_cast<float>(0.12 * std::sin(2 * gul_audio_test::pi * 440 * i / 48000));
    const auto right = static_cast<float>(0.12 * std::sin(2 * gul_audio_test::pi * 660 * i / 48000));
    const auto voice = static_cast<float>(0.16 * std::sin(2 * gul_audio_test::pi * 880 * i / 48000));
    game.push_back(left); game.push_back(right);
    hardware.push_back(left + voice); hardware.push_back(right + voice);
    leaked.push_back(left + voice); leaked.push_back(right + voice);
    mono.push_back(left + right); mono.push_back(left + right);
  }
  const auto proof = gul_audio_test::measure(game, hardware);
  assert(proof.passes());
  assert(proof.left > 0.11 && proof.right > 0.11);
  assert(proof.exclusion_db > 60 && proof.separation_db > 60);
  assert(proof.audible_voice > 0.15);
  assert(!gul_audio_test::measure(leaked, hardware).passes());
  assert(!gul_audio_test::measure(mono, hardware).passes());
  assert(!gul_audio_test::measure(game, game).passes());
  assert(!gul_audio_test::measure({}, hardware).passes());
}
