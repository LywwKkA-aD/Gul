#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <array>
#include <atomic>
#include <cerrno>
#include <cwchar>
#include <thread>

namespace {
constexpr UINT kDown = WM_APP + 1;
constexpr UINT kUp = WM_APP + 2;
constexpr std::array<DWORD, 8> kModifierKeys = {
    VK_LCONTROL, VK_RCONTROL, VK_LMENU, VK_RMENU,
    VK_LSHIFT, VK_RSHIFT, VK_LWIN, VK_RWIN};
DWORD mainThread = 0;
std::atomic<DWORD> writerThread{0};
DWORD selectedKey = 0;
DWORD selectedModifiers = 0;
HANDLE stopping = nullptr;
HANDLE writerReady = nullptr;
std::array<bool, 8> modifiers{};
std::atomic<bool> transmitting{false};
bool keyDown = false;
bool armed = false;

void requestStop() {
  SetEvent(stopping);
  PostThreadMessageW(mainThread, WM_QUIT, 0, 0);
}

DWORD modifierMask() {
  DWORD mask = 0;
  for (size_t index = 0; index < modifiers.size(); ++index) {
    if (modifiers[index]) mask |= (1UL << (index / 2));
  }
  return mask;
}

void publish(bool down) {
  if (transmitting.exchange(down) == down) return;
  // No stdout I/O, locks, allocation or key logging occurs inside the hook.
  if (!PostThreadMessageW(writerThread, down ? kDown : kUp, 0, 0)) requestStop();
}

LRESULT CALLBACK keyboard(int code, WPARAM message, LPARAM data) {
  if (code == HC_ACTION) {
    const auto* event = reinterpret_cast<const KBDLLHOOKSTRUCT*>(data);
    const bool down = message == WM_KEYDOWN || message == WM_SYSKEYDOWN;
    const bool up = message == WM_KEYUP || message == WM_SYSKEYUP;
    if ((down || up) && (event->flags & LLKHF_INJECTED) == 0) {
      bool relevant = false;
      DWORD modifierKey = event->vkCode;
      if (modifierKey == VK_SHIFT) modifierKey = MapVirtualKeyW(event->scanCode, MAPVK_VSC_TO_VK_EX);
      else if (modifierKey == VK_CONTROL) modifierKey = (event->flags & LLKHF_EXTENDED) ? VK_RCONTROL : VK_LCONTROL;
      else if (modifierKey == VK_MENU) modifierKey = (event->flags & LLKHF_EXTENDED) ? VK_RMENU : VK_LMENU;
      for (size_t index = 0; index < kModifierKeys.size(); ++index) {
        if (modifierKey == kModifierKeys[index]) {
          modifiers[index] = down;
          relevant = true;
          break;
        }
      }
      if (event->vkCode == selectedKey) {
        keyDown = down;
        if (up) armed = true;
        relevant = true;
      }
      if (relevant) publish(armed && keyDown && modifierMask() == selectedModifiers);
    }
  }
  // The helper observes the chosen binding and never suppresses any key.
  return CallNextHookEx(nullptr, code, message, data);
}

bool physicalDown(DWORD key) {
  return (GetAsyncKeyState(static_cast<int>(key)) & 0x8000) != 0;
}

void pollRelease() {
  if (!transmitting.load()) return;
  DWORD mask = 0;
  for (size_t index = 0; index < kModifierKeys.size(); ++index) {
    if (physicalDown(kModifierKeys[index])) mask |= (1UL << (index / 2));
  }
  // Missing key-up, a secure desktop or a removed hook must close the gate.
  if (!physicalDown(selectedKey)) {
    keyDown = false;
    armed = true;
    publish(false);
  } else if (mask != selectedModifiers) publish(false);
}

void output() {
  writerThread = GetCurrentThreadId();
  MSG message{};
  PeekMessageW(&message, nullptr, WM_USER, WM_USER, PM_NOREMOVE);
  SetEvent(writerReady);
  while (GetMessageW(&message, nullptr, 0, 0) > 0) {
    if (message.message != kDown && message.message != kUp) continue;
    const char* bytes = message.message == kDown ? "down\n" : "up\n";
    const DWORD length = message.message == kDown ? 5UL : 3UL;
    DWORD written = 0;
    if (!WriteFile(GetStdHandle(STD_OUTPUT_HANDLE), bytes, length, &written, nullptr) || written != length) {
      requestStop();
      return;
    }
  }
}

void input() {
  std::array<char, 64> bytes{};
  DWORD count = 0;
  while (WaitForSingleObject(stopping, 30) == WAIT_TIMEOUT) {
    DWORD available = 0;
    if (!PeekNamedPipe(GetStdHandle(STD_INPUT_HANDLE), nullptr, 0, nullptr, &available, nullptr)) {
      requestStop();
      return;
    }
    if (available == 0) continue;
    const DWORD length = available < bytes.size() ? available : static_cast<DWORD>(bytes.size());
    if (!ReadFile(GetStdHandle(STD_INPUT_HANDLE), bytes.data(), length, &count, nullptr) || count == 0) {
      requestStop();
      return;
    }
    // No stdin payload is interpreted, stored or printed. EOF ends the hook.
  }
}

bool parse(const wchar_t* value, DWORD& result) {
  if (!value || !*value) return false;
  for (const wchar_t* current = value; *current; ++current) {
    if (*current < L'0' || *current > L'9') return false;
  }
  errno = 0;
  wchar_t* end = nullptr;
  const unsigned long parsed = std::wcstoul(value, &end, 10);
  if (errno == ERANGE || !end || *end) return false;
  result = parsed;
  return true;
}

bool validKey(DWORD key) {
  if ((key >= L'0' && key <= L'9') || (key >= L'A' && key <= L'Z') ||
      (key >= VK_F1 && key <= VK_F24) || (key >= VK_NUMPAD0 && key <= VK_NUMPAD9)) return true;
  switch (key) {
    case VK_BACK: case VK_TAB: case VK_RETURN: case VK_CAPITAL: case VK_ESCAPE:
    case VK_SPACE: case VK_PRIOR: case VK_NEXT: case VK_END: case VK_HOME:
    case VK_LEFT: case VK_UP: case VK_RIGHT: case VK_DOWN: case VK_INSERT: case VK_DELETE:
      return true;
    default: return false;
  }
}
}  // namespace

