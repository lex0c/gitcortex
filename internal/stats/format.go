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
