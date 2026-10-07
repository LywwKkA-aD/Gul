#include "process-policy.hpp"
#include "audio-ring.hpp"
#include "input-revisions.hpp"
#include <pulse/pulseaudio.h>
#include <pulse/mainloop-signal.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>
#include <csignal>
#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <map>
#include <memory>
#include <sstream>
#include <string>
#include <vector>

namespace {
using gul_audio::Identity;
std::optional<Identity> processIdentity(unsigned pid) {
  const auto path = "/proc/" + std::to_string(pid);
  struct stat attributes{};
  if (stat(path.c_str(), &attributes) != 0) return std::nullopt;
  std::ifstream file(path + "/stat");
  std::string line;
  if (!std::getline(file, line)) return std::nullopt;
  const auto end = line.rfind(')');
  if (end == std::string::npos) return std::nullopt;
  std::istringstream fields(line.substr(end + 2));
  std::string token;
  unsigned parent = 0;
  std::uint64_t started = 0;
  for (unsigned i = 0; i <= 19; ++i) {
    if (!(fields >> token)) return std::nullopt;
    if (i == 1) {
      const auto result = std::from_chars(token.data(), token.data() + token.size(), parent);
      if (result.ec != std::errc{} || result.ptr != token.data() + token.size()) return std::nullopt;
    } else if (i == 19) {
      const auto result = std::from_chars(token.data(), token.data() + token.size(), started);
      if (result.ec != std::errc{} || result.ptr != token.data() + token.size()) return std::nullopt;
    }
  }
  return Identity{pid, parent, static_cast<unsigned>(attributes.st_uid), started};
}
bool validNonce(const std::string& value) {
  if (value.size() != 32) return false;
  for (char c : value) if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'))) return false;
  return true;
}
bool argumentContains(const char* arguments, const std::string& key, const std::string& value) {
  if (!arguments) return false;
  std::istringstream words(arguments);
  std::string word;
  while (words >> word) if (word == key + "=" + value) return true;
  return false;
}
struct Pair {
  pa_stream* record = nullptr;
  pa_stream* playback = nullptr;
  unsigned sink = 0;
  Identity producer{};
  gul_audio::AudioRing ring{19200};
  void pump() {
    if (!playback || pa_stream_get_state(playback) != PA_STREAM_READY) return;
    const auto writable = pa_stream_writable_size(playback);
    if (writable == static_cast<std::size_t>(-1)) return;
    auto count = std::min(writable - writable % 4, ring.available());
    unsigned char buffer[3840];
    while (count >= 4) {
      const auto chunk = std::min(count, sizeof(buffer));
      ring.pull(buffer, chunk);
      if (pa_stream_write(playback, buffer, chunk, nullptr, 0, PA_SEEK_RELATIVE) < 0) break;
      count -= chunk;
    }
  }
  ~Pair() {
    for (auto* stream : {record, playback}) if (stream) {
      pa_stream_set_read_callback(stream, nullptr, nullptr);
      pa_stream_set_write_callback(stream, nullptr, nullptr);
      pa_stream_disconnect(stream);
      pa_stream_unref(stream);
    }
  }
};
class Capture {
 public:
  Capture(std::string nonce, bool cleanup) : name_("gul_share_" + nonce),
    label_("Gul-Screen-Audio-" + nonce), cleanup_(cleanup) {
    root_ = processIdentity(static_cast<unsigned>(getppid()));
    loop_ = pa_mainloop_new();
    api_ = pa_mainloop_get_api(loop_);
    pa_signal_init(api_);
    pa_signal_new(SIGTERM, [](pa_mainloop_api*, pa_signal_event*, int, void* data) {
      static_cast<Capture*>(data)->stop();
    }, this);
    pa_signal_new(SIGINT, [](pa_mainloop_api*, pa_signal_event*, int, void* data) {
      static_cast<Capture*>(data)->stop();
    }, this);
    auto* properties = pa_proplist_new();
    pa_proplist_sets(properties, PA_PROP_APPLICATION_NAME, "Gul screen audio");
    pa_proplist_sets(properties, PA_PROP_APPLICATION_PROCESS_ID, std::to_string(getpid()).c_str());
    context_ = pa_context_new_with_proplist(api_, "Gul screen audio", properties);
    pa_proplist_free(properties);
  }
  ~Capture() {
    pairs_.clear();
    pa_context_disconnect(context_);
    pa_context_unref(context_);
    pa_signal_done();
    pa_mainloop_free(loop_);
  }
  int run() {
    if (!cleanup_ && (!root_ || root_->uid != getuid())) return 1;
    const auto* server = std::getenv("PULSE_SERVER");
    if (!server || std::string(server).rfind("unix:/", 0) != 0) return 1;
    const std::string path = std::string(server).substr(5);
    if (path.find_first_of(" \t\r\n:\v\f") != std::string::npos) return 1;
    struct stat endpoint{};
    if (stat(path.c_str(), &endpoint) != 0 || !S_ISSOCK(endpoint.st_mode) || endpoint.st_uid != getuid()) return 1;
    pa_context_set_state_callback(context_, [](pa_context* context, void* data) {
      auto* self = static_cast<Capture*>(data);
      if (pa_context_get_state(context) == PA_CONTEXT_READY) self->connected();
      else if (pa_context_get_state(context) == PA_CONTEXT_FAILED ||
               pa_context_get_state(context) == PA_CONTEXT_TERMINATED) self->fail();
    }, this);
    if (pa_context_connect(context_, server, PA_CONTEXT_NOAUTOSPAWN, nullptr) < 0) return 1;
    if (!cleanup_) {
      input_ = api_->io_new(api_, STDIN_FILENO, PA_IO_EVENT_INPUT,
        [](pa_mainloop_api*, pa_io_event*, int fd, pa_io_event_flags_t, void* data) {
          auto* self = static_cast<Capture*>(data);
          char buffer[128];
          const auto count = read(fd, buffer, sizeof(buffer));
          if (count <= 0) self->stop();
          else self->lastPing_ = pa_rtclock_now();
        }, this);
      scheduleTick();
    }
    int result = 0;
    pa_mainloop_run(loop_, &result);
    return result;
  }
 private:
  std::string name_, label_;
  bool cleanup_ = false, stopping_ = false, ready_ = false;
  std::optional<Identity> root_;
  pa_mainloop* loop_ = nullptr;
  pa_mainloop_api* api_ = nullptr;
  pa_context* context_ = nullptr;
  pa_io_event* input_ = nullptr;
  pa_time_event* tick_ = nullptr;
  pa_usec_t lastPing_ = pa_rtclock_now();
  unsigned sinkModule_ = PA_INVALID_INDEX, sourceModule_ = PA_INVALID_INDEX;
  unsigned unloading_ = 0;
  std::map<unsigned, std::unique_ptr<Pair>> pairs_;
  gul_audio::InputRevisions revisions_;
  struct InputQuery { Capture* self; unsigned index; std::uint64_t revision; };
  struct Request { Capture* self; unsigned index; Identity producer; unsigned sink; std::uint64_t revision; std::string monitor; };

