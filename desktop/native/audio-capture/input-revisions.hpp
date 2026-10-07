#pragma once
#include <cstddef>
#include <cstdint>
#include <map>
namespace gul_audio {
/** Pending callbacks must match the latest lifecycle of the same sink-input index. */
class InputRevisions {
 public:
  explicit InputRevisions(std::size_t limit = 512) : limit_(limit) {}
  std::uint64_t changed(unsigned index) {
    if (!values_.count(index) && values_.size() >= limit_) return 0;
    return values_[index] = ++clock_;
  }
  std::uint64_t initial(unsigned index) { return values_.count(index) ? 0 : changed(index); }
  void remove(unsigned index) {
    if (!enumerating_) values_.erase(index);
    else if (values_.count(index) || values_.size() < limit_) values_[index] = 0;
  }
  bool current(unsigned index, std::uint64_t revision) const {
    const auto value = values_.find(index);
    return revision && value != values_.end() && value->second == revision;
  }
  void completeInitial() {
    enumerating_ = false;
    for (auto value = values_.begin(); value != values_.end();)
      if (!value->second) value = values_.erase(value); else ++value;
  }
 private:
  const std::size_t limit_;
  std::uint64_t clock_ = 0;
  bool enumerating_ = true;
  std::map<unsigned, std::uint64_t> values_;
};
}  // namespace gul_audio
