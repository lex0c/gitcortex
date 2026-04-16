package stats

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

type Formatter struct {
	w      io.Writer
	format string
}

func NewFormatter(w io.Writer, format string) *Formatter {
	return &Formatter{w: w, format: format}
}

func (f *Formatter) PrintSummary(s Summary) error {
	switch f.format {
	case "json":
		return f.writeJSON(s)
	case "csv":
		return f.writeCSV(
			[]string{"metric", "value"},
			[][]string{
				{"total_commits", fmt.Sprintf("%d", s.TotalCommits)},
				{"total_devs", fmt.Sprintf("%d", s.TotalDevs)},
				{"total_files", fmt.Sprintf("%d", s.TotalFiles)},
				{"total_additions", fmt.Sprintf("%d", s.TotalAdditions)},
				{"total_deletions", fmt.Sprintf("%d", s.TotalDeletions)},
				{"merge_commits", fmt.Sprintf("%d", s.MergeCommits)},
				{"avg_additions", fmt.Sprintf("%.1f", s.AvgAdditions)},
				{"avg_deletions", fmt.Sprintf("%.1f", s.AvgDeletions)},
				{"avg_files_changed", fmt.Sprintf("%.1f", s.AvgFilesChanged)},
				{"first_commit", s.FirstCommitDate},
				{"last_commit", s.LastCommitDate},
			},
		)
	default:
		tw := tabwriter.NewWriter(f.w, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "Total commits\t%d\n", s.TotalCommits)
		fmt.Fprintf(tw, "Total devs\t%d\n", s.TotalDevs)
		fmt.Fprintf(tw, "Total files touched\t%d\n", s.TotalFiles)
		fmt.Fprintf(tw, "Total additions\t%d\n", s.TotalAdditions)
		fmt.Fprintf(tw, "Total deletions\t%d\n", s.TotalDeletions)
		fmt.Fprintf(tw, "Merge commits\t%d\n", s.MergeCommits)
		fmt.Fprintf(tw, "Avg additions/commit\t%.1f\n", s.AvgAdditions)
		fmt.Fprintf(tw, "Avg deletions/commit\t%.1f\n", s.AvgDeletions)
		fmt.Fprintf(tw, "Avg files/commit\t%.1f\n", s.AvgFilesChanged)
		fmt.Fprintf(tw, "First commit\t%s\n", s.FirstCommitDate)
		fmt.Fprintf(tw, "Last commit\t%s\n", s.LastCommitDate)
		return tw.Flush()
	}
}

func (f *Formatter) PrintRanking(ranked []RankedContributor) error {
	switch f.format {
	case "json":
		return f.writeJSON(ranked)
	case "csv":
		rows := make([][]string, len(ranked))
		for i, r := range ranked {
			rows[i] = []string{
				r.Name, r.Email,
				fmt.Sprintf("%.1f", r.Score),
				fmt.Sprintf("%d", r.Commits),
				fmt.Sprintf("%d", r.LinesChanged),
				fmt.Sprintf("%d", r.FilesTouched),
				fmt.Sprintf("%d", r.ActiveDays),
				r.FirstDate, r.LastDate,
			}
		}
		return f.writeCSV([]string{"name", "email", "score", "commits", "lines_changed", "files_touched", "active_days", "first_date", "last_date"}, rows)
	default:
		tw := tabwriter.NewWriter(f.w, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "NAME\tEMAIL\tSCORE\tCOMMITS\tLINES\tFILES\tACTIVE DAYS\tFIRST\tLAST\n")
		fmt.Fprintf(tw, "----\t-----\t-----\t-------\t-----\t-----\t-----------\t-----\t----\n")
		for _, r := range ranked {
			fmt.Fprintf(tw, "%s\t%s\t%.1f\t%d\t%d\t%d\t%d\t%s\t%s\n",
				r.Name, r.Email, r.Score, r.Commits, r.LinesChanged, r.FilesTouched, r.ActiveDays, r.FirstDate, r.LastDate)
		}
		return tw.Flush()
	}
}

