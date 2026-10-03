/* ebuild worker public ABI — stable C interface consumed via CGO.
 *
 * SPDX-License-Identifier: Apache-2.0
 *
 * Design goals:
 *  - POD-only types across the ABI boundary (no C struct layout surprises in Go).
 *  - Every fallible function returns ebuild_status_t; human text via ebuild_strerror().
 *  - Opaque handles: ebuild_worker_t, ebuild_fsmon_t, ebuild_lua_t.
 *  - No exceptions, no globals (except thread-local error slot), no hidden allocs:
 *    any buffer the C side allocates is released with an explicit _free/_destroy call.
 *
 * Build:
 *   cc -std=c11 -O2 -Wall -Wextra -pthread -I. -c internal/daemon/worker.c
 *   Optional deps (auto-detected, gracefully degraded if absent):
 *     - lua5.4  (lua.h)      -> bytecode sandbox; without it Lua calls return EBUILD_ERR_NOLUA
 *     - libblake3 (blake3.h) -> SIMD BLAKE3; without it a bundled SHA-256 is used for CAS
 */
#ifndef EBUILD_WORKER_H
#define EBUILD_WORKER_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* ------------------------------------------------------------------ version */
#define EBUILD_VERSION_MAJOR 0
#define EBUILD_VERSION_MINOR 1
#define EBUILD_VERSION_PATCH 0
#define EBUILD_PATH_MAX 1024
#define EBUILD_HASH_LEN 32
#define EBUILD_ERRBUF 256

#if defined(_WIN32) || defined(__CYGWIN__)
#  if defined(EBUILD_BUILDING_DLL)
#    define EBUILD_API __declspec(dllexport)
#  else
#    define EBUILD_API __declspec(dllimport)
#  endif
#else
#  define EBUILD_API __attribute__((visibility("default")))
#endif

/* ------------------------------------------------------------------ status */
typedef enum ebuild_status {
    EBUILD_OK            = 0,
    EBUILD_ERR_INVAL     = -1,  /* bad argument */
    EBUILD_ERR_NOMEM     = -2,  /* allocation failure */
    EBUILD_ERR_IO        = -3,  /* filesystem / watcher I/O failure */
    EBUILD_ERR_NOSYS     = -4,  /* unsupported on this platform */
    EBUILD_ERR_BUSY      = -5,  /* queue full / would block */
    EBUILD_ERR_EMPTY     = -6,  /* queue empty / timeout */
    EBUILD_ERR_NOLUA     = -7,  /* Lua runtime not linked in */
    EBUILD_ERR_LUA       = -8,  /* Lua compile/runtime/sandbox violation */
    EBUILD_ERR_HASH      = -9,  /* hashing failure */
    EBUILD_ERR_TOOLARGE  = -10, /* input exceeds configured limit */
    EBUILD_ERR_STOPPED   = -11  /* daemon is shutting down */
} ebuild_status_t;

/* ------------------------------------------------------------------ events */
typedef enum ebuild_event_kind {
    EBUILD_EV_NONE   = 0,
    EBUILD_EV_CREATE = 1,
    EBUILD_EV_MODIFY = 2,
    EBUILD_EV_DELETE = 3,
    EBUILD_EV_RENAME = 4,
    EBUILD_EV_ATTRIB = 5,
    EBUILD_EV_OVERFLOW = 6   /* kernel dropped events; caller must rescan */
} ebuild_event_kind_t;

typedef struct ebuild_event {
    uint32_t kind;                       /* ebuild_event_kind_t */
    uint32_t cookie;                     /* rename correlation id (inotify) */
    uint64_t size;                       /* best-effort file size, UINT64_MAX if unknown */
    uint64_t mtime_ns;                   /* best-effort mtime, 0 if unknown */
    unsigned char hash[EBUILD_HASH_LEN]; /* content hash when kind==MODIFY/CREATE and hashing enabled, else zero */
    char path[EBUILD_PATH_MAX];          /* UTF-8, NUL-terminated, OS-native separators */
} ebuild_event_t;

/* ------------------------------------------------------------------ tasks */
typedef enum ebuild_lang {
    EBUILD_LANG_UNKNOWN = 0,
    EBUILD_LANG_C, EBUILD_LANG_CXX, EBUILD_LANG_RUST, EBUILD_LANG_GO,
    EBUILD_LANG_JAVA, EBUILD_LANG_KOTLIN, EBUILD_LANG_CS, EBUILD_LANG_SWIFT,
    EBUILD_LANG_JS, EBUILD_LANG_TS, EBUILD_LANG_PYTHON, EBUILD_LANG_PHP,
    EBUILD_LANG_PERL, EBUILD_LANG_R, EBUILD_LANG_LUA, EBUILD_LANG_FORTRAN,
    EBUILD_LANG_ADA, EBUILD_LANG_MATLAB, EBUILD_LANG_COUNT
} ebuild_lang_t;

