// The C half of the package sqlite: SQLite's amalgamation compiled
// to WebAssembly with this file, which gives SQLite its operating system
// layer and the module its exports. The Nomi driver
// (internal/engine.nomi) calls the exports; the operating system layer
// calls the imports, which the driver answers with Nomi functions over std
// (internal/vfs.nomi). build.sh compiles it; README.md says how.
//
// Every export but alloc is called by std::wasm's `Instance.run`: alloc
// answers where the input goes, and the export reads it there, writes its
// answer into one buffer the shim owns, and returns where it is, as
// (address << 32 | length). The answer starts with SQLite's extended
// result code. A failure follows the code with SQLite's message and the
// byte of the SQL it is at (-1 for none); a success follows it with what
// the export answers. Numbers are little-endian. A string is its length
// (i32) and its bytes. A value is its storage class (u8, numbered as
// SQLite numbers them) and its payload: an INTEGER as an i64, a FLOAT as
// an f64, TEXT and BLOB as a string, and NULL as nothing.

#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "sqlite3.h"

#define IMPORT(name) __attribute__((import_module("vfs"), import_name(#name)))
#define EXPORT(name) __attribute__((export_name(#name)))

// --- Imports -------------------------------------------------------------

// A file is the number the driver gave it. open answers it, above 0, for a
// file opened to read and write, its negative for one opened only to read,
// and 0 for one that cannot be opened. A name of no bytes is a temporary
// file, deleted when it is closed.
IMPORT(open) int32_t vfs_open(const char *name, int32_t length, int32_t flags);
IMPORT(close) int32_t vfs_close(int32_t file);
// The number of bytes read into buf, fewer at the end of the file, or -1.
IMPORT(read) int32_t vfs_read(int32_t file, void *buf, int32_t amount, int64_t offset);
// 0, or -1 for a failure.
IMPORT(write) int32_t vfs_write(int32_t file, const void *buf, int32_t amount, int64_t offset);
IMPORT(truncate) int32_t vfs_truncate(int32_t file, int64_t size);
// flags: 1 is a full sync (PRAGMA fullfsync), 2 syncs the file's directory
// too.
IMPORT(sync) int32_t vfs_sync(int32_t file, int32_t flags);
// The file's size, or -1.
IMPORT(size) int64_t vfs_size(int32_t file);
// The file's SQLite lock moved from level up (lock) or down (unlock) to
// wanted: the level reached times 4, plus 0 when done, 1 when another
// handle's lock is in the way, and 2 for a failure. When lock takes the
// file from no lock to SHARED, its answer also holds the file's size plus
// 1, times 256 (0 for none).
IMPORT(lock) int64_t vfs_lock(int32_t file, int32_t level, int32_t wanted);
IMPORT(unlock) int32_t vfs_unlock(int32_t file, int32_t level, int32_t wanted);
// 1 when a handle holds the file's RESERVED lock, this one at level or
// another, 0 when none does, -1 for a failure.
IMPORT(reserved) int32_t vfs_reserved(int32_t file, int32_t level);
// 0 when removed, 1 when the file was not there, -1 for a failure.
IMPORT(delete) int32_t vfs_delete(const char *name, int32_t length, int32_t sync_dir);
// 1 when the file is there (and not empty, for flags 0; writable, for
// flags 1), 0 when not, -1 for a failure.
IMPORT(access) int32_t vfs_access(const char *name, int32_t length, int32_t flags);
IMPORT(random) int32_t vfs_random(void *buf, int32_t length);
IMPORT(sleep) int32_t vfs_sleep(int32_t microseconds);
// Milliseconds since the Unix epoch.
IMPORT(now) int64_t vfs_now(void);
// The UTC offset, in seconds, of the zone the machine is set to at a
// second since the Unix epoch, or INT32_MIN when the machine's zone has no
// name the driver can read.
IMPORT(local_offset) int32_t vfs_local_offset(int64_t seconds);
// 1 when the task whose call is running is stopping, which interrupts the
// statement: SQLite's progress handler.
IMPORT(stop) int32_t vfs_stop(void);

// --- The operating system layer -------------------------------------------

// The sector size SQLite's Unix layer reports, which SQLite pads a
// journal's header to.
#define SECTOR_SIZE 4096

// How much a read of a database's next page reads ahead: a scan reads its
// pages in order, and each read crosses to the driver once for this many
// bytes rather than once a page.
#define AHEAD (64 * 1024)

