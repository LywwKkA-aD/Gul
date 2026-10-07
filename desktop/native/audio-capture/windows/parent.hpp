#pragma once

#include "protocol.hpp"
#include <windows.h>
#include <tlhelp32.h>
#include <vector>

namespace gul_audio {
class Handle {
 public:
  explicit Handle(HANDLE value = nullptr) : value_(value == INVALID_HANDLE_VALUE ? nullptr : value) {}
  ~Handle() { if (value_) CloseHandle(value_); }
  Handle(const Handle&) = delete;
  Handle& operator=(const Handle&) = delete;
  HANDLE get() const { return value_; }
 private:
  HANDLE value_;
};
inline std::uint64_t creation_time(HANDLE process) {
  FILETIME birth{}, exit{}, kernel{}, user{};
  if (!GetProcessTimes(process, &birth, &exit, &kernel, &user)) return 0;
  return (static_cast<std::uint64_t>(birth.dwHighDateTime) << 32) | birth.dwLowDateTime;
}
inline std::vector<BYTE> process_user(HANDLE process) {
  HANDLE raw = nullptr;
  if (!OpenProcessToken(process, TOKEN_QUERY, &raw)) return {};
  Handle token(raw);
  DWORD size = 0;
  GetTokenInformation(token.get(), TokenUser, nullptr, 0, &size);
  if (size == 0 || size > 16384) return {};
  std::vector<BYTE> result(size);
  if (!GetTokenInformation(token.get(), TokenUser, result.data(), size, &size)) return {};
  if (!IsValidSid(reinterpret_cast<TOKEN_USER*>(result.data())->User.Sid)) return {};
  return result;
}
inline DWORD actual_parent() {
  Handle snapshot(CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0));
  if (!snapshot.get()) return 0;
  PROCESSENTRY32W entry{};
  entry.dwSize = static_cast<DWORD>(sizeof(entry));
  if (!Process32FirstW(snapshot.get(), &entry)) return 0;
  do {
    if (entry.th32ProcessID == GetCurrentProcessId()) return entry.th32ParentProcessID;
  } while (Process32NextW(snapshot.get(), &entry));
  return 0;
}
class Parent {
 public:
  Parent() : id_(actual_parent()), handle_(OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE, FALSE, id_)) {}
  bool valid() const {
    if (!handle_.get()) return false;
    const auto own_user = process_user(GetCurrentProcess());
    const auto parent_user = process_user(handle_.get());
    if (own_user.empty() || parent_user.empty()) return false;
    const auto* own = reinterpret_cast<const TOKEN_USER*>(own_user.data());
    const auto* parent = reinterpret_cast<const TOKEN_USER*>(parent_user.data());
    DWORD own_session = 0, parent_session = 0;
    const bool same_session = ProcessIdToSessionId(GetCurrentProcessId(), &own_session) &&
                             ProcessIdToSessionId(id_, &parent_session) && own_session == parent_session;
    return parent_allowed(id_, GetCurrentProcessId(), creation_time(handle_.get()),
                          creation_time(GetCurrentProcess()), EqualSid(own->User.Sid, parent->User.Sid) != FALSE,
                          same_session, WaitForSingleObject(handle_.get(), 0) == WAIT_TIMEOUT);
  }
  DWORD id() const { return id_; }
  HANDLE handle() const { return handle_.get(); }
 private:
  DWORD id_;
  Handle handle_;
};
}  // namespace gul_audio
