#include "protocol.hpp"

#include <cassert>
#include <cmath>
#include <limits>
#include <vector>

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

  std::vector<float> input(1001 * 2);
  for (std::size_t i = 0; i < input.size(); ++i) input[i] = static_cast<float>(i % 100) / 100;
  std::vector<Frame> packets;
  std::uint32_t sequence = 10;
  assert(packetize(sequence, 1001, true, input.data(), [&](Frame packet) { packets.push_back(packet); }));
  assert(packets.size() == 3 && sequence == 13);
  std::size_t sample = 0;
  for (std::size_t i = 0; i < packets.size(); ++i) {
    const auto* header = packets[i].data.data();
    assert(read_u32(header + 4) == 10 + i);
    assert(read_u32(header + 8) == (i == 2 ? 41U : 480U));
    assert(read_u32(header + 12) == (i == 0 ? 1U : 0U));
    for (std::size_t offset = 16; offset < packets[i].size; offset += 4)
      assert(read_float(header + offset) == input[sample++]);
  }
  assert(sample == input.size());
  assert(!packetize(sequence, 0, false, nullptr, [&](Frame) { assert(false); }));
  assert(!packetize(sequence, 96001, false, nullptr, [&](Frame) { assert(false); }));

  std::deque<Frame> queue;
  for (std::uint32_t index = 0; index < 13; ++index) {
    Frame next{};
    assert(encode_frame(index, 480, 0, samples.data(), next));
    enqueue_frame(queue, std::move(next));
  }
  assert(queue.size() == 12);
  for (std::size_t index = 0; index < queue.size(); ++index) {
    const auto* header = queue[index].data.data();
    assert(read_u32(header + 4) == index + 1);
    assert(read_u32(header + 12) == (index == 0 ? 1U : 0U));
  }
}
