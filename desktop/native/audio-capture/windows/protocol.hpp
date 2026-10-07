#pragma once

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <cstring>

namespace gul_audio {
constexpr std::uint32_t sample_rate = 48000;
constexpr std::uint32_t channels = 2;
constexpr std::uint32_t max_frames = 480;
constexpr std::uint32_t frame_magic = 0x314c5547;
constexpr std::size_t frame_capacity = 16 + max_frames * channels * sizeof(float);

inline bool parent_allowed(std::uint32_t parent, std::uint32_t self,
                           std::uint64_t parent_birth, std::uint64_t self_birth,
                           bool same_user, bool same_session, bool alive) {
  return parent > 4 && parent != self && parent_birth > 0 && self_birth > 0 &&
         parent_birth <= self_birth && same_user && same_session && alive;
}
inline void write_u32(std::uint8_t* output, std::uint32_t value) {
  for (unsigned i = 0; i < 4; ++i) output[i] = static_cast<std::uint8_t>(value >> (i * 8));
}
inline std::uint32_t read_u32(const std::uint8_t* input) {
  return static_cast<std::uint32_t>(input[0]) |
         (static_cast<std::uint32_t>(input[1]) << 8) |
         (static_cast<std::uint32_t>(input[2]) << 16) |
         (static_cast<std::uint32_t>(input[3]) << 24);
}
inline float read_float(const std::uint8_t* input) {
  const auto bits = read_u32(input);
  float value = 0;
  std::memcpy(&value, &bits, sizeof(value));
  return value;
}
inline std::array<std::uint8_t, 24> session_header() {
  std::array<std::uint8_t, 24> result{'G', 'U', 'L', 'A', 'U', 'D', '1', 0};
  write_u32(result.data() + 8, sample_rate);
  write_u32(result.data() + 12, channels);
  write_u32(result.data() + 16, 1);
  write_u32(result.data() + 20, max_frames);
  return result;
}
struct Frame {
  std::array<std::uint8_t, frame_capacity> data{};
  std::size_t size = 0;
};
inline bool encode_frame(std::uint32_t sequence, std::uint32_t frames,
                         std::uint32_t flags, const float* samples, Frame& result) {
  if (frames == 0 || frames > max_frames || (flags & ~1U) != 0) return false;
  result.size = 16 + frames * channels * sizeof(float);
  write_u32(result.data.data(), frame_magic);
  write_u32(result.data.data() + 4, sequence);
  write_u32(result.data.data() + 8, frames);
  write_u32(result.data.data() + 12, flags);
  for (std::uint32_t i = 0; i < frames * channels; ++i) {
    const float input = samples ? samples[i] : 0.0F;
    const float value = std::isfinite(input) ? std::clamp(input, -1.0F, 1.0F) : 0.0F;
    std::uint32_t bits = 0;
    std::memcpy(&bits, &value, sizeof(bits));
    write_u32(result.data.data() + 16 + i * sizeof(float), bits);
  }
  return true;
}
}  // namespace gul_audio
