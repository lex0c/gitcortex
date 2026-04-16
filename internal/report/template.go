package report

const reportHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>gitcortex report</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; color: #24292f; background: #f6f8fa; padding: 20px; max-width: 1200px; margin: 0 auto; }
h1 { font-size: 24px; margin-bottom: 8px; }
h2 { font-size: 18px; margin: 32px 0 12px; padding-bottom: 8px; border-bottom: 1px solid #d0d7de; }
.subtitle { color: #656d76; font-size: 14px; margin-bottom: 24px; }
.cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 12px; margin-bottom: 24px; }
.card { background: #fff; border: 1px solid #d0d7de; border-radius: 6px; padding: 16px; }
.card .label { font-size: 12px; color: #656d76; text-transform: uppercase; }
.card .value { font-size: 24px; font-weight: 600; margin-top: 4px; }
.card .detail { font-size: 12px; color: #656d76; margin-top: 2px; }
table { width: 100%; border-collapse: collapse; background: #fff; border: 1px solid #d0d7de; border-radius: 6px; overflow: hidden; margin-bottom: 8px; font-size: 13px; }
th { background: #f6f8fa; text-align: left; padding: 8px 12px; font-weight: 600; border-bottom: 1px solid #d0d7de; }
td { padding: 6px 12px; border-bottom: 1px solid #eaeef2; }
tr:last-child td { border-bottom: none; }
.bar-container { display: flex; align-items: center; gap: 8px; }
.bar { height: 18px; border-radius: 3px; min-width: 2px; }
.bar-add { background: #2da44e; }
.bar-del { background: #cf222e; }
.bar-commits { background: #0969da; }
.bar-score { background: #8250df; }
.bar-churn { background: #bf8700; }
.bar-value { font-size: 12px; color: #656d76; white-space: nowrap; }
.heatmap { display: grid; grid-template-columns: 50px repeat(24, 1fr); gap: 2px; margin-bottom: 8px; }
.heatmap .cell { aspect-ratio: 1; border-radius: 3px; display: flex; align-items: center; justify-content: center; font-size: 10px; color: #fff; }
.heatmap .day-label { display: flex; align-items: center; font-size: 12px; color: #656d76; }
.heatmap .hour-label { display: flex; align-items: center; justify-content: center; font-size: 10px; color: #656d76; }
.mono { font-family: "SF Mono", Consolas, monospace; font-size: 12px; }
.truncate { max-width: 400px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
footer { margin-top: 40px; padding-top: 16px; border-top: 1px solid #d0d7de; color: #656d76; font-size: 12px; }
</style>
</head>
<body>

<h1>gitcortex report</h1>
<p class="subtitle">{{.Summary.FirstCommitDate}} to {{.Summary.LastCommitDate}}</p>

<div class="cards">
  <div class="card"><div class="label">Commits</div><div class="value">{{.Summary.TotalCommits}}</div></div>
  <div class="card"><div class="label">Developers</div><div class="value">{{.Summary.TotalDevs}}</div></div>
  <div class="card"><div class="label">Files</div><div class="value">{{.Summary.TotalFiles}}</div></div>
  <div class="card"><div class="label">Additions</div><div class="value">{{.Summary.TotalAdditions}}</div></div>
  <div class="card"><div class="label">Deletions</div><div class="value">{{.Summary.TotalDeletions}}</div></div>
  <div class="card"><div class="label">Merges</div><div class="value">{{.Summary.MergeCommits}}</div></div>
  <div class="card"><div class="label">Avg lines/commit</div><div class="value">{{printf "%.0f" .Summary.AvgAdditions}}</div><div class="detail">additions</div></div>
  <div class="card"><div class="label">Avg files/commit</div><div class="value">{{printf "%.1f" .Summary.AvgFilesChanged}}</div></div>
</div>

{{if .Activity}}
<h2>Activity</h2>
<table>
<tr><th>Period</th><th>Commits</th><th></th><th>Additions</th><th>Deletions</th></tr>
{{$maxCommits := 0}}{{range .Activity}}{{if gt .Commits $maxCommits}}{{$maxCommits = .Commits}}{{end}}{{end}}
{{range .Activity}}
<tr>
  <td class="mono">{{.Period}}</td>
  <td>{{.Commits}}</td>
  <td style="width:40%"><div class="bar-container"><div class="bar bar-commits" style="width: {{pctInt .Commits $maxCommits}}%"></div></div></td>
  <td>{{.Additions}}</td>
  <td>{{.Deletions}}</td>
</tr>
{{end}}
</table>
{{end}}

{{if .Ranking}}
<h2>Contributor Ranking</h2>
<table>
<tr><th>Name</th><th>Email</th><th>Score</th><th></th><th>Commits</th><th>Lines</th><th>Files</th><th>Days</th></tr>
{{range .Ranking}}
<tr>
  <td>{{.Name}}</td>
  <td class="mono" style="font-size:11px">{{.Email}}</td>
  <td>{{printf "%.1f" .Score}}</td>
  <td style="width:20%"><div class="bar-container"><div class="bar bar-score" style="width: {{.Score}}%"></div></div></td>
  <td>{{.Commits}}</td>
  <td>{{.LinesChanged}}</td>
  <td>{{.FilesTouched}}</td>
  <td>{{.ActiveDays}}</td>
</tr>
{{end}}
</table>
{{end}}

{{if .Hotspots}}
<h2>File Hotspots</h2>
<table>
<tr><th>Path</th><th>Commits</th><th>Churn</th><th></th><th>Devs</th></tr>
{{$maxChurn := int64 0}}{{range .Hotspots}}{{if gt .Churn $maxChurn}}{{$maxChurn = .Churn}}{{end}}{{end}}
{{range .Hotspots}}
<tr>
  <td class="mono truncate">{{.Path}}</td>
  <td>{{.Commits}}</td>
  <td>{{.Churn}}</td>
  <td style="width:25%"><div class="bar-container"><div class="bar bar-churn" style="width: {{pct .Churn $maxChurn}}%"></div></div></td>
  <td>{{.UniqueDevs}}</td>
</tr>
{{end}}
</table>
{{end}}

{{if .ChurnRisk}}
<h2>Churn Risk</h2>
<table>
<tr><th>Path</th><th>Risk</th><th></th><th>Recent Churn</th><th>Bus Factor</th><th>Last Change</th></tr>
{{$maxRisk := 0.0}}{{range .ChurnRisk}}{{if gt .RiskScore $maxRisk}}{{$maxRisk = .RiskScore}}{{end}}{{end}}
{{range .ChurnRisk}}
<tr>
  <td class="mono truncate">{{.Path}}</td>
  <td>{{printf "%.1f" .RiskScore}}</td>
  <td style="width:20%"><div class="bar-container"><div class="bar bar-del" style="width: {{printf "%.0f" (pct (int64 .RiskScore) (int64 $maxRisk))}}%"></div></div></td>
  <td>{{printf "%.1f" .RecentChurn}}</td>
  <td>{{.BusFactor}}</td>
  <td class="mono">{{.LastChangeDate}}</td>
</tr>
{{end}}
</table>
{{end}}

{{if .BusFactor}}
<h2>Bus Factor Risk</h2>
<table>
<tr><th>Path</th><th>Bus Factor</th><th>Top Devs</th></tr>
{{range .BusFactor}}
<tr>
  <td class="mono truncate">{{.Path}}</td>
  <td>{{.BusFactor}}</td>
  <td style="font-size:11px">{{joinDevs .TopDevs}}</td>
</tr>
{{end}}
</table>
{{end}}

{{if .Coupling}}
<h2>File Coupling</h2>
<table>
<tr><th>File A</th><th>File B</th><th>Co-changes</th><th>Coupling</th></tr>
{{range .Coupling}}
<tr>
  <td class="mono truncate">{{.FileA}}</td>
  <td class="mono truncate">{{.FileB}}</td>
  <td>{{.CoChanges}}</td>
  <td><div class="bar-container"><div class="bar bar-commits" style="width: {{.CouplingPct}}%"></div><span class="bar-value">{{printf "%.0f" .CouplingPct}}%</span></div></td>
</tr>
{{end}}
</table>
{{end}}

{{if gt .MaxPattern 0}}
<h2>Working Patterns</h2>
<div class="heatmap">
  <div></div>
  {{range $h := seq 0 23}}<div class="hour-label">{{printf "%02d" $h}}</div>{{end}}
  {{$grid := .PatternGrid}}{{$max := .MaxPattern}}
  {{range $d, $dayName := (list "Mon" "Tue" "Wed" "Thu" "Fri" "Sat" "Sun")}}
  <div class="day-label">{{$dayName}}</div>
  {{range $h := seq 0 23}}
  <div class="cell" style="background: {{heatColor (index (index $grid $d) $h) $max}}" title="{{$dayName}} {{printf "%02d" $h}}:00 — {{index (index $grid $d) $h}} commits">{{if gt (index (index $grid $d) $h) 0}}{{index (index $grid $d) $h}}{{end}}</div>
  {{end}}
  {{end}}
</div>
{{end}}

{{if .TopCommits}}
<h2>Top Commits</h2>
<table>
<tr><th>SHA</th><th>Author</th><th>Date</th><th>Lines</th><th>Files</th>{{if (index .TopCommits 0).Message}}<th>Message</th>{{end}}</tr>
{{range .TopCommits}}
<tr>
  <td class="mono">{{slice .SHA 0 12}}</td>
  <td>{{.AuthorName}}</td>
  <td class="mono">{{.Date}}</td>
  <td>{{.LinesChanged}}</td>
  <td>{{.FilesChanged}}</td>
  {{if .Message}}<td class="truncate">{{.Message}}</td>{{end}}
</tr>
{{end}}
</table>
{{end}}

<footer>Generated by gitcortex</footer>

</body>
</html>`
