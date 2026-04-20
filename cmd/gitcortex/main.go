package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lex0c/gitcortex/internal/extract"
	"github.com/lex0c/gitcortex/internal/git"
	reportpkg "github.com/lex0c/gitcortex/internal/report"
	"github.com/lex0c/gitcortex/internal/scan"
	"github.com/lex0c/gitcortex/internal/stats"

	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	rootCmd := &cobra.Command{
		Use:     "gitcortex",
		Short:   "Repository behavior analyzer from git history",
		Version: version,
	}

	rootCmd.AddCommand(extractCmd())
	rootCmd.AddCommand(statsCmd())
	rootCmd.AddCommand(diffCmd())
	rootCmd.AddCommand(ciCmd())
	rootCmd.AddCommand(reportCmd())
	rootCmd.AddCommand(scanCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func extractCmd() *cobra.Command {
	var cfg extract.Config

	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract commit data from a Git repository into JSONL",
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath, err := filepath.Abs(cfg.Repo)
			if err != nil {
				return fmt.Errorf("resolve repo path: %w", err)
			}
			cfg.Repo = repoPath

			if !cmd.Flags().Changed("branch") {
				cfg.Branch = git.DetectDefaultBranch(repoPath)
			}

			if cfg.CommandTimeout == 0 {
				cfg.CommandTimeout = extract.DefaultCommandTimeout
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return extract.Run(ctx, cfg)
		},
	}

	cmd.Flags().StringVar(&cfg.Repo, "repo", ".", "Path to the Git repository")
	cmd.Flags().IntVar(&cfg.BatchSize, "batch-size", 1000, "Checkpoint interval: flush output and save state every N commits")
	cmd.Flags().StringVar(&cfg.Output, "output", "git_data.jsonl", "Output JSONL file path")
	cmd.Flags().StringVar(&cfg.StateFile, "state-file", "git_state", "File to persist worker state")
	cmd.Flags().IntVar(&cfg.StartOffset, "start-offset", -1, "Number of commits to skip before processing")
	cmd.Flags().StringVar(&cfg.StartSHA, "start-sha", "", "Last processed commit SHA to resume after")
	cmd.Flags().StringVar(&cfg.Branch, "branch", "main", "Branch or ref to traverse")
	cmd.Flags().BoolVar(&cfg.IncludeMessages, "include-commit-messages", false, "Include commit messages in output")
	cmd.Flags().DurationVar(&cfg.CommandTimeout, "command-timeout", extract.DefaultCommandTimeout, "Maximum duration for git commands")
	cmd.Flags().BoolVar(&cfg.FirstParent, "first-parent", false, "Restrict to first-parent chain")
	cmd.Flags().BoolVar(&cfg.Mailmap, "mailmap", false, "Use .mailmap to normalize author/committer identities")
	cmd.Flags().StringSliceVar(&cfg.IgnorePatterns, "ignore", nil, "Glob patterns to exclude files (e.g. package-lock.json, *.min.js)")

	return cmd
}

// --- Stats ---

func isValidFormat(s string) bool {
	switch s {
	case "table", "csv", "json":
		return true
	}
	return false
}

func isValidGranularity(s string) bool {
	switch s {
	case "day", "week", "month", "year":
		return true
	}
	return false
}

func isValidStat(s string) bool {
	switch s {
	case "summary", "contributors", "hotspots", "directories", "extensions",
		"activity", "busfactor", "coupling", "churn-risk", "working-patterns",
		"dev-network", "profile", "top-commits", "pareto", "structure":
		return true
	}
	return false
}

type statsFlags struct {
	inputs             []string
	format             string
	topN               int
	granularity        string
	stat               string
	since              string
	from               string
	to                 string
	couplingMaxFiles   int
	couplingMinChanges int
	churnHalfLife      int
	networkMinFiles    int
	email              string
	treeDepth          int
}

