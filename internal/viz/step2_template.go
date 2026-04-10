package viz

const step2Template = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>fishnet — Agent Studio</title>
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
  padding: 12px 24px;
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
#header h1 {
  font-size: 14px;
  font-weight: 600;
  color: #63b3ed;
  letter-spacing: 0.04em;
  flex: 1;
}
#header h1 span { color: #4a5568; font-weight: 400; }

#node-count {
  font-size: 11px;
  color: #718096;
  background: #1a1d27;
  border: 1px solid #2d3748;
  padding: 3px 10px;
  border-radius: 20px;
}

.hbtn {
  font-size: 12px;
  font-family: inherit;
  padding: 5px 14px;
  border-radius: 6px;
  cursor: pointer;
  border: 1px solid #2d3748;
  transition: background 0.15s, border-color 0.15s, color 0.15s;
}

#btn-generate {
  background: #1c4532;
  border-color: #276749;
  color: #68d391;
}
#btn-generate:hover:not(:disabled) { background: #276749; }
#btn-generate:disabled { opacity: 0.5; cursor: not-allowed; }

#btn-new {
  background: #1a365d;
  border-color: #2c5282;
  color: #63b3ed;
}
#btn-new:hover { background: #2c5282; }

#btn-merge-mode {
  background: #1a1d27;
  border-color: #2d3748;
  color: #a0aec0;
}
#btn-merge-mode.active {
  background: #322659;
  border-color: #6b46c1;
  color: #b794f4;
}

#back-link {
  font-size: 12px;
  color: #63b3ed;
  text-decoration: none;
  padding: 5px 12px;
  border: 1px solid #2a4365;
  border-radius: 6px;
  transition: background 0.15s;
}
#back-link:hover { background: #2a4365; }

/* ── Gen status bar ─────────────────────────────────────────────────── */
#gen-status {
  display: none;
  padding: 8px 24px;
  background: #1c4532;
  border-bottom: 1px solid #276749;
  font-size: 12px;
  color: #68d391;
  align-items: center;
  gap: 8px;
}
#gen-status.visible { display: flex; }
.spinner {
  width: 12px; height: 12px;
  border: 2px solid #276749;
  border-top-color: #68d391;
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
  flex-shrink: 0;
}
@keyframes spin { to { transform: rotate(360deg); } }

/* ── Controls ───────────────────────────────────────────────────────── */
#controls {
  padding: 12px 24px;
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  border-bottom: 1px solid #1e2230;
}
#controls label {
  font-size: 11px;
  color: #718096;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  margin-right: 2px;
}
.sort-btn {
  background: #1a1d27;
  border: 1px solid #2d3748;
  color: #a0aec0;
  font-size: 12px;
  font-family: inherit;
  padding: 4px 12px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s;
}
.sort-btn:hover { background: #2d3748; color: #e2e8f0; }
.sort-btn.active { background: #2a4365; border-color: #3182ce; color: #90cdf4; }

#filter-input {
  margin-left: auto;
  background: #1a1d27;
  border: 1px solid #2d3748;
  color: #e2e8f0;
  font-family: inherit;
  font-size: 12px;
  padding: 4px 12px;
  border-radius: 6px;
  width: 180px;
}
#filter-input:focus { outline: none; border-color: #63b3ed; }
#filter-input::placeholder { color: #4a5568; }

/* ── Merge toolbar (floats when active) ─────────────────────────────── */
#merge-bar {
  display: none;
  position: fixed;
  bottom: 24px;
  left: 50%;
  transform: translateX(-50%);
  background: #322659;
  border: 1px solid #6b46c1;
  border-radius: 10px;
  padding: 12px 20px;
  align-items: center;
  gap: 12px;
  z-index: 200;
  box-shadow: 0 8px 32px rgba(0,0,0,0.5);
}
#merge-bar.visible { display: flex; }
#merge-bar span { font-size: 13px; color: #d6bcfa; }
#btn-do-merge {
  background: #6b46c1;
  border: none;
  color: #fff;
  font-family: inherit;
  font-size: 12px;
  font-weight: 600;
  padding: 6px 18px;
  border-radius: 6px;
  cursor: pointer;
}
#btn-do-merge:disabled { opacity: 0.5; cursor: not-allowed; }
#btn-cancel-merge {
  background: none;
  border: none;
  color: #805ad5;
  font-family: inherit;
  font-size: 12px;
  cursor: pointer;
}

