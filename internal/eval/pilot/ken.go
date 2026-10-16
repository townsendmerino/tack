package pilot

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// WarmKen builds ken's index for repo before an A0k run, outside the clock (pilot pre-registration, amendment): it
// starts kenMCP with KenEnv, sends initialize and one `search` call over MCP's stdio transport (newline-delimited
// JSON-RPC), waits for the answer and for the snapshot ken-mcp persists under repo/.ken, then stops it. The ken-mcp
// opencode starts later loads that snapshot instead of building. It returns how long the build took, recorded as
// ken_index_secs and never part of a run's time. log receives ken-mcp's stderr.
func WarmKen(ctx context.Context, kenMCP, model, repo string, log io.Writer, timeout time.Duration) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, kenMCP)
	cmd.Dir = repo
	cmd.Env = os.Environ()
	for k, v := range KenEnv(repo, model) {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	defer func() {
		_ = stdin.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}()
	send := func(v any) error {
		b, _ := json.Marshal(v)
		_, err := stdin.Write(append(b, '\n'))
		return err
	}
	replies := make(chan map[string]any, 8)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				replies <- m
			}
		}
		close(replies)
	}()
	await := func(id float64) (map[string]any, error) {
		for {
			select {
			case m, ok := <-replies:
				if !ok {
					return nil, fmt.Errorf("ken-mcp closed its output")
				}
				if got, _ := m["id"].(float64); got == id {
					if e, ok := m["error"]; ok {
						return nil, fmt.Errorf("ken-mcp: %v", e)
					}
					return m, nil
				}
			case <-ctx.Done():
				return nil, fmt.Errorf("ken-mcp warm-up: %w", ctx.Err())
			}
		}
	}
	if err := send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "tack-pilot-warmup", "version": "0"}}}); err != nil {
		return 0, err
	}
	if _, err := await(1); err != nil {
		return 0, err
	}
	_ = send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if err := send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "search", "arguments": map[string]any{"query": "main"}}}); err != nil {
		return 0, err
	}
	res, err := await(2)
	if err != nil {
		return 0, err
	}
	if r, _ := res["result"].(map[string]any); r != nil && r["isError"] == true {
		return 0, fmt.Errorf("ken-mcp's warm-up search failed: %v", r["content"])
	}
	// The snapshot is persisted when the index is published; wait for its manifest.
	manifest := filepath.Join(repo, ".ken", "snapshot.manifest")
	for {
		if _, err := os.Stat(manifest); err == nil {
			return time.Since(start).Seconds(), nil
		}
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("ken-mcp answered but wrote no %s: %w", manifest, ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}