func addStatsFlags(cmd *cobra.Command, sf *statsFlags) {
	cmd.Flags().StringSliceVar(&sf.inputs, "input", []string{"git_data.jsonl"}, "Input JSONL file(s) from extract (repeatable for multi-repo)")
	cmd.Flags().StringVar(&sf.format, "format", "table", "Output format: table, csv, json")
	cmd.Flags().IntVar(&sf.topN, "top", 10, "Number of top entries to show (0 = all)")
	cmd.Flags().StringVar(&sf.granularity, "granularity", "month", "Activity granularity: day, week, month, year")
	cmd.Flags().StringVar(&sf.stat, "stat", "", "Show a specific stat: summary, contributors, hotspots, directories, extensions, activity, busfactor, coupling, churn-risk, working-patterns, dev-network, profile, top-commits, pareto, structure")
	cmd.Flags().IntVar(&sf.couplingMaxFiles, "coupling-max-files", 50, "Max files per commit for coupling analysis")
	cmd.Flags().IntVar(&sf.couplingMinChanges, "coupling-min-changes", 5, "Min co-changes for coupling results")
	cmd.Flags().IntVar(&sf.churnHalfLife, "churn-half-life", 90, "Half-life in days for churn decay (churn-risk)")
	cmd.Flags().IntVar(&sf.networkMinFiles, "network-min-files", 5, "Min shared files for dev-network edges")
	cmd.Flags().StringVar(&sf.email, "email", "", "Filter by developer email (for profile stat)")
	cmd.Flags().StringVar(&sf.since, "since", "", "Filter to recent period (e.g. 7d, 4w, 3m, 1y)")
	cmd.Flags().StringVar(&sf.from, "from", "", "Window start date YYYY-MM-DD, inclusive (pair with --to for closed window; leave --to empty for open-ended)")
	cmd.Flags().StringVar(&sf.to, "to", "", "Window end date YYYY-MM-DD, inclusive (pair with --from; leave --from empty for 'up to this date')")
	cmd.Flags().IntVar(&sf.treeDepth, "tree-depth", 3, "Max depth for --stat structure (0 = unlimited)")
}

func validateStatsFlags(sf *statsFlags) error {
	if !isValidFormat(sf.format) {
		return fmt.Errorf("invalid --format %q; must be one of: table, csv, json", sf.format)
	}
	if !isValidGranularity(sf.granularity) {
		return fmt.Errorf("invalid --granularity %q; must be one of: day, week, month, year", sf.granularity)
	}
	if sf.stat != "" && !isValidStat(sf.stat) {
		return fmt.Errorf("invalid --stat %q; valid: summary, contributors, hotspots, directories, extensions, activity, busfactor, coupling, churn-risk, working-patterns, dev-network, profile, top-commits, pareto, structure", sf.stat)
	}
	if sf.since != "" && (sf.from != "" || sf.to != "") {
		return fmt.Errorf("--since cannot be combined with --from/--to; pick one window spec")
	}
	if err := validateDate(sf.from, "--from"); err != nil {
		return err
	}
	if err := validateDate(sf.to, "--to"); err != nil {
		return err
	}
	if sf.from != "" && sf.to != "" && sf.from > sf.to {
		return fmt.Errorf("--from (%s) must be on or before --to (%s)", sf.from, sf.to)
	}
	return nil
}

func statsCmd() *cobra.Command {
	var sf statsFlags

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Generate statistics from extracted JSONL data",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateStatsFlags(&sf); err != nil {
				return err
			}

			fromDate := sf.from
			if sf.since != "" {
				d, err := parseSince(sf.since)
				if err != nil {
					return err
				}
				fromDate = d
			}

			ds, err := stats.LoadMultiJSONL(sf.inputs, stats.LoadOptions{
				From:         fromDate,
				To:           sf.to,
				HalfLifeDays: sf.churnHalfLife,
				CoupMaxFiles: sf.couplingMaxFiles,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Loaded %d commits, %d files, %d devs\n\n",
				ds.CommitCount, ds.UniqueFileCount, ds.DevCount)

			// Suspect vendor/generated warning only fires when the
			// aggregate matched churn exceeds suspectWarningMinChurnRatio
			// of the total. Text-format stats only: JSON/CSV consumers
			// typically pipe the output and don't want a chatter prefix.
			if sf.format == "" || sf.format == "table" {
				if buckets, worth := stats.DetectSuspectFiles(ds); worth {
					printSuspectWarning(os.Stderr, buckets)
				}
			}

			return renderStats(ds, &sf)
		},
	}

	addStatsFlags(cmd, &sf)
	return cmd
}

// printSuspectWarning emits a stderr block listing likely vendor/generated
// patterns that matched, with a copy-pasteable --ignore suggestion. Called
// only when DetectSuspectFiles reports the matched churn crosses the
// noise floor, so repos with one incidental .lock file don't get spammed.
func printSuspectWarning(w io.Writer, buckets []stats.SuspectBucket) {
	if len(buckets) == 0 {
		return
	}
	// Top 6 buckets — enough to be useful, not enough to drown the prompt.
	const maxShown = 6
	shown := buckets
	if len(shown) > maxShown {
		shown = shown[:maxShown]
	}
	fmt.Fprintln(w, "⚠  Suspect vendor/generated paths detected — they inflate churn and bus factor")
	fmt.Fprintln(w, "   without reflecting hand-authored code. Top matches:")
	for _, b := range shown {
		fmt.Fprintf(w, "     %-22s %4d files, %8d churn   (%s)\n",
			b.Pattern.Glob, len(b.Paths), b.Churn, b.Pattern.Reason)
	}
	if len(buckets) > len(shown) {
		fmt.Fprintf(w, "     ... and %d more bucket(s) — see suggestion below for full set\n",
			len(buckets)-len(shown))
	}
	// Suggestions cover ALL buckets, not just the shown subset — the
	// warning threshold is computed over every bucket, so a remediation
	// that skips unshown ones would leave the warning firing after the
	// suggested fix.
	suggestions := stats.CollectAllSuggestions(buckets)
	fmt.Fprint(w, "   Rerun extract with --ignore to drop them, e.g.:\n     gitcortex extract --repo .")
	for _, s := range suggestions {
		fmt.Fprintf(w, " --ignore %s", stats.ShellQuoteSingle(s))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w)
}

