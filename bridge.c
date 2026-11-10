#include "bridge.h"

#if defined(__APPLE__) && defined(__x86_64__)
#include <pthread.h>
#include <stdio.h>
#include <string.h>

/* Darwin/amd64 Go runtimes use the same thread-local g slot. A thread
 * entering the other runtime must not carry a g from the caller's runtime.
 * Each synchronous crossing uses a fresh C thread in both directions. The
 * caller's C buffers stay alive until pthread_join completes. No Go pointer
 * or runtime-private TLS layout is inspected or modified here. */
typedef struct {
    cliproxy_host_api* host;
    cliproxy_plugin_api* plugin;
    char* method;
    uint8_t* request;
    size_t length;
    cliproxy_buffer* response;
    void* buffer;
    int result;
} bridge_call;

static void isolated_call(void* (*entry)(void*), bridge_call* call) {
    pthread_t thread;
    int err = pthread_create(&thread, NULL, entry, call);
    if (err == 0) {
        err = pthread_join(thread, NULL);
    }
    if (err != 0) {
        /* Continuing on the caller's Go thread would corrupt runtime state.
         * As with Go's own runtime thread creation failure, fail explicitly. */
        fprintf(stderr, "newapi-checkin: native bridge thread failed: %s\n", strerror(err));
        abort();
    }
}

static void* enter_init(void* arg) {
    bridge_call* c = arg;
    c->result = newapiPluginInit(c->host, c->plugin);
    return NULL;
}
static void* enter_plugin(void* arg) {
    bridge_call* c = arg;
    c->result = cliproxyPluginCall(c->method, c->request, c->length, c->response);
    return NULL;
}
static void* enter_shutdown(void* arg) {
    (void)arg;
    cliproxyPluginShutdown();
    return NULL;
}
static void* enter_host(void* arg) {
    bridge_call* c = arg;
    c->result = c->host->call(c->host->host_ctx, c->method, c->request, c->length, c->response);
    return NULL;
}
static void* enter_host_free(void* arg) {
    bridge_call* c = arg;
    c->host->free_buffer(c->buffer, c->length);
    return NULL;
}

int cliproxy_plugin_init(cliproxy_host_api* host, cliproxy_plugin_api* plugin) {
    bridge_call c = {.host = host, .plugin = plugin};
    isolated_call(enter_init, &c);
    return c.result;
}
static int plugin_call(char* method, uint8_t* request, size_t length, cliproxy_buffer* response) {
    bridge_call c = {.method = method, .request = request, .length = length, .response = response};
    isolated_call(enter_plugin, &c);
    return c.result;
}
static void plugin_free(void* ptr, size_t length) {
    (void)length;
    /* Response storage comes from C.CBytes, so no Go entry is necessary. */
    free(ptr);
}
static void plugin_shutdown(void) {
    bridge_call c = {0};
    isolated_call(enter_shutdown, &c);
}
int invoke_host(cliproxy_host_api* host, const char* method, const uint8_t* request, size_t length, cliproxy_buffer* response) {
    bridge_call c = {.host = host, .method = (char*)method, .request = (uint8_t*)request, .length = length, .response = response};
    isolated_call(enter_host, &c);
    return c.result;
}
void release_host(cliproxy_host_api* host, void* ptr, size_t length) {
    if (ptr != NULL) {
        bridge_call c = {.host = host, .buffer = ptr, .length = length};
        isolated_call(enter_host_free, &c);
    }
}
#else
int cliproxy_plugin_init(cliproxy_host_api* host, cliproxy_plugin_api* plugin) {
    return newapiPluginInit(host, plugin);
}
static int plugin_call(char* method, uint8_t* request, size_t length, cliproxy_buffer* response) {
    return cliproxyPluginCall(method, request, length, response);
}
static void plugin_free(void* ptr, size_t length) {
    cliproxyPluginFree(ptr, length);
}
static void plugin_shutdown(void) {
    cliproxyPluginShutdown();
}
int invoke_host(cliproxy_host_api* host, const char* method, const uint8_t* request, size_t length, cliproxy_buffer* response) {
    return host->call(host->host_ctx, method, request, length, response);
}
void release_host(cliproxy_host_api* host, void* ptr, size_t length) {
    if (ptr != NULL) {
        host->free_buffer(ptr, length);
    }
}
#endif

void set_plugin_api(cliproxy_plugin_api* plugin) {
    plugin->abi_version = 1;
    plugin->call = plugin_call;
    plugin->free_buffer = plugin_free;
    plugin->shutdown = plugin_shutdown;
}
