// An in-memory file system for the Go runtime in the tour's worker, so the
// compiler in nomi.wasm can read a project of files as it reads them from
// disk. A tour block that names modules (a `// FILE: go.mod` section, a
// generator's module under it) is written here by vmhost.LoadMarked and
// loaded from here: go.mod's requirements and each module's nomi.toml are
// files the front end reads with Go's `os` package, and a tool reads and
// writes the block's files here too.
//
// Go's js/wasm `os` calls the Node-style `globalThis.fs` (syscall/fs_js.go):
// each call takes a callback, which this file calls before it returns, so a
// Go call never waits on the event loop. wasm_exec.js installs a stub that
// answers ENOSYS to everything but writes to fds 1 and 2; this file replaces
// the stub's file calls and keeps its writes to fds 1 and 2 (run-worker.js
// reads fd 2). Load it after wasm_exec.js and before the module runs: Go
// reads `fs.constants` when it starts.
//
// Nothing here persists: the files live as long as the worker. A program's
// own std/fs calls never reach this file system; they reach the in-memory
// one vmhost gives each run (vmhost.WithFileSystem).
(() => {
  const fs = globalThis.fs;
  const stubWrite = fs.write.bind(fs);
  const S_IFDIR = 0o040000;
  const S_IFREG = 0o100000;
  const O = { O_WRONLY: 1, O_RDWR: 2, O_CREAT: 64, O_EXCL: 128, O_TRUNC: 512, O_APPEND: 1024, O_DIRECTORY: 65536 };

  // nodes holds every directory and file by absolute path: a directory is
  // { dir: true }, a file { data: Uint8Array }.
  const nodes = new Map([["/", { dir: true, ino: 1, mtime: Date.now() }]]);
  let nextIno = 2;
  // fds holds each open file: its path, position and flags.
  const fds = new Map();
  let nextFd = 100;
  let cwd = "/";

  const error = (code) => {
    const err = new Error(code);
    err.code = code;
    return err;
  };

  const normalize = (path) => {
    const parts = [];
    for (const part of (path.startsWith("/") ? path : cwd + "/" + path).split("/")) {
      if (part === "" || part === ".") continue;
      if (part === "..") parts.pop();
      else parts.push(part);
    }
    return "/" + parts.join("/");
  };
  const parent = (path) => (path === "/" ? "/" : path.slice(0, path.lastIndexOf("/")) || "/");

  const statOf = (node) => {
    const size = node.dir ? 0 : node.data.length;
    return {
      dev: 1,
      ino: node.ino,
      mode: node.dir ? S_IFDIR | 0o755 : S_IFREG | 0o644,
      nlink: 1,
      uid: 0,
      gid: 0,
      rdev: 0,
      size,
      blksize: 4096,
      blocks: Math.ceil(size / 512),
      atimeMs: node.mtime,
      mtimeMs: node.mtime,
      ctimeMs: node.mtime,
      isDirectory: () => !!node.dir,
    };
  };

  // call runs body and hands its answer, or the error it throws, to
  // callback.
  const call = (callback, body) => {
    let value;
    try {
      value = body();
    } catch (err) {
      if (err && typeof err.code === "string") {
        callback(err);
        return;
      }
      throw err;
    }
    callback(null, value);
  };

  const lookup = (path) => {
    const node = nodes.get(normalize(path));
    if (!node) throw error("ENOENT");
    return node;
  };
  const openFile = (fd) => {
    const f = fds.get(fd);
    if (!f) throw error("EBADF");
    return f;
  };
  const directory = (path) => {
    const node = nodes.get(path);
    if (!node) throw error("ENOENT");
    if (!node.dir) throw error("ENOTDIR");
  };
  const children = (path) => {
    const prefix = path === "/" ? "/" : path + "/";
    const names = [];
    for (const key of nodes.keys()) {
      if (key !== path && key.startsWith(prefix) && !key.slice(prefix.length).includes("/")) {
        names.push(key.slice(prefix.length));
      }
    }
    return names.sort();
  };

  Object.assign(fs, {
    constants: O,
    open(path, flags, mode, callback) {
      call(callback, () => {
        const p = normalize(path);
        let node = nodes.get(p);
        if (node && flags & O.O_CREAT && flags & O.O_EXCL) throw error("EEXIST");
        if (!node) {
          if (!(flags & O.O_CREAT)) throw error("ENOENT");
          directory(parent(p));
          node = { data: new Uint8Array(0), ino: nextIno++, mtime: Date.now() };
          nodes.set(p, node);
        }
        const writes = flags & (O.O_WRONLY | O.O_RDWR);
        if (node.dir && writes) throw error("EISDIR");
        if (flags & O.O_DIRECTORY && !node.dir) throw error("ENOTDIR");
        if (!node.dir && flags & O.O_TRUNC && writes) {
          node.data = new Uint8Array(0);
          node.mtime = Date.now();
        }
        const fd = nextFd++;
        fds.set(fd, { path: p, pos: 0, flags });
        return fd;
      });
    },
    close(fd, callback) {
      call(callback, () => {
        openFile(fd);
        fds.delete(fd);
      });
    },
    fstat(fd, callback) {
      call(callback, () => statOf(lookup(openFile(fd).path)));
    },
    stat(path, callback) {
      call(callback, () => statOf(lookup(path)));
    },
    lstat(path, callback) {
      call(callback, () => statOf(lookup(path)));
    },
    read(fd, buffer, offset, length, position, callback) {
      call(callback, () => {
        const f = openFile(fd);
        const node = lookup(f.path);
        if (node.dir) throw error("EISDIR");
        const at = position === null || position === undefined ? f.pos : position;
        const chunk = node.data.subarray(at, at + length);
        buffer.set(chunk, offset);
        if (position === null || position === undefined) f.pos += chunk.length;
        return chunk.length;
      });
    },
    write(fd, buffer, offset, length, position, callback) {
      if (fd === 1 || fd === 2) {
        stubWrite(fd, buffer, offset, length, position, callback);
        return;
      }
      call(callback, () => {
        const f = openFile(fd);
        const node = lookup(f.path);
        if (node.dir) throw error("EISDIR");
        let at = position === null || position === undefined ? f.pos : position;
        if (f.flags & O.O_APPEND) at = node.data.length;
        const end = at + length;
        if (end > node.data.length) {
          const grown = new Uint8Array(end);
          grown.set(node.data);
          node.data = grown;
        }
        node.data.set(buffer.subarray(offset, offset + length), at);
        node.mtime = Date.now();
        if (position === null || position === undefined) f.pos = end;
        return length;
      });
    },
    readdir(path, callback) {
      call(callback, () => {
        const p = normalize(path);
        directory(p);
        return children(p);
      });
    },
    mkdir(path, perm, callback) {
      call(callback, () => {
        const p = normalize(path);
        if (nodes.has(p)) throw error("EEXIST");
        directory(parent(p));
        nodes.set(p, { dir: true, ino: nextIno++, mtime: Date.now() });
      });
    },
    unlink(path, callback) {
      call(callback, () => {
        const p = normalize(path);
        if (lookup(p).dir) throw error("EISDIR");
        nodes.delete(p);
      });
    },
    rmdir(path, callback) {
      call(callback, () => {
        const p = normalize(path);
        directory(p);
        if (p === "/") throw error("EBUSY");
        if (children(p).length > 0) throw error("ENOTEMPTY");
        nodes.delete(p);
      });
    },
    rename(from, to, callback) {
      call(callback, () => {
        const a = normalize(from);
        const b = normalize(to);
        const node = lookup(a);
        directory(parent(b));
        if (node.dir) {
          for (const key of [...nodes.keys()]) {
            if (key === a || key.startsWith(a + "/")) {
              const moved = nodes.get(key);
              nodes.delete(key);
              nodes.set(b + key.slice(a.length), moved);
            }
          }
        } else {
          nodes.delete(a);
          nodes.set(b, node);
        }
      });
    },
    ftruncate(fd, length, callback) {
      call(callback, () => {
        const node = lookup(openFile(fd).path);
        const data = new Uint8Array(length);
        data.set(node.data.subarray(0, length));
        node.data = data;
      });
    },
    truncate(path, length, callback) {
      call(callback, () => {
        const node = lookup(path);
        const data = new Uint8Array(length);
        data.set(node.data.subarray(0, length));
        node.data = data;
      });
    },
    fsync(fd, callback) {
      callback(null);
    },
    chmod(path, mode, callback) {
      call(callback, () => void lookup(path));
    },
    fchmod(fd, mode, callback) {
      call(callback, () => void openFile(fd));
    },
    utimes(path, atime, mtime, callback) {
      call(callback, () => {
        lookup(path).mtime = mtime * 1000;
      });
    },
    readlink(path, callback) {
      call(callback, () => {
        lookup(path);
        throw error("EINVAL");
      });
    },
  });

  // os.Getwd and a relative path resolve against cwd.
  globalThis.process.cwd = () => cwd;
  globalThis.process.chdir = (path) => {
    const p = normalize(path);
    directory(p);
    cwd = p;
  };
})();
