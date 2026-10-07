#pragma once

#include "parent.hpp"
#include <atomic>
#include <condition_variable>
#include <deque>
#include <mutex>
#include <thread>

namespace gul_audio {
inline bool write_all(HANDLE output, const std::uint8_t* bytes, std::size_t size) {
  while (size > 0) {
    DWORD written = 0;
    if (!WriteFile(output, bytes, static_cast<DWORD>(size), &written, nullptr) || written == 0) return false;
    bytes += written;
    size -= written;
  }
  return true;
}
class Writer {
 public:
  explicit Writer(std::atomic<bool>& running) : running_(running), worker_([this] { run(); }) {}
  ~Writer() {
    running_.store(false);
    changed_.notify_all();
    CancelSynchronousIo(worker_.native_handle());
    worker_.join();
  }
  void push(Frame frame) {
    std::lock_guard<std::mutex> lock(mutex_);
    enqueue_frame(queue_, std::move(frame));
    changed_.notify_one();
  }
 private:
  void run() {
    const HANDLE output = GetStdHandle(STD_OUTPUT_HANDLE);
    const auto header = session_header();
    if (!write_all(output, header.data(), header.size())) { running_.store(false); return; }
    while (running_.load()) {
      Frame frame{};
      {
        std::unique_lock<std::mutex> lock(mutex_);
        changed_.wait(lock, [this] { return !running_.load() || !queue_.empty(); });
        if (!running_.load()) break;
        frame = std::move(queue_.front());
        queue_.pop_front();
      }
      if (!write_all(output, frame.data.data(), frame.size)) { running_.store(false); break; }
    }
  }
  std::atomic<bool>& running_;
  std::mutex mutex_;
  std::condition_variable changed_;
  std::deque<Frame> queue_;
  std::thread worker_;
};
class Heartbeat {
 public:
  explicit Heartbeat(std::atomic<bool>& running) : running_(running), last_(GetTickCount64()), worker_([this] { read(); }) {}
  ~Heartbeat() {
    running_.store(false);
    CancelSynchronousIo(worker_.native_handle());
    worker_.join();
  }
  bool fresh() const { return GetTickCount64() - last_.load() < 3000; }
 private:
  void read() {
    const HANDLE input = GetStdHandle(STD_INPUT_HANDLE);
    char line[5]{};
    unsigned offset = 0;
    while (running_.load()) {
      DWORD received = 0;
      char value = 0;
      if (!ReadFile(input, &value, 1, &received, nullptr) || received != 1) break;
      if (offset >= sizeof(line)) break;
      line[offset++] = value;
      if (value != '\n') continue;
      if (offset != 5 || std::memcmp(line, "PING\n", 5) != 0) break;
      last_.store(GetTickCount64());
      offset = 0;
    }
    running_.store(false);
  }
  std::atomic<bool>& running_;
  std::atomic<ULONGLONG> last_;
  std::thread worker_;
};
}  // namespace gul_audio
