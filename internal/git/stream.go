package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

const (
	metaEndMarker = "\x1e\x02"
)

type StreamCommit struct {
	Meta     CommitMeta
	Raw      []RawEntry
	Numstats map[string]NumstatEntry
	Totals   Totals
}

type LogStreamer struct {
	cmd    *exec.Cmd
	stderr *bytes.Buffer
	reader *bufio.Reader
	cancel context.CancelFunc
	done   bool
}

func NewLogStreamer(ctx context.Context, repo, branch, resumeSHA string, firstParent, includeMessages, useMailmap bool) (*LogStreamer, error) {
	// %aN/%aE/%cN/%cE use .mailmap normalization; %an/%ae/%cn/%ce don't
	an, ae, cn, ce := "%an", "%ae", "%cn", "%ce"
	if useMailmap {
		an, ae, cn, ce = "%aN", "%aE", "%cN", "%cE"
	}

	format := fmt.Sprintf("%%x00%%x01%%H%%x1f%%T%%x1f%%P%%x1f%s%%x1f%s%%x1f%%aI%%x1f%s%%x1f%s%%x1f%%cI%%x1f%%B%%x1e%%x02", an, ae, cn, ce)
	if !includeMessages {
		format = fmt.Sprintf("%%x00%%x01%%H%%x1f%%T%%x1f%%P%%x1f%s%%x1f%s%%x1f%%aI%%x1f%s%%x1f%s%%x1f%%cI%%x1f%%x1e%%x02", an, ae, cn, ce)
	}

	args := []string{"-C", repo, "log",
		"--raw", "--numstat", "-M",
		"--abbrev=40",
		fmt.Sprintf("--format=%s", format),
	}
	if firstParent {
		args = append(args, "--first-parent")
	}

	ref := branch
	if ref == "" {
		ref = "HEAD"
	}

	if resumeSHA != "" {
		// Log goes newest-first (no --reverse). We already processed from tip
		// down to resumeSHA. Continue with older commits: start from its parent.
		// If resumeSHA is the root commit (no parent), there's nothing left.
		if err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet", resumeSHA+"^").Run(); err != nil {
			return nil, nil // root commit — all history already processed
		}
		args = append(args, resumeSHA+"^")
	} else {
		args = append(args, ref)
	}

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, "git", args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("git log stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("git log start: %w", err)
	}

	return &LogStreamer{
		cmd:    cmd,
		stderr: &stderr,
		reader: bufio.NewReaderSize(stdout, 4<<20),
		cancel: cancel,
	}, nil
}

func (ls *LogStreamer) Next() (*StreamCommit, error) {
	if ls.done {
		return nil, nil
	}

	block, err := ls.readBlock()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read commit block: %w", err)
	}
	if len(block) == 0 {
		ls.done = true
		return nil, nil
	}
	if errors.Is(err, io.EOF) {
		ls.done = true
	}

	commit, parseErr := parseCommitBlock(block)
	if parseErr != nil {
		return nil, parseErr
	}

	return commit, nil
}

func (ls *LogStreamer) Close() error {
	ls.cancel()
	err := ls.cmd.Wait()
	if err != nil && ls.stderr.Len() > 0 {
		return fmt.Errorf("git log: %s", strings.TrimSpace(ls.stderr.String()))
	}
	return nil
}

func (ls *LogStreamer) readBlock() ([]byte, error) {
	var block bytes.Buffer

	for {
		b, err := ls.reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if block.Len() > 0 {
					return block.Bytes(), io.EOF
				}
				return nil, io.EOF
			}
			return nil, err
		}

		if b == 0x00 {
			next, peekErr := ls.reader.Peek(1)
			if peekErr == nil && len(next) > 0 && next[0] == 0x01 {
				ls.reader.ReadByte() // consume 0x01

				if block.Len() > 0 {
					return block.Bytes(), nil
				}
				// Start of first commit, continue reading
				continue
			}
		}

		block.WriteByte(b)
	}
}