typedef struct {
  sqlite3_file base;
  int32_t fd;
  int level;    // the SQLite lock level held
  int journal;  // whether this is a rollback journal SQLite created
  int dir_synced;
  int main;     // whether this is a main database, which reads ahead
  // The bytes read ahead, from ahead_at, and where the last read ended. No
  // other connection writes the file while this one holds a lock on it,
  // so what was read ahead holds until this connection writes or lets go
  // of its lock.
  uint8_t *ahead;
  int64_t ahead_at;
  int32_t ahead_len;
  int64_t next;
  // The file's size, which lock answered as it took SHARED, when size is
  // above -1: no other handle changes it while this one holds a lock, and
  // this one's own write forgets it.
  int64_t size;
} File;

static void forget(File *f) {
  f->ahead_len = 0;
  f->size = -1;
}

static int xClose(sqlite3_file *p) {
  File *f = (File *)p;
  free(f->ahead);
  f->ahead = 0;
  int rc = vfs_close(f->fd);
  return rc == 0 ? SQLITE_OK : SQLITE_IOERR_CLOSE;
}

static int xRead(sqlite3_file *p, void *buf, int amount, sqlite3_int64 offset) {
  File *f = (File *)p;
  int64_t end = offset + amount;
  if (f->ahead_len > 0 && offset >= f->ahead_at && end <= f->ahead_at + f->ahead_len) {
    memcpy(buf, f->ahead + (offset - f->ahead_at), (size_t)amount);
    f->next = end;
    return SQLITE_OK;
  }
  int32_t n;
  if (f->main && f->level >= SQLITE_LOCK_SHARED && amount >= 512 && amount < AHEAD && offset == f->next) {
    if (!f->ahead && !(f->ahead = malloc(AHEAD))) return SQLITE_IOERR_NOMEM;
    int32_t got = vfs_read(f->fd, f->ahead, AHEAD, offset);
    if (got < 0) {
      forget(f);
      return SQLITE_IOERR_READ;
    }
    f->ahead_at = offset;
    f->ahead_len = got;
    n = got < amount ? got : amount;
    memcpy(buf, f->ahead, (size_t)n);
  } else {
    n = vfs_read(f->fd, buf, amount, offset);
    if (n < 0) return SQLITE_IOERR_READ;
  }
  f->next = end;
  if (n < amount) {
    memset((char *)buf + n, 0, (size_t)(amount - n));
    return SQLITE_IOERR_SHORT_READ;
  }
  return SQLITE_OK;
}

static int xWrite(sqlite3_file *p, const void *buf, int amount, sqlite3_int64 offset) {
  File *f = (File *)p;
  forget(f);
  return vfs_write(f->fd, buf, amount, offset) == 0 ? SQLITE_OK : SQLITE_IOERR_WRITE;
}

static int xTruncate(sqlite3_file *p, sqlite3_int64 size) {
  File *f = (File *)p;
  forget(f);
  return vfs_truncate(f->fd, size) == 0 ? SQLITE_OK : SQLITE_IOERR_TRUNCATE;
}

// xSync syncs as SQLite's Unix layer does: an ordinary sync, NORMAL with
// or without DATAONLY, is the driver's data sync (fdatasync, fsync on
// macOS), and only a FULL one, which PRAGMA fullfsync and
// checkpoint_fullfsync ask for, waits for the drive's own cache.
static int xSync(sqlite3_file *p, int flags) {
  File *f = (File *)p;
  int32_t how = (flags & 0x0F) == SQLITE_SYNC_FULL ? 1 : 0;
  // A journal SQLite created is in its directory once the directory is
  // synced, which is done once, at the journal's first sync.
  if (f->journal && !f->dir_synced) how |= 2;
  if (vfs_sync(f->fd, how) != 0) return SQLITE_IOERR_FSYNC;
  if (how & 2) f->dir_synced = 1;
  return SQLITE_OK;
}

static int xFileSize(sqlite3_file *p, sqlite3_int64 *size) {
  File *f = (File *)p;
  if (f->size >= 0 && f->level >= SQLITE_LOCK_SHARED) {
    *size = f->size;
    return SQLITE_OK;
  }
  int64_t n = vfs_size(f->fd);
  if (n < 0) return SQLITE_IOERR_FSTAT;
  *size = n;
  return SQLITE_OK;
}