func (f *Formatter) PrintContributors(contributors []ContributorStat) error {
	switch f.format {
	case "json":
		return f.writeJSON(contributors)
	case "csv":
		rows := make([][]string, len(contributors))
		for i, c := range contributors {
			rows[i] = []string{
				c.Name, c.Email,
				fmt.Sprintf("%d", c.Commits),
				fmt.Sprintf("%d", c.Additions),
				fmt.Sprintf("%d", c.Deletions),
			}
		}
		return f.writeCSV([]string{"name", "email", "commits", "additions", "deletions"}, rows)
	default:
		tw := tabwriter.NewWriter(f.w, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "NAME\tEMAIL\tCOMMITS\tADDITIONS\tDELETIONS\n")
		fmt.Fprintf(tw, "----\t-----\t-------\t---------\t---------\n")
		for _, c := range contributors {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\n", c.Name, c.Email, c.Commits, c.Additions, c.Deletions)
		}
		return tw.Flush()
	}
}

func (f *Formatter) PrintHotspots(hotspots []FileStat) error {
	switch f.format {
	case "json":
		return f.writeJSON(hotspots)
	case "csv":
		rows := make([][]string, len(hotspots))
		for i, h := range hotspots {
			rows[i] = []string{
				h.Path,
				fmt.Sprintf("%d", h.Commits),
				fmt.Sprintf("%d", h.Churn),
				fmt.Sprintf("%d", h.UniqueDevs),
			}
		}
		return f.writeCSV([]string{"path", "commits", "churn", "unique_devs"}, rows)
	default:
		tw := tabwriter.NewWriter(f.w, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "PATH\tCOMMITS\tCHURN\tDEVS\n")
		fmt.Fprintf(tw, "----\t-------\t-----\t----\n")
		for _, h := range hotspots {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%d\n", h.Path, h.Commits, h.Churn, h.UniqueDevs)
		}
		return tw.Flush()
	}
}

func (f *Formatter) PrintActivity(buckets []ActivityBucket) error {
	switch f.format {
	case "json":
		return f.writeJSON(buckets)
	case "csv":
		rows := make([][]string, len(buckets))
		for i, b := range buckets {
			rows[i] = []string{
				b.Period,
				fmt.Sprintf("%d", b.Commits),
				fmt.Sprintf("%d", b.Additions),
				fmt.Sprintf("%d", b.Deletions),
			}
		}
		return f.writeCSV([]string{"period", "commits", "additions", "deletions"}, rows)
	default:
		tw := tabwriter.NewWriter(f.w, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "PERIOD\tCOMMITS\tADDITIONS\tDELETIONS\n")
		fmt.Fprintf(tw, "------\t-------\t---------\t---------\n")
		for _, b := range buckets {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%d\n", b.Period, b.Commits, b.Additions, b.Deletions)
		}
		return tw.Flush()
	}
}

func (f *Formatter) PrintBusFactor(results []BusFactorResult) error {
	switch f.format {
	case "json":
		return f.writeJSON(results)
	case "csv":
		rows := make([][]string, len(results))
		for i, r := range results {
			rows[i] = []string{
				r.Path,
				fmt.Sprintf("%d", r.BusFactor),
				strings.Join(r.TopDevs, ";"),
			}
		}
		return f.writeCSV([]string{"path", "bus_factor", "top_devs"}, rows)
	default:
		tw := tabwriter.NewWriter(f.w, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "PATH\tBUS FACTOR\tTOP DEVS\n")
		fmt.Fprintf(tw, "----\t----------\t--------\n")
		for _, r := range results {
			fmt.Fprintf(tw, "%s\t%d\t%s\n", r.Path, r.BusFactor, strings.Join(r.TopDevs, ", "))
		}
		return tw.Flush()
	}
}

func (f *Formatter) PrintCoupling(results []CouplingResult) error {
	switch f.format {
	case "json":
		return f.writeJSON(results)
	case "csv":
		rows := make([][]string, len(results))
		for i, r := range results {
			rows[i] = []string{
				r.FileA, r.FileB,
				fmt.Sprintf("%d", r.CoChanges),
				fmt.Sprintf("%.0f", r.CouplingPct),
				fmt.Sprintf("%d", r.ChangesA),
				fmt.Sprintf("%d", r.ChangesB),
			}
		}
		return f.writeCSV([]string{"file_a", "file_b", "co_changes", "coupling_pct", "changes_a", "changes_b"}, rows)
	default:
		tw := tabwriter.NewWriter(f.w, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "FILE A\tFILE B\tCO-CHANGES\tCOUPLING\tCHANGES A\tCHANGES B\n")
		fmt.Fprintf(tw, "------\t------\t----------\t--------\t---------\t---------\n")
		for _, r := range results {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%.0f%%\t%d\t%d\n", r.FileA, r.FileB, r.CoChanges, r.CouplingPct, r.ChangesA, r.ChangesB)
		}
		return tw.Flush()
	}
}