func parseCommitBlock(block []byte) (*StreamCommit, error) {
	metaEnd := bytes.Index(block, []byte(metaEndMarker))
	if metaEnd < 0 {
		return nil, fmt.Errorf("missing metadata end marker in commit block")
	}

	metaRaw := string(block[:metaEnd])
	diffRaw := string(block[metaEnd+len(metaEndMarker):])

	parts := strings.SplitN(metaRaw, "\x1f", 10)
	if len(parts) < 10 {
		return nil, fmt.Errorf("unexpected commit format: got %d fields, sha prefix=%q", len(parts), string(block[:min(40, len(block))]))
	}

	parents := []string{}
	if parts[2] != "" {
		parents = strings.Fields(parts[2])
	}

	meta := CommitMeta{
		SHA:            parts[0],
		Tree:           parts[1],
		Parents:        parents,
		AuthorName:     parts[3],
		AuthorEmail:    parts[4],
		AuthorDate:     parts[5],
		CommitterName:  parts[6],
		CommitterEmail: parts[7],
		CommitterDate:  parts[8],
		Message:        strings.TrimRight(parts[9], "\n\r"),
	}

	raw, numstats, totals := parseDiffSection(diffRaw)

	return &StreamCommit{
		Meta:     meta,
		Raw:      raw,
		Numstats: numstats,
		Totals:   totals,
	}, nil
}

func parseDiffSection(diffRaw string) ([]RawEntry, map[string]NumstatEntry, Totals) {
	var rawEntries []RawEntry
	numstats := make(map[string]NumstatEntry)
	var totals Totals

	for _, line := range strings.Split(diffRaw, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, ":") {
			if entry, ok := parseRawLine(line); ok {
				rawEntries = append(rawEntries, entry)
			}
			continue
		}

		if len(line) > 0 && (line[0] >= '0' && line[0] <= '9' || line[0] == '-') {
			fields := strings.SplitN(line, "\t", 3)
			if len(fields) < 3 {
				continue
			}
			add, addErr := parseInt64(fields[0])
			del, delErr := parseInt64(fields[1])
			if addErr != nil || delErr != nil {
				continue
			}

			path := fields[2]
			entry := NumstatEntry{Additions: add, Deletions: del}

			oldP, newP := resolveRenamePath(path)
			numstats[newP] = entry
			if oldP != newP {
				numstats[oldP] = entry
			}

			totals.Additions += add
			totals.Deletions += del
		}
	}

	return rawEntries, numstats, totals
}

func parseRawLine(line string) (RawEntry, bool) {
	if !strings.HasPrefix(line, ":") {
		return RawEntry{}, false
	}
	line = line[1:]

	tabParts := strings.SplitN(line, "\t", 3)
	if len(tabParts) < 2 {
		return RawEntry{}, false
	}

	metaParts := strings.Fields(tabParts[0])
	if len(metaParts) < 5 {
		return RawEntry{}, false
	}

	status := metaParts[4]
	pathOld := tabParts[1]
	pathNew := pathOld

	if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
		if len(tabParts) >= 3 {
			pathNew = tabParts[2]
		}
	} else if status == "D" {
		pathNew = ""
	}

	return RawEntry{
		Status:  status,
		OldHash: metaParts[2],
		NewHash: metaParts[3],
		PathOld: pathOld,
		PathNew: pathNew,
	}, true
}

// resolveRenamePath parses numstat rename notation like "{old => new}/path"
// and returns the old and new full paths.
func resolveRenamePath(path string) (string, string) {
	arrowIdx := strings.Index(path, " => ")
	if arrowIdx < 0 {
		return path, path
	}

	braceOpen := strings.LastIndex(path[:arrowIdx], "{")
	braceCloseRel := strings.Index(path[arrowIdx:], "}")

	if braceOpen >= 0 && braceCloseRel >= 0 {
		braceClose := arrowIdx + braceCloseRel
		prefix := path[:braceOpen]
		suffix := path[braceClose+1:]
		oldPart := path[braceOpen+1 : arrowIdx]
		newPart := path[arrowIdx+4 : braceClose]
		return prefix + oldPart + suffix, prefix + newPart + suffix
	}

	return path[:arrowIdx], path[arrowIdx+4:]
}
