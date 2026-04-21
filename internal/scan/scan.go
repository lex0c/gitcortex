package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lex0c/gitcortex/internal/extract"
	"github.com/lex0c/gitcortex/internal/git"
)

// Config holds scan command input. The Extract template is copied per-repo
// and its Repo/Output/StateFile/Branch are overwritten per worker — the
// template exists to carry shared flags (ignore patterns, batch size,
// mailmap, etc.) without re-declaring them on the scan surface.
type Config struct {
	Roots      []string
	Output     string
	IgnoreFile string
	MaxDepth   int
	Parallel   int
	Extract    extract.Config
}

// Manifest is persisted to <output>/manifest.json so subsequent runs
// (or the report stage) can discover which JSONL files to consolidate
// without re-walking the filesystem.
type Manifest struct {
	GeneratedAt string         `json:"generated_at"`
	Roots       []string       `json:"roots"`
	Repos       []ManifestRepo `json:"repos"`
}

type ManifestRepo struct {
	Slug      string `json:"slug"`
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	JSONL     string `json:"jsonl"`
	StateFile string `json:"state_file"`
	Status    string `json:"status"` // ok | failed | skipped
	Error     string `json:"error,omitempty"`
	Commits   int    `json:"commits,omitempty"`
	DurationMS int64 `json:"duration_ms,omitempty"`
}

// Result is returned to the caller so the CLI can drive the report stage
// with the concrete JSONL paths (no need to re-read the manifest).
type Result struct {
	OutputDir string
	Manifest  Manifest
	JSONLs    []string // only successful repos
}

// Run discovers repos under cfg.Roots, writes one JSONL per repo via
// the existing extract pipeline, and emits a manifest. Failures on
// individual repos are recorded in the manifest with Status="failed"
// but do not abort the whole scan — the report can still be generated
// from the repos that succeeded.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if len(cfg.Roots) == 0 {
		return nil, fmt.Errorf("scan: at least one --root is required")
	}
	if cfg.Output == "" {
		return nil, fmt.Errorf("scan: --output is required")
	}
	if err := os.MkdirAll(cfg.Output, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}
	if cfg.Parallel <= 0 {
		cfg.Parallel = 1
	}

	matcher, err := loadMatcher(cfg)
	if err != nil {
		return nil, err
	}

	repos, err := Discover(ctx, cfg.Roots, matcher, cfg.MaxDepth)
	if err != nil {
		return nil, err
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("scan: no git repositories found under %v", cfg.Roots)
	}
	log.Printf("scan: discovered %d repositories", len(repos))

	manifest := Manifest{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Roots:       cfg.Roots,
		Repos:       make([]ManifestRepo, len(repos)),
	}
	// Pre-fill every slot with the repo's identity and a "pending"
	// status. If the user cancels (Ctrl+C) before a worker reaches a
	// repo, the manifest still names it with Status="pending" instead
	// of an empty struct that misleads "ok, 0 failed" accounting.
	for i, r := range repos {
		manifest.Repos[i] = ManifestRepo{
			Slug:      r.Slug,
			Path:      r.AbsPath,
			JSONL:     filepath.Join(cfg.Output, r.Slug+".jsonl"),
			StateFile: filepath.Join(cfg.Output, r.Slug+".state"),
			Status:    "pending",
		}
	}

	// Worker pool over repos. Each worker runs extract.Run against one
	// repo and writes to its dedicated JSONL + state file. The extract
	// package already checkpoints per-repo state, so an interrupted scan
	// can be resumed by re-invoking the command — completed repos will
	// be skipped (their state file's last SHA prevents re-emission) and
	// partials will resume.
	type job struct {
		idx  int
		repo Repo
	}
	jobs := make(chan job)
	var wg sync.WaitGroup

	for w := 0; w < cfg.Parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				manifest.Repos[j.idx] = runJobSafely(ctx, cfg, j.repo)
			}
		}()
	}

	go func() {
		for i, r := range repos {
			select {
			case <-ctx.Done():
				close(jobs)
				return
			case jobs <- job{idx: i, repo: r}:
			}
		}
		close(jobs)
	}()

	wg.Wait()

	manifestPath := filepath.Join(cfg.Output, "manifest.json")
	if err := writeManifest(manifestPath, manifest); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	var jsonls []string
	var ok, failed, pending int
	for _, r := range manifest.Repos {
		switch r.Status {
		case "ok":
			jsonls = append(jsonls, r.JSONL)
			ok++
		case "pending":
			pending++
		default:
			failed++
		}
	}
	log.Printf("scan: %d ok, %d failed, %d not started; manifest at %s", ok, failed, pending, manifestPath)

	res := &Result{
		OutputDir: cfg.Output,
		Manifest:  manifest,
		JSONLs:    jsonls,
	}
	// Surface cancellation as a non-nil error so the CLI exits non-zero
	// — but return the partial result anyway so the caller can still
	// inspect what completed before the interrupt.
	if err := ctx.Err(); err != nil {
		return res, err
	}
	return res, nil
}

