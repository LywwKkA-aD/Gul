#include "client.hpp"
#include "writer.hpp"
#include <algorithm>
#include <cstring>

namespace {
int fail(const char* stage) {
  DWORD written = 0;
  WriteFile(GetStdHandle(STD_ERROR_HANDLE), stage, static_cast<DWORD>(std::strlen(stage)), &written, nullptr);
  return 1;
}
bool consume(gul_audio::Client& client, gul_audio::Writer& writer, std::uint32_t& sequence) {
  UINT32 available = 0;
  if (FAILED(client.capture()->GetNextPacketSize(&available))) return false;
  while (available > 0) {
    BYTE* data = nullptr;
    UINT32 frames = 0;
    DWORD flags = 0;
    if (FAILED(client.capture()->GetBuffer(&data, &frames, &flags, nullptr, nullptr))) return false;
    const float* samples = (flags & AUDCLNT_BUFFERFLAGS_SILENT) ? nullptr : reinterpret_cast<float*>(data);
    const bool valid = gul_audio::packetize(sequence, frames,
        (flags & AUDCLNT_BUFFERFLAGS_DATA_DISCONTINUITY) != 0, samples,
        [&](gul_audio::Frame packet) { writer.push(std::move(packet)); });
    if (FAILED(client.capture()->ReleaseBuffer(frames))) return false;
    if (!valid) return false;
    if (FAILED(client.capture()->GetNextPacketSize(&available))) return false;
  }
  return true;
}
int execute(bool probe) {
  gul_audio::Parent parent;
  if (!parent.valid()) return fail("GUL_AUDIO_PARENT\n");
  gul_audio::Client client;
  if (!client.initialize(parent)) return fail("GUL_AUDIO_UNSUPPORTED\n");
  if (probe) {
    constexpr std::uint8_t message[]{'S', 'U', 'P', 'P', 'O', 'R', 'T', 'E', 'D', '\n'};
    return gul_audio::write_all(GetStdHandle(STD_OUTPUT_HANDLE), message, sizeof(message)) ? 0 : 1;
  }
  if (!client.start()) return fail("GUL_AUDIO_START\n");
  std::atomic<bool> running{true};
  gul_audio::Heartbeat heartbeat(running);
  gul_audio::Writer writer(running);
  std::uint32_t sequence = 0;
  bool success = true;
  const HANDLE handles[]{client.samples(), parent.handle()};
  while (running.load() && heartbeat.fresh()) {
    const DWORD ready = WaitForMultipleObjects(2, handles, FALSE, 100);
    if (ready == WAIT_OBJECT_0 + 1) break;
    if (ready == WAIT_TIMEOUT) continue;
    if (ready != WAIT_OBJECT_0 || !consume(client, writer, sequence)) { success = false; break; }
  }
  running.store(false);
  client.stop();
  return success ? 0 : fail("GUL_AUDIO_CAPTURE\n");
}
}  // namespace

int main(int argc, char** argv) {
  if (argc != 2 || (std::strcmp(argv[1], "--probe") != 0 && std::strcmp(argv[1], "--capture") != 0))
    return fail("GUL_AUDIO_ARGUMENTS\n");
  if (FAILED(CoInitializeEx(nullptr, COINIT_MULTITHREADED))) return fail("GUL_AUDIO_COM\n");
  const int result = execute(std::strcmp(argv[1], "--probe") == 0);
  CoUninitialize();
  return result;
}
