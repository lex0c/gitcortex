package report

import (
	"html/template"
	"io"
	"time"
)

// ScanIndexEntry is one repo's row on the scan-index landing page.
// Successful repos populate the numeric fields; failed / pending
// repos leave them zero and surface Error instead. ReportHref is the
// relative URL the index uses to link into each per-repo report —
// empty when no report exists for that entry.
type ScanIndexEntry struct {
	Slug            string
	Path            string
	Status          string
	Error           string
	Commits         int
	Devs            int
	Files           int
	Churn           int64
	FirstCommitDate string
	LastCommitDate  string
	ReportHref      string
}

// ScanIndexData is the top-level template input for the index page.
type ScanIndexData struct {
	GeneratedAt string
	Roots       []string
	Repos       []ScanIndexEntry
	// TotalRepos / OKRepos / FailedRepos / PendingRepos are
	// precomputed so the template doesn't need conditional arithmetic.
	// Pending is distinct from failed: a pending repo is one the
	// worker never reached (cancelled mid-scan), a failed repo is one
	// whose extract or render broke. Mixing them in the summary would
	// read a cancel-shaped partial run as a fleet-of-errors.
	TotalRepos   int
	OKRepos      int
	FailedRepos  int
	PendingRepos int
	TotalCommits int
	TotalDevs    int
	// Largest repo commit count — used to normalize the bar widths so
	// the relative-volume bars are visually comparable across repos.
	MaxCommits int
}

// GenerateScanIndex writes the scan landing page: a per-repo card
// list with links to each repo's standalone report and a short
// summary strip. Failures are surfaced inline rather than hidden so
// operators can spot them at a glance and dig into the manifest.
func GenerateScanIndex(w io.Writer, data ScanIndexData) error {
	if data.GeneratedAt == "" {
		data.GeneratedAt = time.Now().Format("2006-01-02 15:04")
	}
	return scanIndexTmpl.Execute(w, data)
}

var scanIndexTmpl = template.Must(template.New("scan-index").Funcs(funcMap).Parse(scanIndexHTML))

const scanIndexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>gitcortex scan index ({{.TotalRepos}} repositories)</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; color: #24292f; background: #f6f8fa; padding: 20px; max-width: 1200px; margin: 0 auto; }
h1 { font-size: 24px; margin-bottom: 4px; }
.subtitle { color: #656d76; font-size: 13px; margin-bottom: 24px; }
.summary { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 12px; margin-bottom: 24px; }
.summary-card { background: #fff; border: 1px solid #d0d7de; border-radius: 6px; padding: 16px; }
.summary-card .label { font-size: 12px; color: #656d76; text-transform: uppercase; }
.summary-card .value { font-size: 24px; font-weight: 600; margin-top: 4px; }
.repo { background: #fff; border: 1px solid #d0d7de; border-radius: 6px; padding: 16px 20px; margin-bottom: 10px; display: grid; grid-template-columns: 2fr 1fr 1fr 1fr 1fr 1.2fr; gap: 16px; align-items: center; }
.repo.failed { border-left: 4px solid #cf222e; }
.repo.pending { border-left: 4px solid #bf8700; }
.repo.ok { border-left: 4px solid #2da44e; }
.repo .name { font-weight: 600; font-size: 15px; }
.repo .name a { color: #0969da; text-decoration: none; }
.repo .name a:hover { text-decoration: underline; }
.repo .path { font-family: "SF Mono", Consolas, monospace; font-size: 11px; color: #656d76; margin-top: 2px; word-break: break-all; }
.repo .error { font-size: 12px; color: #cf222e; margin-top: 4px; }
.repo .metric { font-size: 13px; }
.repo .metric .val { font-weight: 600; font-size: 16px; }
.repo .metric .lbl { font-size: 11px; color: #656d76; text-transform: uppercase; display: block; margin-top: 2px; }
.repo .bar-container { display: flex; align-items: center; gap: 8px; }
.repo .bar-outer { flex: 1; height: 8px; background: #eaeef2; border-radius: 3px; overflow: hidden; }
.repo .bar-inner { height: 100%; background: #2da44e; }
.repo .dates { font-size: 11px; color: #656d76; font-family: "SF Mono", Consolas, monospace; }
.status-pill { display: inline-block; padding: 2px 8px; border-radius: 10px; font-size: 11px; font-weight: 600; }
.status-ok { background: #dafbe1; color: #1a7f37; }
.status-failed { background: #ffebe9; color: #cf222e; }
.status-pending { background: #fff4d4; color: #9a6700; }
footer { margin-top: 30px; color: #656d76; font-size: 12px; text-align: center; border-top: 1px solid #d0d7de; padding-top: 14px; }
.hint { font-size: 12px; color: #656d76; font-style: italic; margin-bottom: 14px; }
</style>
</head>
<body>

<h1>Scan Index <span style="font-size:14px; color:#656d76; font-weight:normal;">{{.TotalRepos}} repositor{{if eq .TotalRepos 1}}y{{else}}ies{{end}}</span></h1>
<p class="subtitle">Generated {{.GeneratedAt}}{{if .Roots}} · roots: {{range $i, $r := .Roots}}{{if $i}}, {{end}}<code>{{$r}}</code>{{end}}{{end}}</p>

<div class="summary">
  <div class="summary-card"><div class="label">Repositories</div><div class="value">{{thousands .OKRepos}}{{if gt .FailedRepos 0}} <span style="font-size:14px; color:#cf222e;">({{.FailedRepos}} failed)</span>{{end}}{{if gt .PendingRepos 0}} <span style="font-size:14px; color:#9a6700;">({{.PendingRepos}} pending)</span>{{end}}</div></div>
  <div class="summary-card"><div class="label">Total commits</div><div class="value" title="{{thousands .TotalCommits}}">{{humanize .TotalCommits}}</div></div>
  <div class="summary-card"><div class="label">Unique devs</div><div class="value">{{thousands .TotalDevs}}</div></div>
</div>

<p class="hint">Each repo below links to its own standalone report. Metrics are per-repository — no cross-repo aggregation that would mix signals from unrelated codebases. For a consolidated developer view, use <code>gitcortex scan --report &lt;file&gt; --email &lt;address&gt;</code>.</p>

{{$max := .MaxCommits}}
{{range .Repos}}
<div class="repo {{.Status}}">
  <div>
    <div class="name">
      {{if .ReportHref}}<a href="{{.ReportHref}}">{{.Slug}}</a>{{else}}{{.Slug}}{{end}}
      <span class="status-pill status-{{.Status}}">{{.Status}}</span>
    </div>
    <div class="path">{{.Path}}</div>
    {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
  </div>
  {{if eq .Status "ok"}}
  <div class="metric">
    <span class="val" title="{{thousands .Commits}}">{{humanize .Commits}}</span>
    <span class="lbl">commits</span>
  </div>
  <div class="metric">
    <span class="val" title="{{thousands .Churn}}">{{humanize .Churn}}</span>
    <span class="lbl">churn</span>
  </div>
  <div class="metric">
    <span class="val">{{.Devs}}</span>
    <span class="lbl">devs</span>
  </div>
  <div class="metric">
    <span class="val">{{humanize .Files}}</span>
    <span class="lbl">files</span>
  </div>
  <div class="dates">
    {{.FirstCommitDate}}<br>→ {{.LastCommitDate}}
  </div>
  {{else}}
  <div style="grid-column: span 5; color:#656d76; font-style:italic;">No report available.</div>
  {{end}}
</div>
{{if eq .Status "ok"}}
<div class="bar-container" style="margin:-4px 0 12px 20px; max-width:600px;">
  <div class="bar-outer"><div class="bar-inner" style="width:{{if gt $max 0}}{{pctInt .Commits $max}}%{{else}}0%{{end}};"></div></div>
  <span style="font-size:11px; color:#656d76; font-family: 'SF Mono', Consolas, monospace; white-space:nowrap;">{{humanize .Commits}} / {{humanize $max}} max</span>
</div>
{{end}}
{{end}}

<footer>Generated by <a href="https://github.com/lex0c/gitcortex" style="color:#0969da;">gitcortex</a></footer>

</body>
</html>
`
