//go:build !js

package goside

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// The scratch server: a throwaway Postgres cluster, one per process, that
// Scratch makes a fresh empty database on for each call. It runs from the
// initdb and postgres on PATH, in a temporary directory, on a free local
// port, with durability off (fsync, synchronous_commit and
// full_page_writes), since nothing on it outlives the process.
//
// The server is started under `sh`, which reads its standard input and
// stops the server and removes the directory when that input closes. The
// write end of the pipe is held by this process and closed by the operating
// system when it exits, however it exits, so no server outlives the
// program that started it. That needs a POSIX shell, so Windows has no
// scratch server.
var scratch struct {
	mu   sync.Mutex
	err  error
	port int
	// stop is the write end of the server's watchdog pipe. It is never
	// written or closed: holding it keeps the server running.
	stop  *os.File
	count int
}

// watchdog runs postgres in the background and stops it when standard
// input closes, then removes the cluster's directory.
const watchdog = `postgres -D "$1/data" -p "$2" -c listen_addresses=127.0.0.1 -c unix_socket_directories= ` +
	`-c fsync=off -c synchronous_commit=off -c full_page_writes=off >"$1/log" 2>&1 &
pid=$!
cat >/dev/null
kill -INT "$pid" 2>/dev/null
wait "$pid"
rm -rf "$1"`

// Scratch answers the URL of a fresh empty database on the process's
// scratch server, starting the server on first use.
func Scratch(ctx context.Context) (string, error) {
	scratch.mu.Lock()
	defer scratch.mu.Unlock()
	if scratch.err != nil {
		return "", scratch.err
	}
	if scratch.stop == nil {
		if err := startScratch(ctx); err != nil {
			scratch.err = err
			return "", err
		}
	}
	scratch.count++
	name := fmt.Sprintf("scratch_%d", scratch.count)
	admin, err := pgx.Connect(ctx, scratchURL("postgres"))
	if err != nil {
		return "", fmt.Errorf("connecting to the scratch server: %w", err)
	}
	defer admin.Close(context.Background())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return "", fmt.Errorf("creating the scratch database %s: %w", name, err)
	}
	return scratchURL(name), nil
}

func scratchURL(database string) string {
	return fmt.Sprintf("postgres://postgres@127.0.0.1:%d/%s?sslmode=disable", scratch.port, database)
}

// startScratch makes and starts the scratch cluster. The caller holds
// scratch.mu.
func startScratch(ctx context.Context) error {
	if runtime.GOOS == "windows" {
		return errors.New("postgres.scratch needs a POSIX shell to stop its server, and Windows has none")
	}
	for _, tool := range []string{"initdb", "postgres", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("postgres.scratch runs a throwaway server with initdb and postgres from PATH, and %s is not on PATH", tool)
		}
	}
	dir, err := os.MkdirTemp("", "nomi-postgres-")
	if err != nil {
		return err
	}
	initdb := exec.CommandContext(ctx, "initdb", "-D", filepath.Join(dir, "data"),
		"-A", "trust", "-U", "postgres", "-E", "UTF8", "--locale=C", "--no-sync")
	if out, err := initdb.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("initdb: %v\n%s", err, out)
	}
	port, err := freePort()
	if err != nil {
		os.RemoveAll(dir)
		return err
	}
	read, write, err := os.Pipe()
	if err != nil {
		os.RemoveAll(dir)
		return err
	}
	cmd := exec.Command("sh", "-c", watchdog, "sh", dir, fmt.Sprint(port))
	cmd.Stdin = read
	if err := cmd.Start(); err != nil {
		read.Close()
		write.Close()
		os.RemoveAll(dir)
		return err
	}
	read.Close()
	go cmd.Wait()
	scratch.port = port
	// Wait for the server to accept connections.
	deadline := time.Now().Add(30 * time.Second)
	for {
		conn, err := pgx.Connect(ctx, scratchURL("postgres"))
		if err == nil {
			conn.Close(context.Background())
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			write.Close()
			log, _ := os.ReadFile(filepath.Join(dir, "log"))
			return fmt.Errorf("the scratch server did not start: %v\n%s", err, log)
		}
		time.Sleep(50 * time.Millisecond)
	}
	scratch.stop = write
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