func (f *Formatter) PrintChurnRisk(results []ChurnRiskResult) error {
	switch f.format {
	case "json":
		return f.writeJSON(results)
	case "csv":
		rows := make([][]string, len(results))
		for i, r := range results {
			rows[i] = []string{
				r.Path,
				fmt.Sprintf("%.1f", r.RiskScore),
				fmt.Sprintf("%.1f", r.RecentChurn),
				fmt.Sprintf("%d", r.BusFactor),
				fmt.Sprintf("%d", r.TotalChanges),
				r.LastChangeDate,
			}
		}
		return f.writeCSV([]string{"path", "risk_score", "recent_churn", "bus_factor", "total_changes", "last_change"}, rows)
	default:
		tw := tabwriter.NewWriter(f.w, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "PATH\tRISK\tRECENT CHURN\tBUS FACTOR\tTOTAL CHANGES\tLAST CHANGE\n")
		fmt.Fprintf(tw, "----\t----\t------------\t----------\t-------------\t-----------\n")
		for _, r := range results {
			fmt.Fprintf(tw, "%s\t%.1f\t%.1f\t%d\t%d\t%s\n", r.Path, r.RiskScore, r.RecentChurn, r.BusFactor, r.TotalChanges, r.LastChangeDate)
		}
		return tw.Flush()
	}
}

func (f *Formatter) PrintWorkingPatterns(patterns []WorkingPattern) error {
	switch f.format {
	case "json":
		return f.writeJSON(patterns)
	case "csv":
		rows := make([][]string, len(patterns))
		for i, p := range patterns {
			rows[i] = []string{p.Day, fmt.Sprintf("%d", p.Hour), fmt.Sprintf("%d", p.Commits)}
		}
		return f.writeCSV([]string{"day", "hour", "commits"}, rows)
	default:
		days := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
		grid := [7][24]int{}
		for _, p := range patterns {
			for d, name := range days {
				if name == p.Day {
					grid[d][p.Hour] = p.Commits
				}
			}
		}

		tw := tabwriter.NewWriter(f.w, 0, 2, 1, ' ', 0)
		fmt.Fprintf(tw, "HOUR\t")
		for _, d := range days {
			fmt.Fprintf(tw, "%s\t", d)
		}
		fmt.Fprintln(tw)
		for h := 0; h < 24; h++ {
			fmt.Fprintf(tw, "%02d:00\t", h)
			for d := 0; d < 7; d++ {
				if grid[d][h] > 0 {
					fmt.Fprintf(tw, "%d\t", grid[d][h])
				} else {
					fmt.Fprintf(tw, ".\t")
				}
			}
			fmt.Fprintln(tw)
		}
		return tw.Flush()
	}
}

func (f *Formatter) PrintDevNetwork(edges []DevEdge) error {
	switch f.format {
	case "json":
		return f.writeJSON(edges)
	case "csv":
		rows := make([][]string, len(edges))
		for i, e := range edges {
			rows[i] = []string{
				e.DevA, e.DevB,
				fmt.Sprintf("%d", e.SharedFiles),
				fmt.Sprintf("%.1f", e.Weight),
			}
		}
		return f.writeCSV([]string{"dev_a", "dev_b", "shared_files", "weight_pct"}, rows)
	default:
		tw := tabwriter.NewWriter(f.w, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "DEV A\tDEV B\tSHARED FILES\tWEIGHT\n")
		fmt.Fprintf(tw, "-----\t-----\t------------\t------\n")
		for _, e := range edges {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%.1f%%\n", e.DevA, e.DevB, e.SharedFiles, e.Weight)
		}
		return tw.Flush()
	}
}