typedef struct ebuild_task {
    uint64_t id;                         /* assigned by Go scheduler; 0 = auto-assign */
    uint32_t lang;                       /* ebuild_lang_t */
    uint32_t op;                         /* 0=build, 1=test, 2=lint, 3=custom */
    char path[EBUILD_PATH_MAX];          /* source file or directory */
    char args[EBUILD_PATH_MAX];          /* opaque extra args forwarded to plan */
} ebuild_task_t;

/* ------------------------------------------------------------------ plans */
typedef struct ebuild_plan_action {
    char rule[64];                       /* e.g. "cc", "rustc", "go-build" */
    char target[EBUILD_PATH_MAX];        /* output artifact */
    char workdir[EBUILD_PATH_MAX];       /* cwd for the action */
    char argv[2048];                     /* command line, NUL-terminated */
    unsigned char inputs_hash[EBUILD_HASH_LEN];
} ebuild_plan_action_t;

typedef struct ebuild_plan {
    uint32_t nactions;
    uint32_t cap;                        /* internal */
    ebuild_plan_action_t *actions;       /* malloc'd; free with ebuild_plan_free() */
} ebuild_plan_t;

typedef struct ebuild_worker_config {
    char root[EBUILD_PATH_MAX];          /* workspace root to watch */
    uint32_t num_threads;                /* 0 = auto (min(cpu, 64)) */
    uint32_t queue_depth;                /* power of two recommended, e.g. 4096 */
    uint32_t hash_on_event;              /* 1 = hash file content on CREATE/MODIFY */
    uint32_t recursive_watch;            /* 1 = watch subdirectories recursively */
    uint64_t lua_mem_limit;              /* bytes, e.g. 64<<20; 0 = default */
    uint64_t lua_instr_limit;            /* e.g. 10M; 0 = default */
} ebuild_worker_config_t;

typedef struct ebuild_worker ebuild_worker_t;
typedef struct ebuild_fsmon ebuild_fsmon_t;
typedef struct ebuild_lua ebuild_lua_t;

/* ------------------------------------------------------------------ misc */
EBUILD_API const char *ebuild_version(void);
EBUILD_API const char *ebuild_strerror(ebuild_status_t st);
EBUILD_API uint32_t ebuild_infer_language(const char *path);
EBUILD_API ebuild_status_t ebuild_hash_file(const char *path,
                                            unsigned char out[EBUILD_HASH_LEN]);
EBUILD_API ebuild_status_t ebuild_hash_bytes(const void *data, size_t len,
                                             unsigned char out[EBUILD_HASH_LEN]);
EBUILD_API void ebuild_hex(const unsigned char hash[EBUILD_HASH_LEN],
                           char out[2 * EBUILD_HASH_LEN + 1]);

/* ------------------------------------------------------------------ fsmon */
EBUILD_API ebuild_status_t ebuild_fsmon_create(const char *root, int recursive,
                                               uint32_t depth, ebuild_fsmon_t **out);
EBUILD_API ebuild_status_t ebuild_fsmon_add(ebuild_fsmon_t *m, const char *path);
EBUILD_API ebuild_status_t ebuild_fsmon_poll(ebuild_fsmon_t *m, ebuild_event_t *out,
                                             int timeout_ms);
EBUILD_API void ebuild_fsmon_destroy(ebuild_fsmon_t *m);

/* ------------------------------------------------------------------ lua */
EBUILD_API ebuild_status_t ebuild_lua_open(uint64_t mem_limit, uint64_t instr_limit,
                                           ebuild_lua_t **out);
/* Evaluate precompiled Lua *bytecode* (mode "b") with a `delta` global set from
 * `delta_src` (descriptive string, e.g. changed path list). On success the
 * chunk must return a table of actions; parsed into *plan (caller frees). */
EBUILD_API ebuild_status_t ebuild_lua_eval(ebuild_lua_t *L, const void *bytecode,
                                           size_t bc_len, const char *delta_src,
                                           ebuild_plan_t *plan);
EBUILD_API void ebuild_lua_close(ebuild_lua_t *L);

/* ------------------------------------------------------------------ plan */
EBUILD_API void ebuild_plan_free(ebuild_plan_t *plan);

/* ------------------------------------------------------------------ daemon */
EBUILD_API ebuild_status_t ebuild_worker_create(const ebuild_worker_config_t *cfg,
                                                ebuild_worker_t **out);
EBUILD_API ebuild_status_t ebuild_worker_start(ebuild_worker_t *w);
EBUILD_API ebuild_status_t ebuild_worker_push(ebuild_worker_t *w,
                                              const ebuild_task_t *task);
EBUILD_API ebuild_status_t ebuild_worker_poll_event(ebuild_worker_t *w,
                                                    ebuild_event_t *out,
                                                    int timeout_ms);
EBUILD_API ebuild_status_t ebuild_worker_stop(ebuild_worker_t *w);
EBUILD_API void ebuild_worker_destroy(ebuild_worker_t *w);

#ifdef __cplusplus
}
#endif

#endif /* EBUILD_WORKER_H */
