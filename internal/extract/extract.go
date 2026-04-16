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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitcortex/internal/git"
	"gitcortex/internal/model"
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
	DiscardWarnLimit int
	DiscardError     bool
	Debug            bool
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

type devCandidate struct {
	name  string
	email string
}

type commitPayload struct {
	entries []interface{}
	devs    []devCandidate
}

func Run(ctx context.Context, cfg Config) error {
	git.DebugLogging = cfg.Debug

	initialState, err := LoadState(cfg.StateFile, cfg.StartOffset, cfg.StartSHA)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	out, err := os.Create(cfg.Output)
	if err != nil {
		return fmt.Errorf("open output: %w", err)
	}
	defer out.Close()

	devCache := make(map[string]struct{})
	writer := bufio.NewWriter(out)
	defer writer.Flush()

	policy := git.DiscardPolicy{WarnLimit: cfg.DiscardWarnLimit, FailOnExcess: cfg.DiscardError}

	return processCommits(ctx, cfg.Repo, cfg.Branch, initialState, cfg.BatchSize,
		cfg.StateFile, cfg.CommandTimeout, writer, devCache, policy,
		cfg.IncludeMessages, cfg.FirstParent)
}

func processCommits(
	ctx context.Context,
	repo, branch string,
	initialState State,
	batchSize int,
	stateFile string,
	commandTimeout time.Duration,
	writer *bufio.Writer,
	devCache map[string]struct{},
	policy git.DiscardPolicy,
	includeMessages, firstParent bool,
) error {
	processedCount := initialState.CommitOffset
	lastProcessedSHA := initialState.LastProcessedSHA

	if lastProcessedSHA != "" {
		index, err := git.FindCommitIndex(ctx, repo, branch, lastProcessedSHA, commandTimeout, firstParent)
		if err != nil {
			return fmt.Errorf("locate last processed sha %s: %w", lastProcessedSHA, err)
		}
		processedCount = index + 1
	}

	if lastProcessedSHA == "" && processedCount > 0 {
		sha, err := git.CommitAtOffset(ctx, repo, branch, processedCount-1, commandTimeout, firstParent)
		if err != nil {
			return fmt.Errorf("resolve commit offset %d: %w", processedCount, err)
		}
		if sha == "" {
			return fmt.Errorf("commit offset %d exceeds history length", processedCount)
		}
		lastProcessedSHA = sha
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		commits, err := git.ListCommits(ctx, repo, branch, processedCount, batchSize, commandTimeout, firstParent)
		if err != nil {
			return err
		}
		if len(commits) == 0 {
			log.Printf("no more commits to process; processed %d commits total", processedCount)
			return nil
		}

		if err := processBatch(ctx, repo, commits, batchSize, commandTimeout, writer, devCache, policy, includeMessages); err != nil {
			return err
		}

		if err := writer.Flush(); err != nil {
			return fmt.Errorf("flush output: %w", err)
		}

		processedCount += len(commits)
		lastProcessedSHA = commits[len(commits)-1]
		newState := State{LastProcessedSHA: lastProcessedSHA, CommitOffset: processedCount}
		data, err := json.Marshal(newState)
		if err != nil {
			log.Printf("failed to encode state at offset %d: %v", processedCount, err)
		} else if err := os.WriteFile(stateFile, data, 0o644); err != nil {
			log.Printf("failed to update state at offset %d: %v", processedCount, err)
		}

		if len(commits) < batchSize {
			log.Printf("no more commits to process; processed %d commits total", processedCount)
			return nil
		}
	}
}

