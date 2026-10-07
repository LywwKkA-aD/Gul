#include "tone-test.hpp"
#include "writer.hpp"
#include <cstdio>
#include <string>
#include <memory>

namespace {
bool read_exact(HANDLE pipe, std::uint8_t* target, DWORD size) {
  while (size > 0) {
    DWORD count = 0;
    if (!ReadFile(pipe, target, size, &count, nullptr) || count == 0) return false;
    target += count; size -= count;
  }
  return true;
}
std::wstring quote(const std::wstring& value) {
  // Paths here are derived from the build directory, never from the application renderer.
  if (value.find_first_of(L"\"\r\n") != std::wstring::npos) return {};
  return L"\"" + value + L"\"";
}
bool launch(const std::wstring& file, const std::wstring& arguments,
            HANDLE input, HANDLE output, PROCESS_INFORMATION& process) {
  auto command = quote(file) + L" " + arguments;
  if (quote(file).empty()) return false;
  STARTUPINFOW startup{};
  startup.cb = static_cast<DWORD>(sizeof(startup)); startup.dwFlags = STARTF_USESTDHANDLES;
  startup.hStdInput = input; startup.hStdOutput = output; startup.hStdError = GetStdHandle(STD_ERROR_HANDLE);
  return CreateProcessW(file.c_str(), command.data(), nullptr, nullptr, TRUE,
      CREATE_NO_WINDOW, nullptr, nullptr, &startup, &process) != FALSE;
}
int owner(const std::wstring& helper) {
  gul_audio_test::Tone voice;
  if (!voice.initialize(880, 880, 0.16) || !voice.start()) return 1;
  PROCESS_INFORMATION child{};
  if (!launch(helper, L"--capture", GetStdHandle(STD_INPUT_HANDLE), GetStdHandle(STD_OUTPUT_HANDLE), child)) return 1;
  gul_audio::Handle process(child.hProcess), thread(child.hThread);
  if (WaitForSingleObject(process.get(), 15000) != WAIT_OBJECT_0) { TerminateProcess(process.get(), 1); return 1; }
  DWORD code = 1;
  GetExitCodeProcess(process.get(), &code);
  return static_cast<int>(code);
}
bool endpoint_available() {
  Microsoft::WRL::ComPtr<IMMDeviceEnumerator> devices;
  Microsoft::WRL::ComPtr<IMMDevice> endpoint;
  return SUCCEEDED(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL,
      __uuidof(IMMDeviceEnumerator), &devices)) &&
      SUCCEEDED(devices->GetDefaultAudioEndpoint(eRender, eConsole, &endpoint));
}
int integration(const std::wstring& helper) {
  if (!endpoint_available()) { std::puts("GUL_WINDOWS_AUDIO_ENDPOINT_UNAVAILABLE"); return 77; }
  gul_audio_test::Tone game;
  if (!game.initialize(440, 660, 0.12) || !game.start()) return 1;
  gul_audio_test::EndpointReference hardware;
  if (!hardware.initialize() || !hardware.start()) return 1;
  SECURITY_ATTRIBUTES security{static_cast<DWORD>(sizeof(SECURITY_ATTRIBUTES)), nullptr, TRUE};
  HANDLE read_output = nullptr, write_output = nullptr, read_input = nullptr, write_input = nullptr;
  if (!CreatePipe(&read_output, &write_output, &security, 65536)) return 1;
  gul_audio::Handle output(read_output);
  auto inherited_output = std::make_unique<gul_audio::Handle>(write_output);
  if (!CreatePipe(&read_input, &write_input, &security, 4096)) return 1;
  auto input = std::make_unique<gul_audio::Handle>(read_input);
  auto heartbeat_output = std::make_unique<gul_audio::Handle>(write_input);
  SetHandleInformation(output.get(), HANDLE_FLAG_INHERIT, 0);
  SetHandleInformation(heartbeat_output->get(), HANDLE_FLAG_INHERIT, 0);
  wchar_t executable[32768]{};
  if (GetModuleFileNameW(nullptr, executable, 32768) == 0) return 1;
  PROCESS_INFORMATION child{};
  if (!launch(executable, L"--owner " + quote(helper), input->get(), inherited_output->get(), child)) return 1;
  gul_audio::Handle process(child.hProcess), thread(child.hThread);
  input.reset(); inherited_output.reset();
  std::atomic<bool> alive{true};
  std::thread ping([&] {
    while (alive.load()) {
      constexpr std::uint8_t bytes[]{'P', 'I', 'N', 'G', '\n'};
      if (!gul_audio::write_all(heartbeat_output->get(), bytes, sizeof(bytes))) break;
      Sleep(200);
    }
  });
  // A watchdog bounds broken audio services, startup, pipe reads and cleanup on CI.
  std::thread deadline([&] {
    for (unsigned i = 0; i < 150 && alive.load(); ++i) Sleep(100);
    if (alive.load()) TerminateProcess(process.get(), 1);
  });
  bool valid = true;
  std::array<std::uint8_t, 24> preamble{};
  const auto expected = gul_audio::session_header();
  valid = read_exact(output.get(), preamble.data(), 24) && preamble == expected;
  std::vector<float> pcm;
  pcm.reserve(48000 * 2 * 5);
  const ULONGLONG start = GetTickCount64();
  while (valid && GetTickCount64() - start < 6000) {
    std::array<std::uint8_t, 16> header{};
    if (!read_exact(output.get(), header.data(), 16)) { valid = false; break; }
    const auto frames = gul_audio::read_u32(header.data() + 8);
    if (gul_audio::read_u32(header.data()) != gul_audio::frame_magic || frames == 0 || frames > 480) { valid = false; break; }
    std::array<std::uint8_t, 3840> samples{};
    if (!read_exact(output.get(), samples.data(), frames * 8)) { valid = false; break; }
    if (GetTickCount64() - start < 1000) continue;
    for (std::uint32_t i = 0; i < frames * 2; ++i) pcm.push_back(gul_audio::read_float(samples.data() + i * 4));
  }
  alive.store(false); ping.join(); deadline.join();
  // Closing the producer's stdin stops the real helper; owner then exits as well.
  heartbeat_output.reset();
  const DWORD stopped = WaitForSingleObject(process.get(), 5000);
  if (stopped != WAIT_OBJECT_0) TerminateProcess(process.get(), 1);
  hardware.stop();
  const auto proof = gul_audio_test::measure(pcm, hardware.pcm());
  std::printf("GUL_WINDOWS_AUDIO_FACTS left=%.5f right=%.5f exclusionDb=%.2f separationDb=%.2f audibleVoice=%.5f\n",
      proof.left, proof.right, proof.exclusion_db, proof.separation_db, proof.audible_voice);
  return valid && stopped == WAIT_OBJECT_0 && pcm.size() >= 48000 * 2 && proof.passes() ? 0 : 1;
}
}  // namespace

int wmain(int argc, wchar_t** argv) {
  if (FAILED(CoInitializeEx(nullptr, COINIT_MULTITHREADED))) return 1;
  int result = 1;
  if (argc == 3 && std::wstring(argv[1]) == L"--owner") result = owner(argv[2]);
  else if (argc == 2) result = integration(argv[1]);
  CoUninitialize();
  return result;
}
