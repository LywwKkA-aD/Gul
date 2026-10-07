#include <gio/gio.h>
#include <glib-unix.h>
#include <csignal>
#include <cstring>
#include <fcntl.h>
#include <string>
#include <unistd.h>
#include <vector>

namespace {
constexpr const char* name = "org.freedesktop.portal.Desktop";
constexpr const char* path = "/org/freedesktop/portal/desktop";
constexpr const char* interface = "org.freedesktop.portal.GlobalShortcuts";
constexpr const char* xml = R"XML(<node>
<interface name="org.freedesktop.portal.GlobalShortcuts">
<property name="version" type="u" access="read"/>
<method name="CreateSession"><arg type="a{sv}" direction="in"/><arg type="o" direction="out"/></method>
<method name="BindShortcuts"><arg type="o" direction="in"/><arg type="a(sa{sv})" direction="in"/><arg type="s" direction="in"/><arg type="a{sv}" direction="in"/><arg type="o" direction="out"/></method>
</interface>
<interface name="org.freedesktop.portal.Session"><method name="Close"/></interface>
</node>)XML";
struct Fixture {
  GDBusConnection* bus = nullptr;
  GDBusConnection* forged = nullptr;
  GDBusNodeInfo* information = nullptr;
  GMainLoop* loop = nullptr;
  GPid child = 0;
  gint input = -1, output = -1;
  guint owner = 0, watcher = 0, timeout = 0;
  std::vector<guint> objects;
  std::vector<std::string> states;
  std::string executable, mode, destination, session, buffer;
  bool success = false, bindingVerified = false;
  unsigned stage = 0;