func renderStats(ds *stats.Dataset, sf *statsFlags) error {
	showAll := sf.stat == ""
	f := stats.NewFormatter(os.Stdout, sf.format)

	if sf.format == "json" {
		return renderStatsJSON(f, ds, sf)
	}

	if showAll || sf.stat == "summary" {
		fmt.Fprintln(os.Stderr, "=== Summary ===")
		if err := f.PrintSummary(stats.ComputeSummary(ds)); err != nil {
			return err
		}
	}
	if showAll || sf.stat == "contributors" {
		fmt.Fprintf(os.Stderr, "\n=== Top %d Contributors ===\n", sf.topN)
		if err := f.PrintContributors(stats.TopContributors(ds, sf.topN)); err != nil {
			return err
		}
	}
	if showAll || sf.stat == "hotspots" {
		fmt.Fprintf(os.Stderr, "\n=== Top %d File Hotspots ===\n", sf.topN)
		if err := f.PrintHotspots(stats.FileHotspots(ds, sf.topN)); err != nil {
			return err
		}
	}
	if showAll || sf.stat == "directories" {
		fmt.Fprintf(os.Stderr, "\n=== Top %d Directories ===\n", sf.topN)
		if err := f.PrintDirectories(stats.DirectoryStats(ds, sf.topN)); err != nil {
			return err
		}
	}
	if showAll || sf.stat == "extensions" {
		fmt.Fprintf(os.Stderr, "\n=== Top %d Extensions ===\n", sf.topN)
		if err := f.PrintExtensions(stats.ExtensionStats(ds, sf.topN)); err != nil {
			return err
		}
	}
	if showAll || sf.stat == "activity" {
		fmt.Fprintf(os.Stderr, "\n=== Activity (%s) ===\n", sf.granularity)
		if err := f.PrintActivity(stats.ActivityOverTime(ds, sf.granularity)); err != nil {
			return err
		}
	}
	if showAll || sf.stat == "busfactor" {
		fmt.Fprintf(os.Stderr, "\n=== Top %d Bus Factor Risk ===\n", sf.topN)
		if err := f.PrintBusFactor(stats.BusFactor(ds, sf.topN)); err != nil {
			return err
		}
	}
	if showAll || sf.stat == "coupling" {
		fmt.Fprintf(os.Stderr, "\n=== Top %d File Coupling ===\n", sf.topN)
		if err := f.PrintCoupling(stats.FileCoupling(ds, sf.topN, sf.couplingMinChanges)); err != nil {
			return err
		}
	}
	if showAll || sf.stat == "churn-risk" {
		fmt.Fprintf(os.Stderr, "\n=== Top %d Churn Risk ===\n", sf.topN)
		if err := f.PrintChurnRisk(stats.ChurnRisk(ds, sf.topN)); err != nil {
			return err
		}
	}
	if showAll || sf.stat == "working-patterns" {
		fmt.Fprintln(os.Stderr, "\n=== Working Patterns ===")
		if err := f.PrintWorkingPatterns(stats.WorkingPatterns(ds)); err != nil {
			return err
		}
	}
	if showAll || sf.stat == "dev-network" {
		fmt.Fprintf(os.Stderr, "\n=== Top %d Developer Connections ===\n", sf.topN)
		if err := f.PrintDevNetwork(stats.DeveloperNetwork(ds, sf.topN, sf.networkMinFiles)); err != nil {
			return err
		}
	}
	if sf.stat == "profile" {
		label := "All Developers"
		if sf.email != "" {
			label = sf.email
		}
		fmt.Fprintf(os.Stderr, "\n=== Profile: %s ===\n", label)
		if err := f.PrintProfiles(stats.DevProfiles(ds, sf.email, 0)); err != nil {
			return err
		}
	}
	if showAll || sf.stat == "pareto" {
		fmt.Fprintln(os.Stderr, "\n=== Concentration (Pareto) ===")
		p := reportpkg.ComputePareto(ds)
		// Labels are precomputed in ComputePareto so CLI and HTML share
		// one source of truth on thresholds and wording.
		fmt.Fprintf(os.Stdout, "Files:  %d of %d files concentrate 80%% of churn — %s\n", p.TopChurnFiles, p.TotalFiles, p.FilesLabel)
		fmt.Fprintf(os.Stdout, "Devs (commits): %d of %d devs produce 80%% of commits — %s\n", p.TopCommitDevs, p.TotalDevs, p.DevsCommitsLabel)
		fmt.Fprintf(os.Stdout, "Devs (churn):   %d of %d devs produce 80%% of line churn — %s\n", p.TopChurnDevs, p.TotalDevs, p.DevsChurnLabel)
		fmt.Fprintf(os.Stdout, "Dirs:   %d of %d dirs concentrate 80%% of churn — %s\n", p.TopChurnDirs, p.TotalDirs, p.DirsLabel)
	}
	if showAll || sf.stat == "top-commits" {
		fmt.Fprintf(os.Stderr, "\n=== Top %d Commits ===\n", sf.topN)
		if err := f.PrintTopCommits(stats.TopCommits(ds, sf.topN)); err != nil {
			return err
		}
	}
	if sf.stat == "structure" {
		root := reportpkg.BuildRepoTree(stats.FileHotspots(ds, 0), sf.treeDepth)
		// CSV skips the stderr banner — downstream parsers sometimes
		// tail stderr onto stdout, and a stray "=== ... ===" would
		// break the single-table contract.
		if sf.format != "csv" {
			depthLabel := "unlimited"
			if sf.treeDepth > 0 {
				depthLabel = fmt.Sprintf("%d", sf.treeDepth)
			}
			fmt.Fprintf(os.Stderr, "\n=== Repo Structure (depth %s) ===\n", depthLabel)
		}
		if err := reportpkg.RenderTreeForFormat(os.Stdout, root, sf.format); err != nil {
			return err
		}
	}

	return nil
}