func processBatch(
	ctx context.Context,
	repo string,
	commits []string,
	batchSize int,
	commandTimeout time.Duration,
	writer *bufio.Writer,
	devCache map[string]struct{},
	policy git.DiscardPolicy,
	includeMessages bool,
) error {
	type workItem struct {
		index int
		sha   string
	}

	type commitResult struct {
		index   int
		sha     string
		entries []interface{}
		devs    []devCandidate
		err     error
	}

	workCh := make(chan workItem)
	resultCh := make(chan commitResult)

	resultLimit := batchSize
	if resultLimit < 1 {
		resultLimit = 1
	}
	inFlight := make(chan struct{}, resultLimit)

	numWorkers := runtime.NumCPU()
	if numWorkers < 1 {
		numWorkers = 1
	}

	var workerWG sync.WaitGroup
	workerWG.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func() {
			defer workerWG.Done()
			for work := range workCh {
				res, err := handleCommit(ctx, repo, work.sha, commandTimeout, policy, includeMessages)
				if err != nil {
					resultCh <- commitResult{index: work.index, sha: work.sha, err: err}
					continue
				}
				resultCh <- commitResult{index: work.index, sha: work.sha, entries: res.entries, devs: res.devs}
			}
		}()
	}

	go func() {
		defer close(workCh)
		for i, sha := range commits {
			select {
			case <-ctx.Done():
				return
			case inFlight <- struct{}{}:
			}

			select {
			case <-ctx.Done():
				<-inFlight
				return
			case workCh <- workItem{index: i, sha: sha}:
			}
		}
	}()

	go func() {
		workerWG.Wait()
		close(resultCh)
	}()

	nextIndex := 0
	pending := make(map[int]commitResult)

	for res := range resultCh {
		if ctx.Err() != nil {
			<-inFlight
			continue
		}

		pending[res.index] = res
		for {
			nextRes, ok := pending[nextIndex]
			if !ok {
				break
			}

			if nextRes.err != nil {
				log.Printf("error processing commit %s: %v", nextRes.sha, nextRes.err)
			} else {
				for _, entry := range nextRes.entries {
					if err := writeJSON(writer, entry); err != nil {
						<-inFlight
						return err
					}
				}
				for _, dev := range nextRes.devs {
					emitDev(writer, devCache, dev.name, dev.email)
				}
			}

			delete(pending, nextIndex)
			nextIndex++
			<-inFlight
		}
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	return nil
}

func handleCommit(ctx context.Context, repo, sha string, commandTimeout time.Duration, policy git.DiscardPolicy, includeMessages bool) (commitPayload, error) {
	meta, err := git.ReadCommitMetadata(ctx, repo, sha, commandTimeout, includeMessages)
	if err != nil {
		return commitPayload{}, err
	}

	numstats, totals, err := git.ReadNumstat(ctx, repo, sha, commandTimeout, policy)
	if err != nil {
		return commitPayload{}, err
	}

	rawEntries, err := git.ReadRawChanges(ctx, repo, sha, commandTimeout, policy)
	if err != nil {
		return commitPayload{}, err
	}

	sizeMap, err := git.BlobSizes(ctx, repo, rawEntries, commandTimeout, policy)
	if err != nil {
		return commitPayload{}, err
	}

	commit := model.CommitInfo{
		Type:           model.CommitType,
		SHA:            meta.SHA,
		Tree:           meta.Tree,
		Parents:        meta.Parents,
		AuthorName:     meta.AuthorName,
		AuthorEmail:    meta.AuthorEmail,
		AuthorDate:     meta.AuthorDate,
		CommitterName:  meta.CommitterName,
		CommitterEmail: meta.CommitterEmail,
		CommitterDate:  meta.CommitterDate,
		Message:        meta.Message,
		Additions:      totals.Additions,
		Deletions:      totals.Deletions,
		FilesChanged:   len(rawEntries),
	}

	entries := []interface{}{commit}

	for _, p := range meta.Parents {
		entries = append(entries, model.CommitParentInfo{
			Type:      model.CommitParentType,
			SHA:       sha,
			ParentSHA: p,
		})
	}

	for _, entry := range rawEntries {
		var additions, deletions int64
		if stats, ok := numstats[entry.PathNew]; ok {
			additions = stats.Additions
			deletions = stats.Deletions
		} else if stats, ok := numstats[entry.PathOld]; ok {
			additions = stats.Additions
			deletions = stats.Deletions
		}

		entries = append(entries, model.CommitFileInfo{
			Type:         model.CommitFileType,
			Commit:       sha,
			PathCurrent:  entry.PathNew,
			PathPrevious: entry.PathOld,
			Status:       entry.Status,
			OldHash:      entry.OldHash,
			NewHash:      entry.NewHash,
			OldSize:      sizeMap[entry.OldHash],
			NewSize:      sizeMap[entry.NewHash],
			Additions:    additions,
			Deletions:    deletions,
		})
	}

	devs := []devCandidate{
		{name: meta.AuthorName, email: meta.AuthorEmail},
		{name: meta.CommitterName, email: meta.CommitterEmail},
	}

	return commitPayload{entries: entries, devs: devs}, nil
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
