#ifndef NEWAPI_CHECKIN_BRIDGE_H
#define NEWAPI_CHECKIN_BRIDGE_H

#include <stdint.h>
#include <stdlib.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);
typedef struct { uint32_t abi_version; void* host_ctx; cliproxy_host_call_fn call; cliproxy_host_free_fn free_buffer; } cliproxy_host_api;
typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; cliproxy_plugin_call_fn call; cliproxy_plugin_free_fn free_buffer; cliproxy_plugin_shutdown_fn shutdown; } cliproxy_plugin_api;

int newapiPluginInit(cliproxy_host_api*, cliproxy_plugin_api*);
int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
void cliproxyPluginFree(void*, size_t);
void cliproxyPluginShutdown(void);

#ifdef _WIN32
#define NEWAPI_EXPORT __declspec(dllexport)
#else
#define NEWAPI_EXPORT __attribute__((visibility("default")))
#endif
NEWAPI_EXPORT int cliproxy_plugin_init(cliproxy_host_api*, cliproxy_plugin_api*);
void set_plugin_api(cliproxy_plugin_api*);
int invoke_host(cliproxy_host_api*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
void release_host(cliproxy_host_api*, void*, size_t);

#endif