func renderStatsJSON(f *stats.Formatter, ds *stats.Dataset, sf *statsFlags) error {
	showAll := sf.stat == ""
	report := make(map[string]interface{})

	if showAll || sf.stat == "summary" {
		report["summary"] = stats.ComputeSummary(ds)
	}
	if showAll || sf.stat == "contributors" {
		report["contributors"] = stats.TopContributors(ds, sf.topN)
	}
	if showAll || sf.stat == "hotspots" {
		report["hotspots"] = stats.FileHotspots(ds, sf.topN)
	}
	if showAll || sf.stat == "directories" {
		report["directories"] = stats.DirectoryStats(ds, sf.topN)
	}
	if showAll || sf.stat == "extensions" {
		report["extensions"] = stats.ExtensionStats(ds, sf.topN)
	}
	if showAll || sf.stat == "activity" {
		report["activity"] = stats.ActivityOverTime(ds, sf.granularity)
	}
	if showAll || sf.stat == "busfactor" {
		report["busfactor"] = stats.BusFactor(ds, sf.topN)
	}
	if showAll || sf.stat == "coupling" {
		report["coupling"] = stats.FileCoupling(ds, sf.topN, sf.couplingMinChanges)
	}
	if showAll || sf.stat == "churn-risk" {
		report["churn_risk"] = stats.ChurnRisk(ds, sf.topN)
	}
	if showAll || sf.stat == "working-patterns" {
		report["working_patterns"] = stats.WorkingPatterns(ds)
	}
	if showAll || sf.stat == "dev-network" {
		report["dev_network"] = stats.DeveloperNetwork(ds, sf.topN, sf.networkMinFiles)
	}
	if sf.stat == "profile" {
		report["profiles"] = stats.DevProfiles(ds, sf.email, 0)
	}
	if showAll || sf.stat == "pareto" {
		report["pareto"] = reportpkg.ComputePareto(ds)
	}
	if showAll || sf.stat == "top-commits" {
		report["top_commits"] = stats.TopCommits(ds, sf.topN)
	}
	if sf.stat == "structure" {
		report["structure"] = reportpkg.BuildRepoTree(stats.FileHotspots(ds, 0), sf.treeDepth)
	}

	return f.PrintReport(report)
}

// --- Diff ---

func diffCmd() *cobra.Command {
	var (
		input string
		from  string
		to    string
		vsFrom string
		vsTo   string
		format string
		topN   int
	)

	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Compare stats between two time periods",
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" || to == "" {
				return fmt.Errorf("--from and --to are required (format: YYYY-MM-DD)")
			}
			if !isValidFormat(format) {
				return fmt.Errorf("invalid --format %q; must be one of: table, csv, json", format)
			}

			optsA := stats.LoadOptions{From: from, To: to, HalfLifeDays: 90, CoupMaxFiles: 50}
			periodA, err := stats.LoadJSONL(input, optsA)
			if err != nil {
				return err
			}
			labelA := fmt.Sprintf("%s to %s", from, to)

			fmt.Fprintf(os.Stderr, "Period A (%s): %d commits, %d files\n",
				labelA, periodA.CommitCount, periodA.UniqueFileCount)

			if vsFrom != "" && vsTo != "" {
				optsB := stats.LoadOptions{From: vsFrom, To: vsTo, HalfLifeDays: 90, CoupMaxFiles: 50}
				periodB, err := stats.LoadJSONL(input, optsB)
				if err != nil {
					return err
				}
				labelB := fmt.Sprintf("%s to %s", vsFrom, vsTo)

				fmt.Fprintf(os.Stderr, "Period B (%s): %d commits, %d files\n\n",
					labelB, periodB.CommitCount, periodB.UniqueFileCount)

				return renderDiff(periodA, periodB, labelA, labelB, format, topN)
			}

			fmt.Fprintln(os.Stderr)

			sf := &statsFlags{format: format, topN: topN, granularity: "month",
				couplingMaxFiles: 50, couplingMinChanges: 5, churnHalfLife: 90, networkMinFiles: 5}
			return renderStats(periodA, sf)
		},
	}

	cmd.Flags().StringVar(&input, "input", "git_data.jsonl", "Input JSONL file")
	cmd.Flags().StringVar(&from, "from", "", "Start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&to, "to", "", "End date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&vsFrom, "vs-from", "", "Comparison period start date")
	cmd.Flags().StringVar(&vsTo, "vs-to", "", "Comparison period end date")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table, csv, json")
	cmd.Flags().IntVar(&topN, "top", 10, "Number of top entries (0 = all)")

	return cmd
}

