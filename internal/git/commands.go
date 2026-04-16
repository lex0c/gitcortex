package git

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var DebugLogging bool

func debugf(format string, args ...interface{}) {
	if !DebugLogging {
		return
	}
	log.Printf("debug: "+format, args...)
}

type CommitMeta struct {
	SHA            string
	Tree           string
	Parents        []string
	AuthorName     string
	AuthorEmail    string
	AuthorDate     string
	CommitterName  string
	CommitterEmail string
	CommitterDate  string
	Message        string
}

func ListCommits(ctx context.Context, repo, branch string, processedCount, maxCount int, commandTimeout time.Duration, firstParent bool) ([]string, error) {
	branchRef := branch
	if branchRef == "" {
		branchRef = "HEAD"
	}

	countArgs := []string{"-C", repo, "rev-list"}
	if firstParent {
		countArgs = append(countArgs, "--first-parent")
	}
	countArgs = append(countArgs, "--count", branchRef)

	countCtx, cancelCount := context.WithTimeout(ctx, commandTimeout)
	countCmd := exec.CommandContext(countCtx, "git", countArgs...)
	countOut, err := countCmd.Output()
	cancelCount()
	if err != nil {
		if errors.Is(countCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("rev-list --count timed out: %w", countCtx.Err())
		}
		if errors.Is(countCtx.Err(), context.Canceled) {
			return nil, fmt.Errorf("rev-list --count canceled: %w", countCtx.Err())
		}
		return nil, fmt.Errorf("rev-list --count: %w", err)
	}

	totalCommits, err := strconv.Atoi(strings.TrimSpace(string(countOut)))
	if err != nil {
		return nil, fmt.Errorf("parse rev-list --count output: %w", err)
	}
	if processedCount >= totalCommits {
		return nil, nil
	}

	batchSize := totalCommits - processedCount
	if maxCount > 0 && maxCount < batchSize {
		batchSize = maxCount
	}
	skipNewest := totalCommits - processedCount - batchSize
	if skipNewest < 0 {
		skipNewest = 0
	}

	args := []string{"-C", repo, "rev-list", "--reverse"}
	if firstParent {
		args = append(args, "--first-parent")
	}
	if skipNewest > 0 {
		args = append(args, fmt.Sprintf("--skip=%d", skipNewest))
	}
	if batchSize > 0 {
		args = append(args, fmt.Sprintf("--max-count=%d", batchSize))
	}
	args = append(args, branchRef)

	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("rev-list stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("rev-list start: %w", err)
	}
	waited := false
	defer func() {
		if cmd.Process == nil {
			return
		}
		if cmdCtx.Err() != nil {
			_ = cmd.Process.Kill()
		}
		if !waited {
			_ = cmd.Wait()
		}
	}()

	var commits []string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		commits = append(commits, strings.TrimSpace(scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if err := cmd.Wait(); err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("rev-list timed out: %w", cmdCtx.Err())
		}
		if errors.Is(cmdCtx.Err(), context.Canceled) {
			return nil, fmt.Errorf("rev-list canceled: %w", cmdCtx.Err())
		}
		return nil, fmt.Errorf("rev-list wait: %w", err)
	}
	waited = true

	if maxCount > 0 && len(commits) > maxCount {
		commits = commits[:maxCount]
	}

	return commits, nil
}

func FindCommitIndex(ctx context.Context, repo, branch, targetSHA string, commandTimeout time.Duration, firstParent bool) (int, error) {
	branchRef := branch
	if branchRef == "" {
		branchRef = "HEAD"
	}

	args := []string{"-C", repo, "rev-list", "--reverse"}
	if firstParent {
		args = append(args, "--first-parent")
	}
	args = append(args, branchRef)

	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("rev-list stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("rev-list start: %w", err)
	}
	waited := false
	defer func() {
		if cmd.Process == nil {
			return
		}
		if cmdCtx.Err() != nil {
			_ = cmd.Process.Kill()
		}
		if !waited {
			_ = cmd.Wait()
		}
	}()

	index := 0
	found := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		sha := strings.TrimSpace(scanner.Text())
		if sha == targetSHA {
			found = true
			cancel()
			break
		}
		index++
	}
	if err := scanner.Err(); err != nil && !(found && errors.Is(cmdCtx.Err(), context.Canceled)) {
		return 0, err
	}

	if err := cmd.Wait(); err != nil {
		if found && errors.Is(cmdCtx.Err(), context.Canceled) {
			waited = true
			return index, nil
		}
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return 0, fmt.Errorf("rev-list search timed out: %w", cmdCtx.Err())
		}
		if errors.Is(cmdCtx.Err(), context.Canceled) {
			return 0, fmt.Errorf("rev-list search canceled: %w", cmdCtx.Err())
		}
		return 0, fmt.Errorf("rev-list search wait: %w", err)
	}
	waited = true

	if !found {
		return 0, fmt.Errorf("commit %s not found on %s", targetSHA, branchRef)
	}

	return index, nil
}

