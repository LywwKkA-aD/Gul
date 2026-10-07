#include <gio/gio.h>
#include <glib-unix.h>
#include <algorithm>
#include <cerrno>
#include <climits>
#include <csignal>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <string>
#include <sys/prctl.h>
#include <unistd.h>
#include <vector>

namespace {
constexpr const char* portalName = "org.freedesktop.portal.Desktop";
constexpr const char* portalPath = "/org/freedesktop/portal/desktop";
constexpr const char* shortcutsInterface = "org.freedesktop.portal.GlobalShortcuts";
constexpr const char* requestInterface = "org.freedesktop.portal.Request";
constexpr const char* sessionInterface = "org.freedesktop.portal.Session";
constexpr const char* shortcutID = "gul-ptt";

bool validTrigger(const std::string& trigger) {
  if (trigger.empty() || trigger.size() > 96) return false;
  std::size_t start = 0;
  unsigned modifiers = 0;
  const std::vector<std::string> names = {"CTRL", "ALT", "SHIFT", "LOGO"};
  for (auto separator = trigger.find('+'); separator != std::string::npos;
       separator = trigger.find('+', start)) {
    const auto part = trigger.substr(start, separator - start);
    const auto found = std::find(names.begin(), names.end(), part);
    if (found == names.end()) return false;
    const unsigned bit = 1u << static_cast<unsigned>(found - names.begin());
    if ((modifiers & bit) != 0) return false;
    modifiers |= bit;
    start = separator + 1;
  }
  const auto key = trigger.substr(start);
  const std::vector<std::string> special = {"BackSpace", "Tab", "Return", "Caps_Lock", "Escape", "space", "Prior", "Next", "End", "Home", "Left", "Up", "Right", "Down", "Insert", "Delete"};
  bool valid = std::find(special.begin(), special.end(), key) != special.end();
  if (key.size() == 1) valid = (key[0] >= 'a' && key[0] <= 'z') || (key[0] >= '0' && key[0] <= '9');
  if (key.size() == 4 && key.compare(0, 3, "KP_") == 0) valid = key[3] >= '0' && key[3] <= '9';
  if (key.size() >= 2 && key.size() <= 3 && key[0] == 'F') {
    const auto number = key.substr(1);
    valid = number[0] != '0' && std::all_of(number.begin(), number.end(), [](char ch) { return ch >= '0' && ch <= '9'; }) && std::stoi(number) <= 24;
  }
  return valid && !(key == "Delete" && (modifiers & 3) == 3) && !(key == "l" && (modifiers & 8) != 0);
}

struct App {
  GMainLoop* loop = nullptr;
  GDBusConnection* bus = nullptr;
  GCancellable* cancel = nullptr;
  std::string owner, trigger, request, session, sessionExpected, prefix, requestToken;
  std::vector<guint> subscriptions, sources;
  unsigned phase = 0;
  bool ready = false, pressed = false, stopping = false;
  int result = 1;