func (f *Formatter) PrintTopCommits(commits []BigCommit) error {
	switch f.format {
	case "json":
		return f.writeJSON(commits)
	case "csv":
		rows := make([][]string, len(commits))
		for i, c := range commits {
			rows[i] = []string{
				c.SHA[:12], c.AuthorName, c.AuthorEmail, c.Date,
				fmt.Sprintf("%d", c.Additions), fmt.Sprintf("%d", c.Deletions),
				fmt.Sprintf("%d", c.LinesChanged), fmt.Sprintf("%d", c.FilesChanged),
				c.Message,
			}
		}
		return f.writeCSV([]string{"sha", "author", "email", "date", "additions", "deletions", "lines_changed", "files_changed", "message"}, rows)
	default:
		hasMsg := false
		for _, c := range commits {
			if c.Message != "" {
				hasMsg = true
				break
			}
		}
		tw := tabwriter.NewWriter(f.w, 0, 4, 2, ' ', 0)
		if hasMsg {
			fmt.Fprintf(tw, "SHA\tAUTHOR\tDATE\tLINES\tFILES\tMESSAGE\n")
			fmt.Fprintf(tw, "---\t------\t----\t-----\t-----\t-------\n")
			for _, c := range commits {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n",
					c.SHA[:12], c.AuthorName, c.Date, c.LinesChanged, c.FilesChanged, c.Message)
			}
		} else {
			fmt.Fprintf(tw, "SHA\tAUTHOR\tDATE\tADDITIONS\tDELETIONS\tLINES\tFILES\n")
			fmt.Fprintf(tw, "---\t------\t----\t---------\t---------\t-----\t-----\n")
			for _, c := range commits {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%d\n",
					c.SHA[:12], c.AuthorName, c.Date, c.Additions, c.Deletions, c.LinesChanged, c.FilesChanged)
			}
		}
		return tw.Flush()
	}
}

func (f *Formatter) PrintProfiles(profiles []DevProfile) error {
	switch f.format {
	case "json":
		return f.writeJSON(profiles)
	case "csv":
		rows := make([][]string, len(profiles))
		for i, p := range profiles {
			rows[i] = []string{
				p.Name, p.Email,
				fmt.Sprintf("%.1f", p.Score),
				fmt.Sprintf("%d", p.Commits),
				fmt.Sprintf("%d", p.LinesChanged),
				fmt.Sprintf("%d", p.FilesTouched),
				fmt.Sprintf("%d", p.ActiveDays),
				fmt.Sprintf("%.1f", p.WeekendPct),
				p.FirstDate, p.LastDate,
			}
		}
		return f.writeCSV([]string{"name", "email", "score", "commits", "lines_changed", "files_touched", "active_days", "weekend_pct", "first_date", "last_date"}, rows)
	default:
		for i, p := range profiles {
			if i > 0 {
				fmt.Fprintln(f.w)
			}
			fmt.Fprintf(f.w, "%s <%s>\n", p.Name, p.Email)
			fmt.Fprintf(f.w, "  Score: %.1f | Commits: %d | Lines: %d | Files: %d | Active: %d days | Weekend: %.1f%%\n",
				p.Score, p.Commits, p.LinesChanged, p.FilesTouched, p.ActiveDays, p.WeekendPct)
			fmt.Fprintf(f.w, "  Period: %s to %s\n", p.FirstDate, p.LastDate)

			if len(p.TopFiles) > 0 {
				fmt.Fprintln(f.w, "  Top files:")
				for _, tf := range p.TopFiles {
					fmt.Fprintf(f.w, "    %-50s %3d commits  %6d churn\n", tf.Path, tf.Commits, tf.Churn)
				}
			}

			if len(p.MonthlyActivity) > 0 {
				fmt.Fprintln(f.w, "  Activity:")
				maxCommits := 0
				for _, b := range p.MonthlyActivity {
					if b.Commits > maxCommits {
						maxCommits = b.Commits
					}
				}
				for _, b := range p.MonthlyActivity {
					barLen := 0
					if maxCommits > 0 {
						barLen = b.Commits * 30 / maxCommits
					}
					bar := ""
					for j := 0; j < barLen; j++ {
						bar += "#"
					}
					fmt.Fprintf(f.w, "    %s  %-30s %d\n", b.Period, bar, b.Commits)
				}
			}
		}
		return nil
	}
}

func (f *Formatter) PrintReport(v interface{}) error {
	return f.writeJSON(v)
}

func (f *Formatter) writeJSON(v interface{}) error {
	enc := json.NewEncoder(f.w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (f *Formatter) writeCSV(header []string, rows [][]string) error {
	w := csv.NewWriter(f.w)
	if err := w.Write(header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