func loadMatcher(cfg Config) (*Matcher, error) {
	// Per-root default: look for .gitcortex-ignore at the first root when
	// no explicit path was given. Single-file load is enough for the MVP;
	// if users need per-root rule sets they can concat them into one file.
	path := cfg.IgnoreFile
	if path == "" && len(cfg.Roots) > 0 {
		candidate := filepath.Join(cfg.Roots[0], ".gitcortex-ignore")
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
		}
	}
	if path == "" {
		return NewMatcher(nil), nil
	}
	return LoadMatcher(path)
}

// runJobFn is the injection point the worker pool uses. Default is
// the real runOne; tests override it to simulate extract panics and
// verify the pool's recovery path. Not exposed via any user-facing
// API — this is strictly a test seam.
var runJobFn = runOne

// runJobSafely wraps runJobFn in a defer-recover. If extract panics
// inside a worker the goroutine would otherwise crash the entire
// process and the repo's manifest slot would stay at "pending", which
// is indistinguishable from a job that was never dispatched. Convert
// panics to a well-formed ManifestRepo with Status="failed" so the
// manifest reflects reality and the rest of the pool keeps running.
//
// extract.Run doesn't panic under normal inputs — the recover exists
// for the "impossible" cases (e.g. a git binary that writes malformed
// output, nil-deref bugs introduced by a future refactor). Better to
// isolate the blast radius than leave a silent corruption mode.
func runJobSafely(ctx context.Context, cfg Config, repo Repo) (entry ManifestRepo) {
	defer func() {
		if r := recover(); r != nil {
			entry = ManifestRepo{
				Slug:      repo.Slug,
				Path:      repo.AbsPath,
				JSONL:     filepath.Join(cfg.Output, repo.Slug+".jsonl"),
				StateFile: filepath.Join(cfg.Output, repo.Slug+".state"),
				Status:    "failed",
				Error:     fmt.Sprintf("panic: %v", r),
			}
			log.Printf("scan: [%s] panicked: %v", repo.Slug, r)
		}
	}()
	return runJobFn(ctx, cfg, repo)
}

func runOne(ctx context.Context, cfg Config, repo Repo) ManifestRepo {
	jsonlPath := filepath.Join(cfg.Output, repo.Slug+".jsonl")
	statePath := filepath.Join(cfg.Output, repo.Slug+".state")
	entry := ManifestRepo{
		Slug:      repo.Slug,
		Path:      repo.AbsPath,
		JSONL:     jsonlPath,
		StateFile: statePath,
	}

	// Skip cleanly if the user already cancelled before this worker
	// picked up the job — avoids a noisy "extracting..." log followed
	// by an immediate ctx.Err() from extract.
	if err := ctx.Err(); err != nil {
		entry.Status = "skipped"
		entry.Error = err.Error()
		return entry
	}

	branch := cfg.Extract.Branch
	if branch == "" {
		branch = git.DetectDefaultBranch(repo.AbsPath)
	}
	if branch == "" {
		entry.Status = "failed"
		entry.Error = "could not detect default branch"
		return entry
	}
	entry.Branch = branch

	// Copy the extract config so each worker gets its own Repo/Output/State
	// without racing on shared fields. Preserves ignore patterns, batch
	// size, mailmap, etc. from the template.
	sub := cfg.Extract
	sub.Repo = repo.AbsPath
	sub.Branch = branch
	sub.Output = jsonlPath
	sub.StateFile = statePath
	if sub.CommandTimeout == 0 {
		sub.CommandTimeout = extract.DefaultCommandTimeout
	}
	// Always force StartOffset back to -1 (= "load from state file")
	// regardless of the template's value. Programmatic callers passing
	// a zero-value Config would otherwise feed flagOffset=0 to
	// extract.LoadState, which would IGNORE the per-repo state file and
	// re-extract from scratch. StartSHA gets cleared for the same
	// reason: a single CLI-level SHA cannot apply to N different repos.
	sub.StartSHA = ""
	sub.StartOffset = -1

	log.Printf("scan: [%s] extracting from %s (branch %s)", repo.Slug, repo.AbsPath, branch)

	start := time.Now()
	if err := extract.Run(ctx, sub); err != nil {
		entry.Status = "failed"
		entry.Error = fmt.Errorf("extract %s: %w", repo.Slug, err).Error()
		entry.DurationMS = time.Since(start).Milliseconds()
		log.Printf("scan: [%s] failed: %v", repo.Slug, err)
		return entry
	}
	entry.Status = "ok"
	entry.DurationMS = time.Since(start).Milliseconds()
	return entry
}

func writeManifest(path string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