func CommitAtOffset(ctx context.Context, repo, branch string, offset int, commandTimeout time.Duration, firstParent bool) (string, error) {
	if offset < 0 {
		return "", fmt.Errorf("offset must be non-negative")
	}

	branchRef := branch
	if branchRef == "" {
		branchRef = "HEAD"
	}

	countArgs := []string{"-C", repo, "rev-list"}
	if firstParent {
		countArgs = append(countArgs, "--first-parent")
	}
	countArgs = append(countArgs, "--count", branchRef)

	countCtx, cancelCount := context.WithTimeout(ctx, commandTimeout)
	countCmd := exec.CommandContext(countCtx, "git", countArgs...)
	countOut, err := countCmd.Output()
	cancelCount()
	if err != nil {
		if errors.Is(countCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("rev-list --count timed out: %w", countCtx.Err())
		}
		if errors.Is(countCtx.Err(), context.Canceled) {
			return "", fmt.Errorf("rev-list --count canceled: %w", countCtx.Err())
		}
		return "", fmt.Errorf("rev-list --count: %w", err)
	}

	totalCommits, err := strconv.Atoi(strings.TrimSpace(string(countOut)))
	if err != nil {
		return "", fmt.Errorf("parse rev-list --count output: %w", err)
	}
	if offset >= totalCommits {
		return "", nil
	}

	skipFromTip := totalCommits - offset - 1
	if skipFromTip < 0 {
		skipFromTip = 0
	}

	args := []string{"-C", repo, "rev-list"}
	if firstParent {
		args = append(args, "--first-parent")
	}
	if skipFromTip > 0 {
		args = append(args, fmt.Sprintf("--skip=%d", skipFromTip))
	}
	args = append(args, "--max-count=1", branchRef)

	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	out, err := exec.CommandContext(cmdCtx, "git", args...).CombinedOutput()
	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("rev-list offset lookup timed out: %w", cmdCtx.Err())
		}
		if errors.Is(cmdCtx.Err(), context.Canceled) {
			return "", fmt.Errorf("rev-list offset lookup canceled: %w", cmdCtx.Err())
		}
		return "", fmt.Errorf("rev-list offset lookup: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return strings.TrimSpace(string(out)), nil
}

func ReadCommitMetadata(ctx context.Context, repo, sha string, commandTimeout time.Duration, includeMessage bool) (CommitMeta, error) {
	format := "%H%x1f%T%x1f%P%x1f%an%x1f%ae%x1f%aI%x1f%cn%x1f%ce%x1f%cI%x1f%B%x1e"
	if !includeMessage {
		format = "%H%x1f%T%x1f%P%x1f%an%x1f%ae%x1f%aI%x1f%cn%x1f%ce%x1f%cI%x1f%x1e"
	}

	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", "-C", repo, "show", "-s", fmt.Sprintf("--format=%s", format), sha)
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return CommitMeta{}, fmt.Errorf("show timed out: %w", cmdCtx.Err())
		}
		if errors.Is(cmdCtx.Err(), context.Canceled) {
			return CommitMeta{}, fmt.Errorf("show canceled: %w", cmdCtx.Err())
		}
		return CommitMeta{}, fmt.Errorf("show output: %w", err)
	}

	parts := strings.Split(strings.TrimSuffix(string(out), "\x1e"), "\x1f")
	if len(parts) < 10 {
		return CommitMeta{}, fmt.Errorf("unexpected commit format for %s", sha)
	}

	parents := []string{}
	if parts[2] != "" {
		parents = strings.Fields(parts[2])
	}

	return CommitMeta{
		SHA:            parts[0],
		Tree:           parts[1],
		Parents:        parents,
		AuthorName:     parts[3],
		AuthorEmail:    parts[4],
		AuthorDate:     parts[5],
		CommitterName:  parts[6],
		CommitterEmail: parts[7],
		CommitterDate:  parts[8],
		Message:        parts[9],
	}, nil
}

