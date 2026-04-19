package report

const reportHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.RepoName}} report</title>
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
.churn-chips { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-bottom: 12px; }
.churn-chips .chip { padding: 3px 10px; border-radius: 12px; font-size: 11px; font-weight: 500; white-space: nowrap; }
.chip-legacy-hotspot { background: #cf222e; color: #fff; }
.chip-silo { background: #bf8700; color: #fff; }
.chip-active-core { background: #0969da; color: #fff; }
.chip-active { background: #2da44e; color: #fff; }
.chip-cold { background: #eaeef2; color: #656d76; }
.chip-note { font-size: 11px; color: #656d76; font-style: italic; margin-left: 4px; }
.glossary { background: #fff; border: 1px solid #d0d7de; border-radius: 6px; padding: 10px 16px; margin-bottom: 24px; }
.glossary summary { cursor: pointer; font-weight: 600; font-size: 13px; color: #24292f; }
.glossary[open] summary { margin-bottom: 8px; }
.glossary dl { font-size: 12px; color: #24292f; margin: 0; }
.glossary dt { font-weight: 600; margin-top: 8px; }
.glossary dt:first-child { margin-top: 0; }
.glossary dd { color: #656d76; margin: 2px 0 0; }
</style>
</head>
<body>

<h1>{{.RepoName}} report</h1>
<p class="subtitle">{{.Summary.FirstCommitDate}} to {{.Summary.LastCommitDate}}</p>

<details class="glossary">
  <summary>Glossary — what do these terms mean?</summary>
  <dl>
    <dt>Bus factor</dt>
    <dd>How many developers would need to leave before critical knowledge is lost. A file with bus factor 1 has a single owner — losing that person means losing the context.</dd>
    <dt>Churn</dt>
    <dd>Total lines added plus lines removed. High churn files are heavily modified — often where bugs accumulate.</dd>
    <dt>Recent churn</dt>
    <dd>Churn weighted so recent changes count more. Default half-life is 90 days (a change from a year ago is worth ~⅛ of a change today).</dd>
    <dt>Legacy-hotspot</dt>
    <dd>An old file with concentrated ownership and declining activity — deprecated code still being touched. Usually the most urgent refactor target.</dd>
    <dt>Silo</dt>
    <dd>Old, concentrated, and still stable or growing — a knowledge bottleneck. Plan transfer before the owner moves on.</dd>
    <dt>Active-core</dt>
    <dd>Newer code with a single main author. Often fine during early development; revisit if it ages without spreading ownership.</dd>
    <dt>Trend</dt>
    <dd>Ratio of recent churn to older churn for a file. Below 0.5 means activity is declining sharply; around 1 is stable; above 1.5 is growing. The declining case is what flips an old concentrated file from <b>silo</b> to <b>legacy-hotspot</b>.</dd>
    <dt>Age P__ / Trend P__</dt>
    <dd>Percentile suffixes on Churn Risk labels show where this file sits in the repo's own distribution. <b>Age P90</b> = older than 90% of tracked files; <b>Trend P10</b> = declining more sharply than 90%. Useful to separate a borderline classification (P76/P24) from a real alarm (P98/P03).</dd>
    <dt>Coupling</dt>
    <dd>How often two files change in the same commit. 100% means every change to the less-active file touches the other too. Expected for test-plus-code pairs; unexpected coupling reveals hidden dependencies.</dd>
    <dt>Pareto concentration</dt>
    <dd>Fraction of items holding 80% of activity. "Extremely concentrated" means ≤10% of files or developers carry 80% of churn — either a critical core or a dangerous bottleneck, depending on context.</dd>
    <dt>Specialization (Herfindahl index)</dt>
    <dd>0 = the developer works across many directories; 1 = all their files are in one directory. Measures where files live on disk, not domain expertise.</dd>
    <dt>Pace</dt>
    <dd>Commits per active day. High pace can mean productive small-PR flow or noisy commit habits; low pace can mean large reviewed patches or part-time contribution.</dd>
    <dt>Weekend %</dt>
    <dd>Fraction of commits on Saturday or Sunday. High values often signal overtime, an internationally distributed team, or off-hours deploy cadence — context tells which.</dd>
  </dl>
</details>

<div class="cards">
  <div class="card"><div class="label">Commits</div><div class="value" title="{{thousands .Summary.TotalCommits}}">{{humanize .Summary.TotalCommits}}</div></div>
  <div class="card"><div class="label">Developers</div><div class="value" title="{{thousands .Summary.TotalDevs}}">{{humanize .Summary.TotalDevs}}</div></div>
  <div class="card"><div class="label">Files</div><div class="value" title="{{thousands .Summary.TotalFiles}}">{{humanize .Summary.TotalFiles}}</div></div>
  <div class="card"><div class="label">Additions</div><div class="value" title="{{thousands .Summary.TotalAdditions}}">{{humanize .Summary.TotalAdditions}}</div></div>
  <div class="card"><div class="label">Deletions</div><div class="value" title="{{thousands .Summary.TotalDeletions}}">{{humanize .Summary.TotalDeletions}}</div></div>
  <div class="card"><div class="label">Merges</div><div class="value" title="{{thousands .Summary.MergeCommits}}">{{humanize .Summary.MergeCommits}}</div></div>
</div>

<h2>Concentration</h2>
<p class="hint">Pareto distribution across files, developers, and directories. Few items carrying 80% of activity means high concentration. Red and yellow markers deserve a closer look — concentration may signal a critical core module or a knowledge bottleneck, depending on context.</p>
<div style="display:flex; flex-direction:column; gap:12px; margin-bottom:24px;">
  <div style="background:#fff; border:1px solid #d0d7de; border-radius:6px; padding:14px 16px; display:flex; align-items:center; gap:12px;">
    <span style="font-size:20px;">{{.Pareto.FilesMarker}}</span>
    <div>
      <div style="font-size:14px; font-weight:600;">{{thousands .Pareto.TopChurnFiles}} files concentrate 80% of all churn</div>
      <div style="font-size:12px; color:#656d76;">out of {{thousands .Pareto.TotalFiles}} total files — {{.Pareto.FilesLabel}}</div>
    </div>
  </div>
  <div style="background:#fff; border:1px solid #d0d7de; border-radius:6px; padding:14px 16px; display:flex; align-items:center; gap:12px;">
    <span style="font-size:20px;">{{.Pareto.DevsCommitsMarker}}</span>
    <div>
      <div style="font-size:14px; font-weight:600;">{{thousands .Pareto.TopCommitDevs}} devs produce 80% of all commits</div>
      <div style="font-size:12px; color:#656d76;">out of {{thousands .Pareto.TotalDevs}} total devs — {{.Pareto.DevsCommitsLabel}}</div>
    </div>
  </div>
  <div style="background:#fff; border:1px solid #d0d7de; border-radius:6px; padding:14px 16px; display:flex; align-items:center; gap:12px;">
    <span style="font-size:20px;">{{.Pareto.DevsChurnMarker}}</span>
    <div>
      <div style="font-size:14px; font-weight:600;">{{thousands .Pareto.TopChurnDevs}} devs produce 80% of all line churn</div>
      <div style="font-size:12px; color:#656d76;">out of {{thousands .Pareto.TotalDevs}} total devs — {{.Pareto.DevsChurnLabel}}. Compare to the commits card: divergence reveals bots (commits ≫ churn) or feature owners (churn ≫ commits).</div>
    </div>
  </div>
  <div style="background:#fff; border:1px solid #d0d7de; border-radius:6px; padding:14px 16px; display:flex; align-items:center; gap:12px;">
    <span style="font-size:20px;">{{.Pareto.DirsMarker}}</span>
    <div>
      <div style="font-size:14px; font-weight:600;">{{thousands .Pareto.TopChurnDirs}} directories concentrate 80% of all churn</div>
      <div style="font-size:12px; color:#656d76;">out of {{thousands .Pareto.TotalDirs}} total directories — {{.Pareto.DirsLabel}}</div>
    </div>
  </div>
</div>

{{if .ActivityYears}}
<h2 style="display:flex; justify-content:space-between; align-items:center;">Activity <button onclick="var h=document.getElementById('act-heatmap'),t=document.getElementById('act-table');h.hidden=!h.hidden;t.hidden=!t.hidden;this.textContent=h.hidden?'heatmap':'table'" style="font-size:11px; font-weight:normal; padding:2px 10px; border:1px solid #d0d7de; border-radius:4px; background:#f6f8fa; color:#24292f; cursor:pointer;">table</button></h2>
<p class="hint">Monthly commit heatmap. Darker = more commits. Sudden drop-offs may mark team changes, re-orgs, or freezes; steady cadence signals healthy pace. Hover for details; toggle to table for exact numbers.</p>
{{$max := .MaxActivityCommits}}{{$grid := .ActivityGrid}}
<div id="act-heatmap">
<div style="display:grid; grid-template-columns:40px repeat(12, 1fr); gap:2px; margin-bottom:8px;">
  <div></div>
  {{range (list "J" "F" "M" "A" "M" "J" "J" "A" "S" "O" "N" "D")}}<div style="text-align:center; font-size:10px; color:#656d76;">{{.}}</div>{{end}}
  {{range $y, $year := $.ActivityYears}}
  <div class="mono" style="font-size:11px; color:#656d76; display:flex; align-items:center;">{{$year}}</div>
  {{range $m := seq 0 11}}{{$cell := index (index $grid $y) $m}}<div style="aspect-ratio:1.6; border-radius:2px; background:{{actColor $cell.Commits $max}}; display:flex; align-items:center; justify-content:center; font-size:9px; {{if $cell.HasData}}color:#fff;{{else}}color:transparent;{{end}}" title="{{$year}}-{{printf "%02d" (plusInt $m 1)}}:  {{$cell.Commits}} commits  +{{$cell.Additions}} -{{$cell.Deletions}}{{if and $cell.HasData (gt $cell.Additions 0)}}  ratio {{printf "%.2f" $cell.Ratio}}{{end}}">{{if $cell.HasData}}{{$cell.Commits}}{{end}}</div>{{end}}
  {{end}}
</div>
<div style="font-size:11px; color:#656d76; display:flex; gap:4px; align-items:center;">
  <span>Less</span>
  <div style="width:12px; height:12px; background:#ebedf0; border-radius:2px;"></div>
  <div style="width:12px; height:12px; background:#9be9a8; border-radius:2px;"></div>
  <div style="width:12px; height:12px; background:#40c463; border-radius:2px;"></div>
  <div style="width:12px; height:12px; background:#30a14e; border-radius:2px;"></div>
  <div style="width:12px; height:12px; background:#216e39; border-radius:2px;"></div>
  <span>More</span>
</div>
</div>
<div id="act-table" hidden>
<table>
<tr><th>Period</th><th>Commits</th><th>Additions</th><th>Deletions</th><th>Del/Add</th></tr>
{{range .ActivityRaw}}
<tr>
  <td class="mono">{{.Period}}</td>
  <td>{{thousands .Commits}}</td>
  <td>{{thousands .Additions}}</td>
  <td>{{thousands .Deletions}}</td>
  <td class="mono">{{if gt .Additions 0}}{{printf "%.2f" (pctRatio .Deletions .Additions)}}{{else}}—{{end}}</td>
</tr>
{{end}}
</table>
</div>
{{end}}

{{if .Contributors}}
<h2>Top Contributors</h2>
<p class="hint">Ranked by commit count. High commit count with low lines may indicate small fixes; low count with high lines may indicate large features.</p>
<table>
<tr><th>Name</th><th>Email</th><th>Commits</th><th></th><th>Additions</th><th>Deletions</th></tr>
{{$maxContrib := 0}}{{range .Contributors}}{{if gt .Commits $maxContrib}}{{$maxContrib = .Commits}}{{end}}{{end}}
{{range .Contributors}}
<tr>
  <td>{{.Name}}</td>
  <td class="mono" style="font-size:11px">{{.Email}}</td>
  <td>{{thousands .Commits}}</td>
  <td style="width:20%"><div class="bar-container"><div class="bar bar-commits" style="width: {{pctInt .Commits $maxContrib}}%"></div></div></td>
  <td>{{thousands .Additions}}</td>
  <td>{{thousands .Deletions}}</td>
</tr>
{{end}}
</table>
{{end}}

{{if .Hotspots}}
<h2>File Hotspots</h2>
<p class="hint">Most frequently changed files. High churn with few devs = knowledge silo. High churn with many devs = shared bottleneck.</p>
<table>
<tr><th>Path</th><th>Commits</th><th>Churn</th><th></th><th>Devs</th></tr>
{{$maxChurn := int64 0}}{{range .Hotspots}}{{if gt .Churn $maxChurn}}{{$maxChurn = .Churn}}{{end}}{{end}}
{{range .Hotspots}}
<tr>
  <td class="mono truncate">{{.Path}}</td>
  <td>{{thousands .Commits}}</td>
  <td>{{thousands .Churn}}</td>
  <td style="width:25%"><div class="bar-container"><div class="bar bar-churn" style="width: {{pct .Churn $maxChurn}}%"></div></div></td>
  <td>{{.UniqueDevs}}</td>
</tr>
{{end}}
</table>
{{end}}

{{if .Directories}}
<h2>Directories</h2>
<p class="hint">Module-level health. <b>File touches</b> is the sum of per-file commit counts (one commit touching N files contributes N), not distinct commits. Low bus factor = knowledge concentrated in few people.</p>
<table>
<tr><th>Directory</th><th>File Touches</th><th>Churn</th><th>Files</th><th>Devs</th><th>Bus Factor</th></tr>
{{range .Directories}}
<tr>
  <td class="mono">{{.Dir}}</td>
  <td>{{thousands .FileTouches}}</td>
  <td>{{thousands .Churn}}</td>
  <td>{{thousands .Files}}</td>
  <td>{{.UniqueDevs}}</td>
  <td>{{.BusFactor}}</td>
</tr>
{{end}}
</table>
{{end}}

{{if .ChurnRisk}}
<h2>Churn Risk</h2>
<p class="hint">Files ranked by recent churn. Label classifies context so you can judge action: <b>legacy-hotspot</b> (old code + concentrated + declining) is the urgent alarm; <b>silo</b> suggests knowledge transfer; <b>active-core</b> is young code with a single author (often fine); <b>active</b> is shared healthy work; <b>cold</b> is quiet.{{if (index .ChurnRisk 0).AgePercentile}} <b>Age P__ / Trend P__</b> under the label show where this file sits in the repo's distribution: age P90 means older than 90% of tracked files; trend P10 means declining more sharply than 90%. Classification boundaries are the P75 age and P25 trend of this dataset (see METRICS.md).{{end}}</p>
{{if .ChurnRiskLabelCounts}}
<div class="churn-chips">
  {{range .ChurnRiskLabelCounts}}
  <span class="chip chip-{{.Label}}">{{thousands .Count}} {{.Label}}</span>
  {{end}}
  <span class="chip-note">Distribution across the full dataset — the table below shows the top {{len .ChurnRisk}} by label priority, then recent churn.</span>
</div>
{{end}}
<table>
<tr><th>Path</th><th>Label</th><th>Recent Churn</th><th></th><th>BF</th><th>Age</th><th>Trend</th><th>Last Change</th></tr>
{{$maxChurn := 0.0}}{{range .ChurnRisk}}{{if gt .RecentChurn $maxChurn}}{{$maxChurn = .RecentChurn}}{{end}}{{end}}
{{range .ChurnRisk}}
<tr>
  <td class="mono truncate">{{.Path}}</td>
  <td>{{if eq .Label "legacy-hotspot"}}<span style="background:#cf222e; color:#fff; padding:2px 8px; border-radius:10px; font-size:11px;">🔴 {{.Label}}</span>{{else if eq .Label "silo"}}<span style="background:#bf8700; color:#fff; padding:2px 8px; border-radius:10px; font-size:11px;">🟡 {{.Label}}</span>{{else if eq .Label "active-core"}}<span style="background:#0969da; color:#fff; padding:2px 8px; border-radius:10px; font-size:11px;">{{.Label}}</span>{{else if eq .Label "active"}}<span style="background:#2da44e; color:#fff; padding:2px 8px; border-radius:10px; font-size:11px;">{{.Label}}</span>{{else}}<span style="background:#eaeef2; color:#656d76; padding:2px 8px; border-radius:10px; font-size:11px;">{{.Label}}</span>{{end}}{{if .AgePercentile}}<div style="font-size:10px; color:#656d76; margin-top:2px;">age P{{derefInt .AgePercentile}} · trend P{{derefInt .TrendPercentile}}</div>{{end}}</td>
  <td>{{printf "%.1f" .RecentChurn}}</td>
  <td style="width:18%"><div class="bar-container"><div class="bar bar-churn" style="width: {{printf "%.0f" (pct (int64 .RecentChurn) (int64 $maxChurn))}}%"></div></div></td>
  <td>{{.BusFactor}}</td>
  <td>{{.AgeDays}}d</td>
  <td>{{if lt .Trend 0.5}}↓ {{printf "%.2f" .Trend}}{{else if gt .Trend 1.5}}↑ {{printf "%.2f" .Trend}}{{else}}→ {{printf "%.2f" .Trend}}{{end}}</td>
  <td class="mono">{{.LastChangeDate}}</td>
</tr>
{{end}}
</table>
{{end}}

{{if .BusFactor}}
<h2>Bus Factor Risk</h2>
<p class="hint">Files with fewest developers owning 80%+ of changes. Bus factor 1 = if that person leaves, nobody else knows the code.</p>
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
<p class="hint">Files that always change together. Expected for test+code pairs. Unexpected coupling between unrelated modules signals hidden dependencies.</p>
<table>
<tr><th>File A</th><th>File B</th><th>Co-changes</th><th>Coupling</th></tr>
{{range .Coupling}}
<tr>
  <td class="mono truncate">{{.FileA}}</td>
  <td class="mono truncate">{{.FileB}}</td>
  <td>{{thousands .CoChanges}}</td>
  <td><div class="bar-container"><div class="bar bar-commits" style="width: {{.CouplingPct}}%"></div><span class="bar-value">{{printf "%.0f" .CouplingPct}}%</span></div></td>
</tr>
{{end}}
</table>
{{end}}

{{if gt .MaxPattern 0}}
<h2>Working Patterns</h2>
<p class="hint">Commit distribution by day and hour. Reveals team timezones, work-life balance, and off-hours work patterns.</p>
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
<p class="hint">Largest commits by lines changed. Unusually large commits may be imports, generated code, or risky big-bang changes worth reviewing.</p>
<table>
<tr><th>SHA</th><th>Author</th><th>Date</th><th>Lines</th><th>Files</th>{{if and (gt (len .TopCommits) 0) (index .TopCommits 0).Message}}<th>Message</th>{{end}}</tr>
{{range .TopCommits}}
<tr>
  <td class="mono">{{slice .SHA 0 12}}</td>
  <td>{{.AuthorName}}</td>
  <td class="mono">{{.Date}}</td>
  <td>{{thousands .LinesChanged}}</td>
  <td>{{thousands .FilesChanged}}</td>
  {{if .Message}}<td class="truncate">{{.Message}}</td>{{end}}
</tr>
{{end}}
</table>
{{end}}

{{if .DevNetwork}}
<h2>Developer Network</h2>
<p class="hint">Developers who modify the same files. <b>Shared lines</b> = Σ min(lines_A, lines_B) per file — measures real overlap, not trivial one-line touches. Sorted by shared lines.</p>
<table>
<tr><th>Developer A</th><th>Developer B</th><th>Shared Files</th><th>Shared Lines</th><th>Weight</th></tr>
{{range .DevNetwork}}
<tr>
  <td class="mono" style="font-size:11px">{{.DevA}}</td>
  <td class="mono" style="font-size:11px">{{.DevB}}</td>
  <td>{{thousands .SharedFiles}}</td>
  <td>{{thousands .SharedLines}}</td>
  <td><div class="bar-container"><div class="bar bar-score" style="width: {{.Weight}}%"></div><span class="bar-value">{{printf "%.1f" .Weight}}%</span></div></td>
</tr>
{{end}}
</table>
{{end}}

{{if .Profiles}}
<h2>Developer Profiles</h2>
<p class="hint">Per-developer view. Use to spot silos (narrow scope + few collaborators), knowledge concentration (high pace on few directories), and cultural patterns (weekend or refactor-heavy work).</p>
{{range .Profiles}}
<div style="background:#fff; border:1px solid #d0d7de; border-radius:6px; padding:16px; margin-bottom:16px;">
  <div style="font-size:16px; font-weight:600;">{{.Name}} <span class="mono" style="font-size:12px; color:#656d76;">&lt;{{.Email}}&gt;</span></div>
  <div style="margin:6px 0 12px; font-size:13px; color:#656d76;">{{.FirstDate}} to {{.LastDate}} · {{thousands .ActiveDays}} active days · {{thousands .Commits}} commits</div>

  <div style="display:grid; grid-template-columns:110px 1fr; gap:4px 12px; font-size:13px; margin-bottom:12px;">
    <span style="color:#656d76;">Scope</span>
    <span>{{range $i, $s := .Scope}}{{if $i}}, {{end}}<b>{{$s.Dir}}</b> ({{printf "%.0f" $s.Pct}}%){{end}}</span>

    <span style="color:#656d76;">Specialization</span>
    <span>{{printf "%.3f" .Specialization}} <span style="color:#656d76;">({{if lt .Specialization 0.15}}broad generalist{{else if lt .Specialization 0.35}}balanced{{else if lt .Specialization 0.7}}focused specialist{{else}}narrow specialist{{end}})</span></span>

    <span style="color:#656d76;">Contribution</span>
    <span>{{if eq .ContribType "growth"}}<span style="color:#2da44e;">{{.ContribType}}</span>{{else if eq .ContribType "refactor"}}<span style="color:#cf222e;">{{.ContribType}}</span>{{else}}<span style="color:#bf8700;">{{.ContribType}}</span>{{end}} <span style="color:#656d76;">(ratio {{printf "%.2f" .ContribRatio}} · +{{thousands .Additions}} −{{thousands .Deletions}})</span></span>

    <span style="color:#656d76;">Pace</span>
    <span>{{printf "%.1f" .Pace}} commits/active day</span>

    <span style="color:#656d76;">Collaboration</span>
    <span>{{if .Collaborators}}{{range $i, $c := .Collaborators}}{{if $i}}, {{end}}{{$c.Email}} ({{thousands $c.SharedFiles}} files, {{thousands $c.SharedLines}} lines){{end}}{{else}}solo contributor{{end}}</span>

    <span style="color:#656d76;">Weekend</span>
    <span>{{printf "%.1f" .WeekendPct}}%</span>
  </div>

  {{if .TopFiles}}
  <div style="font-size:12px;">
    <div style="font-weight:600; margin-bottom:4px;">Top files:</div>
    {{range .TopFiles}}
    <div class="mono" style="display:flex; gap:8px;">
      <span class="truncate" style="min-width:300px;">{{.Path}}</span>
      <span style="color:#656d76;">{{thousands .Commits}} commits</span>
      <span style="color:#656d76;">{{thousands .Churn}} churn</span>
    </div>
    {{end}}
  </div>
  {{end}}
</div>
{{end}}
{{end}}

<footer>Generated by <a href="https://github.com/lex0c/gitcortex" target="_blank" rel="noopener noreferrer" style="color:#0969da; text-decoration:none;">gitcortex</a> · {{.GeneratedAt}}</footer>

</body>
</html>`
