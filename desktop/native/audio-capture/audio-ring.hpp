#pragma once
#include <algorithm>
#include <cstddef>
#include <cstring>
#include <vector>

namespace gul_audio {
/** Stereo s16 frames; bounded buffering drops the oldest audio instead of accumulating delay. */
class AudioRing {
 public:
  explicit AudioRing(std::size_t capacity) : bytes_(capacity - capacity % 4) {}
  std::size_t available() const { return used_; }
  void push(const void* input, std::size_t size) {
    size -= size % 4;
    if (bytes_.empty() || !size) return;
    const auto* data = static_cast<const unsigned char*>(input);
    if (size > bytes_.size()) {
      if (data) data += size - bytes_.size();
      size = bytes_.size();
    }
    const auto dropped = std::max(used_ + size, bytes_.size()) - bytes_.size();
    read_ = (read_ + dropped) % bytes_.size();
    used_ -= std::min(used_, dropped);
    for (std::size_t i = 0; i < size; ++i)
      bytes_[(read_ + used_ + i) % bytes_.size()] = data ? data[i] : 0;
    used_ += size;
  }
  void pull(void* output, std::size_t size) {
    auto* data = static_cast<unsigned char*>(output);
    std::memset(data, 0, size);
    const auto count = std::min(size - size % 4, used_);
    for (std::size_t i = 0; i < count; ++i) data[i] = bytes_[(read_ + i) % bytes_.size()];
    if (!bytes_.empty()) read_ = (read_ + count) % bytes_.size();
    used_ -= count;
  }
 private:
  std::vector<unsigned char> bytes_;
  std::size_t read_ = 0;
  std::size_t used_ = 0;
};
}  // namespace gul_audio
