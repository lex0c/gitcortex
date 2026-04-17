package git

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

const nullHash = "0000000000000000000000000000000000000000"

type BlobSizeResolver struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Scanner
	cancel context.CancelFunc
}

func NewBlobSizeResolver(ctx context.Context, repo string) (*BlobSizeResolver, error) {
	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, "git", "-C", repo, "cat-file", "--batch-check")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("cat-file stdin: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("cat-file stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("cat-file start: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)

	return &BlobSizeResolver{
		cmd:    cmd,
		stdin:  stdin,
		reader: scanner,
		cancel: cancel,
	}, nil
}

func (r *BlobSizeResolver) Resolve(entries []RawEntry) (map[string]int64, error) {
	needed := make(map[string]struct{})
	for _, e := range entries {
		if e.OldHash != nullHash && e.OldHash != "" {
			needed[e.OldHash] = struct{}{}
		}
		if e.NewHash != nullHash && e.NewHash != "" {
			needed[e.NewHash] = struct{}{}
		}
	}

	if len(needed) == 0 {
		return map[string]int64{}, nil
	}

	// Write hashes in a goroutine to avoid deadlock: if we write all hashes
	// before reading, the OS pipe buffer (64KB) can fill up. cat-file blocks
	// trying to write responses, and we block trying to write more hashes.
	// The channel has buffer 1 so the goroutine always completes (no leak).
	writeErr := make(chan error, 1)
	go func() {
		for h := range needed {
			if _, err := io.WriteString(r.stdin, h+"\n"); err != nil {
				writeErr <- fmt.Errorf("cat-file write: %w", err)
				return
			}
		}
		writeErr <- nil
	}()

	sizes := make(map[string]int64, len(needed))
	for i := 0; i < len(needed); i++ {
		if !r.reader.Scan() {
			if err := r.reader.Err(); err != nil {
				return nil, fmt.Errorf("cat-file read: %w", err)
			}
			return nil, fmt.Errorf("cat-file: unexpected EOF after %d/%d responses", i, len(needed))
		}

		parts := strings.Fields(r.reader.Text())
		if len(parts) < 3 || parts[1] != "blob" {
			continue
		}

		size, err := parseInt64(parts[2])
		if err != nil {
			continue
		}
		sizes[parts[0]] = size
	}

	if err := <-writeErr; err != nil {
		return nil, err
	}

	return sizes, nil
}

func (r *BlobSizeResolver) Close() error {
	r.stdin.Close()
	r.cancel()
	_ = r.cmd.Wait()
	return nil
}
