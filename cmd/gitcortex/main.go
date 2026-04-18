package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lex0c/gitcortex/internal/extract"
	"github.com/lex0c/gitcortex/internal/git"
	reportpkg "github.com/lex0c/gitcortex/internal/report"
	"github.com/lex0c/gitcortex/internal/stats"

	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	rootCmd := &cobra.Command{
		Use:     "gitcortex",
		Short:   "Git metrics extraction and analysis",
		Version: version,
	}

	rootCmd.AddCommand(extractCmd())
	rootCmd.AddCommand(statsCmd())
	rootCmd.AddCommand(diffCmd())
	rootCmd.AddCommand(ciCmd())
	rootCmd.AddCommand(reportCmd())

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
	case "summary", "contributors", "hotspots", "directories", "activity",
		"busfactor", "coupling", "churn-risk", "working-patterns",
		"dev-network", "profile", "top-commits", "pareto":
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
	couplingMaxFiles   int
	couplingMinChanges int
	churnHalfLife      int
	networkMinFiles    int
	email              string
}

func addStatsFlags(cmd *cobra.Command, sf *statsFlags) {
	cmd.Flags().StringSliceVar(&sf.inputs, "input", []string{"git_data.jsonl"}, "Input JSONL file(s) from extract (repeatable for multi-repo)")
	cmd.Flags().StringVar(&sf.format, "format", "table", "Output format: table, csv, json")
	cmd.Flags().IntVar(&sf.topN, "top", 10, "Number of top entries to show")
	cmd.Flags().StringVar(&sf.granularity, "granularity", "month", "Activity granularity: day, week, month, year")
	cmd.Flags().StringVar(&sf.stat, "stat", "", "Show a specific stat: summary, contributors, hotspots, directories, activity, busfactor, coupling, churn-risk, working-patterns, dev-network, profile, top-commits, pareto")
	cmd.Flags().IntVar(&sf.couplingMaxFiles, "coupling-max-files", 50, "Max files per commit for coupling analysis")
	cmd.Flags().IntVar(&sf.couplingMinChanges, "coupling-min-changes", 5, "Min co-changes for coupling results")
	cmd.Flags().IntVar(&sf.churnHalfLife, "churn-half-life", 90, "Half-life in days for churn decay (churn-risk)")
	cmd.Flags().IntVar(&sf.networkMinFiles, "network-min-files", 5, "Min shared files for dev-network edges")
	cmd.Flags().StringVar(&sf.email, "email", "", "Filter by developer email (for profile stat)")
	cmd.Flags().StringVar(&sf.since, "since", "", "Filter to recent period (e.g. 7d, 4w, 3m, 1y)")
}

