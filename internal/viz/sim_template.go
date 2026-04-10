package viz

const simTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>fishnet &#8212; Simulation</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
body {
  background: #0f1117;
  color: #e2e8f0;
  font-family: 'SF Mono', 'Fira Code', monospace;
  min-height: 100vh;
}

/* ── Header ──────────────────────────────────────────────────────────── */
#header {
  position: sticky; top: 0; z-index: 100;
  background: #0f1117; border-bottom: 1px solid #2d3748;
  padding: 14px 24px;
  display: flex; align-items: center; gap: 16px;
}
#header h1 { font-size: 15px; font-weight: 600; color: #63b3ed; letter-spacing: 0.04em; flex: 1; }
#header h1 span { color: #4a5568; font-weight: 400; }
.nav-link {
  font-size: 12px; color: #63b3ed; text-decoration: none;
  padding: 5px 12px; border: 1px solid #2a4365; border-radius: 6px;
  transition: background 0.15s;
}
.nav-link:hover { background: #2a4365; }
.nav-current {
  font-size: 12px; color: #68d391; padding: 5px 12px;
  border: 1px solid #276749; border-radius: 6px; background: #1c4532;
}

/* ── Layout ──────────────────────────────────────────────────────────── */
#layout {
  display: grid;
  grid-template-columns: 380px 1fr;
  gap: 20px;
  padding: 20px 24px;
  max-width: 1280px;
  margin: 0 auto;
}
@media (max-width: 860px) {
  #layout { grid-template-columns: 1fr; }
}

/* ── Config Panel ────────────────────────────────────────────────────── */
.config-card {
  background: #1a1d27; border: 1px solid #2d3748; border-radius: 10px;
  padding: 20px; display: flex; flex-direction: column; gap: 18px;
  align-self: start;
}
.config-card h2 { font-size: 11px; color: #718096; text-transform: uppercase; letter-spacing: 0.08em; }

#scenario {
  width: 100%; background: #0f1117; border: 1px solid #2d3748;
  color: #e2e8f0; font-family: inherit; font-size: 13px;
  padding: 10px 12px; border-radius: 6px; resize: vertical;
  min-height: 80px; margin-top: 8px;
}
#scenario:focus { outline: none; border-color: #63b3ed; }
#scenario::placeholder { color: #4a5568; }

