#include "process-policy.hpp"
#include "audio-ring.hpp"
#include "input-revisions.hpp"
#include <cassert>
#include <cstring>
#include <map>

int main() {
  using namespace gul_audio;
  const Identity root{100, 1, 1000, 10};
  std::map<unsigned, Identity> graph{{100, root}, {101, {101, 100, 1000, 11}},
    {102, {102, 101, 1000, 12}}, {200, {200, 1, 1000, 20}}, {1, {1, 0, 0, 1}}};
  auto read = [&](unsigned pid) -> std::optional<Identity> {
    auto found = graph.find(pid);
    return found == graph.end() ? std::nullopt : std::optional<Identity>(found->second);
  };
  assert(classify(root, 100, read) == Ownership::Own);
  assert(classify(root, 102, read) == Ownership::Own);
  assert(classify(root, 200, read) == Ownership::Foreign);
  assert(classify(root, 999, read) == Ownership::Unknown);
  assert(classify(root, 1, read) == Ownership::Unknown);
  graph[200] = {200, 1, 1001, 20};
  assert(classify(root, 200, read) == Ownership::Unknown);
  graph[200] = {200, 200, 1000, 20};
  assert(classify(root, 200, read) == Ownership::Unknown);
  graph[100] = {100, 1, 1000, 99};
  assert(classify(root, 102, read) == Ownership::Unknown);
  assert(!sameProcess(root, graph[100]));
  assert(!parsePID("100tail"));
  assert(!parsePID("0"));
  assert(parsePID("100") == 100);

  InputRevisions revisions(2);
  const auto firstRevision = revisions.initial(3);
  assert(revisions.current(3, firstRevision));
  revisions.remove(3);
  assert(!revisions.current(3, firstRevision));
  assert(revisions.initial(3) == 0); // An old initial-list response cannot revive a removed index.
  const auto reused = revisions.changed(3);
  assert(reused != firstRevision && revisions.current(3, reused));
  assert(!revisions.current(3, firstRevision));
  const auto changed = revisions.changed(3);
  assert(!revisions.current(3, reused) && revisions.current(3, changed));
  revisions.completeInitial();
  revisions.remove(3);
  assert(!revisions.current(3, changed));
  assert(revisions.changed(4));
  assert(revisions.changed(5));
  assert(revisions.changed(6) == 0); // Bounded metadata fails closed rather than evicting a live approval.

  AudioRing ring(16);
  const unsigned char first[]{1,2,3,4,5,6,7,8};
  ring.push(first, 8);
  unsigned char output[24]{};
  ring.pull(output, 4);
  assert(std::memcmp(output, first, 4) == 0);
  ring.push(first, 8);
  ring.pull(output, 16);
  const unsigned char expected[]{5,6,7,8,1,2,3,4,5,6,7,8,0,0,0,0};
  assert(std::memcmp(output, expected, 16) == 0);
  unsigned char overflow[24];
  for (unsigned i = 0; i < 24; ++i) overflow[i] = i;
  ring.push(overflow, 24);
  ring.pull(output, 16);
  assert(std::memcmp(output, overflow + 8, 16) == 0);
  ring.push(nullptr, 8);
  ring.pull(output, 8);
  for (unsigned i = 0; i < 8; ++i) assert(output[i] == 0);
}
