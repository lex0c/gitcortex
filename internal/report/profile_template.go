package report

const profileHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Profile.Name}} — {{.RepoName}}</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; color: #24292f; background: #f6f8fa; padding: 20px; max-width: 900px; margin: 0 auto; }
h1 { font-size: 22px; margin-bottom: 4px; }
h2 { font-size: 16px; margin: 28px 0 8px; padding-bottom: 6px; border-bottom: 1px solid #d0d7de; }
.subtitle { color: #656d76; font-size: 13px; margin-bottom: 20px; }
.cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(130px, 1fr)); gap: 10px; margin-bottom: 20px; }
.card { background: #fff; border: 1px solid #d0d7de; border-radius: 6px; padding: 12px; }
.card .label { font-size: 11px; color: #656d76; text-transform: uppercase; }
.card .value { font-size: 20px; font-weight: 600; margin-top: 2px; }
.card .detail { font-size: 11px; color: #656d76; margin-top: 2px; }
.grid-info { display: grid; grid-template-columns: 100px 1fr; gap: 4px 12px; font-size: 13px; margin-bottom: 16px; }
.grid-info .lbl { color: #656d76; }
table { width: 100%; border-collapse: collapse; background: #fff; border: 1px solid #d0d7de; border-radius: 6px; overflow: hidden; margin-bottom: 8px; font-size: 12px; }
th { background: #f6f8fa; text-align: left; padding: 6px 10px; font-weight: 600; border-bottom: 1px solid #d0d7de; }
td { padding: 5px 10px; border-bottom: 1px solid #eaeef2; }
tr:last-child td { border-bottom: none; }
.mono { font-family: "SF Mono", Consolas, monospace; font-size: 11px; }
.truncate { max-width: 350px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.heatmap { display: grid; gap: 2px; margin-bottom: 8px; }
.heatmap .cell { border-radius: 2px; display: flex; align-items: center; justify-content: center; font-size: 9px; color: #fff; }
.heatmap .day-label { display: flex; align-items: center; font-size: 11px; color: #656d76; }
.heatmap .hour-label { display: flex; align-items: center; justify-content: center; font-size: 9px; color: #656d76; }
.bar { height: 16px; border-radius: 3px; min-width: 2px; }
.bar-add { background: #2da44e; }
.bar-del { background: #cf222e; }
.bar-churn { background: #bf8700; }
footer { margin-top: 32px; padding-top: 12px; border-top: 1px solid #d0d7de; color: #656d76; font-size: 11px; }
</style>
</head>
<body>

<h1>{{.Profile.Name}}</h1>
<p class="subtitle">{{.Profile.Email}} · {{.RepoName}} · {{.Profile.FirstDate}} to {{.Profile.LastDate}}</p>

<div class="cards">
  <div class="card"><div class="label">Commits</div><div class="value">{{.Profile.Commits}}</div></div>
  <div class="card"><div class="label">Lines Changed</div><div class="value">{{.Profile.LinesChanged}}</div></div>
  <div class="card"><div class="label">Files Touched</div><div class="value">{{.Profile.FilesTouched}}</div></div>
  <div class="card"><div class="label">Active Days</div><div class="value">{{.Profile.ActiveDays}}</div></div>
  <div class="card"><div class="label">Pace</div><div class="value">{{printf "%.1f" .Profile.Pace}}</div><div class="detail">commits/active day</div></div>
  <div class="card"><div class="label">Weekend</div><div class="value">{{printf "%.1f" .Profile.WeekendPct}}%</div></div>
</div>

<div style="margin-bottom:16px;">
  <div style="font-size:13px; font-weight:600; margin-bottom:6px;">Scope</div>
  {{range .Profile.Scope}}
  <div style="display:flex; align-items:center; gap:8px; margin-bottom:3px; font-size:12px;">
    <span class="mono" style="min-width:200px;">{{.Dir}}</span>
    <div style="flex:1; height:14px; background:#ebedf0; border-radius:3px; overflow:hidden;"><div style="height:100%; width:{{printf "%.0f" .Pct}}%; background:#0969da; border-radius:3px;"></div></div>
    <span style="min-width:35px; text-align:right; color:#656d76;">{{printf "%.0f" .Pct}}%</span>
  </div>
  {{end}}
</div>

<div style="display:flex; gap:24px; margin-bottom:16px; font-size:13px;">
  <div>
    <span style="color:#656d76;">Contribution:</span>
    {{if eq .Profile.ContribType "growth"}}<span style="color:#2da44e; font-weight:600;">growth</span>{{else if eq .Profile.ContribType "refactor"}}<span style="color:#cf222e; font-weight:600;">refactor</span>{{else}}<span style="color:#bf8700; font-weight:600;">balanced</span>{{end}}
    <span style="color:#656d76;">({{printf "%.2f" .Profile.ContribRatio}} �� +{{.Profile.Additions}} −{{.Profile.Deletions}})</span>
  </div>
</div>

{{if .Profile.Collaborators}}
<div style="margin-bottom:16px;">
  <div style="font-size:13px; font-weight:600; margin-bottom:6px;">Collaboration</div>
  <div style="display:flex; flex-wrap:wrap; gap:6px;">
    {{range .Profile.Collaborators}}
    <span style="display:inline-flex; align-items:center; gap:4px; padding:3px 10px; background:#fff; border:1px solid #d0d7de; border-radius:16px; font-size:11px;">
      <span class="mono">{{.Email}}</span>
      <span style="background:#0969da; color:#fff; border-radius:8px; padding:0 6px; font-size:10px;">{{.SharedFiles}}</span>
    </span>
    {{end}}
  </div>
</div>
{{end}}

{{if .Profile.TopFiles}}
<h2>Top Files</h2>
<table>
<tr><th>Path</th><th>Commits</th><th>Churn</th><th></th></tr>
{{$maxChurn := int64 0}}{{range .Profile.TopFiles}}{{if gt .Churn $maxChurn}}{{$maxChurn = .Churn}}{{end}}{{end}}
{{range .Profile.TopFiles}}
<tr>
  <td class="mono truncate">{{.Path}}</td>
  <td>{{.Commits}}</td>
  <td>{{.Churn}}</td>
  <td style="width:30%"><div style="display:flex;"><div class="bar bar-churn" style="width:{{pct .Churn $maxChurn}}%"></div></div></td>
</tr>
{{end}}
</table>
{{end}}

{{if .ActivityYears}}
<h2>Activity</h2>
{{$max := .MaxActivityCommits}}{{$grid := .ActivityGrid}}
<div class="heatmap" style="grid-template-columns:35px repeat(12, 1fr);">
  <div></div>
  {{range (list "J" "F" "M" "A" "M" "J" "J" "A" "S" "O" "N" "D")}}<div class="hour-label">{{.}}</div>{{end}}
  {{range $y, $year := $.ActivityYears}}
  <div class="mono" style="font-size:10px; color:#656d76; display:flex; align-items:center;">{{$year}}</div>
  {{range $m := seq 0 11}}{{$cell := index (index $grid $y) $m}}<div class="cell" style="aspect-ratio:1.6; background:{{actColor $cell.Commits $max}}; {{if $cell.HasData}}color:#fff;{{else}}color:transparent;{{end}}" title="{{$year}}-{{printf "%02d" (plusInt $m 1)}}: {{$cell.Commits}} commits">{{if $cell.HasData}}{{$cell.Commits}}{{end}}</div>{{end}}
  {{end}}
</div>
{{end}}

{{if gt .MaxPattern 0}}
<h2>Working Hours</h2>
{{$pgrid := .PatternGrid}}{{$pmax := .MaxPattern}}
<div class="heatmap" style="grid-template-columns:35px repeat(24, 1fr);">
  <div></div>
  {{range $h := seq 0 23}}<div class="hour-label">{{printf "%02d" $h}}</div>{{end}}
  {{range $d, $dayName := (list "Mon" "Tue" "Wed" "Thu" "Fri" "Sat" "Sun")}}
  <div class="day-label" style="font-size:10px;">{{$dayName}}</div>
  {{range $h := seq 0 23}}
  <div class="cell" style="aspect-ratio:1; background:{{heatColor (index (index $pgrid $d) $h) $pmax}};" title="{{$dayName}} {{printf "%02d" $h}}:00 — {{index (index $pgrid $d) $h}} commits">{{if gt (index (index $pgrid $d) $h) 0}}{{index (index $pgrid $d) $h}}{{end}}</div>
  {{end}}
  {{end}}
</div>
{{end}}

<footer>Generated by <a href="https://github.com/lex0c/gitcortexv2" style="color:#0969da; text-decoration:none;">gitcortex</a></footer>

</body>
</html>`