int wmain(int argc, wchar_t* argv[]) {
  DWORD parentPID = 0;
  if (argc != 4 || !parse(argv[1], selectedKey) || !parse(argv[2], selectedModifiers) ||
      !parse(argv[3], parentPID) || !validKey(selectedKey) || selectedModifiers > 15 ||
      parentPID == 0 || parentPID == GetCurrentProcessId() ||
      (selectedKey == VK_DELETE && (selectedModifiers & 3) == 3) ||
      (selectedKey == L'L' && (selectedModifiers & 8))) return 2;
  const HANDLE parent = OpenProcess(SYNCHRONIZE, FALSE, parentPID);
  if (!parent) return 3;
  stopping = CreateEventW(nullptr, TRUE, FALSE, nullptr);
  writerReady = CreateEventW(nullptr, TRUE, FALSE, nullptr);
  if (!stopping || !writerReady) {
    if (stopping) CloseHandle(stopping);
    if (writerReady) CloseHandle(writerReady);
    CloseHandle(parent);
    return 4;
  }
  mainThread = GetCurrentThreadId();
  MSG message{};
  PeekMessageW(&message, nullptr, WM_USER, WM_USER, PM_NOREMOVE);
  for (size_t index = 0; index < kModifierKeys.size(); ++index) modifiers[index] = physicalDown(kModifierKeys[index]);
  armed = !physicalDown(selectedKey);
  std::thread writer(output);
  const bool writerStarted = WaitForSingleObject(writerReady, 2000) == WAIT_OBJECT_0;
  const HHOOK hook = writerStarted ? SetWindowsHookExW(WH_KEYBOARD_LL, keyboard, GetModuleHandleW(nullptr), 0) : nullptr;
  const UINT_PTR timer = hook ? SetTimer(nullptr, 0, 30, nullptr) : 0;
  if (!hook || !timer) {
    if (hook) UnhookWindowsHookEx(hook);
    PostThreadMessageW(writerThread, WM_QUIT, 0, 0);
    writer.join();
    CloseHandle(writerReady); CloseHandle(stopping); CloseHandle(parent);
    return 5;
  }
  std::thread reader(input);
  std::thread watcher([parent] {
    const HANDLE handles[] = {stopping, parent};
    if (WaitForMultipleObjects(2, handles, FALSE, INFINITE) != WAIT_OBJECT_0) requestStop();
  });
  // An initial up is the ready handshake: the gate always starts closed.
  PostThreadMessageW(writerThread, kUp, 0, 0);
  while (GetMessageW(&message, nullptr, 0, 0) > 0) {
    if (message.message == WM_TIMER) pollRelease();
    TranslateMessage(&message);
    DispatchMessageW(&message);
  }
  SetEvent(stopping);
  UnhookWindowsHookEx(hook);
  KillTimer(nullptr, timer);
  PostThreadMessageW(writerThread, kUp, 0, 0);
  PostThreadMessageW(writerThread, WM_QUIT, 0, 0);
  reader.join(); watcher.join(); writer.join();
  CloseHandle(writerReady); CloseHandle(stopping); CloseHandle(parent);
  return 0;
}
