package viz

const step2Template = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>fishnet — Agent Overview</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }

body {
  background: #0f1117;
  color: #e2e8f0;
  font-family: 'SF Mono', 'Fira Code', monospace;
  min-height: 100vh;
}

/* ── Header ─────────────────────────────────────────────────────────── */
#header {
  position: sticky;
  top: 0;
  z-index: 100;
  background: #0f1117;
  border-bottom: 1px solid #2d3748;
  padding: 14px 24px;
  display: flex;
  align-items: center;
  gap: 16px;
}

#header h1 {
  font-size: 15px;
  font-weight: 600;
  color: #63b3ed;
  letter-spacing: 0.04em;
  flex: 1;
}

#header h1 span {
  color: #4a5568;
  font-weight: 400;
}

#node-count {
  font-size: 12px;
  color: #718096;
  background: #1a1d27;
  border: 1px solid #2d3748;
  padding: 4px 10px;
  border-radius: 20px;
}

#back-link {
  font-size: 12px;
  color: #63b3ed;
  text-decoration: none;
  padding: 5px 12px;
  border: 1px solid #2a4365;
  border-radius: 6px;
  transition: background 0.15s, color 0.15s;
}
#back-link:hover { background: #2a4365; color: #90cdf4; }

/* ── Controls ───────────────────────────────────────────────────────── */
#controls {
  padding: 16px 24px;
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  border-bottom: 1px solid #1e2230;
}

#controls label {
  font-size: 11px;
  color: #718096;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  margin-right: 4px;
}

.sort-btn {
  background: #1a1d27;
  border: 1px solid #2d3748;
  color: #a0aec0;
  font-size: 12px;
  font-family: inherit;
  padding: 5px 14px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s, color 0.15s;
}
.sort-btn:hover { background: #2d3748; color: #e2e8f0; }
.sort-btn.active {
  background: #2a4365;
  border-color: #3182ce;
  color: #90cdf4;
}

#filter-input {
  margin-left: auto;
  background: #1a1d27;
  border: 1px solid #2d3748;
  color: #e2e8f0;
  font-family: inherit;
  font-size: 12px;
  padding: 5px 12px;
  border-radius: 6px;
  width: 200px;
}
#filter-input:focus { outline: none; border-color: #63b3ed; }
#filter-input::placeholder { color: #4a5568; }

/* ── Grid ───────────────────────────────────────────────────────────── */
#grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16px;
  padding: 20px 24px;
}

@media (max-width: 1100px) {
  #grid { grid-template-columns: repeat(2, 1fr); }
}
@media (max-width: 680px) {
  #grid { grid-template-columns: 1fr; }
  #header h1 { font-size: 13px; }
  #controls { padding: 12px 16px; }
  #grid { padding: 12px 16px; }
}

/* ── Card ───────────────────────────────────────────────────────────── */
.card {
  background: #1a1d27;
  border: 1px solid #2d3748;
  border-radius: 10px;
  overflow: hidden;
  transition: border-color 0.2s, box-shadow 0.2s;
}
.card:hover {
  border-color: #4a5568;
  box-shadow: 0 4px 20px rgba(0,0,0,0.4);
}

.card-top {
  padding: 16px 16px 12px;
  border-bottom: 1px solid #1e2230;
}

