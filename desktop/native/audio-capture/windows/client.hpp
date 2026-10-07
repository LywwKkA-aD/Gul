#pragma once

#include "parent.hpp"
#include <audioclient.h>
#include <audioclientactivationparams.h>
#include <mmdeviceapi.h>
#include <mmreg.h>
#include <wrl.h>
#include <wrl/implements.h>

namespace gul_audio {
class Activation final : public Microsoft::WRL::RuntimeClass<
    Microsoft::WRL::RuntimeClassFlags<Microsoft::WRL::ClassicCom>,
    Microsoft::WRL::FtmBase, IActivateAudioInterfaceCompletionHandler> {
 public:
  Activation() : ready_(CreateEventW(nullptr, TRUE, FALSE, nullptr)) {}
  STDMETHOD(ActivateCompleted)(IActivateAudioInterfaceAsyncOperation* operation) override {
    Microsoft::WRL::ComPtr<IUnknown> unknown;
    HRESULT activation = E_FAIL;
    result_ = operation->GetActivateResult(&activation, &unknown);
    if (SUCCEEDED(result_)) result_ = activation;
    if (SUCCEEDED(result_)) result_ = unknown.As(&client_);
    if (ready_.get()) SetEvent(ready_.get());
    return S_OK;
  }
  HRESULT wait(const Parent& parent, Microsoft::WRL::ComPtr<IAudioClient>& client) {
    if (!ready_.get()) return E_FAIL;
    const HANDLE handles[]{ready_.get(), parent.handle()};
    if (WaitForMultipleObjects(2, handles, FALSE, 5000) != WAIT_OBJECT_0 || !parent.valid()) return E_FAIL;
    if (SUCCEEDED(result_)) client = client_;
    return result_;
  }
 private:
  Handle ready_;
  HRESULT result_ = E_FAIL;
  Microsoft::WRL::ComPtr<IAudioClient> client_;
};
class Client {
 public:
  Client() : samples_(CreateEventW(nullptr, FALSE, FALSE, nullptr)) {}
  bool initialize(const Parent& parent) {
    if (!samples_.get() || !parent.valid()) return false;
    AUDIOCLIENT_ACTIVATION_PARAMS parameters{};
    parameters.ActivationType = AUDIOCLIENT_ACTIVATION_TYPE_PROCESS_LOOPBACK;
    parameters.ProcessLoopbackParams.TargetProcessId = parent.id();
    parameters.ProcessLoopbackParams.ProcessLoopbackMode = PROCESS_LOOPBACK_MODE_EXCLUDE_TARGET_PROCESS_TREE;
    PROPVARIANT property{};
    property.vt = VT_BLOB;
    property.blob.cbSize = static_cast<ULONG>(sizeof(parameters));
    property.blob.pBlobData = reinterpret_cast<BYTE*>(&parameters);
    const auto handler = Microsoft::WRL::Make<Activation>();
    if (!handler) return false;
    Microsoft::WRL::ComPtr<IActivateAudioInterfaceAsyncOperation> operation;
    if (FAILED(ActivateAudioInterfaceAsync(VIRTUAL_AUDIO_DEVICE_PROCESS_LOOPBACK,
        __uuidof(IAudioClient), &property, handler.Get(), &operation))) return false;
    if (FAILED(handler->wait(parent, audio_)) || !audio_) return false;
    WAVEFORMATEX format{};
    format.wFormatTag = WAVE_FORMAT_IEEE_FLOAT;
    format.nChannels = static_cast<WORD>(channels);
    format.nSamplesPerSec = sample_rate;
    format.wBitsPerSample = 32;
    format.nBlockAlign = static_cast<WORD>(channels * sizeof(float));
    format.nAvgBytesPerSec = sample_rate * format.nBlockAlign;
    const DWORD flags = AUDCLNT_STREAMFLAGS_LOOPBACK | AUDCLNT_STREAMFLAGS_EVENTCALLBACK |
                        AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY;
    if (FAILED(audio_->Initialize(AUDCLNT_SHAREMODE_SHARED, flags, 0, 0, &format, nullptr))) return false;
    if (FAILED(audio_->SetEventHandle(samples_.get()))) return false;
    return SUCCEEDED(audio_->GetService(__uuidof(IAudioCaptureClient), &capture_));
  }
  bool start() { return audio_ && SUCCEEDED(audio_->Start()); }
  void stop() { if (audio_) audio_->Stop(); }
  HANDLE samples() const { return samples_.get(); }
  IAudioCaptureClient* capture() const { return capture_.Get(); }
 private:
  Handle samples_;
  Microsoft::WRL::ComPtr<IAudioClient> audio_;
  Microsoft::WRL::ComPtr<IAudioCaptureClient> capture_;
};
}  // namespace gul_audio