func renderDiff(a, b *stats.Dataset, labelA, labelB, format string, topN int) error {
	f := stats.NewFormatter(os.Stdout, format)

	summA := stats.ComputeSummary(a)
	summB := stats.ComputeSummary(b)

	if format == "json" {
		report := map[string]interface{}{
			"period_a": map[string]interface{}{
				"label":        labelA,
				"summary":      summA,
				"contributors": stats.TopContributors(a, topN),
				"hotspots":     stats.FileHotspots(a, topN),
			},
			"period_b": map[string]interface{}{
				"label":        labelB,
				"summary":      summB,
				"contributors": stats.TopContributors(b, topN),
				"hotspots":     stats.FileHotspots(b, topN),
			},
		}
		return f.PrintReport(report)
	}

	fmt.Fprintf(os.Stderr, "=== Summary: %s vs %s ===\n", labelA, labelB)
	printDiffLine := func(label string, va, vb int) {
		delta := vb - va
		sign := "+"
		if delta < 0 {
			sign = ""
		}
		fmt.Fprintf(os.Stdout, "%-25s %8d  →  %8d  (%s%d)\n", label, va, vb, sign, delta)
	}

	printDiffLine("Commits", summA.TotalCommits, summB.TotalCommits)
	printDiffLine("Additions", int(summA.TotalAdditions), int(summB.TotalAdditions))
	printDiffLine("Deletions", int(summA.TotalDeletions), int(summB.TotalDeletions))
	printDiffLine("Files touched", summA.TotalFiles, summB.TotalFiles)
	printDiffLine("Merge commits", summA.MergeCommits, summB.MergeCommits)

	fmt.Fprintf(os.Stderr, "\n=== Top %d Contributors: %s ===\n", topN, labelA)
	if err := f.PrintContributors(stats.TopContributors(a, topN)); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\n=== Top %d Contributors: %s ===\n", topN, labelB)
	if err := f.PrintContributors(stats.TopContributors(b, topN)); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\n=== Top %d Hotspots: %s ===\n", topN, labelA)
	if err := f.PrintHotspots(stats.FileHotspots(a, topN)); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\n=== Top %d Hotspots: %s ===\n", topN, labelB)
	return f.PrintHotspots(stats.FileHotspots(b, topN))
}

// --- CI ---

func isValidCIFormat(s string) bool {
	switch s {
	case "text", "github-actions", "gitlab", "json":
		return true
	}
	return false
}

func ciCmd() *cobra.Command {
	var (
		input          string
		format         string
		bfThreshold    int
		churnThreshold float64
		halfLife       int
	)

	cmd := &cobra.Command{
		Use:   "ci",
		Short: "Run quality gates for CI pipelines",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isValidCIFormat(format) {
				return fmt.Errorf("invalid --format %q; must be one of: text, github-actions, gitlab, json", format)
			}

			ds, err := stats.LoadJSONL(input, stats.LoadOptions{HalfLifeDays: halfLife, CoupMaxFiles: 50})
			if err != nil {
				return err
			}

			var violations []ciViolation

			if bfThreshold > 0 {
				for _, bf := range stats.BusFactor(ds, 0) {
					if bf.BusFactor <= bfThreshold {
						violations = append(violations, ciViolation{
							File:    bf.Path,
							Rule:    "busfactor",
							Message: fmt.Sprintf("Bus factor %d (only %s)", bf.BusFactor, stats.JoinDevs(bf.TopDevs)),
							Level:   "warning",
						})
					}
				}
			}

			if churnThreshold > 0 {
				for _, cr := range stats.ChurnRisk(ds, 0) {
					if cr.RiskScore >= churnThreshold {
						violations = append(violations, ciViolation{
							File:    cr.Path,
							Rule:    "churn-risk",
							Message: fmt.Sprintf("Churn risk %.1f exceeds threshold %.0f", cr.RiskScore, churnThreshold),
							Level:   "warning",
						})
					}
				}
			}

			switch format {
			case "github-actions":
				for _, v := range violations {
					fmt.Printf("::%s file=%s::%s\n", v.Level, v.File, v.Message)
				}
			case "gitlab":
				printGitlabCodeQuality(violations)
			case "json":
				printCIJSON(violations)
			default:
				for _, v := range violations {
					fmt.Printf("[%s] %s: %s\n", v.Level, v.File, v.Message)
				}
			}

			if len(violations) > 0 {
				fmt.Fprintf(os.Stderr, "\n%d violation(s) found\n", len(violations))
				cmd.SilenceUsage = true
				return fmt.Errorf("%d violation(s)", len(violations))
			}

			fmt.Fprintln(os.Stderr, "No violations found")
			return nil
		},
	}

	cmd.Flags().StringVar(&input, "input", "git_data.jsonl", "Input JSONL file")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text, github-actions, gitlab, json")
	cmd.Flags().IntVar(&bfThreshold, "fail-on-busfactor", 0, "Fail if any file has bus factor <= N (0 = disabled)")
	cmd.Flags().Float64Var(&churnThreshold, "fail-on-churn-risk", 0, "Fail if any file has churn risk >= N (0 = disabled)")
	cmd.Flags().IntVar(&halfLife, "churn-half-life", 90, "Half-life in days for churn decay")

	return cmd
}