.card-name {
  font-size: 15px;
  font-weight: 700;
  color: #f7fafc;
  margin-bottom: 8px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.badge-row {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
  margin-bottom: 10px;
}

.badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 12px;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.stance-supportive { background: #1c4532; color: #68d391; border: 1px solid #276749; }
.stance-opposing   { background: #3d1515; color: #fc8181; border: 1px solid #742a2a; }
.stance-neutral    { background: #2d3748; color: #a0aec0; border: 1px solid #4a5568; }
.stance-observer   { background: #1a365d; color: #63b3ed; border: 1px solid #2c5282; }
.badge-community   { background: #322659; color: #b794f4; border: 1px solid #44337a; }

.influence-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: #f6e05e;
  font-weight: 700;
}
.influence-row .inf-label {
  font-size: 10px;
  color: #718096;
  font-weight: 400;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

/* ── Middle ──────────────────────────────────────────────────────────── */
.card-middle {
  padding: 12px 16px;
  border-bottom: 1px solid #1e2230;
}

.bar-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.bar-row:last-of-type { margin-bottom: 0; }

.bar-label {
  font-size: 10px;
  color: #718096;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  width: 54px;
  flex-shrink: 0;
}

.bar-track {
  flex: 1;
  height: 5px;
  background: #2d3748;
  border-radius: 3px;
  overflow: hidden;
}

.bar-fill {
  height: 100%;
  border-radius: 3px;
  transition: width 0.3s ease;
}

.bar-value {
  font-size: 10px;
  color: #718096;
  width: 36px;
  text-align: right;
  flex-shrink: 0;
}

.sentiment-track {
  flex: 1;
  height: 5px;
  background: #2d3748;
  border-radius: 3px;
  position: relative;
  overflow: visible;
}
.sentiment-center {
  position: absolute;
  left: 50%;
  top: -1px;
  width: 1px;
  height: 7px;
  background: #4a5568;
}
.sentiment-fill {
  position: absolute;
  top: 0;
  height: 5px;
  border-radius: 3px;
}

.mini-stats {
  display: flex;
  gap: 12px;
  margin-top: 10px;
}
.mini-stat {
  display: flex;
  flex-direction: column;
  align-items: center;
  flex: 1;
  background: #0f1117;
  border-radius: 6px;
  padding: 6px 4px;
}
.mini-stat .ms-val {
  font-size: 13px;
  font-weight: 700;
  color: #e2e8f0;
}
.mini-stat .ms-key {
  font-size: 9px;
  color: #4a5568;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  margin-top: 2px;
}

/* ── Expandable bottom ───────────────────────────────────────────────── */
.card-toggle {
  width: 100%;
  background: none;
  border: none;
  border-top: 1px solid #1e2230;
  color: #4a5568;
  font-family: inherit;
  font-size: 11px;
  padding: 8px 16px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: space-between;
  transition: color 0.15s, background 0.15s;
  text-transform: uppercase;
  letter-spacing: 0.06em;
}
.card-toggle:hover { background: #0f1117; color: #a0aec0; }
.card-toggle .arrow { transition: transform 0.2s; }
.card-toggle.open .arrow { transform: rotate(180deg); }

.card-bottom {
  display: none;
  padding: 14px 16px;
  border-top: 1px solid #1e2230;
}
.card-bottom.visible { display: block; }

.big-five-title, .hours-title {
  font-size: 10px;
  color: #718096;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  margin-bottom: 8px;
}

.hours-grid {
  display: grid;
  grid-template-columns: repeat(24, 1fr);
  gap: 2px;
  margin-bottom: 14px;
}
.hour-dot {
  height: 8px;
  border-radius: 2px;
  background: #2d3748;
}
.hour-dot.active { background: #63b3ed; }

.summary-text {
  font-size: 11px;
  color: #718096;
  line-height: 1.6;
}

/* ── Loading ──────────────────────────────────────────────────────────── */
#loading {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 200px;
  color: #4a5568;
  font-size: 13px;
  grid-column: 1 / -1;
}
</style>
</head>
<body>

<div id="header">
  <h1>fishnet <span>&#8212;</span> Agent Overview</h1>
  <span id="node-count">loading&#8230;</span>
  <a href="/" id="back-link">Graph View</a>
</div>

<div id="controls">
  <label>Sort</label>
  <button class="sort-btn active" data-sort="influence">Influence</button>
  <button class="sort-btn" data-sort="activity">Activity</button>
  <button class="sort-btn" data-sort="community">Community</button>
  <button class="sort-btn" data-sort="name">Name</button>
  <input id="filter-input" type="text" placeholder="Filter agents&#8230;" />
</div>

<div id="grid">
  <div id="loading">Fetching agents&#8230;</div>
</div>

<script>
// ── Helpers ────────────────────────────────────────────────────────────────

var TYPE_PALETTE = [
  '#63b3ed','#68d391','#f6e05e','#fc8181','#b794f4',
  '#f6ad55','#76e4f7','#fbb6ce','#9ae6b4','#fbd38d',
  '#48bb78','#ed8936','#38b2ac','#e53e3e','#805ad5'
];

function typeColor(str) {
  var h = 5381;
  for (var i = 0; i < str.length; i++) { h = ((h * 33) + str.charCodeAt(i)) | 0; }
  return TYPE_PALETTE[Math.abs(h) % TYPE_PALETTE.length];
}

function fmt1(v) { return Number(v).toFixed(1); }
function fmt2(v) { return Number(v).toFixed(2); }

function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)); }

function escHtml(str) {
  return String(str || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// ── State ──────────────────────────────────────────────────────────────────

var allAgents = [];
var sortKey = 'influence';
var filterText = '';

// ── Render ─────────────────────────────────────────────────────────────────

function renderGrid() {
  var grid = document.getElementById('grid');

  var agents = allAgents.filter(function(a) {
    if (!filterText) return true;
    var q = filterText.toLowerCase();
    return (
      a.name.toLowerCase().indexOf(q) >= 0 ||
      a.node_type.toLowerCase().indexOf(q) >= 0 ||
      a.stance.toLowerCase().indexOf(q) >= 0 ||
      (a.summary || '').toLowerCase().indexOf(q) >= 0
    );
  });

  agents = agents.slice().sort(function(a, b) {
    if (sortKey === 'influence') return b.influence_weight - a.influence_weight;
    if (sortKey === 'activity')  return b.activity_level   - a.activity_level;
    if (sortKey === 'community') return a.community_id      - b.community_id;
    if (sortKey === 'name')      return a.name.localeCompare(b.name);
    return 0;
  });

  if (agents.length === 0) {
    grid.innerHTML = '<div id="loading">No agents match the filter.</div>';
    return;
  }

  grid.innerHTML = agents.map(buildCard).join('');

  grid.querySelectorAll('.card-toggle').forEach(function(btn) {
    btn.addEventListener('click', function() {
      btn.classList.toggle('open');
      var bottom = btn.nextElementSibling;
      bottom.classList.toggle('visible');
    });
  });
}

function buildCard(a) {
  var color = typeColor(a.node_type);

  var stanceClass = {
    supportive: 'stance-supportive',
    opposing:   'stance-opposing',
    neutral:    'stance-neutral',
    observer:   'stance-observer'
  }[a.stance] || 'stance-neutral';

  var communityBadge = a.community_id >= 0
    ? '<span class="badge badge-community">Community ' + a.community_id + '</span>'
    : '';

  var actPct = clamp(a.activity_level * 100, 0, 100).toFixed(0);

  var sbias = clamp(a.sentiment_bias, -1, 1);
  var sbColor = sbias > 0.05 ? '#68d391' : (sbias < -0.05 ? '#fc8181' : '#a0aec0');
  var sbLeft, sbWidth;
  if (sbias >= 0) {
    sbLeft  = '50%';
    sbWidth = (sbias * 50).toFixed(1) + '%';
  } else {
    sbWidth = (Math.abs(sbias) * 50).toFixed(1) + '%';
    sbLeft  = (50 + sbias * 50).toFixed(1) + '%';
  }
  var sbLabel = sbias > 0 ? '+' + fmt2(sbias) : fmt2(sbias);

  var bigFive = [
    { label: 'Creativity',   val: a.creativity,   color: '#b794f4' },
    { label: 'Rationality',  val: a.rationality,  color: '#63b3ed' },
    { label: 'Empathy',      val: a.empathy,      color: '#68d391' },
    { label: 'Extraversion', val: a.extraversion, color: '#f6ad55' },
    { label: 'Openness',     val: a.openness,     color: '#76e4f7' }
  ];

  var bigFiveBars = bigFive.map(function(t) {
    var pct = clamp(t.val * 100, 0, 100).toFixed(0);
    return '<div class="bar-row">' +
      '<span class="bar-label">' + t.label + '</span>' +
      '<div class="bar-track"><div class="bar-fill" style="width:' + pct + '%;background:' + t.color + '"></div></div>' +
      '<span class="bar-value">' + fmt2(t.val) + '</span>' +
      '</div>';
  }).join('');

  var activeSet = {};
  (a.active_hours || []).forEach(function(h) { activeSet[h] = true; });
  var hourDots = '';
  for (var h = 0; h < 24; h++) {
    hourDots += '<div class="hour-dot' + (activeSet[h] ? ' active' : '') + '" title="' + h + ':00"></div>';
  }

  var summaryHtml = a.summary
    ? '<div class="summary-text">' + escHtml(a.summary) + '</div>'
    : '';

  return '<div class="card">' +
    '<div class="card-top">' +
      '<div class="card-name" title="' + escHtml(a.name) + '">' + escHtml(a.name) + '</div>' +
      '<div class="badge-row">' +
        '<span class="badge" style="background:' + color + '22;color:' + color + ';border:1px solid ' + color + '44">' + escHtml(a.node_type) + '</span>' +
        '<span class="badge ' + stanceClass + '">' + escHtml(a.stance) + '</span>' +
        communityBadge +
      '</div>' +
      '<div class="influence-row">' +
        '<span>&#9733; ' + fmt2(a.influence_weight) + '</span>' +
        '<span class="inf-label">influence</span>' +
      '</div>' +
    '</div>' +

    '<div class="card-middle">' +
      '<div class="bar-row">' +
        '<span class="bar-label">Activity</span>' +
        '<div class="bar-track"><div class="bar-fill" style="width:' + actPct + '%;background:#63b3ed"></div></div>' +
        '<span class="bar-value">' + fmt2(a.activity_level) + '</span>' +
      '</div>' +
      '<div class="bar-row">' +
        '<span class="bar-label">Sentiment</span>' +
        '<div class="sentiment-track">' +
          '<div class="sentiment-center"></div>' +
          '<div class="sentiment-fill" style="left:' + sbLeft + ';width:' + sbWidth + ';background:' + sbColor + '"></div>' +
        '</div>' +
        '<span class="bar-value" style="color:' + sbColor + '">' + sbLabel + '</span>' +
      '</div>' +
      '<div class="mini-stats">' +
        '<div class="mini-stat"><span class="ms-val">' + fmt1(a.posts_per_hour) + '</span><span class="ms-key">posts/hr</span></div>' +
        '<div class="mini-stat"><span class="ms-val">' + fmt1(a.comments_per_hour) + '</span><span class="ms-key">cmts/hr</span></div>' +
        '<div class="mini-stat"><span class="ms-val">' + (a.active_hours || []).length + '</span><span class="ms-key">act hrs</span></div>' +
      '</div>' +
    '</div>' +

    '<button class="card-toggle">Details <span class="arrow">&#9662;</span></button>' +

    '<div class="card-bottom">' +
      '<div class="big-five-title">Personality</div>' +
      bigFiveBars +
      '<div class="hours-title" style="margin-top:12px">Active Hours</div>' +
      '<div class="hours-grid">' + hourDots + '</div>' +
      summaryHtml +
    '</div>' +

  '</div>';
}

// ── Controls ───────────────────────────────────────────────────────────────

document.querySelectorAll('.sort-btn').forEach(function(btn) {
  btn.addEventListener('click', function() {
    document.querySelectorAll('.sort-btn').forEach(function(b) { b.classList.remove('active'); });
    btn.classList.add('active');
    sortKey = btn.dataset.sort;
    renderGrid();
  });
});

document.getElementById('filter-input').addEventListener('input', function() {
  filterText = this.value;
  renderGrid();
});

// ── Fetch ──────────────────────────────────────────────────────────────────

fetch('/api/agents')
  .then(function(r) { return r.json(); })
  .then(function(data) {
    allAgents = data || [];
    var n = allAgents.length;
    document.getElementById('node-count').textContent = n + ' agent' + (n === 1 ? '' : 's');
    renderGrid();
  })
  .catch(function(err) {
    document.getElementById('grid').innerHTML =
      '<div id="loading">Error loading agents: ' + escHtml(String(err)) + '</div>';
  });
</script>
</body>
</html>`