// xLock and xUnlock move the file's SQLite lock level, which the shim
// keeps, through lock and unlock, which take SQLite's byte-range locks in
// vfs.nomi. Each answers the level it reached times 4, plus 0 for done, 1
// for another handle's lock in the way, and 2 for a failure.
static int moved(File *f, int64_t answer, int failure) {
  // What was read ahead under no lock, or before one, may have changed.
  if (f->level == SQLITE_LOCK_NONE || (answer & 0xff) >> 2 == SQLITE_LOCK_NONE) forget(f);
  if (answer >> 8 > 0) f->size = (answer >> 8) - 1;
  answer &= 0xff;
  f->level = (int)(answer >> 2);
  switch (answer & 3) {
  case 0:
    return SQLITE_OK;
  case 1:
    return SQLITE_BUSY;
  }
  return failure;
}

static int xLock(sqlite3_file *p, int level) {
  File *f = (File *)p;
  if (f->level >= level) return SQLITE_OK;
  return moved(f, vfs_lock(f->fd, f->level, level), SQLITE_IOERR_LOCK);
}

static int xUnlock(sqlite3_file *p, int level) {
  File *f = (File *)p;
  if (f->level <= level) return SQLITE_OK;
  return moved(f, vfs_unlock(f->fd, f->level, level), SQLITE_IOERR_UNLOCK);
}

static int xCheckReservedLock(sqlite3_file *p, int *out) {
  File *f = (File *)p;
  int32_t held = vfs_reserved(f->fd, f->level);
  if (held < 0) return SQLITE_IOERR_CHECKRESERVEDLOCK;
  *out = held;
  return SQLITE_OK;
}

static int xFileControl(sqlite3_file *p, int op, void *arg) {
  (void)p;
  if (op == SQLITE_FCNTL_LOCKSTATE) {
    *(int *)arg = ((File *)p)->level;
    return SQLITE_OK;
  }
  (void)arg;
  return SQLITE_NOTFOUND;
}

static int xSectorSize(sqlite3_file *p) {
  (void)p;
  return SECTOR_SIZE;
}

static int xDeviceCharacteristics(sqlite3_file *p) {
  (void)p;
  return SQLITE_IOCAP_POWERSAFE_OVERWRITE;
}

// No shared memory: WAL needs it, so SQLite keeps a rollback journal
// unless the connection's locking mode is EXCLUSIVE.
static const sqlite3_io_methods methods = {
    1,
    xClose,
    xRead,
    xWrite,
    xTruncate,
    xSync,
    xFileSize,
    xLock,
    xUnlock,
    xCheckReservedLock,
    xFileControl,
    xSectorSize,
    xDeviceCharacteristics,
};

static int vOpen(sqlite3_vfs *vfs, sqlite3_filename name, sqlite3_file *p, int flags, int *out) {
  (void)vfs;
  File *f = (File *)p;
  memset(f, 0, sizeof *f);
  int32_t fd = vfs_open(name ? name : "", name ? (int32_t)strlen(name) : 0, flags);
  if (fd == 0) return SQLITE_CANTOPEN;
  int ro = fd < 0;
  f->fd = ro ? -fd : fd;
  f->journal = (flags & SQLITE_OPEN_MAIN_JOURNAL) != 0;
  f->main = (flags & SQLITE_OPEN_MAIN_DB) != 0;
  f->next = -1;
  f->size = -1;
  f->base.pMethods = &methods;
  if (out) {
    *out = ro ? (flags & ~(SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE)) | SQLITE_OPEN_READONLY : flags;
  }
  return SQLITE_OK;
}

static int vDelete(sqlite3_vfs *vfs, const char *name, int sync_dir) {
  (void)vfs;
  switch (vfs_delete(name, (int32_t)strlen(name), sync_dir)) {
  case 0:
    return SQLITE_OK;
  case 1:
    return SQLITE_IOERR_DELETE_NOENT;
  }
  return SQLITE_IOERR_DELETE;
}

static int vAccess(sqlite3_vfs *vfs, const char *name, int flags, int *out) {
  (void)vfs;
  int32_t got = vfs_access(name, (int32_t)strlen(name), flags == SQLITE_ACCESS_EXISTS ? 0 : 1);
  if (got < 0) return SQLITE_IOERR_ACCESS;
  *out = got;
  return SQLITE_OK;
}

