package report

const profileHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Profile.Name}} — {{.RepoName}}</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; color: #24292f; background: #f6f8fa; padding: 20px; max-width: 1200px; margin: 0 auto; }
h1 { font-size: 24px; margin-bottom: 8px; }
h2 { font-size: 18px; margin: 32px 0 4px; padding-bottom: 8px; border-bottom: 1px solid #d0d7de; }
.subtitle { color: #656d76; font-size: 14px; margin-bottom: 24px; }
.hint { color: #656d76; font-size: 12px; margin-bottom: 12px; font-style: italic; }
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

<h1>{{.Profile.Name}} <span style="font-size:16px; font-weight:normal; color:#656d76;">· {{.RepoName}}</span></h1>
<p class="subtitle">{{.Profile.Email}} · {{.Profile.FirstDate}} to {{.Profile.LastDate}}</p>

<div class="cards">
  <div class="card"><div class="label">Commits</div><div class="value">{{.Profile.Commits}}</div></div>
  <div class="card"><div class="label">Lines Changed</div><div class="value">{{.Profile.LinesChanged}}</div></div>
  <div class="card"><div class="label">Files Touched</div><div class="value">{{.Profile.FilesTouched}}</div></div>
  <div class="card"><div class="label">Active Days</div><div class="value">{{.Profile.ActiveDays}}</div></div>
  <div class="card"><div class="label">Pace</div><div class="value">{{printf "%.1f" .Profile.Pace}}</div><div class="detail">commits/active day</div></div>
  <div class="card"><div class="label">Weekend</div><div class="value">{{printf "%.1f" .Profile.WeekendPct}}%</div></div>
</div>

<div style="margin-bottom:16px;">
  <div style="font-size:13px; font-weight:600; margin-bottom:2px;">Scope</div>
  <div class="hint" style="margin-bottom:6px;">Where this developer works. Percentage of unique files touched per directory.</div>
  <div style="display:flex; height:28px; border-radius:4px; overflow:hidden; gap:1px;">
    {{range $i, $s := .Profile.Scope}}<div style="flex:{{printf "%.0f" $s.Pct}}; background:{{index (list "#0969da" "#2da44e" "#8250df" "#bf8700" "#cf222e") $i}}; display:flex; align-items:center; justify-content:center; color:#fff; font-size:10px; min-width:30px; overflow:hidden;" title="{{$s.Dir}} — {{$s.Files}} files ({{printf "%.0f" $s.Pct}}%)">{{if gt $s.Pct 8.0}}{{$s.Dir}} {{printf "%.0f" $s.Pct}}%{{end}}</div>{{end}}
  </div>
  <div style="display:flex; flex-wrap:wrap; gap:8px; margin-top:4px; font-size:11px; color:#656d76;">
    {{range $i, $s := .Profile.Scope}}<span><span style="display:inline-block; width:8px; height:8px; border-radius:2px; background:{{index (list "#0969da" "#2da44e" "#8250df" "#bf8700" "#cf222e") $i}};"></span> {{$s.Dir}} ({{printf "%.0f" $s.Pct}}%)</span>{{end}}
  </div>
</div>

<div style="margin-bottom:16px; font-size:13px;">
  <div style="margin-bottom:2px;">
    <span style="font-weight:600;">Contribution</span>
    <span style="font-size:11px; color:#656d76; font-style:italic; margin-left:4px;">Growth (add &gt;&gt; del), balanced, or refactor (del &gt;&gt; add).</span>
  </div>
  <div>
    {{if eq .Profile.ContribType "growth"}}<span style="color:#2da44e; font-weight:600;">growth</span>{{else if eq .Profile.ContribType "refactor"}}<span style="color:#cf222e; font-weight:600;">refactor</span>{{else}}<span style="color:#bf8700; font-weight:600;">balanced</span>{{end}}
    <span style="color:#656d76;">(ratio {{printf "%.2f" .Profile.ContribRatio}} · +{{.Profile.Additions}} −{{.Profile.Deletions}})</span>
  </div>
</div>

{{if .Profile.Collaborators}}
<div style="margin-bottom:16px;">
  <div style="font-size:13px; font-weight:600; margin-bottom:2px;">Collaboration</div>
  <div class="hint" style="margin-bottom:6px;">Developers who modify the same files. Number = shared files.</div>
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
<p class="hint">Most impacted files by this developer. Churn = additions + deletions.</p>
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
<p class="hint">Monthly commit heatmap. Darker = more active. Gaps = inactive periods.</p>
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
<p class="hint">Commit distribution by day and hour. Reveals timezone and work-life patterns.</p>
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

<footer>Generated by <a href="https://github.com/lex0c/gitcortex" style="color:#0969da; text-decoration:none;">gitcortex</a> · {{.GeneratedAt}}</footer>

</body>
</html>`
