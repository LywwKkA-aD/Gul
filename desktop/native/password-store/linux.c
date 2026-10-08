#include <gio/gio.h>
#include <glib-unix.h>
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <sys/prctl.h>
#include <unistd.h>

static const char *service = "org.freedesktop.secrets";
static const char *root = "/org/freedesktop/secrets";
static const char *interface = "org.freedesktop.Secret.Service";
static const char *application;
static GDBusConnection *connection;
static GMainLoop *loop;
static const char *prompt;
static const char *result = "UNAVAILABLE";
static gboolean dismissed = FALSE;
static gboolean completed = FALSE;

/* Metadata only: never invoke OpenSession, GetSecret, GetSecrets or CreateItem. */
static GVariant *call(const char *path, const char *type, const char *method,
                      GVariant *parameters, const GVariantType *reply_type) {
  GError *error = NULL;
  GVariant *reply = g_dbus_connection_call_sync(connection, service, path, type,
      method, parameters, reply_type, G_DBUS_CALL_FLAGS_NONE, 5000, NULL, &error);
  g_clear_error(&error);
  return reply;
}

static gboolean paths(GVariant *array, GPtrArray *target) {
  const gsize count = g_variant_n_children(array);
  if (count > 64) return FALSE;
  for (gsize index = 0; index < count; ++index) {
    GVariant *entry = g_variant_get_child_value(array, index);
    const char *path = g_variant_get_string(entry, NULL);
    gboolean valid = strlen(path) <= 512 && strcmp(path, "/") != 0;
    if (valid && target) g_ptr_array_add(target, g_strdup(path));
    g_variant_unref(entry);
    if (!valid) return FALSE;
  }
  return TRUE;
}

static const char *status(GPtrArray *locked) {
  GVariantBuilder attributes;
  g_variant_builder_init(&attributes, G_VARIANT_TYPE("a{ss}"));
  g_variant_builder_add(&attributes, "{ss}", "application", application);
  GVariant *reply = call(root, interface, "SearchItems",
      g_variant_new("(a{ss})", &attributes), G_VARIANT_TYPE("(aoao)"));
  if (!reply) return "UNAVAILABLE";
  GVariant *unlocked_items = g_variant_get_child_value(reply, 0);
  GVariant *locked_items = g_variant_get_child_value(reply, 1);
  const gsize unlocked_count = g_variant_n_children(unlocked_items);
  const gsize locked_count = g_variant_n_children(locked_items);
  gboolean valid = paths(unlocked_items, NULL) && paths(locked_items, locked);
  g_variant_unref(unlocked_items);
  g_variant_unref(locked_items);
  g_variant_unref(reply);
  if (!valid) return "UNAVAILABLE";
  if (locked_count) return "LOCKED";
  if (unlocked_count) return "READY";
  reply = call(root, interface, "ReadAlias", g_variant_new("(s)", "default"),
      G_VARIANT_TYPE("(o)"));
  if (!reply) return "UNAVAILABLE";
  const char *value;
  g_variant_get(reply, "(&o)", &value);
  char *collection = g_strdup(value);
  g_variant_unref(reply);
  if (strcmp(collection, "/") == 0) { g_free(collection); return "MISSING"; }
  reply = call(collection, "org.freedesktop.DBus.Properties", "Get",
      g_variant_new("(ss)", "org.freedesktop.Secret.Collection", "Locked"),
      G_VARIANT_TYPE("(v)"));
  if (!reply) { g_free(collection); return "UNAVAILABLE"; }
  GVariant *wrapped = g_variant_get_child_value(reply, 0);
  GVariant *property = g_variant_get_variant(wrapped);
  const gboolean correct_type = g_variant_is_of_type(property, G_VARIANT_TYPE_BOOLEAN);
  const gboolean is_locked = correct_type && g_variant_get_boolean(property);
  if (is_locked && locked) g_ptr_array_add(locked, g_strdup(collection));
  g_variant_unref(property);
  g_variant_unref(wrapped);
  g_variant_unref(reply);
  g_free(collection);
  return !correct_type ? "UNAVAILABLE" : is_locked ? "LOCKED" : "READY";
}

static void complete(GDBusConnection *bus, const char *sender, const char *path,
    const char *type, const char *name, GVariant *parameters, gpointer data) {
  (void)bus; (void)sender; (void)path; (void)type; (void)name; (void)data;
  if (!g_variant_is_of_type(parameters, G_VARIANT_TYPE("(bv)"))) return;
  GVariant *flag = g_variant_get_child_value(parameters, 0);
  dismissed = g_variant_get_boolean(flag);
  g_variant_unref(flag);
  result = dismissed ? "CANCELLED" : "READY";
  completed = TRUE;
  g_main_loop_quit(loop);
}