  bool writeState(bool down) {
    const char* state = down ? "down\n" : "up\n";
    const std::size_t length = down ? 5 : 3;
    ssize_t written;
    do { written = write(STDOUT_FILENO, state, length); } while (written < 0 && errno == EINTR);
    if (written != static_cast<ssize_t>(length)) { stop(); return false; }
    pressed = down;
    return true;
  }
  void stop(int status = 1) {
    if (stopping) return;
    stopping = true;
    result = status;
    if (pressed) {
      const auto ignored = write(STDOUT_FILENO, "up\n", 3);
      (void)ignored;
      pressed = false;
    }
    g_cancellable_cancel(cancel);
    g_main_loop_quit(loop);
  }
  void call(const char* method, GVariant* arguments) {
    GError* error = nullptr;
    GVariant* value = g_dbus_connection_call_sync(bus, owner.c_str(), portalPath, shortcutsInterface, method, arguments,
      G_VARIANT_TYPE("(o)"), G_DBUS_CALL_FLAGS_NONE, 5000, cancel, &error);
    const char* path = nullptr;
    if (value) g_variant_get(value, "(&o)", &path);
    // Startup calls have a bounded deadline. Queued Response signals are handled
    // only after their method has returned the exact caller-owned request path.
    if (!path || request != path) stop();
    if (value) g_variant_unref(value);
    g_clear_error(&error);
  }
  GVariant* options(bool create) {
    GVariantBuilder dictionary;
    g_variant_builder_init(&dictionary, G_VARIANT_TYPE_VARDICT);
    g_variant_builder_add(&dictionary, "{sv}", "handle_token", g_variant_new_string(requestToken.c_str()));
    if (create) g_variant_builder_add(&dictionary, "{sv}", "session_handle_token", g_variant_new_string("gul_ptt"));
    return g_variant_builder_end(&dictionary);
  }
  void bind() {
    phase = 1;
    requestToken = "gul_bind";
    request = prefix + "/" + requestToken;
    GVariantBuilder entries, description;
    g_variant_builder_init(&entries, G_VARIANT_TYPE("a(sa{sv})"));
    g_variant_builder_init(&description, G_VARIANT_TYPE_VARDICT);
    g_variant_builder_add(&description, "{sv}", "description", g_variant_new_string("Gul push to talk"));
    g_variant_builder_add(&description, "{sv}", "preferred_trigger", g_variant_new_string(trigger.c_str()));
    g_variant_builder_add(&entries, "(s@a{sv})", shortcutID, g_variant_builder_end(&description));
    call("BindShortcuts", g_variant_new("(o@a(sa{sv})s@a{sv})", session.c_str(), g_variant_builder_end(&entries), "", options(false)));
  }
  bool containsShortcut(GVariant* entries) {
    if (!entries || !g_variant_is_of_type(entries, G_VARIANT_TYPE("a(sa{sv})"))) return false;
    GVariantIter iterator;
    g_variant_iter_init(&iterator, entries);
    const char* id = nullptr;
    GVariant* values = nullptr;
    bool found = false;
    while (g_variant_iter_next(&iterator, "(&s@a{sv})", &id, &values)) {
      if (std::strcmp(id, shortcutID) == 0) found = true;
      g_variant_unref(values);
    }
    return found;
  }
  void response(const char* path, GVariant* parameters) {
    if (stopping || request != path || phase > 1) return;
    if (!g_variant_is_of_type(parameters, G_VARIANT_TYPE("(ua{sv})"))) { stop(); return; }
    guint code = 1;
    GVariant* values = nullptr;
    g_variant_get(parameters, "(u@a{sv})", &code, &values);
    if (code != 0) { g_variant_unref(values); stop(); return; }
    if (phase == 0) {
      const char* handle = nullptr;
      if (!g_variant_lookup(values, "session_handle", "&s", &handle) || !handle || sessionExpected != handle) {
        g_variant_unref(values); stop(); return;
      }
      session = handle;
      g_variant_unref(values);
      bind();
    } else {
      GVariant* bound = g_variant_lookup_value(values, "shortcuts", G_VARIANT_TYPE("a(sa{sv})"));
      const bool accepted = containsShortcut(bound);
      if (bound) g_variant_unref(bound);
      g_variant_unref(values);
      if (!accepted) { stop(); return; }
      phase = 2;
      ready = writeState(false);
    }
  }
  void signal(const char* member, GVariant* parameters) {
    if (stopping || !ready) return;
    if (std::strcmp(member, "ShortcutsChanged") == 0) {
      if (!g_variant_is_of_type(parameters, G_VARIANT_TYPE("(oa(sa{sv}))"))) { stop(); return; }
      const char* handle = nullptr;
      GVariant* entries = nullptr;
      g_variant_get(parameters, "(&o@a(sa{sv}))", &handle, &entries);
      if (session == handle && !containsShortcut(entries)) stop();
      g_variant_unref(entries);
      return;
    }
    if (std::strcmp(member, "Activated") != 0 && std::strcmp(member, "Deactivated") != 0) return;
    if (!g_variant_is_of_type(parameters, G_VARIANT_TYPE("(osta{sv})"))) { stop(); return; }
    const char* handle = nullptr;
    const char* id = nullptr;
    guint64 timestamp = 0;
    GVariant* values = nullptr;
    g_variant_get(parameters, "(&o&st@a{sv})", &handle, &id, &timestamp, &values);
    g_variant_unref(values);
    if (session != handle || std::strcmp(id, shortcutID) != 0) return;
    const bool down = std::strcmp(member, "Activated") == 0;
    if (pressed != down) writeState(down);
  }
  void subscribe(const char* name, const char* interface, const char* member, const char* path, GDBusSignalCallback callback) {
    subscriptions.push_back(g_dbus_connection_signal_subscribe(bus, name, interface, member, path, nullptr, G_DBUS_SIGNAL_FLAGS_NONE, callback, this, nullptr));
  }
  bool start() {
    GError* error = nullptr;
    bus = g_bus_get_sync(G_BUS_TYPE_SESSION, cancel, &error);
    g_clear_error(&error);
    if (!bus) return false;
    g_dbus_connection_set_exit_on_close(bus, false);
    // A property call starts the portal service when it is not running yet.
    GVariant* capability = g_dbus_connection_call_sync(bus, portalName, portalPath, "org.freedesktop.DBus.Properties", "Get", g_variant_new("(ss)", shortcutsInterface, "version"), G_VARIANT_TYPE("(v)"), G_DBUS_CALL_FLAGS_NONE, 5000, cancel, &error);
    g_clear_error(&error);
    if (!capability) return false;
    GVariant* version = nullptr;
    g_variant_get(capability, "(v)", &version);
    const bool supported = g_variant_is_of_type(version, G_VARIANT_TYPE_UINT32) && g_variant_get_uint32(version) >= 1;
    g_variant_unref(version);
    g_variant_unref(capability);
    if (!supported) return false;
    // Bind every signal to the current unique owner, never a replacement service.
    GVariant* result = g_dbus_connection_call_sync(bus, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "GetNameOwner", g_variant_new("(s)", portalName), G_VARIANT_TYPE("(s)"), G_DBUS_CALL_FLAGS_NONE, 5000, cancel, &error);
    g_clear_error(&error);
    if (!result) return false;
    const char* currentOwner = nullptr;
    g_variant_get(result, "(&s)", &currentOwner);
    owner = currentOwner;
    g_variant_unref(result);
    const char* uniqueName = g_dbus_connection_get_unique_name(bus);
    if (!uniqueName || uniqueName[0] != ':') return false;
    std::string unique = uniqueName + 1;
    std::replace(unique.begin(), unique.end(), '.', '_');
    prefix = "/org/freedesktop/portal/desktop";
    sessionExpected = prefix + "/session/" + unique + "/gul_ptt";
    prefix += "/request/" + unique;
    // prefix below remains the caller's request namespace, not another caller's.
    requestToken = "gul_create";
    request = prefix + "/" + requestToken;
    subscribe(owner.c_str(), requestInterface, "Response", nullptr, [](GDBusConnection*, const gchar*, const gchar* path, const gchar*, const gchar*, GVariant* args, gpointer data) { static_cast<App*>(data)->response(path, args); });
    subscribe(owner.c_str(), shortcutsInterface, nullptr, portalPath, [](GDBusConnection*, const gchar*, const gchar*, const gchar*, const gchar* member, GVariant* args, gpointer data) { static_cast<App*>(data)->signal(member, args); });
    subscribe(owner.c_str(), sessionInterface, "Closed", sessionExpected.c_str(), [](GDBusConnection*, const gchar*, const gchar*, const gchar*, const gchar*, GVariant*, gpointer data) { static_cast<App*>(data)->stop(); });
    subscribe("org.freedesktop.DBus", "org.freedesktop.DBus", "NameOwnerChanged", "/org/freedesktop/DBus", [](GDBusConnection*, const gchar*, const gchar*, const gchar*, const gchar*, GVariant* args, gpointer data) {
      auto& app = *static_cast<App*>(data);
      if (!g_variant_is_of_type(args, G_VARIANT_TYPE("(sss)"))) { app.stop(); return; }
      const char *name = nullptr, *previous = nullptr, *next = nullptr;
      g_variant_get(args, "(&s&s&s)", &name, &previous, &next);
      if (std::strcmp(name, portalName) == 0 && app.owner != next) app.stop();
    });
    g_signal_connect(bus, "closed", G_CALLBACK(+[](GDBusConnection*, gboolean, GError*, gpointer data) { static_cast<App*>(data)->stop(); }), this);
    call("CreateSession", g_variant_new("(@a{sv})", options(true)));
    return true;
  }
  void cleanup() {
    if (bus && !g_dbus_connection_is_closed(bus)) {
      if (!session.empty()) g_dbus_connection_call(bus, owner.c_str(), session.c_str(), sessionInterface, "Close", nullptr, nullptr, G_DBUS_CALL_FLAGS_NONE, 500, nullptr, nullptr, nullptr);
      else if (!request.empty()) g_dbus_connection_call(bus, owner.c_str(), request.c_str(), requestInterface, "Close", nullptr, nullptr, G_DBUS_CALL_FLAGS_NONE, 500, nullptr, nullptr, nullptr);
      g_dbus_connection_flush_sync(bus, nullptr, nullptr);
    }
    for (const auto id : subscriptions) g_dbus_connection_signal_unsubscribe(bus, id);
    for (const auto id : sources) if (g_main_context_find_source_by_id(nullptr, id)) g_source_remove(id);
    if (bus) { g_signal_handlers_disconnect_by_data(bus, this); g_object_unref(bus); }
    g_object_unref(cancel);
    g_main_loop_unref(loop);
  }
};
}