  void fail() { g_main_loop_quit(loop); }
  void activate(const char* member, const std::string& handle, const char* id, GDBusConnection* sender = nullptr) {
    GVariantBuilder empty;
    g_variant_builder_init(&empty, G_VARIANT_TYPE_VARDICT);
    g_dbus_connection_emit_signal(sender ? sender : bus, destination.c_str(), path, interface, member,
      g_variant_new("(ost@a{sv})", handle.c_str(), id, static_cast<guint64>(0), g_variant_builder_end(&empty)), nullptr);
  }
  void transition(const std::string& state) {
    states.push_back(state);
    if (mode == "cancel" || mode == "empty" || mode == "wrong-session") { fail(); return; }
    if (stage == 0) {
      if (state != "up") { fail(); return; }
      stage = 1;
      // None of these may open the microphone gate.
      activate("Activated", session + "_other", "gul-ptt");
      activate("Activated", session, "other-shortcut");
      activate("Activated", session, "gul-ptt", forged);
      g_timeout_add(40, [](gpointer data) -> gboolean {
        auto& current = *static_cast<Fixture*>(data);
        if (current.states.size() != 1) current.fail();
        else current.activate("Activated", current.session, "gul-ptt");
        return G_SOURCE_REMOVE;
      }, this);
    } else if (stage == 1) {
      if (state != "down") { fail(); return; }
      stage = 2;
      activate("Activated", session, "gul-ptt");
      if (mode == "stdin") { close(input); input = -1; }
      else if (mode == "owner-loss") { g_bus_unown_name(owner); owner = 0; }
      else if (mode == "closed") g_dbus_connection_emit_signal(bus, destination.c_str(), session.c_str(), "org.freedesktop.portal.Session", "Closed", g_variant_new("()"), nullptr);
      else activate("Deactivated", session, "gul-ptt");
    } else if (stage == 2) {
      if (state != "up") { fail(); return; }
      stage = 3;
      if (mode == "normal") { close(input); input = -1; }
    } else fail();
  }
  void response(const std::string& request, GVariant* dictionary, guint code) {
    g_dbus_connection_emit_signal(bus, destination.c_str(), request.c_str(), "org.freedesktop.portal.Request", "Response", g_variant_new("(u@a{sv})", code, dictionary), nullptr);
  }
  static void method(GDBusConnection*, const gchar* sender, const gchar*, const gchar*, const gchar* member, GVariant* parameters, GDBusMethodInvocation* invocation, gpointer data) {
    auto& current = *static_cast<Fixture*>(data);
    if (std::strcmp(member, "Close") == 0) { g_dbus_method_invocation_return_value(invocation, g_variant_new("()")); return; }
    current.destination = sender;
    std::string caller = sender + 1;
    for (char& character : caller) if (character == '.') character = '_';
    GVariant* options = g_variant_get_child_value(parameters, std::strcmp(member, "CreateSession") == 0 ? 0 : 3);
    const char* token = nullptr;
    g_assert_true(g_variant_lookup(options, "handle_token", "&s", &token));
    const std::string request = std::string(path) + "/request/" + caller + "/" + token;
    GVariantBuilder result;
    g_variant_builder_init(&result, G_VARIANT_TYPE_VARDICT);
    if (std::strcmp(member, "CreateSession") == 0) {
      const char* sessionToken = nullptr;
      g_assert_true(g_variant_lookup(options, "session_handle_token", "&s", &sessionToken));
      current.session = std::string(path) + "/session/" + caller + "/" + sessionToken;
      static const GDBusInterfaceVTable table = {method, nullptr, nullptr, {nullptr}};
      current.objects.push_back(g_dbus_connection_register_object(current.bus, current.session.c_str(), current.information->interfaces[1], &table, &current, nullptr, nullptr));
      g_variant_builder_add(&result, "{sv}", "session_handle", g_variant_new_string(current.mode == "wrong-session" ? "/org/freedesktop/portal/desktop/session/foreign/gul_ptt" : current.session.c_str()));
    } else {
      const char* handle = nullptr;
      GVariant* entries = nullptr;
      const char* parent = nullptr;
      GVariant* ignored = nullptr;
      g_variant_get(parameters, "(&o@a(sa{sv})&s@a{sv})", &handle, &entries, &parent, &ignored);
      g_assert_cmpstr(handle, ==, current.session.c_str());
      g_assert_cmpstr(parent, ==, "");
      GVariant* entry = g_variant_get_child_value(entries, 0);
      const char* id = nullptr;
      GVariant* description = nullptr;
      g_variant_get(entry, "(&s@a{sv})", &id, &description);
      const char* trigger = nullptr;
      g_assert_cmpstr(id, ==, "gul-ptt");
      g_assert_true(g_variant_lookup(description, "preferred_trigger", "&s", &trigger));
      g_assert_cmpstr(trigger, ==, "CTRL+F8");
      current.bindingVerified = true;
      GVariantBuilder accepted;
      g_variant_builder_init(&accepted, G_VARIANT_TYPE("a(sa{sv})"));
      if (current.mode != "empty") g_variant_builder_add_value(&accepted, entry);
      g_variant_builder_add(&result, "{sv}", "shortcuts", g_variant_builder_end(&accepted));
      g_variant_unref(description); g_variant_unref(entry); g_variant_unref(entries); g_variant_unref(ignored);
    }
    g_variant_unref(options);
    // Fast Response before the method's reply checks the subscription race.
    current.response(request, g_variant_builder_end(&result), current.mode == "cancel" && std::strcmp(member, "BindShortcuts") == 0 ? 1 : 0);
    g_dbus_method_invocation_return_value(invocation, g_variant_new("(o)", request.c_str()));
  }
  void spawn() {
    std::string parent = std::to_string(getpid());
    gchar* argv[] = {const_cast<gchar*>(executable.c_str()), const_cast<gchar*>("CTRL+F8"), const_cast<gchar*>(parent.c_str()), nullptr};
    g_assert_true(g_spawn_async_with_pipes(nullptr, argv, nullptr, G_SPAWN_DO_NOT_REAP_CHILD, nullptr, nullptr, &child, &input, &output, nullptr, nullptr));
    fcntl(output, F_SETFL, fcntl(output, F_GETFL) | O_NONBLOCK);
    watcher = g_unix_fd_add(output, static_cast<GIOCondition>(G_IO_IN | G_IO_HUP), [](gint fd, GIOCondition, gpointer data) -> gboolean {
      auto& current = *static_cast<Fixture*>(data);
      char bytes[256];
      const auto count = read(fd, bytes, sizeof bytes);
      if (count > 0) {
        current.buffer.append(bytes, static_cast<std::size_t>(count));
        std::size_t newline;
        while ((newline = current.buffer.find('\n')) != std::string::npos) {
          const auto line = current.buffer.substr(0, newline);
          current.buffer.erase(0, newline + 1);
          current.transition(line);
        }
      }
      return G_SOURCE_CONTINUE;
    }, this);
    g_child_watch_add(child, [](GPid pid, gint status, gpointer data) {
      auto& current = *static_cast<Fixture*>(data);
      const bool rejected = current.mode == "cancel" || current.mode == "empty" || current.mode == "wrong-session";
      current.success = rejected ? current.states.empty() && status != 0 : current.states == std::vector<std::string>({"up", "down", "up"}) && current.bindingVerified && ((current.mode == "normal" || current.mode == "stdin") ? status == 0 : status != 0);
      current.child = 0;
      g_spawn_close_pid(pid);
      g_main_loop_quit(current.loop);
    }, this);
  }
};
}
int main(int argc, char** argv) {
  if (argc != 2) return 1;
  GTestDBus* testBus = g_test_dbus_new(G_TEST_DBUS_NONE);
  g_test_dbus_up(testBus);
  for (const char* mode : {"normal", "stdin", "owner-loss", "closed", "cancel", "empty", "wrong-session"}) {
    Fixture fixture;
    fixture.mode = mode; fixture.executable = argv[1];
    fixture.loop = g_main_loop_new(nullptr, false);
    fixture.information = g_dbus_node_info_new_for_xml(xml, nullptr);
    fixture.bus = g_dbus_connection_new_for_address_sync(g_test_dbus_get_bus_address(testBus), static_cast<GDBusConnectionFlags>(G_DBUS_CONNECTION_FLAGS_AUTHENTICATION_CLIENT | G_DBUS_CONNECTION_FLAGS_MESSAGE_BUS_CONNECTION), nullptr, nullptr, nullptr);
    fixture.forged = g_dbus_connection_new_for_address_sync(g_test_dbus_get_bus_address(testBus), static_cast<GDBusConnectionFlags>(G_DBUS_CONNECTION_FLAGS_AUTHENTICATION_CLIENT | G_DBUS_CONNECTION_FLAGS_MESSAGE_BUS_CONNECTION), nullptr, nullptr, nullptr);
    static const GDBusInterfaceVTable table = {Fixture::method, [](GDBusConnection*, const gchar*, const gchar*, const gchar*, const gchar*, GError**, gpointer) -> GVariant* { return g_variant_new_uint32(1); }, nullptr, {nullptr}};
    fixture.objects.push_back(g_dbus_connection_register_object(fixture.bus, path, fixture.information->interfaces[0], &table, &fixture, nullptr, nullptr));
    fixture.owner = g_bus_own_name_on_connection(fixture.bus, name, G_BUS_NAME_OWNER_FLAGS_NONE, [](GDBusConnection*, const gchar*, gpointer data) { static_cast<Fixture*>(data)->spawn(); }, nullptr, &fixture, nullptr);
    fixture.timeout = g_timeout_add_seconds(5, [](gpointer data) -> gboolean { static_cast<Fixture*>(data)->fail(); return G_SOURCE_REMOVE; }, &fixture);
    g_main_loop_run(fixture.loop);
    if (fixture.child != 0) { kill(fixture.child, SIGKILL); g_spawn_close_pid(fixture.child); }
    if (fixture.owner) g_bus_unown_name(fixture.owner);
    if (g_main_context_find_source_by_id(nullptr, fixture.timeout)) g_source_remove(fixture.timeout);
    if (fixture.watcher) g_source_remove(fixture.watcher);
    if (fixture.input >= 0) close(fixture.input);
    if (fixture.output >= 0) close(fixture.output);
    for (const auto id : fixture.objects) g_dbus_connection_unregister_object(fixture.bus, id);
    g_dbus_connection_close_sync(fixture.bus, nullptr, nullptr);
    g_dbus_connection_close_sync(fixture.forged, nullptr, nullptr);
    g_object_unref(fixture.bus); g_object_unref(fixture.forged);
    g_dbus_node_info_unref(fixture.information); g_main_loop_unref(fixture.loop);
    if (!fixture.success) { g_printerr("Portal fixture failed: %s, states=%zu, stage=%u\n", mode, fixture.states.size(), fixture.stage); return 1; }
  }
  g_test_dbus_down(testBus); g_object_unref(testBus);
  return 0;
}