// A name is the path as the program wrote it: the driver's std::fs calls
// resolve it, against the directory a `fs.within` confines them to when
// there is one.
static int vFullPathname(sqlite3_vfs *vfs, const char *name, int n, char *out) {
  (void)vfs;
  size_t len = strlen(name);
  if ((int)len >= n) return SQLITE_CANTOPEN;
  memcpy(out, name, len + 1);
  return SQLITE_OK;
}

static int vRandomness(sqlite3_vfs *vfs, int n, char *out) {
  (void)vfs;
  return vfs_random(out, n) == 0 ? n : 0;
}

static int vSleep(sqlite3_vfs *vfs, int microseconds) {
  (void)vfs;
  vfs_sleep(microseconds);
  return microseconds;
}

// The Julian day number of the Unix epoch, in milliseconds.
#define UNIX_EPOCH_JD_MS ((sqlite3_int64)210866760000000)

static int vCurrentTimeInt64(sqlite3_vfs *vfs, sqlite3_int64 *out) {
  (void)vfs;
  *out = UNIX_EPOCH_JD_MS + vfs_now();
  return SQLITE_OK;
}

static int vCurrentTime(sqlite3_vfs *vfs, double *out) {
  sqlite3_int64 ms;
  vCurrentTimeInt64(vfs, &ms);
  *out = ms / 86400000.0;
  return SQLITE_OK;
}

// SQLite's localtime and utc modifiers read the machine's local time with
// localtime_r, which build.sh names this: WASI's C library has no time
// zone and reads every one as UTC. The local time is the UTC one moved by
// the offset the driver answers. A machine whose zone cannot be read
// fails the call, and SQLite reports "local time unavailable".
struct tm *nomi_localtime_r(const time_t *t, struct tm *tm) {
  int32_t offset = vfs_local_offset((int64_t)*t);
  if (offset == INT32_MIN) return 0;
  time_t local = *t + offset;
  return gmtime_r(&local, tm);
}

static int vGetLastError(sqlite3_vfs *vfs, int n, char *out) {
  (void)vfs;
  (void)n;
  (void)out;
  return 0;
}

static sqlite3_vfs nomi_vfs = {
    2,                 // iVersion
    sizeof(File),      // szOsFile
    4096,              // mxPathname
    0,                 // pNext
    "nomi",            // zName
    0,                 // pAppData
    vOpen,             //
    vDelete,           //
    vAccess,           //
    vFullPathname,     //
    0, 0, 0, 0,        // no loadable extensions
    vRandomness,       //
    vSleep,            //
    vCurrentTime,      //
    vGetLastError,     //
    vCurrentTimeInt64, //
};

int sqlite3_os_init(void) { return sqlite3_vfs_register(&nomi_vfs, 1); }

int sqlite3_os_end(void) { return SQLITE_OK; }

// --- The answer buffer ---------------------------------------------------

static uint8_t *out;
static size_t out_len, out_cap;

static void room(size_t n) {
  if (out_len + n <= out_cap) return;
  size_t cap = out_cap ? out_cap : 4096;
  while (cap < out_len + n) cap *= 2;
  uint8_t *grown = realloc(out, cap);
  if (!grown) abort();
  out = grown;
  out_cap = cap;
}

static void put(const void *p, size_t n) {
  room(n);
  memcpy(out + out_len, p, n);
  out_len += n;
}

static void put_u8(uint8_t v) { put(&v, 1); }
static void put_i32(int32_t v) { put(&v, 4); }
static void put_i64(int64_t v) { put(&v, 8); }
static void put_f64(double v) { put(&v, 8); }

static void put_str(const void *p, int n) {
  if (!p) n = 0;
  put_i32(n);
  if (n > 0) put(p, (size_t)n);
}

static void put_cstr(const char *s) { put_str(s, s ? (int)strlen(s) : 0); }

static int64_t answer(void) {
  return (int64_t)(((uint64_t)(uintptr_t)out << 32) | (uint64_t)out_len);
}

static void begin(int rc) {
  out_len = 0;
  put_i32(rc);
}

static int64_t failed(sqlite3 *db, int rc, int at) {
  begin(rc);
  put_cstr(db ? sqlite3_errmsg(db) : sqlite3_errstr(rc));
  put_i32(at);
  return answer();
}

// --- Input ---------------------------------------------------------------

static uint8_t *in;
static size_t in_cap;