int main(int argc, char** argv) {
  if (argc != 3 || !validTrigger(argv[1])) return 1;
  char* end = nullptr;
  errno = 0;
  const long parent = std::strtol(argv[2], &end, 10);
  if (errno != 0 || !end || *end != '\0' || parent <= 1 || parent > INT_MAX || getppid() != parent) return 1;
  // Close the race between checking the parent and installing its death signal.
  if (prctl(PR_SET_PDEATHSIG, SIGTERM) != 0 || getppid() != parent) return 1;
  std::signal(SIGPIPE, SIG_IGN);
  if (fcntl(STDOUT_FILENO, F_SETFL, fcntl(STDOUT_FILENO, F_GETFL) | O_NONBLOCK) < 0 || fcntl(STDIN_FILENO, F_SETFL, fcntl(STDIN_FILENO, F_GETFL) | O_NONBLOCK) < 0) return 1;
  App app;
  app.trigger = argv[1];
  app.loop = g_main_loop_new(nullptr, false);
  app.cancel = g_cancellable_new();
  for (const int signal : {SIGTERM, SIGINT}) app.sources.push_back(g_unix_signal_add(signal, [](gpointer data) -> gboolean { static_cast<App*>(data)->stop(0); return G_SOURCE_REMOVE; }, &app));
  app.sources.push_back(g_unix_fd_add(STDIN_FILENO, static_cast<GIOCondition>(G_IO_IN | G_IO_HUP | G_IO_ERR | G_IO_NVAL), [](gint fd, GIOCondition condition, gpointer data) -> gboolean {
    auto& current = *static_cast<App*>(data);
    char discarded[256];
    const auto count = read(fd, discarded, sizeof discarded);
    if ((condition & (G_IO_HUP | G_IO_ERR | G_IO_NVAL)) != 0 || count == 0 || (count < 0 && errno != EAGAIN && errno != EINTR)) { current.stop(0); return G_SOURCE_REMOVE; }
    return G_SOURCE_CONTINUE;
  }, &app));
  app.sources.push_back(g_timeout_add_seconds(60, [](gpointer data) -> gboolean {
    auto& current = *static_cast<App*>(data);
    if (!current.ready) current.stop();
    return G_SOURCE_REMOVE;
  }, &app));
  if (app.start() && !app.stopping) g_main_loop_run(app.loop);
  app.cleanup();
  return app.result;
}
