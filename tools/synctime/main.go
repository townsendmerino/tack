// Command synctime measures what a session-log sync costs on this disk while goinfer writes a large cache file
// (ADR-004's open item). It starts goinfer-serve on a model that has no cache yet, reached through a symlink in a
// scratch directory so the new cache is written there, and while that write runs it times 300 syncs per method, each
// after a 1 KiB append, interleaved: F_FULLFSYNC (Go's File.Sync on darwin), fsync(2) and F_BARRIERFSYNC. It reports
// p50, p99 and max per method, how many samples fell inside the cache write, and where the cache was written.
//
// It loads the machine, so it runs through the night queue (CLAUDE.md, run budget):
//
//	go run ./tools/synctime -goinfer-serve BIN -model ~/models/gemma-2-2b-it-Q8_0.gguf -dir ~/tack-runs/synctime
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const fBarrierFsync = 85 // <sys/fcntl.h> F_BARRIERFSYNC

type method struct {
	name string
	do   func(*os.File) error
}

func main() {
	serve := flag.String("goinfer-serve", "", "goinfer-serve binary")
	model := flag.String("model", "", "a .gguf in ~/models with no cache yet for -backend")
	backend := flag.String("backend", "metal", "goinfer backend")
	dir := flag.String("dir", "", "scratch directory on the disk to measure (durable; results go here too)")
	n := flag.Int("n", 300, "syncs per method")
	flag.Parse()
	if *serve == "" || *model == "" || *dir == "" || runtime.GOOS != "darwin" {
		fmt.Fprintln(os.Stderr, "usage (darwin): synctime -goinfer-serve BIN -model FILE.gguf -dir DIR [-backend metal] [-n 300]")
		os.Exit(2)
	}
	if err := run(*serve, *model, *backend, *dir, *n); err != nil {
		fmt.Fprintln(os.Stderr, "synctime:", err)
		os.Exit(1)
	}
}

func run(serve, model, backend, dir string, n int) error {
	work, err := os.MkdirTemp(dir, "load-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work) // the cache goinfer writes beside the symlink goes with it
	link := filepath.Join(work, filepath.Base(model))
	if err := os.Symlink(model, link); err != nil {
		return err
	}
	modelDirBefore := giwFiles(filepath.Dir(model))

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	addr := l.Addr().String()
	l.Close()
	logPath := filepath.Join(dir, "goinfer-serve.log")
	logf, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logf.Close()
	pr, pw, _ := os.Pipe()
	cmd := exec.Command(serve, "-backend", backend, "-model", link, "-addr", addr)
	cmd.Stdout, cmd.Stderr = pw, pw
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pw.Close()
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() }()

	// The cache write is under way from goinfer's "transcoding" line until "cache ready" (or the server serving).
	var writing, writeDone atomic.Bool
	started := make(chan struct{})
	var startOnce, doneAt atomic.Int64
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			line := sc.Text()
			fmt.Fprintln(logf, line)
			switch {
			case strings.Contains(line, "transcoding") && !writing.Load():
				writing.Store(true)
				startOnce.Store(time.Now().UnixNano())
				close(started)
			case writing.Load() && (strings.Contains(line, "cache ready") || strings.Contains(line, "goinfer serving")):
				if !writeDone.Load() {
					writeDone.Store(true)
					doneAt.Store(time.Now().UnixNano())
				}
			}
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Minute):
		return fmt.Errorf("goinfer never started writing a cache in 3 min (does %s already have one? see %s)", model, logPath)
	}

	f, err := os.Create(filepath.Join(dir, "synctime.data"))
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	rec := make([]byte, 1024)
	for i := range rec {
		rec[i] = 'x'
	}
	rec[len(rec)-1] = '\n'
	methods := []method{
		{"F_FULLFSYNC (File.Sync)", (*os.File).Sync},
		{"fsync(2)", func(f *os.File) error { return syscall.Fsync(int(f.Fd())) }},
		{"F_BARRIERFSYNC", func(f *os.File) error {
			if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), fBarrierFsync, 0); e != 0 {
				return e
			}
			return nil
		}},
	}
	times := make([][]time.Duration, len(methods))
	during := 0
	t0 := time.Now()
	for i := 0; i < n; i++ {
		for m := range methods {
			if _, err := f.Write(rec); err != nil {
				return err
			}
			s := time.Now()
			if err := methods[m].do(f); err != nil {
				return err
			}
			times[m] = append(times[m], time.Since(s))
			if !writeDone.Load() {
				during++
			}
		}
	}
	elapsed := time.Since(t0)

	out := map[string]any{"model": model, "backend": backend, "samples_per_method": n, "elapsed_ms": elapsed.Milliseconds(),
		"samples_during_cache_write": during, "samples_total": n * len(methods)}
	fmt.Printf("synctime: %d syncs per method in %s; %d of %d samples during goinfer's cache write\n", n,
		elapsed.Round(time.Millisecond), during, n*len(methods))
	rows := []map[string]any{}
	for m, ts := range times {
		sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
		q := func(p float64) time.Duration { return ts[int(p*float64(len(ts)-1))].Round(time.Microsecond) }
		fmt.Printf("%-24s p50 %-10s p99 %-10s max %s\n", methods[m].name, q(0.50), q(0.99), ts[len(ts)-1].Round(time.Microsecond))
		rows = append(rows, map[string]any{"method": methods[m].name, "p50_us": q(0.50).Microseconds(),
			"p99_us": q(0.99).Microseconds(), "max_us": ts[len(ts)-1].Microseconds()})
	}
	out["methods"] = rows
	// Where the cache landed: beside the symlink (removed with the scratch dir) or beside the model in ~/models.
	if added := newer(giwFiles(filepath.Dir(model)), modelDirBefore); len(added) > 0 {
		out["cache_written_beside_model"] = added
		fmt.Printf("note: goinfer wrote its cache beside the model, not the symlink: %v (left in place)\n", added)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("synctime-%s.json", time.Now().Format("2006-01-02T15-04"))), b, 0o644)
}

func giwFiles(dir string) map[string]bool {
	m := map[string]bool{}
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if strings.Contains(e.Name(), ".giw") {
			m[e.Name()] = true
		}
	}
	return m
}

func newer(after, before map[string]bool) []string {
	var out []string
	for k := range after {
		if !before[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