// alloc answers where n bytes of input may be written. The memory is the
// shim's, used again by the next call.
EXPORT(alloc) uint8_t *alloc(int32_t n) {
  if ((size_t)n > in_cap) {
    size_t cap = in_cap ? in_cap : 4096;
    while (cap < (size_t)n) cap *= 2;
    uint8_t *grown = realloc(in, cap);
    if (!grown) return 0;
    in = grown;
    in_cap = cap;
  }
  return in;
}

static int32_t get_i32(const uint8_t **p) {
  int32_t v;
  memcpy(&v, *p, 4);
  *p += 4;
  return v;
}

static int64_t get_i64(const uint8_t **p) {
  int64_t v;
  memcpy(&v, *p, 8);
  *p += 8;
  return v;
}

// --- Connections ---------------------------------------------------------

// The number of virtual machine steps between two calls of the progress
// handler, which asks whether the task is stopping.
#define PROGRESS_STEPS 10000

static int progress(void *arg) {
  (void)arg;
  return vfs_stop();
}

// open opens the database its input names, creating it, and answers the
// connection (an i32). A name may be a `file:` URI.
EXPORT(open) int64_t open_db(const uint8_t *at, int32_t length) {
  char *name = malloc((size_t)length + 1);
  if (!name) return failed(0, SQLITE_NOMEM, -1);
  memcpy(name, at, (size_t)length);
  name[length] = 0;
  sqlite3 *db = 0;
  int rc = sqlite3_open_v2(name, &db, SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE | SQLITE_OPEN_URI, 0);
  free(name);
  if (rc != SQLITE_OK) {
    int64_t a = failed(db, db ? sqlite3_extended_errcode(db) : rc, -1);
    sqlite3_close_v2(db);
    return a;
  }
  sqlite3_extended_result_codes(db, 1);
  sqlite3_progress_handler(db, PROGRESS_STEPS, progress, 0);
  begin(SQLITE_OK);
  put_i32((int32_t)(uintptr_t)db);
  return answer();
}

// close closes the connection its input names (an i32), finalizing any
// statement still open on it, and answers the code.
EXPORT(close) int64_t close_db(const uint8_t *at, int32_t length) {
  (void)length;
  int32_t db = get_i32(&at);
  begin(sqlite3_close_v2((sqlite3 *)(uintptr_t)db));
  return answer();
}

// --- Statements ----------------------------------------------------------

// A statement's input: the connection (an i32), the SQL (a string) and
// the parameters (an i32 count, then each a value), bound as ?1, ?2, ...
// to every statement the SQL holds.
typedef struct {
  sqlite3 *db;
  const char *sql;
  int sql_len;
  const uint8_t *params;
  int nparams;
} Input;

static Input read_input(const uint8_t *p) {
  Input i;
  i.db = (sqlite3 *)(uintptr_t)get_i32(&p);
  i.sql_len = get_i32(&p);
  i.sql = (const char *)p;
  p += i.sql_len;
  i.nparams = get_i32(&p);
  i.params = p;
  return i;
}

static int bind(sqlite3_stmt *stmt, const Input *i) {
  int n = sqlite3_bind_parameter_count(stmt);
  const uint8_t *p = i->params;
  for (int k = 1; k <= i->nparams; k++) {
    int type = *p++;
    int rc = SQLITE_OK;
    switch (type) {
    case SQLITE_INTEGER: {
      int64_t v = get_i64(&p);
      if (k <= n) rc = sqlite3_bind_int64(stmt, k, v);
      break;
    }
    case SQLITE_FLOAT: {
      double v;
      memcpy(&v, p, 8);
      p += 8;
      if (k <= n) rc = sqlite3_bind_double(stmt, k, v);
      break;
    }
    case SQLITE_TEXT:
    case SQLITE_BLOB: {
      int32_t len = get_i32(&p);
      if (k <= n) {
        rc = type == SQLITE_TEXT
                 ? sqlite3_bind_text(stmt, k, (const char *)p, len, SQLITE_TRANSIENT)
                 : sqlite3_bind_blob(stmt, k, len ? (const void *)p : "", len, SQLITE_TRANSIENT);
      }
      p += len;
      break;
    }
    default:
      if (k <= n) rc = sqlite3_bind_null(stmt, k);
    }
    if (rc != SQLITE_OK) return rc;
  }
  return SQLITE_OK;
}

static void put_columns(sqlite3_stmt *stmt) {
  int n = stmt ? sqlite3_column_count(stmt) : 0;
  put_i32(n);
  for (int c = 0; c < n; c++) {
    put_cstr(sqlite3_column_name(stmt, c));
    put_cstr(sqlite3_column_decltype(stmt, c));
  }
}