  bool rootAlive() const {
    if (!root_) return false;
    const auto current = processIdentity(root_->pid);
    return current && gul_audio::sameProcess(*root_, *current);
  }
  static void release(pa_operation* operation) { if (operation) pa_operation_unref(operation); }
  void fail() {
    if (!cleanup_) { std::puts("ERROR"); std::fflush(stdout); }
    pairs_.clear();
    stopping_ = true;
    pa_mainloop_quit(loop_, 1);
  }
  void scheduleTick() {
    timeval next{};
    pa_timeval_store(&next, pa_rtclock_now() + 1000000);
    tick_ = pa_context_rttime_new(context_, pa_rtclock_now() + 1000000,
      [](pa_mainloop_api* api, pa_time_event* event, const timeval*, void* data) {
        auto* self = static_cast<Capture*>(data);
        api->time_free(event);
        self->tick_ = nullptr;
        if (!self->rootAlive() || pa_rtclock_now() - self->lastPing_ > 3000000) self->stop();
        else if (!self->stopping_) self->scheduleTick();
      }, this);
  }
  void unload(unsigned index) {
    if (index == PA_INVALID_INDEX || pa_context_get_state(context_) != PA_CONTEXT_READY) return;
    ++unloading_;
    auto* operation = pa_context_unload_module(context_, index,
      [](pa_context*, int, void* data) {
        auto* self = static_cast<Capture*>(data);
        if (--self->unloading_ == 0 && self->stopping_) pa_mainloop_quit(self->loop_, 0);
      }, this);
    if (!operation) --unloading_;
    release(operation);
  }
  void stop() {
    if (stopping_) return;
    stopping_ = true;
    ready_ = false;
    if (input_) { api_->io_free(input_); input_ = nullptr; }
    if (tick_) { api_->time_free(tick_); tick_ = nullptr; }
    pairs_.clear();
    unload(sourceModule_);
    unload(sinkModule_);
    if (!unloading_) pa_mainloop_quit(loop_, 0);
  }
  void connected() {
    if (cleanup_) {
      release(pa_context_get_module_info_list(context_, [](pa_context*, const pa_module_info* info, int end, void* data) {
        auto* self = static_cast<Capture*>(data);
        if (info && ((std::string(info->name) == "module-null-sink" && argumentContains(info->argument, "sink_name", self->name_)) ||
                     (std::string(info->name) == "module-remap-source" && argumentContains(info->argument, "source_name", self->name_))))
          self->unload(info->index);
        if (end) { self->stopping_ = true; if (!self->unloading_) pa_mainloop_quit(self->loop_, 0); }
      }, this));
      return;
    }
    const auto arguments = "sink_name=" + name_ + " rate=48000 channels=2 channel_map=front-left,front-right sink_properties=\"device.description=" + label_ + " device.class=abstract priority.session=-1000 node.virtual=true\"";
    release(pa_context_load_module(context_, "module-null-sink", arguments.c_str(),
      [](pa_context*, unsigned index, void* data) {
        auto* self = static_cast<Capture*>(data);
        if (index == PA_INVALID_INDEX) { self->fail(); return; }
        self->sinkModule_ = index;
        if (self->stopping_) { self->unload(index); return; }
        self->createSource();
      }, this));
  }
  void createSource() {
    const auto arguments = "master=" + name_ + ".monitor source_name=" + name_ + " rate=48000 channels=2 channel_map=front-left,front-right source_properties=\"device.description=" + label_ + " device.class=abstract priority.session=-1000 node.virtual=true\"";
    release(pa_context_load_module(context_, "module-remap-source", arguments.c_str(),
      [](pa_context*, unsigned index, void* data) {
        auto* self = static_cast<Capture*>(data);
        if (index == PA_INVALID_INDEX) { self->fail(); return; }
        self->sourceModule_ = index;
        if (self->stopping_) { self->unload(index); return; }
        self->ready_ = true;
        std::puts("READY"); std::fflush(stdout);
        self->listen();
      }, this));
  }
  void listen() {
    pa_context_set_subscribe_callback(context_, [](pa_context*, pa_subscription_event_type_t event, unsigned index, void* data) {
      auto* self = static_cast<Capture*>(data);
      if ((event & PA_SUBSCRIPTION_EVENT_FACILITY_MASK) != PA_SUBSCRIPTION_EVENT_SINK_INPUT || self->stopping_) return;
      self->pairs_.erase(index);
      if ((event & PA_SUBSCRIPTION_EVENT_TYPE_MASK) == PA_SUBSCRIPTION_EVENT_REMOVE) self->revisions_.remove(index);
      else {
        const auto revision = self->revisions_.changed(index);
        if (!revision) return;
        auto* query = new InputQuery{self, index, revision};
        auto* operation = pa_context_get_sink_input_info(self->context_, index, queriedInput, query);
        if (!operation) delete query;
        release(operation);
      }
    }, this);
    release(pa_context_subscribe(context_, PA_SUBSCRIPTION_MASK_SINK_INPUT, nullptr, nullptr));
    release(pa_context_get_sink_input_info_list(context_, initialInput, this));
  }
  static void initialInput(pa_context*, const pa_sink_input_info* info, int end, void* data) {
    auto* self = static_cast<Capture*>(data);
    if (end) { self->revisions_.completeInitial(); return; }
    if (!info) return;
    const auto revision = self->revisions_.initial(info->index);
    if (revision) self->considerInput(info, revision);
  }
  static void queriedInput(pa_context*, const pa_sink_input_info* info, int end, void* data) {
    auto* query = static_cast<InputQuery*>(data);
    if (end) { delete query; return; }
    if (info && query->self->revisions_.current(query->index, query->revision))
      query->self->considerInput(info, query->revision);
  }
  void considerInput(const pa_sink_input_info* info, std::uint64_t revision) {
    auto* self = this;
    if (!info || self->stopping_ || !self->rootAlive()) return;
    const auto* value = pa_proplist_gets(info->proplist, PA_PROP_APPLICATION_PROCESS_ID);
    const auto pid = value ? gul_audio::parsePID(value) : std::nullopt;
    const auto producer = pid ? processIdentity(*pid) : std::nullopt;
    if (!producer || gul_audio::classify(*self->root_, *pid, processIdentity) != gul_audio::Ownership::Foreign) {
      self->pairs_.erase(info->index);
      return;
    }
    const auto previous = self->pairs_.find(info->index);
    if (previous != self->pairs_.end() && previous->second->sink == info->sink &&
        gul_audio::sameProcess(previous->second->producer, *producer)) return;
    self->pairs_.erase(info->index);
    if (self->pairs_.size() >= 64) return;
    auto* request = new Request{self, info->index, *producer, info->sink, revision, ""};
    auto* operation = pa_context_get_sink_info_by_index(self->context_, info->sink, sinkInfo, request);
    if (!operation) delete request;
    release(operation);
  }
  static void sinkInfo(pa_context*, const pa_sink_info* info, int end, void* data) {
    auto* request = static_cast<Request*>(data);
    auto* self = request->self;
    if (end) { delete request; return; }
    const auto current = processIdentity(request->producer.pid);
    if (!info || !info->monitor_source_name || self->stopping_ || !self->rootAlive() || !self->revisions_.current(request->index, request->revision) ||
        !current || !gul_audio::sameProcess(*current, request->producer) ||
        gul_audio::classify(*self->root_, current->pid, processIdentity) != gul_audio::Ownership::Foreign ||
        self->pairs_.size() >= 64 || self->pairs_.count(request->index)) return;
    auto* revalidate = new Request(*request);
    revalidate->monitor = info->monitor_source_name;
    auto* operation = pa_context_get_sink_input_info(self->context_, request->index,
      [](pa_context*, const pa_sink_input_info* input, int end, void* data) {
        auto* request = static_cast<Request*>(data);
        auto* self = request->self;
        if (end) { delete request; return; }
        if (!input || self->stopping_ || !self->rootAlive() || !self->revisions_.current(request->index, request->revision)) return;
        const auto* raw = pa_proplist_gets(input->proplist, PA_PROP_APPLICATION_PROCESS_ID);
        const auto pid = raw ? gul_audio::parsePID(raw) : std::nullopt;
        const auto producer = pid ? processIdentity(*pid) : std::nullopt;
        if (producer && gul_audio::sameProcess(request->producer, *producer) && input->sink == request->sink &&
            gul_audio::classify(*self->root_, *pid, processIdentity) == gul_audio::Ownership::Foreign &&
            self->pairs_.size() < 64 && !self->pairs_.count(request->index))
          self->createPair(*request, request->monitor.c_str());
      }, revalidate);
    if (!operation) delete revalidate;
    release(operation);
  }
  void createPair(const Request& request, const char* monitor) {
    const pa_sample_spec format{PA_SAMPLE_S16LE, 48000, 2};
    auto pair = std::make_unique<Pair>();
    pair->sink = request.sink;
    pair->producer = request.producer;
    pair->record = pa_stream_new(context_, "Gul selected playback", &format, nullptr);
    pair->playback = pa_stream_new(context_, "Gul private capture mix", &format, nullptr);
    if (!pair->record || !pair->playback || pa_stream_set_monitor_stream(pair->record, request.index) < 0) return;
    const pa_buffer_attr buffering{38400, 9600, 3840, 1920, 1920};
    pa_stream_set_read_callback(pair->record, [](pa_stream* stream, std::size_t, void* data) {
      auto* pair = static_cast<Pair*>(data);
      const void* samples = nullptr;
      std::size_t count = 0;
      while (pa_stream_readable_size(stream) > 0 && pa_stream_peek(stream, &samples, &count) >= 0 && count) {
        pair->ring.push(samples, count);
        pa_stream_drop(stream);
      }
      pair->pump();
    }, pair.get());
    pa_stream_set_write_callback(pair->playback, [](pa_stream*, std::size_t, void* data) {
      static_cast<Pair*>(data)->pump();
    }, pair.get());
    const auto flags = static_cast<pa_stream_flags_t>(PA_STREAM_ADJUST_LATENCY | PA_STREAM_DONT_MOVE | PA_STREAM_NO_REMIX_CHANNELS);
    if (pa_stream_connect_record(pair->record, monitor, &buffering, flags) < 0 ||
        pa_stream_connect_playback(pair->playback, name_.c_str(), &buffering, flags, nullptr, nullptr) < 0) return;
    pairs_.emplace(request.index, std::move(pair));
  }
};
}  // namespace
int main(int argc, char** argv) {
  if (argc != 3 || !validNonce(argv[2])) return 1;
  const std::string action(argv[1]);
  if (action != "--capture" && action != "--cleanup") return 1;
  Capture capture(argv[2], action == "--cleanup");
  return capture.run();
}