static gboolean cancel(gpointer data) {
  (void)data;
  result = "CANCELLED";
  if (prompt) {
    GVariant *reply = call(prompt, "org.freedesktop.Secret.Prompt", "Dismiss", NULL,
        G_VARIANT_TYPE("()"));
    if (reply) g_variant_unref(reply);
  }
  g_main_loop_quit(loop);
  return G_SOURCE_CONTINUE;
}

static void disconnected(GDBusConnection *bus, gboolean vanished, GError *error, gpointer data) {
  (void)bus; (void)vanished; (void)error; (void)data;
  result = "UNAVAILABLE";
  completed = TRUE;
  g_main_loop_quit(loop);
}

static const char *unlock(void) {
  GPtrArray *locked = g_ptr_array_new_with_free_func(g_free);
  const char *initial = status(locked);
  if (strcmp(initial, "LOCKED") != 0) { g_ptr_array_unref(locked); return initial; }
  GVariantBuilder objects;
  g_variant_builder_init(&objects, G_VARIANT_TYPE("ao"));
  for (guint index = 0; index < locked->len; ++index)
    g_variant_builder_add(&objects, "o", (const char *)g_ptr_array_index(locked, index));
  GVariant *reply = call(root, interface, "Unlock", g_variant_new("(ao)", &objects),
      G_VARIANT_TYPE("(aoo)"));
  g_ptr_array_unref(locked);
  if (!reply) return "UNAVAILABLE";
  GVariant *prompt_path = g_variant_get_child_value(reply, 1);
  char *owned_prompt = g_strdup(g_variant_get_string(prompt_path, NULL));
  g_variant_unref(prompt_path);
  g_variant_unref(reply);
  if (strcmp(owned_prompt, "/") == 0) {
    g_free(owned_prompt);
    return status(NULL);
  }
  if (strlen(owned_prompt) > 512) { g_free(owned_prompt); return "UNAVAILABLE"; }
  prompt = owned_prompt;
  loop = g_main_loop_new(NULL, FALSE);
  guint subscription = g_dbus_connection_signal_subscribe(connection, service,
      "org.freedesktop.Secret.Prompt", "Completed", prompt, NULL,
      G_DBUS_SIGNAL_FLAGS_NONE, complete, NULL, NULL);
  guint timeout = g_timeout_add_seconds(85, cancel, NULL);
  guint termination = g_unix_signal_add(SIGTERM, cancel, NULL);
  guint interruption = g_unix_signal_add(SIGINT, cancel, NULL);
  gulong closed = g_signal_connect(connection, "closed", G_CALLBACK(disconnected), NULL);
  reply = call(prompt, "org.freedesktop.Secret.Prompt", "Prompt", g_variant_new("(s)", ""),
      G_VARIANT_TYPE("()"));
  if (reply) {
    g_variant_unref(reply);
    if (!completed) g_main_loop_run(loop);
    if (strcmp(result, "READY") == 0) result = status(NULL);
  }
  g_source_remove(timeout);
  g_source_remove(termination);
  g_source_remove(interruption);
  g_signal_handler_disconnect(connection, closed);
  g_dbus_connection_signal_unsubscribe(connection, subscription);
  g_main_loop_unref(loop);
  prompt = NULL;
  g_free(owned_prompt);
  return result;
}

int main(int argc, char **argv) {
  if (argc != 3 || (strcmp(argv[1], "--status") != 0 && strcmp(argv[1], "--unlock") != 0) ||
      !argv[2][0] || strlen(argv[2]) > 128) return 64;
  for (const char *p = argv[2]; *p; ++p)
    if ((unsigned char)*p < 32 || (unsigned char)*p == 127) return 64;
  const pid_t parent = getppid();
  if (parent <= 1 || prctl(PR_SET_PDEATHSIG, SIGTERM) != 0 || getppid() != parent) return 64;
  application = argv[2];
  GError *error = NULL;
  connection = g_bus_get_sync(G_BUS_TYPE_SESSION, NULL, &error);
  g_clear_error(&error);
  if (connection) {
    result = strcmp(argv[1], "--unlock") == 0 ? unlock() : status(NULL);
    g_object_unref(connection);
  }
  printf("GUL_PASSWORD_STORE_%s\n", result);
  return 0;
}