static void put_row(sqlite3_stmt *stmt) {
  int n = sqlite3_column_count(stmt);
  for (int c = 0; c < n; c++) {
    int type = sqlite3_column_type(stmt, c);
    put_u8((uint8_t)type);
    switch (type) {
    case SQLITE_INTEGER:
      put_i64(sqlite3_column_int64(stmt, c));
      break;
    case SQLITE_FLOAT:
      put_f64(sqlite3_column_double(stmt, c));
      break;
    case SQLITE_TEXT: {
      const unsigned char *t = sqlite3_column_text(stmt, c);
      put_str(t, sqlite3_column_bytes(stmt, c));
      break;
    }
    case SQLITE_BLOB: {
      const void *b = sqlite3_column_blob(stmt, c);
      put_str(b, sqlite3_column_bytes(stmt, c));
      break;
    }
    }
  }
}

// skip passes space, comments and semicolons: what is left when a text
// holds no more statements.
static const char *skip(const char *p, const char *end) {
  while (p < end) {
    char c = *p;
    if (c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == ';') {
      p++;
    } else if (c == '-' && p + 1 < end && p[1] == '-') {
      while (p < end && *p != '\n') p++;
    } else if (c == '/' && p + 1 < end && p[1] == '*') {
      p += 2;
      while (p + 1 < end && !(p[0] == '*' && p[1] == '/')) p++;
      p = p + 1 < end ? p + 2 : end;
    } else {
      break;
    }
  }
  return p;
}

// The statements of an input run in turn, each but the last to its end
// before the next is prepared, since an earlier one may make the table a
// later one names. The last is prepared and bound for the caller to step.
typedef struct {
  sqlite3_stmt *last;
  int last_at; // the byte the last statement starts at
  int rc;
  int at; // the byte the failure is at, or -1
} Prepared;

static Prepared prepare_all(const Input *i) {
  Prepared p = {0, 0, SQLITE_OK, -1};
  const char *end = i->sql + i->sql_len;
  const char *cur = skip(i->sql, end);
  while (cur < end) {
    sqlite3_stmt *stmt = 0;
    const char *tail = 0;
    int start = (int)(cur - i->sql);
    int rc = sqlite3_prepare_v2(i->db, cur, (int)(end - cur), &stmt, &tail);
    if (rc != SQLITE_OK) {
      int offset = sqlite3_error_offset(i->db);
      p.rc = sqlite3_extended_errcode(i->db);
      p.at = offset >= 0 ? start + offset : -1;
      return p;
    }
    const char *next = skip(tail && tail > cur ? tail : end, end);
    if (!stmt) {
      cur = next;
      continue;
    }
    if ((rc = bind(stmt, i)) != SQLITE_OK) {
      sqlite3_finalize(stmt);
      p.rc = rc;
      p.at = start;
      return p;
    }
    if (next >= end) {
      p.last = stmt;
      p.last_at = start;
      return p;
    }
    while ((rc = sqlite3_step(stmt)) == SQLITE_ROW) {
    }
    sqlite3_finalize(stmt);
    if (rc != SQLITE_DONE) {
      p.rc = sqlite3_extended_errcode(i->db);
      p.at = start;
      return p;
    }
    cur = next;
  }
  return p;
}

// exec runs the statements of its input and answers, after the code, the
// rows the connection's last statement changed (i64), the columns of the
// last statement (an i32 count, then each its name and declared type, as
// strings), and its rows (an i32 count, then each row's values).
EXPORT(exec) int64_t exec(const uint8_t *at, int32_t length) {
  (void)length;
  Input i = read_input(at);
  Prepared p = prepare_all(&i);
  if (p.rc != SQLITE_OK) return failed(i.db, p.rc, p.at);
  begin(SQLITE_OK);
  size_t changes_at = out_len;
  put_i64(0);
  put_columns(p.last);
  size_t count_at = out_len;
  put_i32(0);
  int32_t rows = 0;
  if (p.last) {
    int rc;
    while ((rc = sqlite3_step(p.last)) == SQLITE_ROW) {
      put_row(p.last);
      rows++;
    }
    sqlite3_finalize(p.last);
    if (rc != SQLITE_DONE) return failed(i.db, sqlite3_extended_errcode(i.db), p.last_at);
  }
  int64_t changes = sqlite3_changes64(i.db);
  memcpy(out + changes_at, &changes, 8);
  memcpy(out + count_at, &rows, 4);
  return answer();
}

