#pragma once

#include "client.hpp"
#include "quality.hpp"
#include <atomic>
#include <cmath>
#include <thread>
#include <vector>

namespace gul_audio_test {
class Tone {
 public:
  bool initialize(double left, double right, double gain) {
    left_ = left; right_ = right; gain_ = gain;
    Microsoft::WRL::ComPtr<IMMDeviceEnumerator> devices;
    Microsoft::WRL::ComPtr<IMMDevice> device;
    if (FAILED(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL,
        __uuidof(IMMDeviceEnumerator), &devices))) return false;
    if (FAILED(devices->GetDefaultAudioEndpoint(eRender, eConsole, &device))) return false;
    if (FAILED(device->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr, &audio_))) return false;
    WAVEFORMATEX format{};
    format.wFormatTag = WAVE_FORMAT_IEEE_FLOAT; format.nChannels = 2;
    format.nSamplesPerSec = 48000; format.wBitsPerSample = 32;
    format.nBlockAlign = 8; format.nAvgBytesPerSec = 48000 * 8;
    if (FAILED(audio_->Initialize(AUDCLNT_SHAREMODE_SHARED,
        AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY,
        1000000, 0, &format, nullptr))) return false;
    if (FAILED(audio_->GetBufferSize(&frames_))) return false;
    return SUCCEEDED(audio_->GetService(__uuidof(IAudioRenderClient), &render_));
  }
  bool start() {
    if (FAILED(audio_->Start())) return false;
    running_.store(true);
    thread_ = std::thread([this] {
      CoInitializeEx(nullptr, COINIT_MULTITHREADED);
      std::uint64_t sample = 0;
      while (running_.load()) {
        UINT32 padding = 0;
        if (FAILED(audio_->GetCurrentPadding(&padding)) || padding > frames_) break;
        const UINT32 count = frames_ - padding;
        if (count == 0) { Sleep(2); continue; }
        BYTE* bytes = nullptr;
        if (FAILED(render_->GetBuffer(count, &bytes))) break;
        auto* pcm = reinterpret_cast<float*>(bytes);
        for (UINT32 i = 0; i < count; ++i, ++sample) {
          pcm[i * 2] = static_cast<float>(gain_ * std::sin(2 * pi * left_ * static_cast<double>(sample) / 48000));
          pcm[i * 2 + 1] = static_cast<float>(gain_ * std::sin(2 * pi * right_ * static_cast<double>(sample) / 48000));
        }
        if (FAILED(render_->ReleaseBuffer(count, 0))) break;
        Sleep(2);
      }
      running_.store(false); CoUninitialize();
    });
    return true;
  }
  ~Tone() {
    running_.store(false);
    if (thread_.joinable()) thread_.join();
    if (audio_) audio_->Stop();
  }
 private:
  Microsoft::WRL::ComPtr<IAudioClient> audio_;
  Microsoft::WRL::ComPtr<IAudioRenderClient> render_;
  std::atomic<bool> running_{false};
  std::thread thread_;
  UINT32 frames_ = 0;
  double left_ = 0, right_ = 0, gain_ = 0;
};
class EndpointReference {
 public:
  bool initialize() {
    Microsoft::WRL::ComPtr<IMMDeviceEnumerator> devices;
    Microsoft::WRL::ComPtr<IMMDevice> endpoint;
    if (FAILED(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL,
        __uuidof(IMMDeviceEnumerator), &devices)) ||
        FAILED(devices->GetDefaultAudioEndpoint(eRender, eConsole, &endpoint)) ||
        FAILED(endpoint->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr, &audio_))) return false;
    WAVEFORMATEX format{};
    format.wFormatTag = WAVE_FORMAT_IEEE_FLOAT; format.nChannels = 2;
    format.nSamplesPerSec = 48000; format.wBitsPerSample = 32;
    format.nBlockAlign = 8; format.nAvgBytesPerSec = 48000 * 8;
    if (FAILED(audio_->Initialize(AUDCLNT_SHAREMODE_SHARED,
        AUDCLNT_STREAMFLAGS_LOOPBACK | AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM |
        AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY, 1000000, 0, &format, nullptr))) return false;
    return SUCCEEDED(audio_->GetService(__uuidof(IAudioCaptureClient), &capture_));
  }
  bool start() {
    if (FAILED(audio_->Start())) return false;
    running_.store(true);
    thread_ = std::thread([this] {
      CoInitializeEx(nullptr, COINIT_MULTITHREADED);
      while (running_.load()) {
        UINT32 frames = 0;
        if (FAILED(capture_->GetNextPacketSize(&frames))) break;
        if (frames == 0) { Sleep(2); continue; }
        BYTE* bytes = nullptr; DWORD flags = 0;
        if (FAILED(capture_->GetBuffer(&bytes, &frames, &flags, nullptr, nullptr))) break;
        const float* samples = reinterpret_cast<float*>(bytes);
        if (pcm_.size() + frames * 2 > 48000 * 2 * 12) { capture_->ReleaseBuffer(frames); break; }
        for (UINT32 i = 0; i < frames * 2; ++i)
          pcm_.push_back((flags & AUDCLNT_BUFFERFLAGS_SILENT) ? 0.0F : samples[i]);
        if (FAILED(capture_->ReleaseBuffer(frames))) break;
      }
      CoUninitialize();
    });
    return true;
  }
  void stop() {
    running_.store(false);
    if (thread_.joinable()) thread_.join();
    if (audio_) audio_->Stop();
  }
  ~EndpointReference() { stop(); }
  const std::vector<float>& pcm() const { return pcm_; }
 private:
  Microsoft::WRL::ComPtr<IAudioClient> audio_;
  Microsoft::WRL::ComPtr<IAudioCaptureClient> capture_;
  std::atomic<bool> running_{false};
  std::thread thread_;
  std::vector<float> pcm_;
};
}  // namespace gul_audio_test