func validateStatsFlags(sf *statsFlags) error {
	if !isValidFormat(sf.format) {
		return fmt.Errorf("invalid --format %q; must be one of: table, csv, json", sf.format)
	}
	if !isValidGranularity(sf.granularity) {
		return fmt.Errorf("invalid --granularity %q; must be one of: day, week, month, year", sf.granularity)
	}
	if sf.stat != "" && !isValidStat(sf.stat) {
		return fmt.Errorf("invalid --stat %q; valid: summary, contributors, hotspots, directories, activity, busfactor, coupling, churn-risk, working-patterns, dev-network, profile, top-commits, pareto", sf.stat)
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

			sinceDate, err := parseSince(sf.since)
			if err != nil {
				return err
			}

			ds, err := stats.LoadMultiJSONL(sf.inputs, stats.LoadOptions{
				From:         sinceDate,
				HalfLifeDays: sf.churnHalfLife,
				CoupMaxFiles: sf.couplingMaxFiles,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Loaded %d commits, %d files, %d devs\n\n",
				ds.CommitCount, ds.UniqueFileCount, ds.DevCount)

			return renderStats(ds, &sf)
		},
	}

	addStatsFlags(cmd, &sf)
	return cmd
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
		if err := f.PrintProfiles(stats.DevProfiles(ds, sf.email)); err != nil {
			return err
		}
	}
	if showAll || sf.stat == "pareto" {
		fmt.Fprintln(os.Stderr, "\n=== Concentration (Pareto) ===")
		p := reportpkg.ComputePareto(ds)
		judge := func(pct float64, total int) string {
			if total == 0 {
				return "no data"
			}
			if pct <= 10 {
				return "extremely concentrated"
			} else if pct <= 25 {
				return "moderately concentrated"
			}
			return "well distributed"
		}
		devsLabel := judge(p.DevsPct80Commits, p.TopCommitDevs)
		if p.TopCommitDevs > 0 && p.DevsPct80Commits <= 10 {
			devsLabel += ", key-person dependence"
		}
		// Gate each line on the corresponding TopChurn/TopCommit count.
		// judge() only uses its second arg as a zero-guard, so passing the
		// Top* count (which is 0 when the signal is absent, e.g. zero-churn
		// dataset or empty contributors) maps directly to "no data" without
		// falsely hitting the "extremely concentrated" branch.
		fmt.Fprintf(os.Stdout, "Files:  %d of %d files concentrate 80%% of churn — %s\n", p.TopChurnFiles, p.TotalFiles, judge(p.FilesPct80Churn, p.TopChurnFiles))
		fmt.Fprintf(os.Stdout, "Devs (commits): %d of %d devs produce 80%% of commits — %s\n", p.TopCommitDevs, p.TotalDevs, devsLabel)
		fmt.Fprintf(os.Stdout, "Devs (churn):   %d of %d devs produce 80%% of line churn — %s\n", p.TopChurnDevs, p.TotalDevs, judge(p.DevsPct80Churn, p.TopChurnDevs))
		fmt.Fprintf(os.Stdout, "Dirs:   %d of %d dirs concentrate 80%% of churn — %s\n", p.TopChurnDirs, p.TotalDirs, judge(p.DirsPct80Churn, p.TopChurnDirs))
	}
	if showAll || sf.stat == "top-commits" {
		fmt.Fprintf(os.Stderr, "\n=== Top %d Commits ===\n", sf.topN)
		if err := f.PrintTopCommits(stats.TopCommits(ds, sf.topN)); err != nil {
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
		report["profiles"] = stats.DevProfiles(ds, sf.email)
	}
	if showAll || sf.stat == "pareto" {
		report["pareto"] = reportpkg.ComputePareto(ds)
	}
	if showAll || sf.stat == "top-commits" {
		report["top_commits"] = stats.TopCommits(ds, sf.topN)
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
	cmd.Flags().IntVar(&topN, "top", 10, "Number of top entries")

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

// --- Report ---

func reportCmd() *cobra.Command {
	var (
		input              string
		output             string
		topN               int
		email              string
		since              string
		couplingMaxFiles   int
		couplingMinChanges int
		churnHalfLife      int
		networkMinFiles    int
	)

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate a self-contained HTML report",
		RunE: func(cmd *cobra.Command, args []string) error {
			sinceDate, err := parseSince(since)
			if err != nil {
				return err
			}

			ds, err := stats.LoadJSONL(input, stats.LoadOptions{
				From:         sinceDate,
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
				fmt.Fprintf(os.Stderr, "Profile report for %s written to %s\n", email, output)
			} else {
				if err := reportpkg.Generate(f, ds, repoName, topN, sf); err != nil {
					return fmt.Errorf("generate report: %w", err)
				}
				fmt.Fprintf(os.Stderr, "Report written to %s (%d commits, %d devs)\n", output, ds.CommitCount, ds.DevCount)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&input, "input", "git_data.jsonl", "Input JSONL file")
	cmd.Flags().StringVar(&output, "output", "report.html", "Output HTML file")
	cmd.Flags().StringVar(&email, "email", "", "Generate profile report for a specific developer")
	cmd.Flags().IntVar(&topN, "top", 20, "Number of top entries per section")
	cmd.Flags().IntVar(&couplingMaxFiles, "coupling-max-files", 50, "Max files per commit for coupling")
	cmd.Flags().IntVar(&couplingMinChanges, "coupling-min-changes", 5, "Min co-changes for coupling")
	cmd.Flags().IntVar(&churnHalfLife, "churn-half-life", 90, "Half-life in days for churn decay")
	cmd.Flags().IntVar(&networkMinFiles, "network-min-files", 5, "Min shared files for dev-network edges")
	cmd.Flags().StringVar(&since, "since", "", "Filter to recent period (e.g. 7d, 4w, 3m, 1y)")

	return cmd
}
