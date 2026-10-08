#include <gio/gio.h>
#include <stdio.h>
#include <string.h>

static const char *mode;
static gboolean locked = TRUE;
static char *owner;
static const char *item = "/org/freedesktop/secrets/collection/test/1";
static const char *prompt = "/org/freedesktop/secrets/prompt/test";
static const char *xml =
  "<node><interface name='org.freedesktop.Secret.Service'>"
  "<method name='SearchItems'><arg type='a{ss}' direction='in'/><arg type='ao' direction='out'/><arg type='ao' direction='out'/></method>"
  "<method name='ReadAlias'><arg type='s' direction='in'/><arg type='o' direction='out'/></method>"
  "<method name='Unlock'><arg type='ao' direction='in'/><arg type='ao' direction='out'/><arg type='o' direction='out'/></method>"
  "</interface><interface name='org.freedesktop.Secret.Prompt'>"
  "<method name='Prompt'><arg type='s' direction='in'/></method><method name='Dismiss'/>"
  "<signal name='Completed'><arg type='b'/><arg type='v'/></signal></interface></node>";

static void method(GDBusConnection *connection, const char *sender, const char *path,
    const char *interface, const char *name, GVariant *parameters,
    GDBusMethodInvocation *invocation, gpointer data) {
  (void)path; (void)interface; (void)data;
  if (strcmp(name, "SearchItems") == 0) {
    GVariant *attributes = g_variant_get_child_value(parameters, 0);
    const char *application = NULL;
    gboolean valid = g_variant_lookup(attributes, "application", "&s", &application) &&
        strcmp(application, "GulProof") == 0 && g_variant_n_children(attributes) == 1;
    g_variant_unref(attributes);
    if (!valid) { g_dbus_method_invocation_return_dbus_error(invocation, "org.test.Invalid", "Invalid metadata query"); return; }
    GVariantBuilder available, unavailable;
    g_variant_builder_init(&available, G_VARIANT_TYPE("ao"));
    g_variant_builder_init(&unavailable, G_VARIANT_TYPE("ao"));
    if (strcmp(mode, "missing") != 0)
      g_variant_builder_add(locked ? &unavailable : &available, "o", item);
    g_dbus_method_invocation_return_value(invocation, g_variant_new("(aoao)", &available, &unavailable));
  } else if (strcmp(name, "ReadAlias") == 0) {
    g_dbus_method_invocation_return_value(invocation, g_variant_new("(o)", "/"));
  } else if (strcmp(name, "Unlock") == 0) {
    GVariant *objects = g_variant_get_child_value(parameters, 0);
    gboolean valid = g_variant_n_children(objects) == 1;
    if (valid) {
      GVariant *target = g_variant_get_child_value(objects, 0);
      valid = strcmp(g_variant_get_string(target, NULL), item) == 0;
      g_variant_unref(target);
    }
    g_variant_unref(objects);
    if (!valid || strcmp(mode, "refuse") == 0) {
      g_dbus_method_invocation_return_dbus_error(invocation, "org.test.Refused", "Private error must never escape"); return;
    }
    g_free(owner);
    owner = g_strdup(sender);
    GVariantBuilder empty;
    g_variant_builder_init(&empty, G_VARIANT_TYPE("ao"));
    g_dbus_method_invocation_return_value(invocation, g_variant_new("(aoo)", &empty, prompt));
  } else if (strcmp(name, "Prompt") == 0 || strcmp(name, "Dismiss") == 0) {
    if (!owner || strcmp(sender, owner) != 0) {
      g_dbus_method_invocation_return_dbus_error(invocation, "org.test.WrongOwner", "Connection mismatch"); return;
    }
    g_dbus_method_invocation_return_value(invocation, g_variant_new("()"));
    if (strcmp(mode, "pending") == 0 && strcmp(name, "Prompt") == 0) {
      puts("GUL_TEST_PROMPT_READY");
      fflush(stdout);
      return;
    }
    const gboolean cancelled = strcmp(mode, "cancel") == 0 || strcmp(name, "Dismiss") == 0;
    if (!cancelled) locked = FALSE;
    GVariantBuilder result;
    g_variant_builder_init(&result, G_VARIANT_TYPE("ao"));
    g_dbus_connection_emit_signal(connection, owner, prompt, "org.freedesktop.Secret.Prompt", "Completed",
        g_variant_new("(bv)", cancelled, g_variant_builder_end(&result)), NULL);
  } else {
    g_dbus_method_invocation_return_dbus_error(invocation, "org.test.Forbidden", "Unexpected secret method");
  }
}

int main(int argc, char **argv) {
  if (argc != 2) return 64;
  mode = argv[1];
  locked = strcmp(mode, "ready") != 0;
  GError *error = NULL;
  GDBusConnection *connection = g_bus_get_sync(G_BUS_TYPE_SESSION, NULL, &error);
  if (!connection) return 1;
  GDBusNodeInfo *info = g_dbus_node_info_new_for_xml(xml, &error);
  if (!info) return 1;
  const GDBusInterfaceVTable table = { .method_call = method };
  if (!g_dbus_connection_register_object(connection, "/org/freedesktop/secrets", info->interfaces[0], &table, NULL, NULL, &error) ||
      !g_dbus_connection_register_object(connection, prompt, info->interfaces[1], &table, NULL, NULL, &error)) return 1;
  GVariant *reply = g_dbus_connection_call_sync(connection, "org.freedesktop.DBus", "/org/freedesktop/DBus",
      "org.freedesktop.DBus", "RequestName", g_variant_new("(su)", "org.freedesktop.secrets", 4u),
      G_VARIANT_TYPE("(u)"), G_DBUS_CALL_FLAGS_NONE, 5000, NULL, &error);
  if (!reply) return 1;
  guint code = 0;
  g_variant_get(reply, "(u)", &code);
  g_variant_unref(reply);
  if (code != 1) return 1;
  puts("GUL_TEST_SERVICE_READY");
  fflush(stdout);
  GMainLoop *loop = g_main_loop_new(NULL, FALSE);
  g_main_loop_run(loop);
  return 0;
}
