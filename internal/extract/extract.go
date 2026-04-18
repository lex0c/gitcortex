package extract

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lex0c/gitcortex/internal/git"
	"github.com/lex0c/gitcortex/internal/model"
)

const DefaultCommandTimeout = 2 * time.Minute

type Config struct {
	Repo             string
	Branch           string
	BatchSize        int
	Output           string
	StateFile        string
	StartOffset      int
	StartSHA         string
	IncludeMessages  bool
	CommandTimeout   time.Duration
	FirstParent      bool
	Mailmap          bool
	IgnorePatterns   []string
}

type State struct {
	LastProcessedSHA string `json:"last_processed_sha,omitempty"`
	CommitOffset     int    `json:"commit_offset,omitempty"`
}

func LoadState(stateFile string, flagOffset int, flagSHA string) (State, error) {
	if flagSHA != "" && flagOffset >= 0 {
		return State{}, fmt.Errorf("--start-offset and --start-sha cannot both be set")
	}

	if flagSHA != "" {
		if !git.IsValidSHA(flagSHA) {
			return State{}, fmt.Errorf("invalid start-sha; must be a 40-character hex commit SHA")
		}
		return State{LastProcessedSHA: flagSHA}, nil
	}

	if flagOffset >= 0 {
		return State{CommitOffset: flagOffset}, nil
	}

	data, err := os.ReadFile(stateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, err
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return State{}, nil
	}

	var parsed State
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		if parsed.CommitOffset < 0 {
			return State{}, fmt.Errorf("state file %s contains a negative offset: %d", stateFile, parsed.CommitOffset)
		}
		if parsed.LastProcessedSHA != "" && !git.IsValidSHA(parsed.LastProcessedSHA) {
			return State{}, fmt.Errorf("state file %s contains an invalid last_processed_sha", stateFile)
		}
		return parsed, nil
	}

	count, err := strconv.Atoi(trimmed)
	if err != nil {
		return State{}, fmt.Errorf("state file %s contains unrecognized data; delete it or provide --start-offset to reset: %w", stateFile, err)
	}
	if count < 0 {
		return State{}, fmt.Errorf("state file %s contains a negative offset: %d", stateFile, count)
	}

	return State{CommitOffset: count}, nil
}