type ciViolation struct {
	File    string `json:"file"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
	Level   string `json:"level"`
}


func printGitlabCodeQuality(violations []ciViolation) {
	type glIssue struct {
		Description string `json:"description"`
		Severity    string `json:"severity"`
		Location    struct {
			Path string `json:"path"`
		} `json:"location"`
	}

	issues := make([]glIssue, len(violations))
	for i, v := range violations {
		issues[i].Description = fmt.Sprintf("[%s] %s", v.Rule, v.Message)
		issues[i].Severity = "minor"
		issues[i].Location.Path = v.File
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(issues)
}

func printCIJSON(violations []ciViolation) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(violations)
}

// validateDate accepts "" (treated as "no bound") or a YYYY-MM-DD
// literal. The stats loader compares dates as strings — ISO-8601 date
// literals sort lexicographically, so comparison semantics don't need
// a time.Time round-trip. Parse is still used here to reject
// garbage like "2024-13-40" up front with a clear CLI error instead
// of silently loading an empty window.
func validateDate(s, flag string) error {
	if s == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return fmt.Errorf("invalid %s %q; expected YYYY-MM-DD", flag, s)
	}
	return nil
}

func parseSince(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if len(s) < 2 {
		return "", fmt.Errorf("invalid --since %q; use e.g. 7d, 4w, 3m, 1y", s)
	}
	numStr := s[:len(s)-1]
	unit := s[len(s)-1]

	n := 0
	for _, c := range numStr {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("invalid --since %q; use e.g. 7d, 4w, 3m, 1y", s)
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return "", fmt.Errorf("invalid --since %q; number must be positive", s)
	}

	now := time.Now()
	var from time.Time
	switch unit {
	case 'd':
		from = now.AddDate(0, 0, -n)
	case 'w':
		from = now.AddDate(0, 0, -n*7)
	case 'm':
		from = now.AddDate(0, -n, 0)
	case 'y':
		from = now.AddDate(-n, 0, 0)
	default:
		return "", fmt.Errorf("invalid --since unit %q; use d (days), w (weeks), m (months), y (years)", string(unit))
	}

	return from.Format("2006-01-02"), nil
}

func absPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

// fileURL formats a local path as a file:// URL suitable for ctrl-clicking
// in modern terminal emulators (iTerm, kitty, Windows Terminal, recent
// gnome-terminal). The absolute path is used so relative --output values
// still produce a valid link; filepath.ToSlash handles Windows separators;
// url.URL takes care of escaping spaces and special characters. If the
// terminal doesn't linkify file://, the result is still a legible path —
// no harm done in SSH/CI contexts where the link would not resolve.
func fileURL(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return u.String()
}

// --- Report ---

func reportCmd() *cobra.Command {
	var (
		input              string
		output             string
		topN               int
		email              string
		since              string
		from               string
		to                 string
		couplingMaxFiles   int
		couplingMinChanges int
		churnHalfLife      int
		networkMinFiles    int
	)

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate a self-contained HTML report",
		RunE: func(cmd *cobra.Command, args []string) error {
			// --since and --from/--to express overlapping intent
			// (select a window); combining them is ambiguous — does
			// --since push the start past --from, or does --from
			// override? Reject the combination explicitly instead of
			// picking one silently.
			if since != "" && (from != "" || to != "") {
				return fmt.Errorf("--since cannot be combined with --from/--to; pick one window spec")
			}
			if err := validateDate(from, "--from"); err != nil {
				return err
			}
			if err := validateDate(to, "--to"); err != nil {
				return err
			}
			if from != "" && to != "" && from > to {
				return fmt.Errorf("--from (%s) must be on or before --to (%s)", from, to)
			}

			fromDate := from
			if since != "" {
				d, err := parseSince(since)
				if err != nil {
					return err
				}
				fromDate = d
			}

			ds, err := stats.LoadJSONL(input, stats.LoadOptions{
				From:         fromDate,
				To:           to,
				HalfLifeDays: churnHalfLife,
				CoupMaxFiles: couplingMaxFiles,
			})
			if err != nil {
				return err
			}

			f, err := os.Create(output)
			if err != nil {
				return fmt.Errorf("create %s: %w", output, err)
			}
			defer f.Close()

			sf := stats.StatsFlags{
				CouplingMinChanges: couplingMinChanges,
				NetworkMinFiles:    networkMinFiles,
			}

			repoName := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
			if repoName == "git_data" {
				repoName = filepath.Base(filepath.Dir(absPath(input)))
			}

			if email != "" {
				if err := reportpkg.GenerateProfile(f, ds, repoName, email); err != nil {
					return fmt.Errorf("generate profile: %w", err)
				}
				fmt.Fprintf(os.Stderr, "Profile report for %s written to %s\n", email, fileURL(output))
			} else {
				if err := reportpkg.Generate(f, ds, repoName, topN, sf); err != nil {
					return fmt.Errorf("generate report: %w", err)
				}
				fmt.Fprintf(os.Stderr, "Report written to %s (%d commits, %d devs)\n", fileURL(output), ds.CommitCount, ds.DevCount)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&input, "input", "git_data.jsonl", "Input JSONL file")
	cmd.Flags().StringVar(&output, "output", "report.html", "Output HTML file")
	cmd.Flags().StringVar(&email, "email", "", "Generate profile report for a specific developer")
	cmd.Flags().IntVar(&topN, "top", 20, "Number of top entries per section (0 = all)")
	cmd.Flags().IntVar(&couplingMaxFiles, "coupling-max-files", 50, "Max files per commit for coupling")
	cmd.Flags().IntVar(&couplingMinChanges, "coupling-min-changes", 5, "Min co-changes for coupling")
	cmd.Flags().IntVar(&churnHalfLife, "churn-half-life", 90, "Half-life in days for churn decay")
	cmd.Flags().IntVar(&networkMinFiles, "network-min-files", 5, "Min shared files for dev-network edges")
	cmd.Flags().StringVar(&since, "since", "", "Filter to recent period (e.g. 7d, 4w, 3m, 1y)")
	cmd.Flags().StringVar(&from, "from", "", "Window start date YYYY-MM-DD, inclusive (pair with --to for closed window; leave --to empty for open-ended)")
	cmd.Flags().StringVar(&to, "to", "", "Window end date YYYY-MM-DD, inclusive (pair with --from; leave --from empty for 'up to this date')")

	return cmd
}

// --- Scan ---

func scanCmd() *cobra.Command {
	var (
		roots              []string
		output             string
		ignoreFile         string
		maxDepth           int
		parallel           int
		email              string
		from               string
		to                 string
		since              string
		reportPath         string
		topN               int
		extractIgnore      []string
		batchSize          int
		mailmap            bool
		firstParent        bool
		includeMessages    bool
		couplingMaxFiles   int
		couplingMinChanges int
		churnHalfLife      int
		networkMinFiles    int
	)

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Discover git repositories under one or more roots and consolidate their history",
		Long: `Walk the given root(s), find every git repository, and run extract on each
repository in parallel. Outputs one JSONL per repo plus a manifest in --output.
Optionally generates a consolidated HTML report including a per-repository
breakdown — handy for showing aggregated work across many repos.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(roots) == 0 {
				return fmt.Errorf("--root is required (repeatable for multiple roots)")
			}
			if since != "" && (from != "" || to != "") {
				return fmt.Errorf("--since cannot be combined with --from/--to")
			}
			if err := validateDate(from, "--from"); err != nil {
				return err
			}
			if err := validateDate(to, "--to"); err != nil {
				return err
			}
			if from != "" && to != "" && from > to {
				return fmt.Errorf("--from (%s) must be on or before --to (%s)", from, to)
			}

			cfg := scan.Config{
				Roots:      roots,
				Output:     output,
				IgnoreFile: ignoreFile,
				MaxDepth:   maxDepth,
				Parallel:   parallel,
				Extract: extract.Config{
					BatchSize:       batchSize,
					IncludeMessages: includeMessages,
					CommandTimeout:  extract.DefaultCommandTimeout,
					FirstParent:     firstParent,
					Mailmap:         mailmap,
					IgnorePatterns:  extractIgnore,
					StartOffset:     -1,
				},
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			result, err := scan.Run(ctx, cfg)
			// scan.Run returns a partial result alongside ctx.Err() on
			// cancellation. Honor that — write whatever progress we made
			// to disk and surface the error so the CLI exits non-zero.
			if err != nil {
				return err
			}

			if reportPath == "" {
				fmt.Fprintf(os.Stderr, "Scan complete: %d JSONL file(s) in %s\n", len(result.JSONLs), result.OutputDir)
				return nil
			}
			if len(result.JSONLs) == 0 {
				return fmt.Errorf("no successful repos extracted; cannot build report")
			}

			fromDate := from
			if since != "" {
				d, err := parseSince(since)
				if err != nil {
					return err
				}
				fromDate = d
			}

			ds, err := stats.LoadMultiJSONL(result.JSONLs, stats.LoadOptions{
				From:         fromDate,
				To:           to,
				HalfLifeDays: churnHalfLife,
				CoupMaxFiles: couplingMaxFiles,
			})
			if err != nil {
				return fmt.Errorf("load consolidated dataset: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Loaded %d commits across %d repo(s)\n", ds.CommitCount, len(result.JSONLs))

			f, err := os.Create(reportPath)
			if err != nil {
				return fmt.Errorf("create report: %w", err)
			}
			defer f.Close()

			// Label the report after the basename of the first --root
			// (or the output dir as a fallback). "scan-scan-output" was
			// the previous default; users find the root name far more
			// recognizable as the H1 of the report.
			repoLabel := filepath.Base(result.OutputDir)
			if len(cfg.Roots) > 0 {
				repoLabel = filepath.Base(absPath(cfg.Roots[0]))
			}
			sf := stats.StatsFlags{CouplingMinChanges: couplingMinChanges, NetworkMinFiles: networkMinFiles}
			if email != "" {
				if err := reportpkg.GenerateProfile(f, ds, repoLabel, email); err != nil {
					return fmt.Errorf("generate profile report: %w", err)
				}
				fmt.Fprintf(os.Stderr, "Profile report for %s written to %s\n", email, fileURL(reportPath))
				return nil
			}
			if err := reportpkg.Generate(f, ds, repoLabel, topN, sf); err != nil {
				return fmt.Errorf("generate report: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Consolidated report written to %s\n", fileURL(reportPath))
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&roots, "root", nil, "Root directory to walk for repositories (repeatable)")
	cmd.Flags().StringVar(&output, "output", "scan-output", "Directory to write per-repo JSONL files and the manifest")
	cmd.Flags().StringVar(&ignoreFile, "ignore-file", "", "Gitignore-style file with directories to skip during discovery. When unset, only the first --root is searched for a .gitcortex-ignore; pass an explicit path to apply rules across all roots.")
	cmd.Flags().IntVar(&maxDepth, "max-depth", 0, "Maximum directory depth to descend into when looking for repos (0 = unlimited)")
	cmd.Flags().IntVar(&parallel, "parallel", 4, "Number of repositories to extract in parallel")
	cmd.Flags().StringVar(&email, "email", "", "Generate a per-developer profile report (only when --report is set)")
	cmd.Flags().StringVar(&from, "from", "", "Window start date YYYY-MM-DD (forwarded to the consolidated report)")
	cmd.Flags().StringVar(&to, "to", "", "Window end date YYYY-MM-DD (forwarded to the consolidated report)")
	cmd.Flags().StringVar(&since, "since", "", "Filter to recent period (e.g. 7d, 4w, 3m, 1y); mutually exclusive with --from/--to")
	cmd.Flags().StringVar(&reportPath, "report", "", "If set, generate a consolidated HTML report at this path after the scan")
	cmd.Flags().IntVar(&topN, "top", 20, "Top-N entries per section in the consolidated report")
	cmd.Flags().StringSliceVar(&extractIgnore, "extract-ignore", nil, "Glob patterns forwarded to per-repo extract --ignore (e.g. package-lock.json)")
	cmd.Flags().IntVar(&batchSize, "batch-size", 1000, "Per-repo extract checkpoint interval")
	cmd.Flags().BoolVar(&mailmap, "mailmap", false, "Use .mailmap (per repo) to normalize identities")
	cmd.Flags().BoolVar(&firstParent, "first-parent", false, "Restrict extracts to the first-parent chain")
	cmd.Flags().BoolVar(&includeMessages, "include-commit-messages", false, "Include commit messages in JSONL (needed for Top Commits in the consolidated report)")
	cmd.Flags().IntVar(&couplingMaxFiles, "coupling-max-files", 50, "Max files per commit for coupling analysis (consolidated report)")
	cmd.Flags().IntVar(&couplingMinChanges, "coupling-min-changes", 5, "Min co-changes for coupling results (consolidated report)")
	cmd.Flags().IntVar(&churnHalfLife, "churn-half-life", 90, "Half-life in days for churn decay (consolidated report)")
	cmd.Flags().IntVar(&networkMinFiles, "network-min-files", 5, "Min shared files for dev-network edges (consolidated report)")

	return cmd
}