/* ── Grid ───────────────────────────────────────────────────────────── */
#grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16px;
  padding: 20px 24px;
}
@media (max-width: 1100px) { #grid { grid-template-columns: repeat(2, 1fr); } }
@media (max-width: 680px)  { #grid { grid-template-columns: 1fr; } }

/* ── Card ───────────────────────────────────────────────────────────── */
.card {
  background: #1a1d27;
  border: 1px solid #2d3748;
  border-radius: 10px;
  overflow: hidden;
  transition: border-color 0.2s, box-shadow 0.2s;
  position: relative;
}
.card:hover { border-color: #4a5568; box-shadow: 0 4px 20px rgba(0,0,0,0.4); }
.card.selected { border-color: #6b46c1; box-shadow: 0 0 0 2px #6b46c133; }

.card-actions {
  position: absolute;
  top: 10px;
  right: 10px;
  display: flex;
  gap: 4px;
  opacity: 0;
  transition: opacity 0.15s;
}
.card:hover .card-actions { opacity: 1; }

.card-action-btn {
  background: #0f1117cc;
  border: 1px solid #2d3748;
  color: #a0aec0;
  font-family: inherit;
  font-size: 10px;
  padding: 3px 7px;
  border-radius: 4px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}
.card-action-btn:hover { background: #2d3748; color: #e2e8f0; }
.card-action-btn.del:hover { background: #3d1515; border-color: #742a2a; color: #fc8181; }

.card-top { padding: 14px 14px 10px; border-bottom: 1px solid #1e2230; }
.card-name {
  font-size: 14px;
  font-weight: 700;
  color: #f7fafc;
  margin-bottom: 6px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  padding-right: 64px; /* room for action buttons */
}
.card-sub {
  font-size: 10px;
  color: #718096;
  margin-bottom: 6px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.badge-row { display: flex; flex-wrap: wrap; gap: 4px; margin-bottom: 8px; }
.badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 7px;
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
.badge-no-persona  { background: #2d2a00; color: #f6e05e; border: 1px solid #744210; }

.influence-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: #f6e05e;
  font-weight: 700;
}
.influence-row .inf-label { font-size: 10px; color: #718096; font-weight: 400; text-transform: uppercase; }

/* ── Middle ──────────────────────────────────────────────────────────── */
.card-middle { padding: 10px 14px; border-bottom: 1px solid #1e2230; }
.bar-row { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
.bar-row:last-of-type { margin-bottom: 0; }
.bar-label { font-size: 10px; color: #718096; text-transform: uppercase; width: 54px; flex-shrink: 0; }
.bar-track { flex: 1; height: 5px; background: #2d3748; border-radius: 3px; overflow: hidden; }
.bar-fill { height: 100%; border-radius: 3px; transition: width 0.3s; }
.bar-value { font-size: 10px; color: #718096; width: 36px; text-align: right; flex-shrink: 0; }

.sentiment-track { flex: 1; height: 5px; background: #2d3748; border-radius: 3px; position: relative; overflow: visible; }
.sentiment-center { position: absolute; left: 50%; top: -1px; width: 1px; height: 7px; background: #4a5568; }
.sentiment-fill { position: absolute; top: 0; height: 5px; border-radius: 3px; }

.mini-stats { display: flex; gap: 8px; margin-top: 8px; }
.mini-stat { display: flex; flex-direction: column; align-items: center; flex: 1; background: #0f1117; border-radius: 6px; padding: 5px 4px; }
.mini-stat .ms-val { font-size: 12px; font-weight: 700; color: #e2e8f0; }
.mini-stat .ms-key { font-size: 9px; color: #4a5568; text-transform: uppercase; margin-top: 1px; }

/* ── Expandable bottom ───────────────────────────────────────────────── */
.card-toggle {
  width: 100%;
  background: none;
  border: none;
  border-top: 1px solid #1e2230;
  color: #4a5568;
  font-family: inherit;
  font-size: 11px;
  padding: 7px 14px;
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

.card-bottom { display: none; padding: 12px 14px; border-top: 1px solid #1e2230; }
.card-bottom.visible { display: block; }

.section-title {
  font-size: 10px;
  color: #718096;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  margin-bottom: 6px;
  margin-top: 10px;
}
.section-title:first-child { margin-top: 0; }

.hours-grid { display: grid; grid-template-columns: repeat(24, 1fr); gap: 2px; }
.hour-dot { height: 7px; border-radius: 2px; background: #2d3748; }
.hour-dot.active { background: #63b3ed; }

.chips { display: flex; flex-wrap: wrap; gap: 4px; }
.chip {
  background: #1e2230;
  border: 1px solid #2d3748;
  border-radius: 4px;
  font-size: 10px;
  color: #a0aec0;
  padding: 2px 7px;
}

.summary-text { font-size: 11px; color: #718096; line-height: 1.6; }

/* ── Loading ──────────────────────────────────────────────────────────── */
#loading { display: flex; align-items: center; justify-content: center; height: 200px; color: #4a5568; font-size: 13px; grid-column: 1 / -1; }

/* ── Modal ────────────────────────────────────────────────────────────── */
.modal-overlay {
  display: none;
  position: fixed;
  inset: 0;
  background: rgba(0,0,0,0.7);
  z-index: 300;
  align-items: center;
  justify-content: center;
}
.modal-overlay.visible { display: flex; }

.modal {
  background: #1a1d27;
  border: 1px solid #2d3748;
  border-radius: 12px;
  width: 480px;
  max-width: 95vw;
  max-height: 85vh;
  overflow-y: auto;
  padding: 24px;
  position: relative;
}
.modal h2 {
  font-size: 15px;
  color: #f7fafc;
  margin-bottom: 20px;
  padding-right: 24px;
}
.modal-close {
  position: absolute;
  top: 16px;
  right: 16px;
  background: none;
  border: none;
  color: #4a5568;
  font-size: 18px;
  cursor: pointer;
  line-height: 1;
}
.modal-close:hover { color: #a0aec0; }

.field-group { margin-bottom: 16px; }
.field-label { font-size: 11px; color: #718096; text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: 5px; display: block; }
.field-input {
  width: 100%;
  background: #0f1117;
  border: 1px solid #2d3748;
  color: #e2e8f0;
  font-family: inherit;
  font-size: 13px;
  padding: 7px 10px;
  border-radius: 6px;
}
.field-input:focus { outline: none; border-color: #63b3ed; }

.field-select {
  width: 100%;
  background: #0f1117;
  border: 1px solid #2d3748;
  color: #e2e8f0;
  font-family: inherit;
  font-size: 13px;
  padding: 7px 10px;
  border-radius: 6px;
}

.slider-row { display: flex; align-items: center; gap: 10px; }
.slider-row input[type=range] {
  flex: 1;
  accent-color: #63b3ed;
}
.slider-val { font-size: 12px; color: #a0aec0; width: 40px; text-align: right; flex-shrink: 0; }

.grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }

.modal-actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 24px; }
.btn-cancel {
  background: #1a1d27;
  border: 1px solid #2d3748;
  color: #a0aec0;
  font-family: inherit;
  font-size: 12px;
  padding: 7px 18px;
  border-radius: 6px;
  cursor: pointer;
}
.btn-save {
  background: #2a4365;
  border: 1px solid #3182ce;
  color: #90cdf4;
  font-family: inherit;
  font-size: 12px;
  font-weight: 600;
  padding: 7px 18px;
  border-radius: 6px;
  cursor: pointer;
}
.btn-save:hover { background: #3182ce; color: #fff; }
</style>
</head>
<body>

<!-- Header -->
<div id="header">
  <h1>fishnet <span>&#8212;</span> Agent Studio</h1>
  <span id="node-count">loading&#8230;</span>
  <button class="hbtn" id="btn-generate">&#9654; Generate Personas</button>
  <button class="hbtn" id="btn-new">+ New Agent</button>
  <button class="hbtn" id="btn-merge-mode">&#8853; Merge</button>
  <a href="/" id="back-link">Graph View</a>
</div>

<!-- Gen status -->
<div id="gen-status">
  <div class="spinner"></div>
  <span id="gen-status-text">Generating personalities&#8230;</span>
</div>

<!-- Controls -->
<div id="controls">
  <label>Sort</label>
  <button class="sort-btn active" data-sort="influence">Influence</button>
  <button class="sort-btn" data-sort="activity">Activity</button>
  <button class="sort-btn" data-sort="community">Community</button>
  <button class="sort-btn" data-sort="name">Name</button>
  <input id="filter-input" type="text" placeholder="Filter agents&#8230;" />
</div>

<!-- Grid -->
<div id="grid">
  <div id="loading">Fetching agents&#8230;</div>
</div>

<!-- Merge bar -->
<div id="merge-bar">
  <span id="merge-bar-label">Select 2 agents to merge</span>
  <button id="btn-do-merge" disabled>Merge</button>
  <button id="btn-cancel-merge">Cancel</button>
</div>

<!-- Edit/Create modal -->
<div class="modal-overlay" id="modal-overlay">
  <div class="modal" id="modal">
    <h2 id="modal-title">Edit Agent</h2>
    <button class="modal-close" id="modal-close">&#10005;</button>

    <div id="modal-create-fields" style="display:none">
      <div class="grid-2">
        <div class="field-group">
          <label class="field-label">Name</label>
          <input class="field-input" id="m-name" placeholder="Agent Name" />
        </div>
        <div class="field-group">
          <label class="field-label">Type</label>
          <select class="field-select" id="m-type">
            <option>Person</option>
            <option>Organization</option>
            <option>Location</option>
            <option>Concept</option>
          </select>
        </div>
      </div>
      <div class="field-group">
        <label class="field-label">Bio / Summary</label>
        <input class="field-input" id="m-summary" placeholder="One-line description" />
      </div>
    </div>

    <div class="field-group">
      <label class="field-label">Stance</label>
      <select class="field-select" id="m-stance">
        <option value="neutral">Neutral</option>
        <option value="supportive">Supportive</option>
        <option value="opposing">Opposing</option>
        <option value="observer">Observer</option>
      </select>
    </div>

    <div class="field-group">
      <label class="field-label">Sentiment Bias <span style="color:#4a5568">(-1 negative &#8594; +1 positive)</span></label>
      <div class="slider-row">
        <input type="range" id="m-sentiment" min="-1" max="1" step="0.05" value="0" />
        <span class="slider-val" id="m-sentiment-val">0.00</span>
      </div>
    </div>

    <div class="grid-2">
      <div class="field-group">
        <label class="field-label">Activity Level</label>
        <div class="slider-row">
          <input type="range" id="m-activity" min="0" max="1" step="0.05" value="0.5" />
          <span class="slider-val" id="m-activity-val">0.50</span>
        </div>
      </div>
      <div class="field-group">
        <label class="field-label">Influence Weight</label>
        <div class="slider-row">
          <input type="range" id="m-influence" min="0" max="2" step="0.05" value="1" />
          <span class="slider-val" id="m-influence-val">1.00</span>
        </div>
      </div>
    </div>

    <div class="grid-2">
      <div class="field-group">
        <label class="field-label">Posts / hr</label>
        <input class="field-input" id="m-posts" type="number" min="0.1" max="10" step="0.1" value="1.0" />
      </div>
      <div class="field-group">
        <label class="field-label">Comments / hr</label>
        <input class="field-input" id="m-comments" type="number" min="0.1" max="20" step="0.1" value="2.0" />
      </div>
    </div>

    <div class="field-group" style="margin-top:4px">
      <label class="field-label">Personality (Big Five)</label>
      <div id="big-five-sliders"></div>
    </div>

    <div class="grid-2">
      <div class="field-group">
        <label class="field-label">Profession</label>
        <input class="field-input" id="m-profession" placeholder="Software Engineer" />
      </div>
      <div class="field-group">
        <label class="field-label">Location</label>
        <input class="field-input" id="m-location" placeholder="Tokyo, Japan" />
      </div>
    </div>

    <div class="field-group">
      <label class="field-label">Username (no @)</label>
      <input class="field-input" id="m-username" placeholder="handle_123" />
    </div>

    <div class="field-group">
      <label class="field-label">Topics (comma-separated)</label>
      <input class="field-input" id="m-interests" placeholder="AI, Tech, Politics" />
    </div>

    <div class="field-group">
      <label class="field-label">Catchphrases (comma-separated)</label>
      <input class="field-input" id="m-catchphrases" placeholder="Let's ship it!, Data doesn't lie" />
    </div>

    <div class="modal-actions">
      <button class="btn-cancel" id="modal-btn-cancel">Cancel</button>
      <button class="btn-save" id="modal-btn-save">Save</button>
    </div>
  </div>
</div>

<script>
// ── State ──────────────────────────────────────────────────────────────────
var allAgents = [];
var sortKey = 'influence';
var filterText = '';
var mergeMode = false;
var mergeSelected = [];
var editingID = null; // null = create, string = edit

// ── Helpers ────────────────────────────────────────────────────────────────
var TYPE_PALETTE = [
  '#63b3ed','#68d391','#f6e05e','#fc8181','#b794f4',
  '#f6ad55','#76e4f7','#fbb6ce','#9ae6b4','#fbd38d'
];
function typeColor(str) {
  var h = 5381;
  for (var i = 0; i < str.length; i++) h = ((h * 33) + str.charCodeAt(i)) | 0;
  return TYPE_PALETTE[Math.abs(h) % TYPE_PALETTE.length];
}
function fmt1(v) { return Number(v).toFixed(1); }
function fmt2(v) { return Number(v).toFixed(2); }
function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)); }
function escHtml(s) {
  return String(s || '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// ── Fetch & refresh ────────────────────────────────────────────────────────
function loadAgents() {
  return fetch('/api/agents')
    .then(function(r) { return r.json(); })
    .then(function(data) {
      allAgents = data || [];
      document.getElementById('node-count').textContent =
        allAgents.length + ' agent' + (allAgents.length === 1 ? '' : 's');
      renderGrid();
    });
}

// ── Render grid ────────────────────────────────────────────────────────────
function renderGrid() {
  var grid = document.getElementById('grid');
  var agents = allAgents.filter(function(a) {
    if (!filterText) return true;
    var q = filterText.toLowerCase();
    return a.name.toLowerCase().indexOf(q) >= 0
      || a.node_type.toLowerCase().indexOf(q) >= 0
      || a.stance.toLowerCase().indexOf(q) >= 0
      || (a.summary || '').toLowerCase().indexOf(q) >= 0
      || (a.profession || '').toLowerCase().indexOf(q) >= 0
      || (a.location || '').toLowerCase().indexOf(q) >= 0;
  });
  agents = agents.slice().sort(function(a, b) {
    if (sortKey === 'influence') return b.influence_weight - a.influence_weight;
    if (sortKey === 'activity')  return b.activity_level   - a.activity_level;
    if (sortKey === 'community') return a.community_id      - b.community_id;
    if (sortKey === 'name')      return a.name.localeCompare(b.name);
    return 0;
  });
  if (agents.length === 0) {
    grid.innerHTML = '<div id="loading">No agents match.</div>';
    return;
  }
  grid.innerHTML = agents.map(buildCard).join('');

  grid.querySelectorAll('.card-toggle').forEach(function(btn) {
    btn.addEventListener('click', function() {
      btn.classList.toggle('open');
      btn.nextElementSibling.classList.toggle('visible');
    });
  });
  grid.querySelectorAll('.btn-edit').forEach(function(btn) {
    btn.addEventListener('click', function(e) {
      e.stopPropagation();
      openEditModal(btn.dataset.id);
    });
  });
  grid.querySelectorAll('.btn-del').forEach(function(btn) {
    btn.addEventListener('click', function(e) {
      e.stopPropagation();
      deleteAgent(btn.dataset.id, btn.dataset.name);
    });
  });
  if (mergeMode) {
    grid.querySelectorAll('.card').forEach(function(card) {
      card.style.cursor = 'pointer';
      card.addEventListener('click', function() {
        toggleMergeSelect(card.dataset.id);
      });
    });
    updateMergeSelected();
  }
}

function buildCard(a) {
  var color = typeColor(a.node_type);
  var stanceClass = {supportive:'stance-supportive',opposing:'stance-opposing',neutral:'stance-neutral',observer:'stance-observer'}[a.stance] || 'stance-neutral';
  var communityBadge = a.community_id >= 0
    ? '<span class="badge badge-community">C' + a.community_id + '</span>' : '';
  var noPersonaBadge = !a.has_personality
    ? '<span class="badge badge-no-persona">no persona</span>' : '';

  var actPct = clamp(a.activity_level * 100, 0, 100).toFixed(0);

  var sbias = clamp(a.sentiment_bias, -1, 1);
  var sbColor = sbias > 0.05 ? '#68d391' : (sbias < -0.05 ? '#fc8181' : '#a0aec0');
  var sbLeft, sbWidth;
  if (sbias >= 0) { sbLeft = '50%'; sbWidth = (sbias * 50).toFixed(1) + '%'; }
  else { sbWidth = (Math.abs(sbias) * 50).toFixed(1) + '%'; sbLeft = (50 + sbias * 50).toFixed(1) + '%'; }
  var sbLabel = sbias >= 0 ? '+' + fmt2(sbias) : fmt2(sbias);

  var bigFive = [
    {label:'Creativity',  val:a.creativity,  color:'#b794f4'},
    {label:'Rationality', val:a.rationality, color:'#63b3ed'},
    {label:'Empathy',     val:a.empathy,     color:'#68d391'},
    {label:'Extraversion',val:a.extraversion,color:'#f6ad55'},
    {label:'Openness',    val:a.openness,    color:'#76e4f7'}
  ];
  var bigFiveBars = bigFive.map(function(t) {
    var pct = clamp(t.val * 100, 0, 100).toFixed(0);
    return '<div class="bar-row">'
      + '<span class="bar-label">' + t.label.substring(0,6) + '</span>'
      + '<div class="bar-track"><div class="bar-fill" style="width:' + pct + '%;background:' + t.color + '"></div></div>'
      + '<span class="bar-value">' + fmt2(t.val) + '</span>'
      + '</div>';
  }).join('');

  var activeSet = {};
  (a.active_hours || []).forEach(function(h) { activeSet[h] = true; });
  var hourDots = '';
  for (var h = 0; h < 24; h++) {
    hourDots += '<div class="hour-dot' + (activeSet[h] ? ' active' : '') + '" title="' + h + ':00"></div>';
  }

  var subLine = [a.profession, a.location, a.timezone].filter(Boolean).join(' · ');
  var subHtml = subLine ? '<div class="card-sub">' + escHtml(subLine) + '</div>' : '';

  var usernameHtml = a.username ? '<div class="card-sub">@' + escHtml(a.username) + '</div>' : '';

  var interestChips = (a.interests || []).slice(0, 4).map(function(t) {
    return '<span class="chip">' + escHtml(t) + '</span>';
  }).join('');
  var interestsHtml = interestChips ? '<div class="section-title">Topics</div><div class="chips">' + interestChips + '</div>' : '';

  var cpChips = (a.catchphrases || []).slice(0, 3).map(function(t) {
    return '<span class="chip">&ldquo;' + escHtml(t) + '&rdquo;</span>';
  }).join('');
  var cpHtml = cpChips ? '<div class="section-title" style="margin-top:10px">Catchphrases</div><div class="chips">' + cpChips + '</div>' : '';

  var summaryHtml = a.summary ? '<div class="section-title" style="margin-top:10px">Bio</div><div class="summary-text">' + escHtml(a.summary) + '</div>' : '';

  var selectedClass = mergeSelected.indexOf(a.id) >= 0 ? ' selected' : '';

  return '<div class="card' + selectedClass + '" data-id="' + escHtml(a.id) + '">'
    + '<div class="card-actions">'
    +   '<button class="card-action-btn btn-edit" data-id="' + escHtml(a.id) + '">edit</button>'
    +   '<button class="card-action-btn del btn-del" data-id="' + escHtml(a.id) + '" data-name="' + escHtml(a.name) + '">&#10005;</button>'
    + '</div>'
    + '<div class="card-top">'
    +   '<div class="card-name" title="' + escHtml(a.name) + '">' + escHtml(a.name) + '</div>'
    +   usernameHtml
    +   subHtml
    +   '<div class="badge-row">'
    +     '<span class="badge" style="background:' + color + '22;color:' + color + ';border:1px solid ' + color + '44">' + escHtml(a.node_type) + '</span>'
    +     '<span class="badge ' + stanceClass + '">' + escHtml(a.stance) + '</span>'
    +     communityBadge
    +     noPersonaBadge
    +   '</div>'
    +   '<div class="influence-row"><span>&#9733; ' + fmt2(a.influence_weight) + '</span><span class="inf-label">influence</span></div>'
    + '</div>'
    + '<div class="card-middle">'
    +   '<div class="bar-row">'
    +     '<span class="bar-label">Activity</span>'
    +     '<div class="bar-track"><div class="bar-fill" style="width:' + actPct + '%;background:#63b3ed"></div></div>'
    +     '<span class="bar-value">' + fmt2(a.activity_level) + '</span>'
    +   '</div>'
    +   '<div class="bar-row">'
    +     '<span class="bar-label">Sentiment</span>'
    +     '<div class="sentiment-track"><div class="sentiment-center"></div><div class="sentiment-fill" style="left:' + sbLeft + ';width:' + sbWidth + ';background:' + sbColor + '"></div></div>'
    +     '<span class="bar-value" style="color:' + sbColor + '">' + sbLabel + '</span>'
    +   '</div>'
    +   '<div class="mini-stats">'
    +     '<div class="mini-stat"><span class="ms-val">' + fmt1(a.posts_per_hour) + '</span><span class="ms-key">posts/hr</span></div>'
    +     '<div class="mini-stat"><span class="ms-val">' + fmt1(a.comments_per_hour) + '</span><span class="ms-key">cmts/hr</span></div>'
    +     '<div class="mini-stat"><span class="ms-val">' + (a.active_hours || []).length + '</span><span class="ms-key">act hrs</span></div>'
    +   '</div>'
    + '</div>'
    + '<button class="card-toggle">Details <span class="arrow">&#9662;</span></button>'
    + '<div class="card-bottom">'
    +   '<div class="section-title">Personality</div>'
    +   bigFiveBars
    +   '<div class="section-title" style="margin-top:10px">Active Hours</div>'
    +   '<div class="hours-grid">' + hourDots + '</div>'
    +   interestsHtml
    +   cpHtml
    +   summaryHtml
    + '</div>'
    + '</div>';
}

// ── Delete ─────────────────────────────────────────────────────────────────
function deleteAgent(id, name) {
  if (!confirm('Delete agent "' + name + '"? This also removes all its graph edges.')) return;
  fetch('/api/agents/' + id, {method: 'DELETE'})
    .then(function() { loadAgents(); })
    .catch(function(e) { alert('Delete failed: ' + e); });
}

// ── Merge mode ─────────────────────────────────────────────────────────────
function toggleMergeSelect(id) {
  var idx = mergeSelected.indexOf(id);
  if (idx >= 0) {
    mergeSelected.splice(idx, 1);
  } else if (mergeSelected.length < 2) {
    mergeSelected.push(id);
  }
  updateMergeSelected();
  renderGrid();
}

function updateMergeSelected() {
  var bar = document.getElementById('merge-bar');
  var label = document.getElementById('merge-bar-label');
  var btn = document.getElementById('btn-do-merge');
  bar.classList.toggle('visible', mergeMode);
  if (mergeSelected.length === 0) label.textContent = 'Select 2 agents to merge';
  else if (mergeSelected.length === 1) label.textContent = '1 selected — pick another';
  else label.textContent = '2 selected — ready to merge';
  btn.disabled = mergeSelected.length !== 2;
}

document.getElementById('btn-merge-mode').addEventListener('click', function() {
  mergeMode = !mergeMode;
  mergeSelected = [];
  this.classList.toggle('active', mergeMode);
  updateMergeSelected();
  renderGrid();
});

document.getElementById('btn-cancel-merge').addEventListener('click', function() {
  mergeMode = false;
  mergeSelected = [];
  document.getElementById('btn-merge-mode').classList.remove('active');
  document.getElementById('merge-bar').classList.remove('visible');
  renderGrid();
});

document.getElementById('btn-do-merge').addEventListener('click', function() {
  if (mergeSelected.length !== 2) return;
  var a0 = allAgents.find(function(a) { return a.id === mergeSelected[0]; });
  var a1 = allAgents.find(function(a) { return a.id === mergeSelected[1]; });
  var msg = 'Keep "' + (a0 ? a0.name : mergeSelected[0]) + '" and merge '
    + '"' + (a1 ? a1.name : mergeSelected[1]) + '" into it?\n\n'
    + 'All edges from the second agent will move to the first.';
  if (!confirm(msg)) return;
  fetch('/api/agents/merge', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({keep_id: mergeSelected[0], drop_id: mergeSelected[1]})
  }).then(function() {
    mergeMode = false;
    mergeSelected = [];
    document.getElementById('btn-merge-mode').classList.remove('active');
    document.getElementById('merge-bar').classList.remove('visible');
    loadAgents();
  }).catch(function(e) { alert('Merge failed: ' + e); });
});

// ── Generate ───────────────────────────────────────────────────────────────
document.getElementById('btn-generate').addEventListener('click', function() {
  var scenario = prompt('Scenario for personality generation (e.g. "AI regulation debate"):');
  if (scenario === null) return;
  if (!scenario.trim()) scenario = 'general social media discussion';
  startGenerate(scenario);
});

function startGenerate(scenario) {
  var btn = document.getElementById('btn-generate');
  var statusBar = document.getElementById('gen-status');
  btn.disabled = true;
  statusBar.classList.add('visible');
  fetch('/api/agents/generate', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({scenario: scenario})
  }).then(function() {
    pollGenerate();
  }).catch(function(e) {
    btn.disabled = false;
    statusBar.classList.remove('visible');
    alert('Generate failed: ' + e);
  });
}

function pollGenerate() {
  fetch('/api/agents/generate')
    .then(function(r) { return r.json(); })
    .then(function(s) {
      if (s.error) {
        document.getElementById('btn-generate').disabled = false;
        document.getElementById('gen-status').classList.remove('visible');
        alert('Generation error: ' + s.error);
        return;
      }
      if (s.running) {
        setTimeout(pollGenerate, 1500);
      } else {
        document.getElementById('btn-generate').disabled = false;
        document.getElementById('gen-status').classList.remove('visible');
        document.getElementById('gen-status-text').textContent = 'Generating personalities\u2026';
        loadAgents();
      }
    });
}

// ── Create modal ────────────────────────────────────────────────────────────
document.getElementById('btn-new').addEventListener('click', function() {
  openCreateModal();
});

function openCreateModal() {
  editingID = null;
  document.getElementById('modal-title').textContent = 'New Agent';
  document.getElementById('modal-create-fields').style.display = 'block';
  document.getElementById('m-name').value = '';
  document.getElementById('m-summary').value = '';
  fillModalDefaults({});
  document.getElementById('modal-overlay').classList.add('visible');
}

function openEditModal(id) {
  var a = allAgents.find(function(x) { return x.id === id; });
  if (!a) return;
  editingID = id;
  document.getElementById('modal-title').textContent = 'Edit: ' + a.name;
  document.getElementById('modal-create-fields').style.display = 'none';
  fillModalDefaults(a);
  document.getElementById('modal-overlay').classList.add('visible');
}

function fillModalDefaults(a) {
  setSlider('m-sentiment', 'm-sentiment-val', a.sentiment_bias || 0, 2);
  setSlider('m-activity',  'm-activity-val',  a.activity_level  || 0.5, 2);
  setSlider('m-influence', 'm-influence-val', a.influence_weight || 1.0, 2);
  document.getElementById('m-posts').value    = (a.posts_per_hour    || 1.0).toFixed(1);
  document.getElementById('m-comments').value = (a.comments_per_hour || 2.0).toFixed(1);
  var sel = document.getElementById('m-stance');
  sel.value = a.stance || 'neutral';
  document.getElementById('m-profession').value  = a.profession  || '';
  document.getElementById('m-location').value    = a.location    || '';
  document.getElementById('m-username').value    = a.username    || '';
  document.getElementById('m-interests').value   = (a.interests   || []).join(', ');
  document.getElementById('m-catchphrases').value = (a.catchphrases || []).join(', ');
  buildBigFiveSliders(a);
}

function setSlider(id, valId, val, decimals) {
  var el = document.getElementById(id);
  el.value = val;
  document.getElementById(valId).textContent = Number(val).toFixed(decimals);
  el.oninput = function() { document.getElementById(valId).textContent = Number(this.value).toFixed(decimals); };
}

function buildBigFiveSliders(a) {
  var traits = [
    {key:'creativity',   label:'Creativity',   color:'#b794f4'},
    {key:'rationality',  label:'Rationality',  color:'#63b3ed'},
    {key:'empathy',      label:'Empathy',      color:'#68d391'},
    {key:'extraversion', label:'Extraversion', color:'#f6ad55'},
    {key:'openness',     label:'Openness',     color:'#76e4f7'},
  ];
  var html = traits.map(function(t) {
    var val = (a[t.key] || 0.5).toFixed(2);
    return '<div class="bar-row" style="margin-bottom:8px">'
      + '<span class="bar-label" style="color:' + t.color + '">' + t.label.substring(0,8) + '</span>'
      + '<input type="range" id="m-' + t.key + '" min="0" max="1" step="0.05" value="' + val + '" style="flex:1;accent-color:' + t.color + '"'
      + ' oninput="document.getElementById(\'m-' + t.key + '-val\').textContent=Number(this.value).toFixed(2)">'
      + '<span class="slider-val" id="m-' + t.key + '-val">' + val + '</span>'
      + '</div>';
  }).join('');
  document.getElementById('big-five-sliders').innerHTML = html;
}

function closeModal() {
  document.getElementById('modal-overlay').classList.remove('visible');
  editingID = null;
}

document.getElementById('modal-close').addEventListener('click', closeModal);
document.getElementById('modal-btn-cancel').addEventListener('click', closeModal);
document.getElementById('modal-overlay').addEventListener('click', function(e) {
  if (e.target === this) closeModal();
});

document.getElementById('modal-btn-save').addEventListener('click', function() {
  var traits = ['creativity','rationality','empathy','extraversion','openness'];
  var splitComma = function(s) {
    return s.split(',').map(function(x) { return x.trim(); }).filter(Boolean);
  };
  var payload = {
    stance:           document.getElementById('m-stance').value,
    sentiment_bias:   parseFloat(document.getElementById('m-sentiment').value),
    activity_level:   parseFloat(document.getElementById('m-activity').value),
    influence_weight: parseFloat(document.getElementById('m-influence').value),
    posts_per_hour:   parseFloat(document.getElementById('m-posts').value),
    comments_per_hour:parseFloat(document.getElementById('m-comments').value),
    profession:       document.getElementById('m-profession').value,
    location:         document.getElementById('m-location').value,
    username:         document.getElementById('m-username').value,
    interests:        splitComma(document.getElementById('m-interests').value),
    catchphrases:     splitComma(document.getElementById('m-catchphrases').value),
  };
  traits.forEach(function(k) {
    var el = document.getElementById('m-' + k);
    if (el) payload[k] = parseFloat(el.value);
  });

  if (editingID) {
    // Update existing agent
    fetch('/api/agents/' + editingID, {
      method: 'PUT',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(payload)
    }).then(function() { closeModal(); loadAgents(); })
      .catch(function(e) { alert('Save failed: ' + e); });
  } else {
    // Create new agent
    var name = document.getElementById('m-name').value.trim();
    if (!name) { alert('Name is required'); return; }
    var createPayload = {
      name: name,
      node_type: document.getElementById('m-type').value,
      summary: document.getElementById('m-summary').value,
    };
    fetch('/api/agents', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(createPayload)
    }).then(function(r) { return r.json(); })
      .then(function(agent) {
        // Then update personality fields
        return fetch('/api/agents/' + agent.id, {
          method: 'PUT',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify(payload)
        });
      })
      .then(function() { closeModal(); loadAgents(); })
      .catch(function(e) { alert('Create failed: ' + e); });
  }
});

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

// ── Boot ───────────────────────────────────────────────────────────────────
loadAgents();
</script>
</body>
</html>`