.config-row { display: flex; flex-direction: column; gap: 8px; }
.config-label {
  font-size: 11px; color: #718096;
  text-transform: uppercase; letter-spacing: 0.06em;
  display: flex; justify-content: space-between; align-items: center;
}
.config-label span { color: #e2e8f0; font-size: 12px; text-transform: none; letter-spacing: 0; }

input[type=range] {
  -webkit-appearance: none; width: 100%; height: 4px;
  background: #2d3748; border-radius: 2px; outline: none;
}
input[type=range]::-webkit-slider-thumb {
  -webkit-appearance: none; width: 14px; height: 14px;
  border-radius: 50%; background: #63b3ed; cursor: pointer;
}

/* Mode pills */
.mode-pills { display: flex; gap: 6px; flex-wrap: wrap; }
.mode-pill {
  flex: 1; min-width: 80px;
  background: #0f1117; border: 1px solid #2d3748; color: #a0aec0;
  font-size: 11px; font-family: inherit; padding: 7px 10px;
  border-radius: 6px; cursor: pointer; text-align: center;
  transition: all 0.15s; line-height: 1.4;
}
.mode-pill:hover { border-color: #4a5568; color: #e2e8f0; }
.mode-pill.active { background: #1a365d; border-color: #3182ce; color: #90cdf4; }
.mode-pill strong { display: block; font-size: 12px; margin-bottom: 2px; }
.mode-pill small { color: #4a5568; font-size: 10px; }
.mode-pill.active small { color: #63b3ed; }

/* Platform checkboxes */
.plat-row { display: flex; gap: 10px; }
.plat-check {
  flex: 1; display: flex; align-items: center; gap: 8px;
  background: #0f1117; border: 1px solid #2d3748; border-radius: 6px;
  padding: 8px 12px; cursor: pointer; transition: border-color 0.15s;
}
.plat-check input { accent-color: #63b3ed; cursor: pointer; }
.plat-check label { font-size: 12px; color: #a0aec0; cursor: pointer; }
.plat-check:has(input:checked) { border-color: #3182ce; }
.plat-check:has(input:checked) label { color: #90cdf4; }

/* Agents input */
#agents-input {
  width: 100%; background: #0f1117; border: 1px solid #2d3748;
  color: #e2e8f0; font-family: inherit; font-size: 13px;
  padding: 8px 12px; border-radius: 6px;
}
#agents-input:focus { outline: none; border-color: #63b3ed; }

#run-btn {
  width: 100%; background: #276749; border: 1px solid #38a169;
  color: #f0fff4; font-family: inherit; font-size: 13px; font-weight: 600;
  padding: 12px; border-radius: 8px; cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
  letter-spacing: 0.04em;
}
#run-btn:hover:not(:disabled) { background: #2f855a; }
#run-btn:disabled { opacity: 0.4; cursor: not-allowed; }

#config-error {
  font-size: 12px; color: #fc8181;
  background: #3d1515; border: 1px solid #742a2a;
  border-radius: 6px; padding: 8px 12px; display: none;
}

/* ── Right Panel ─────────────────────────────────────────────────────── */
#right-panel { display: flex; flex-direction: column; gap: 16px; }

/* ── Idle state ──────────────────────────────────────────────────────── */
#idle-hint {
  background: #1a1d27; border: 1px solid #2d3748; border-radius: 10px;
  padding: 40px 24px; text-align: center; color: #4a5568;
  font-size: 13px; line-height: 2;
}
#idle-hint strong { display: block; font-size: 15px; color: #718096; margin-bottom: 8px; }

/* ── Progress ────────────────────────────────────────────────────────── */
#progress-panel {
  background: #1a1d27; border: 1px solid #2d3748; border-radius: 10px;
  padding: 20px; display: none;
}
.prog-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
.prog-round { font-size: 13px; color: #63b3ed; font-weight: 600; }
.prog-mode-badge {
  font-size: 10px; padding: 2px 8px; border-radius: 12px;
  background: #2a4365; border: 1px solid #3182ce; color: #63b3ed;
  text-transform: uppercase; letter-spacing: 0.06em;
}
.prog-bar-track {
  height: 4px; background: #2d3748; border-radius: 2px;
  overflow: hidden; margin-bottom: 14px;
}
.prog-bar-fill {
  height: 100%; background: linear-gradient(90deg, #3182ce, #63b3ed);
  border-radius: 2px; width: 0%; transition: width 0.3s ease;
}
.stats-row {
  display: flex; gap: 12px; margin-bottom: 14px;
}
.stat-chip {
  flex: 1; background: #0f1117; border: 1px solid #2d3748; border-radius: 6px;
  padding: 8px 12px; text-align: center;
}
.stat-chip .sc-val { font-size: 18px; font-weight: 700; color: #e2e8f0; }
.stat-chip .sc-key { font-size: 10px; color: #4a5568; text-transform: uppercase; letter-spacing: 0.05em; margin-top: 2px; }

#log-feed {
  background: #0f1117; border: 1px solid #1e2230; border-radius: 6px;
  padding: 10px; font-size: 11px; color: #718096; line-height: 1.8;
  max-height: 260px; overflow-y: auto;
}
#log-feed:empty::before { content: "Waiting for simulation events..."; color: #4a5568; }
.log-line { margin-bottom: 2px; }
.log-line.info  { color: #63b3ed; }
.log-line.round { color: #68d391; font-weight: 600; border-top: 1px solid #1e2230; padding-top: 4px; margin-top: 4px; }
.log-line.error { color: #fc8181; }

/* ── Results ─────────────────────────────────────────────────────────── */
#results-panel { display: none; flex-direction: column; gap: 16px; }

.result-card {
  background: #1a1d27; border: 1px solid #2d3748; border-radius: 10px;
  padding: 20px;
}
.result-card h3 { font-size: 11px; color: #718096; text-transform: uppercase; letter-spacing: 0.08em; margin-bottom: 14px; }

.metrics-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }
.metric-chip {
  background: #0f1117; border: 1px solid #2d3748; border-radius: 8px;
  padding: 12px; text-align: center;
}
.metric-chip .mc-val { font-size: 22px; font-weight: 700; color: #e2e8f0; }
.metric-chip .mc-key { font-size: 10px; color: #4a5568; text-transform: uppercase; letter-spacing: 0.05em; margin-top: 4px; }
.metric-chip.highlight { border-color: #3182ce; }
.metric-chip.highlight .mc-val { color: #63b3ed; }

.quote-list { display: flex; flex-direction: column; gap: 10px; }
.quote-item {
  background: #0f1117; border: 1px solid #1e2230; border-radius: 8px;
  padding: 12px; border-left: 3px solid #3182ce;
}
.quote-meta {
  font-size: 10px; color: #4a5568; margin-bottom: 6px;
  text-transform: uppercase; letter-spacing: 0.04em;
}
.quote-meta strong { color: #63b3ed; }
.quote-content { font-size: 12px; color: #a0aec0; line-height: 1.6; }
.quote-reactions { font-size: 10px; color: #68d391; margin-top: 4px; }

.action-row { display: flex; gap: 10px; flex-wrap: wrap; }
.dl-btn {
  background: #1a365d; border: 1px solid #2a4365; color: #63b3ed;
  font-size: 12px; font-family: inherit; padding: 8px 16px;
  border-radius: 6px; cursor: pointer; text-decoration: none;
  transition: background 0.15s;
}
.dl-btn:hover { background: #2a4365; }

#run-again-btn {
  background: #1c4532; border: 1px solid #276749; color: #68d391;
  font-size: 12px; font-family: inherit; padding: 8px 16px;
  border-radius: 6px; cursor: pointer; transition: background 0.15s;
}
#run-again-btn:hover { background: #22543d; }
</style>
</head>
<body>

<div id="header">
  <h1>fishnet <span>&#8212;</span> Simulation</h1>
  <a href="/" class="nav-link">Graph</a>
  <a href="/step2" class="nav-link">Agents</a>
  <span class="nav-current">Simulation</span>
</div>

<div id="layout">

  <!-- ── Config ── -->
  <div class="config-card" id="config-panel">
    <div>
      <h2>Scenario</h2>
      <textarea id="scenario" rows="4" placeholder="Describe the social media scenario to simulate&#8230;"></textarea>
    </div>

    <div class="config-row">
      <div class="config-label">Rounds <span id="rounds-val">10</span></div>
      <input type="range" id="rounds" min="1" max="50" value="10">
    </div>

    <div class="config-row">
      <div class="config-label">Mode</div>
      <div class="mode-pills">
        <button class="mode-pill" data-mode="nollm">
          <strong>NoLLM</strong>
          <small>0 tokens</small>
        </button>
        <button class="mode-pill active" data-mode="batch">
          <strong>Batch</strong>
          <small>1 call/round</small>
        </button>
        <button class="mode-pill" data-mode="heavy">
          <strong>Heavy</strong>
          <small>1 call/agent</small>
        </button>
      </div>
    </div>

    <div class="config-row">
      <div class="config-label">Platforms</div>
      <div class="plat-row">
        <div class="plat-check">
          <input type="checkbox" id="plat-twitter" checked>
          <label for="plat-twitter">Twitter</label>
        </div>
        <div class="plat-check">
          <input type="checkbox" id="plat-reddit" checked>
          <label for="plat-reddit">Reddit</label>
        </div>
      </div>
    </div>

    <div class="config-row">
      <div class="config-label">Max Agents <small style="color:#4a5568;text-transform:none">(0 = all)</small></div>
      <input type="number" id="agents-input" value="0" min="0" max="500">
    </div>

    <div id="config-error"></div>
    <button id="run-btn">&#9654; Run Simulation</button>
  </div>

  <!-- ── Right Panel ── -->
  <div id="right-panel">

    <div id="idle-hint">
      <strong>Ready to simulate</strong>
      Configure a scenario and click Run.<br>
      Progress streams live via SSE &#x2192; results appear when complete.
    </div>

    <!-- Progress -->
    <div id="progress-panel">
      <div class="prog-header">
        <span class="prog-round" id="round-label">Starting&#8230;</span>
        <span class="prog-mode-badge" id="mode-badge">batch</span>
      </div>
      <div class="prog-bar-track"><div class="prog-bar-fill" id="prog-fill"></div></div>
      <div class="stats-row">
        <div class="stat-chip">
          <div class="sc-val" id="tw-posts">0</div>
          <div class="sc-key">Twitter posts</div>
        </div>
        <div class="stat-chip">
          <div class="sc-val" id="rd-posts">0</div>
          <div class="sc-key">Reddit posts</div>
        </div>
        <div class="stat-chip">
          <div class="sc-val" id="total-actions">0</div>
          <div class="sc-key">Total actions</div>
        </div>
      </div>
      <div id="log-feed"></div>
    </div>

    <!-- Results -->
    <div id="results-panel">
      <div class="result-card" id="metrics-card">
        <h3>Analytics</h3>
        <div class="metrics-grid" id="metrics-grid"></div>
      </div>

      <div class="result-card" id="quotes-card">
        <h3>Top Posts</h3>
        <div class="quote-list" id="quotes-list"></div>
      </div>

      <div class="result-card">
        <h3>Export</h3>
        <div class="action-row">
          <a id="dl-export" href="/api/sim/result" download="export.json" class="dl-btn">&#8595; export.json</a>
          <button id="run-again-btn">&#8635; Run Again</button>
        </div>
      </div>
    </div>

  </div><!-- /#right-panel -->
</div><!-- /#layout -->

<script>
// ── State ──────────────────────────────────────────────────────────────────────
var selectedMode = 'batch';
var actionCount  = 0;
var es           = null;

// ── Mode pills ─────────────────────────────────────────────────────────────────
document.querySelectorAll('.mode-pill').forEach(function(btn) {
  btn.addEventListener('click', function() {
    document.querySelectorAll('.mode-pill').forEach(function(b) { b.classList.remove('active'); });
    btn.classList.add('active');
    selectedMode = btn.dataset.mode;
  });
});

// ── Rounds slider ──────────────────────────────────────────────────────────────
document.getElementById('rounds').addEventListener('input', function() {
  document.getElementById('rounds-val').textContent = this.value;
});

// ── Run button ─────────────────────────────────────────────────────────────────
document.getElementById('run-btn').addEventListener('click', function() {
  var scenario = document.getElementById('scenario').value.trim();
  if (!scenario) { showError('Scenario is required.'); return; }

  var platforms = [];
  if (document.getElementById('plat-twitter').checked) platforms.push('twitter');
  if (document.getElementById('plat-reddit').checked) platforms.push('reddit');
  if (platforms.length === 0) { showError('Select at least one platform.'); return; }

  var body = {
    scenario:  scenario,
    rounds:    parseInt(document.getElementById('rounds').value),
    mode:      selectedMode,
    agents:    parseInt(document.getElementById('agents-input').value) || 0,
    platforms: platforms
  };

  hideError();
  document.getElementById('run-btn').disabled = true;

  fetch('/api/sim/run', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(body)
  })
  .then(function(resp) {
    if (!resp.ok) {
      return resp.text().then(function(t) { throw new Error(t); });
    }
    startProgress(body.mode, body.rounds);
  })
  .catch(function(err) {
    document.getElementById('run-btn').disabled = false;
    showError(String(err));
  });
});

// ── Progress streaming ─────────────────────────────────────────────────────────
function startProgress(mode, maxRounds) {
  actionCount = 0;
  document.getElementById('idle-hint').style.display    = 'none';
  document.getElementById('results-panel').style.display = 'none';
  document.getElementById('progress-panel').style.display = 'block';
  document.getElementById('mode-badge').textContent = mode;
  document.getElementById('tw-posts').textContent  = '0';
  document.getElementById('rd-posts').textContent  = '0';
  document.getElementById('total-actions').textContent = '0';
  document.getElementById('log-feed').innerHTML = '';

  if (es) { es.close(); }
  es = new EventSource('/api/sim/progress');

  es.onmessage = function(e) {
    var ev;
    try { ev = JSON.parse(e.data); } catch(_) { return; }

    if (ev.type === 'round') {
      var pct = maxRounds > 0 ? (ev.round / ev.max_rounds * 100).toFixed(1) : 0;
      document.getElementById('round-label').textContent = 'Round ' + ev.round + ' / ' + ev.max_rounds;
      document.getElementById('prog-fill').style.width = pct + '%';
      appendLog('round', 'Round ' + ev.round + ' complete  ' + pct + '%');
      if (ev.tw_posts  !== undefined) document.getElementById('tw-posts').textContent  = ev.tw_posts;
      if (ev.rd_posts  !== undefined) document.getElementById('rd-posts').textContent  = ev.rd_posts;
    }

    if (ev.type === 'action') {
      actionCount++;
      document.getElementById('total-actions').textContent = actionCount;
      if (ev.log) appendLog('info', ev.log);
    }

    if (ev.type === 'log' && ev.log) {
      appendLog('info', ev.log);
    }

    if (ev.type === 'done') {
      es.close(); es = null;
      document.getElementById('round-label').textContent = 'Complete';
      document.getElementById('prog-fill').style.width = '100%';
      appendLog('round', 'Simulation complete.');
      loadResults();
    }

    if (ev.type === 'error') {
      es.close(); es = null;
      appendLog('error', 'Error: ' + ev.error);
      document.getElementById('run-btn').disabled = false;
    }
  };

  es.onerror = function() {
    if (es) { es.close(); es = null; }
  };
}

function appendLog(cls, text) {
  var feed = document.getElementById('log-feed');
  var line = document.createElement('div');
  line.className = 'log-line ' + cls;
  line.textContent = text;
  feed.appendChild(line);
  feed.scrollTop = feed.scrollHeight;
  // Keep at most 200 lines
  while (feed.children.length > 200) { feed.removeChild(feed.firstChild); }
}

// ── Results ────────────────────────────────────────────────────────────────────
function loadResults() {
  fetch('/api/sim/result')
    .then(function(r) { return r.json(); })
    .then(function(doc) { renderResults(doc); })
    .catch(function(err) { appendLog('error', 'Could not load results: ' + err); });
}

function renderResults(doc) {
  document.getElementById('progress-panel').style.display = 'none';
  document.getElementById('results-panel').style.display  = 'flex';
  document.getElementById('run-btn').disabled = false;

  // Metrics grid
  var grid = document.getElementById('metrics-grid');
  grid.innerHTML = '';
  var m = doc.sim_metrics;
  if (m) {
    addMetric(grid, (m.branching_factor || 0).toFixed(2), 'R\u2080 (spread)', m.branching_factor >= 1.0);
    addMetric(grid, (m.echo_chamber_final || 0).toFixed(2), 'Echo chamber', m.echo_chamber_final >= 0.5);
    addMetric(grid, m.total_posts  || 0, 'Total posts', false);
    addMetric(grid, m.total_reposts || 0, 'Reposts', false);
    addMetric(grid, m.total_likes  || 0, 'Likes', false);
    var kmax = (m.k_core_reach || []).length;
    addMetric(grid, 'k=' + kmax, 'K-core depth', kmax >= 3);
  }

  // Top quotes
  var list = document.getElementById('quotes-list');
  list.innerHTML = '';
  var quotes = (doc.top_quotes || []).slice(0, 5);
  if (quotes.length === 0) {
    list.innerHTML = '<div style="color:#4a5568;font-size:12px">No posts recorded.</div>';
  }
  quotes.forEach(function(q) {
    var el = document.createElement('div');
    el.className = 'quote-item';
    el.innerHTML =
      '<div class="quote-meta"><strong>' + esc(q.agent) + '</strong> on ' + esc(q.platform) + '  &middot;  round ' + (q.round||'?') + '</div>' +
      '<div class="quote-content">' + esc(q.content || '') + '</div>' +
      '<div class="quote-reactions">+' + (q.reactions||0) + ' reactions</div>';
    list.appendChild(el);
  });
}

function addMetric(grid, val, key, highlight) {
  var el = document.createElement('div');
  el.className = 'metric-chip' + (highlight ? ' highlight' : '');
  el.innerHTML = '<div class="mc-val">' + val + '</div><div class="mc-key">' + key + '</div>';
  grid.appendChild(el);
}

function esc(s) {
  return String(s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}

// ── Run again ──────────────────────────────────────────────────────────────────
document.getElementById('run-again-btn').addEventListener('click', function() {
  document.getElementById('results-panel').style.display = 'none';
  document.getElementById('idle-hint').style.display     = 'block';
});

// ── Helpers ────────────────────────────────────────────────────────────────────
function showError(msg) {
  var el = document.getElementById('config-error');
  el.textContent = msg; el.style.display = 'block';
}
function hideError() {
  document.getElementById('config-error').style.display = 'none';
}

// Prevent Esc from blurring inputs (some browsers blur focused element on Esc)
document.addEventListener('keydown', function(e) {
  if (e.key === 'Escape') {
    var active = document.activeElement;
    if (active && (active.tagName === 'TEXTAREA' || active.tagName === 'INPUT')) {
      e.preventDefault();
      // Keep focus on the element
      active.focus();
    }
  }
});
</script>
</body>
</html>`