// prepare runs the statements of its input but the last, and prepares the
// last, answering after the code the statement (an i32, 0 for none) and
// its columns, as exec writes them. step steps it.
EXPORT(prepare) int64_t prepare(const uint8_t *at, int32_t length) {
  (void)length;
  Input i = read_input(at);
  Prepared p = prepare_all(&i);
  if (p.rc != SQLITE_OK) return failed(i.db, p.rc, p.at);
  begin(SQLITE_OK);
  put_i32((int32_t)(uintptr_t)p.last);
  put_columns(p.last);
  return answer();
}

// step takes a statement and a number of rows (i32s) and answers, after
// the code, whether the statement has ended (u8), then up to that many of
// its rows, as exec writes them. finalize ends the statement, whether or
// not step ended it.
EXPORT(step) int64_t step(const uint8_t *at, int32_t length) {
  (void)length;
  sqlite3_stmt *stmt = (sqlite3_stmt *)(uintptr_t)get_i32(&at);
  int32_t max = get_i32(&at);
  sqlite3 *db = sqlite3_db_handle(stmt);
  begin(SQLITE_OK);
  size_t ended_at = out_len;
  put_u8(0);
  size_t count_at = out_len;
  put_i32(0);
  int32_t rows = 0;
  uint8_t ended = 0;
  while (rows < max) {
    int rc = sqlite3_step(stmt);
    if (rc == SQLITE_ROW) {
      put_row(stmt);
      rows++;
      continue;
    }
    if (rc != SQLITE_DONE) return failed(db, sqlite3_extended_errcode(db), -1);
    ended = 1;
    break;
  }
  memcpy(out + ended_at, &ended, 1);
  memcpy(out + count_at, &rows, 4);
  return answer();
}

// finalize ends the statement its input names (an i32) and answers the
// code.
EXPORT(finalize) int64_t finalize(const uint8_t *at, int32_t length) {
  (void)length;
  sqlite3_stmt *stmt = (sqlite3_stmt *)(uintptr_t)get_i32(&at);
  sqlite3_finalize(stmt);
  begin(SQLITE_OK);
  return answer();
}

// describe takes a connection (an i32) and SQL (the rest of the input),
// prepares the SQL's first statement, and answers, after the code, whether
// more statements follow it (u8), its parameter count (i32) and its
// columns: an i32 count, then each column's name, declared type, table
// and column in that table (strings), and whether that table column is
// NOT NULL or an INTEGER PRIMARY KEY (u8).
EXPORT(describe) int64_t describe(const uint8_t *at, int32_t length) {
  sqlite3 *db = (sqlite3 *)(uintptr_t)get_i32(&at);
  const char *sql = (const char *)at, *end = sql + length - 4;
  const char *cur = skip(sql, end);
  sqlite3_stmt *stmt = 0;
  const char *tail = 0;
  int start = (int)(cur - sql);
  int rc = sqlite3_prepare_v2(db, cur, (int)(end - cur), &stmt, &tail);
  if (rc != SQLITE_OK) {
    int offset = sqlite3_error_offset(db);
    return failed(db, sqlite3_extended_errcode(db), offset >= 0 ? start + offset : -1);
  }
  begin(SQLITE_OK);
  put_u8(stmt && skip(tail ? tail : end, end) < end);
  put_i32(stmt ? sqlite3_bind_parameter_count(stmt) : 0);
  int n = stmt ? sqlite3_column_count(stmt) : 0;
  put_i32(n);
  for (int c = 0; c < n; c++) {
    const char *table = sqlite3_column_table_name(stmt, c);
    const char *origin = sqlite3_column_origin_name(stmt, c);
    put_cstr(sqlite3_column_name(stmt, c));
    put_cstr(sqlite3_column_decltype(stmt, c));
    put_cstr(table);
    put_cstr(origin);
    uint8_t not_null = 0;
    if (table && *table && origin && *origin) {
      const char *declared = 0;
      int nn = 0, pk = 0;
      if (sqlite3_table_column_metadata(db, 0, table, origin, &declared, 0, &nn, &pk, 0) == SQLITE_OK) {
        not_null = nn || (pk && declared && sqlite3_stricmp(declared, "INTEGER") == 0);
      }
    }
    put_u8(not_null);
  }
  sqlite3_finalize(stmt);
  return answer();
}