func ReadNumstat(ctx context.Context, repo, sha string, commandTimeout time.Duration, policy DiscardPolicy) (map[string]NumstatEntry, Totals, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", "-C", repo, "diff-tree", "-r", "--no-commit-id", "--numstat", sha)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, Totals{}, fmt.Errorf("diff-tree numstat stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, Totals{}, fmt.Errorf("diff-tree numstat start: %w", err)
	}
	waited := false
	defer func() {
		if cmd.Process == nil {
			return
		}
		if cmdCtx.Err() != nil {
			_ = cmd.Process.Kill()
		}
		if !waited {
			_ = cmd.Wait()
		}
	}()

	scanner := bufio.NewScanner(stdout)
	discard := newDiscardTracker("numstat", policy)
	defer discard.finalize()
	buf := make([]byte, 0, 1<<20)
	scanner.Buffer(buf, 1<<20)

	stats, agg, err := parseNumstat(scanner, discard)
	if err != nil {
		return nil, Totals{}, err
	}

	if err := cmd.Wait(); err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return nil, Totals{}, fmt.Errorf("diff-tree numstat timed out: %w", cmdCtx.Err())
		}
		if errors.Is(cmdCtx.Err(), context.Canceled) {
			return nil, Totals{}, fmt.Errorf("diff-tree numstat canceled: %w", cmdCtx.Err())
		}
		return nil, Totals{}, fmt.Errorf("diff-tree numstat wait: %w", err)
	}
	waited = true

	return stats, agg, nil
}

func ReadRawChanges(ctx context.Context, repo, sha string, commandTimeout time.Duration, policy DiscardPolicy) ([]RawEntry, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", "-C", repo, "diff-tree", "-r", "-z", "--no-commit-id", "-M", "-C", "--raw", sha)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("diff-tree raw stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("diff-tree raw start: %w", err)
	}
	waited := false
	defer func() {
		if cmd.Process == nil {
			return
		}
		if cmdCtx.Err() != nil {
			_ = cmd.Process.Kill()
		}
		if !waited {
			_ = cmd.Wait()
		}
	}()

	reader := bufio.NewReader(stdout)
	entries, err := parseRawChanges(reader, policy)
	if err != nil {
		return nil, err
	}

	if err := cmd.Wait(); err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("diff-tree raw timed out: %w", cmdCtx.Err())
		}
		if errors.Is(cmdCtx.Err(), context.Canceled) {
			return nil, fmt.Errorf("diff-tree raw canceled: %w", cmdCtx.Err())
		}
		return nil, fmt.Errorf("diff-tree raw wait: %w", err)
	}
	waited = true

	return entries, nil
}

func BlobSizes(ctx context.Context, repo string, entries []RawEntry, commandTimeout time.Duration, policy DiscardPolicy) (map[string]int64, error) {
	const nullHash = "0000000000000000000000000000000000000000"

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

	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", "-C", repo, "cat-file", "--batch-check")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("cat-file stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("cat-file stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cat-file start: %w", err)
	}
	waited := false
	defer func() {
		if cmd.Process == nil {
			return
		}
		if cmdCtx.Err() != nil {
			_ = cmd.Process.Kill()
		}
		if !waited {
			_ = cmd.Wait()
		}
	}()

	writeErrCh := make(chan error, 1)
	go func() {
		defer stdin.Close()
		for h := range needed {
			if _, err := io.WriteString(stdin, h+"\n"); err != nil {
				writeErrCh <- fmt.Errorf("cat-file stdin write: %w", err)
				cancel()
				return
			}
		}
		writeErrCh <- nil
	}()

	sizes := make(map[string]int64)
	scanner := bufio.NewScanner(stdout)
	discard := newDiscardTracker("blob-sizes", policy)
	buf := make([]byte, 0, 1<<20)
	scanner.Buffer(buf, 1<<20)

	for {
		select {
		case err := <-writeErrCh:
			if err != nil {
				return nil, err
			}
			writeErrCh = nil
		default:
		}

		if !scanner.Scan() {
			break
		}
		parts := strings.Fields(scanner.Text())
		if len(parts) < 3 {
			if err := discard.record("unexpected cat-file output fields"); err != nil {
				return nil, err
			}
			continue
		}
		hash := parts[0]
		if parts[1] != "blob" {
			if err := discard.record("non-blob object encountered"); err != nil {
				return nil, err
			}
			continue
		}
		size, err := parseInt64(parts[2])
		if err != nil {
			if err := discard.record(fmt.Sprintf("invalid blob size %q", parts[2])); err != nil {
				return nil, err
			}
			continue
		}

		sizes[hash] = size
	}

	if writeErrCh != nil {
		if err := <-writeErrCh; err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	discard.finalize()

	if err := cmd.Wait(); err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("cat-file timed out: %w", cmdCtx.Err())
		}
		if errors.Is(cmdCtx.Err(), context.Canceled) {
			return nil, fmt.Errorf("cat-file canceled: %w", cmdCtx.Err())
		}
		return nil, fmt.Errorf("cat-file wait: %w", err)
	}
	waited = true

	return sizes, nil
}

func IsValidSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