func Run(ctx context.Context, cfg Config) error {
	// Warn when the repo has a .mailmap but the flag wasn't set — identity
	// stats (bus factor, dev network, profiles) will split the same person
	// across aliases without it. Silent divergence is the main concern.
	if !cfg.Mailmap {
		if _, err := os.Stat(filepath.Join(cfg.Repo, ".mailmap")); err == nil {
			log.Printf("warning: %s contains .mailmap but --mailmap was not set; identity metrics (bus factor, dev network, profiles) may split the same person across aliases", cfg.Repo)
		}
	}

	initialState, err := LoadState(cfg.StateFile, cfg.StartOffset, cfg.StartSHA)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	resuming := initialState.LastProcessedSHA != "" || initialState.CommitOffset > 0
	var out *os.File
	if resuming {
		out, err = os.OpenFile(cfg.Output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	} else {
		out, err = os.Create(cfg.Output)
	}
	if err != nil {
		return fmt.Errorf("open output: %w", err)
	}
	defer out.Close()

	devCache := make(map[string]struct{})
	if resuming {
		if existing, err := loadDevEmails(cfg.Output); err == nil {
			devCache = existing
			log.Printf("resume: loaded %d known dev emails from %s", len(devCache), cfg.Output)
		}
	}

	writer := bufio.NewWriter(out)
	defer writer.Flush()

	return streamExtract(ctx, cfg, initialState, writer, devCache)
}

func streamExtract(ctx context.Context, cfg Config, initialState State, writer *bufio.Writer, devCache map[string]struct{}) (err error) {
	resumeSHA := initialState.LastProcessedSHA
	processedCount := initialState.CommitOffset

	// Resolve offset-only state to a SHA for range-based resume
	if resumeSHA == "" && processedCount > 0 {
		sha, err := git.CommitAtOffset(ctx, cfg.Repo, cfg.Branch, processedCount-1, cfg.CommandTimeout, cfg.FirstParent)
		if err != nil {
			return fmt.Errorf("resolve commit offset %d: %w", processedCount, err)
		}
		if sha == "" {
			return fmt.Errorf("commit offset %d exceeds history length", processedCount)
		}
		resumeSHA = sha
	}

	streamer, err := git.NewLogStreamer(ctx, cfg.Repo, cfg.Branch, resumeSHA, cfg.FirstParent, cfg.IncludeMessages, cfg.Mailmap)
	if err != nil {
		return fmt.Errorf("start log stream: %w", err)
	}
	if streamer == nil {
		log.Printf("done; all commits already processed")
		return nil
	}
	defer func() {
		if closeErr := streamer.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	resolver, err := git.NewBlobSizeResolver(ctx, cfg.Repo)
	if err != nil {
		return fmt.Errorf("start blob resolver: %w", err)
	}
	defer resolver.Close()

	checkpointInterval := cfg.BatchSize
	if checkpointInterval <= 0 {
		checkpointInterval = 1000
	}

	startTime := time.Now()
	lastCheckpoint := time.Now()
	var lastSHA string

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		commit, err := streamer.Next()
		if err != nil {
			return fmt.Errorf("stream commit: %w", err)
		}
		if commit == nil {
			break
		}

		sizeMap, err := resolver.Resolve(commit.Raw)
		if err != nil {
			log.Printf("warning: blob sizes failed for %s: %v", commit.Meta.SHA, err)
			sizeMap = map[string]int64{}
		}

		if err := emitCommit(writer, commit, sizeMap, devCache, cfg.IgnorePatterns); err != nil {
			return err
		}

		processedCount++
		lastSHA = commit.Meta.SHA

		if processedCount%checkpointInterval == 0 {
			if err := writer.Flush(); err != nil {
				return fmt.Errorf("flush output: %w", err)
			}
			saveState(cfg.StateFile, lastSHA, processedCount)

			elapsed := time.Since(lastCheckpoint).Seconds()
			rate := float64(checkpointInterval) / elapsed
			log.Printf("progress: %d commits processed (%.0f commits/s)", processedCount, rate)
			lastCheckpoint = time.Now()
		}
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush output: %w", err)
	}

	if lastSHA != "" {
		saveState(cfg.StateFile, lastSHA, processedCount)
	}

	elapsed := time.Since(startTime)
	log.Printf("done; processed %d commits in %s", processedCount-initialState.CommitOffset, elapsed.Round(time.Millisecond))

	return nil
}

func emitCommit(writer *bufio.Writer, commit *git.StreamCommit, sizeMap map[string]int64, devCache map[string]struct{}, ignorePatterns []string) error {
	// Filter files and recalculate totals
	var totalAdd, totalDel int64
	var filesCount int
	var filteredRaw []git.RawEntry

	for _, entry := range commit.Raw {
		path := entry.PathNew
		if path == "" {
			path = entry.PathOld
		}
		if shouldIgnore(path, ignorePatterns) {
			continue
		}
		filteredRaw = append(filteredRaw, entry)

		var add, del int64
		if stats, ok := commit.Numstats[entry.PathNew]; ok {
			add = stats.Additions
			del = stats.Deletions
		} else if stats, ok := commit.Numstats[entry.PathOld]; ok {
			add = stats.Additions
			del = stats.Deletions
		}
		totalAdd += add
		totalDel += del
		filesCount++
	}

	c := model.CommitInfo{
		Type:           model.CommitType,
		SHA:            commit.Meta.SHA,
		Tree:           commit.Meta.Tree,
		Parents:        commit.Meta.Parents,
		AuthorName:     commit.Meta.AuthorName,
		AuthorEmail:    commit.Meta.AuthorEmail,
		AuthorDate:     commit.Meta.AuthorDate,
		CommitterName:  commit.Meta.CommitterName,
		CommitterEmail: commit.Meta.CommitterEmail,
		CommitterDate:  commit.Meta.CommitterDate,
		Message:        commit.Meta.Message,
		Additions:      totalAdd,
		Deletions:      totalDel,
		FilesChanged:   filesCount,
	}

	if err := writeJSON(writer, c); err != nil {
		return err
	}

	for _, p := range commit.Meta.Parents {
		if err := writeJSON(writer, model.CommitParentInfo{
			Type:      model.CommitParentType,
			SHA:       commit.Meta.SHA,
			ParentSHA: p,
		}); err != nil {
			return err
		}
	}

	for _, entry := range filteredRaw {
		var additions, deletions int64
		if stats, ok := commit.Numstats[entry.PathNew]; ok {
			additions = stats.Additions
			deletions = stats.Deletions
		} else if stats, ok := commit.Numstats[entry.PathOld]; ok {
			additions = stats.Additions
			deletions = stats.Deletions
		}

		if err := writeJSON(writer, model.CommitFileInfo{
			Type:         model.CommitFileType,
			Commit:       commit.Meta.SHA,
			PathCurrent:  entry.PathNew,
			PathPrevious: entry.PathOld,
			Status:       entry.Status,
			OldHash:      entry.OldHash,
			NewHash:      entry.NewHash,
			OldSize:      sizeMap[entry.OldHash],
			NewSize:      sizeMap[entry.NewHash],
			Additions:    additions,
			Deletions:    deletions,
		}); err != nil {
			return err
		}
	}

	emitDev(writer, devCache, commit.Meta.AuthorName, commit.Meta.AuthorEmail)
	emitDev(writer, devCache, commit.Meta.CommitterName, commit.Meta.CommitterEmail)

	return nil
}

func saveState(stateFile, sha string, offset int) {
	state := State{LastProcessedSHA: sha, CommitOffset: offset}
	data, err := json.Marshal(state)
	if err != nil {
		log.Printf("failed to encode state at offset %d: %v", offset, err)
		return
	}
	if err := os.WriteFile(stateFile, data, 0o644); err != nil {
		log.Printf("failed to write state at offset %d: %v", offset, err)
	}
}

func emitDev(writer *bufio.Writer, devCache map[string]struct{}, name, email string) {
	emailLower := strings.ToLower(strings.TrimSpace(email))
	if emailLower == "" {
		return
	}
	if _, exists := devCache[emailLower]; exists {
		return
	}
	devCache[emailLower] = struct{}{}

	hash := sha256.Sum256([]byte(emailLower))
	info := model.DevInfo{
		Type:  model.DevType,
		DevID: hex.EncodeToString(hash[:]),
		Name:  name,
		Email: email,
	}
	if err := writeJSON(writer, info); err != nil {
		log.Printf("dev emit failed for %s: %v", email, err)
	}
}

func loadDevEmails(path string) (map[string]struct{}, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cache := make(map[string]struct{})
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1<<20)
	scanner.Buffer(buf, 10<<20)

	for scanner.Scan() {
		line := scanner.Bytes()
		var peek struct {
			Type  string `json:"type"`
			Email string `json:"email"`
		}
		if err := json.Unmarshal(line, &peek); err != nil {
			continue
		}
		if peek.Type == model.DevType && peek.Email != "" {
			cache[strings.ToLower(strings.TrimSpace(peek.Email))] = struct{}{}
		}
	}

	return cache, scanner.Err()
}

func shouldIgnore(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	for _, pattern := range patterns {
		// Basename match: "*.min.js" matches "dist/app.min.js"
		if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
			return true
		}
		// Full path match: "src/generated/*.go" matches "src/generated/types.go"
		if matched, _ := filepath.Match(pattern, path); matched {
			return true
		}
		// Directory prefix: "dist/*" or "dist/" matches "dist/foo/bar.js"
		prefix := strings.TrimSuffix(pattern, "*")
		prefix = strings.TrimSuffix(prefix, "/")
		if prefix != "" && prefix != pattern && strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func writeJSON(writer *bufio.Writer, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	return writer.WriteByte('\n')
}
