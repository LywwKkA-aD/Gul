#include "protocol.hpp"

#include <cassert>
#include <cmath>
#include <limits>

int main() {
  using namespace gul_audio;
  assert(parent_allowed(123, 456, 10, 20, true, true, true));
  assert(!parent_allowed(0, 456, 10, 20, true, true, true));
  assert(!parent_allowed(4, 456, 10, 20, true, true, true));
  assert(!parent_allowed(456, 456, 10, 20, true, true, true));
  assert(!parent_allowed(123, 456, 21, 20, true, true, true));
  assert(!parent_allowed(123, 456, 10, 20, false, true, true));
  assert(!parent_allowed(123, 456, 10, 20, true, false, true));
  assert(!parent_allowed(123, 456, 10, 20, true, true, false));

  const auto preamble = session_header();
  assert(preamble.size() == 24);
  assert(preamble[0] == 'G' && preamble[6] == '1' && preamble[7] == 0);
  assert(read_u32(preamble.data() + 8) == 48000);
  assert(read_u32(preamble.data() + 12) == 2);
  assert(read_u32(preamble.data() + 16) == 1);
  assert(read_u32(preamble.data() + 20) == 480);

  std::array<float, 960> samples{};
  samples[0] = 0.5F;
  samples[1] = -0.25F;
  samples[2] = 1.2F;
  samples[3] = -1.2F;
  samples[4] = std::numeric_limits<float>::quiet_NaN();
  samples[5] = std::numeric_limits<float>::infinity();
  Frame frame{};
  assert(encode_frame(7, 480, 1, samples.data(), frame));
  assert(frame.size == 3856);
  assert(read_u32(frame.data.data()) == frame_magic);
  assert(read_u32(frame.data.data() + 4) == 7);
  assert(read_u32(frame.data.data() + 8) == 480);
  assert(read_u32(frame.data.data() + 12) == 1);
  assert(read_float(frame.data.data() + 16) == 0.5F);
  assert(read_float(frame.data.data() + 20) == -0.25F);
  assert(read_float(frame.data.data() + 24) == 1.0F);
  assert(read_float(frame.data.data() + 28) == -1.0F);
  assert(read_float(frame.data.data() + 32) == 0.0F);
  assert(read_float(frame.data.data() + 36) == 0.0F);
  assert(!encode_frame(0, 0, 0, samples.data(), frame));
  assert(!encode_frame(0, 481, 0, samples.data(), frame));
  assert(!encode_frame(0, 1, 2, samples.data(), frame));
  assert(encode_frame(0, 1, 0, nullptr, frame));
  assert(read_float(frame.data.data() + 16) == 0.0F);
  assert(read_float(frame.data.data() + 20) == 0.0F);
}
