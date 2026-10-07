#pragma once
#include <charconv>
#include <cstdint>
#include <optional>
#include <set>
#include <string_view>

namespace gul_audio {
struct Identity {
  unsigned pid;
  unsigned parent;
  unsigned uid;
  std::uint64_t started;
};
enum class Ownership { Own, Foreign, Unknown };
inline bool sameProcess(const Identity& a, const Identity& b) {
  return a.pid == b.pid && a.uid == b.uid && a.started == b.started;
}
inline std::optional<unsigned> parsePID(std::string_view value) {
  unsigned pid = 0;
  const auto result = std::from_chars(value.data(), value.data() + value.size(), pid);
  if (result.ec != std::errc{} || result.ptr != value.data() + value.size() || !pid)
    return std::nullopt;
  return pid;
}
/** Unknown ownership is excluded. Names never establish permission to capture. */
template <typename Reader>
Ownership classify(const Identity& root, unsigned pid, Reader read) {
  std::set<unsigned> visited;
  for (unsigned depth = 0; depth < 64; ++depth) {
    if (!visited.insert(pid).second) return Ownership::Unknown;
    const auto process = read(pid);
    if (!process) return Ownership::Unknown;
    if (depth == 0 && process->uid != root.uid) return Ownership::Unknown;
    if (pid == root.pid)
      return sameProcess(root, *process) ? Ownership::Own : Ownership::Unknown;
    // A foreign UID is never captured; PID1 is the sole allowed ancestry terminus.
    if (pid == 1) return Ownership::Foreign;
    if (process->uid != root.uid) return Ownership::Unknown;
    if (!process->parent) return Ownership::Foreign;
    pid = process->parent;
  }
  return Ownership::Unknown;
}
}  // namespace gul_audio
