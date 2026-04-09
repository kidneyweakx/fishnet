package viz

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>fishnet — graph</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { background: #0f1117; color: #e2e8f0; font-family: 'SF Mono', monospace; overflow: hidden; }
  #canvas { width: 100vw; height: 100vh; }
  #panel {
    position: fixed; right: 0; top: 0; width: 300px; height: 100vh;
    background: #1a1d2e; border-left: 1px solid #2d3748; padding: 20px;
    overflow-y: auto; z-index: 10;
  }
  #panel h2 { font-size: 14px; color: #63b3ed; margin-bottom: 12px; letter-spacing: 0.05em; text-transform: uppercase; }
  #stats { font-size: 12px; color: #a0aec0; line-height: 1.8; }
  #detail { display: none; margin-top: 20px; padding-top: 20px; border-top: 1px solid #2d3748; }
  #detail h3 { font-size: 13px; color: #f6e05e; margin-bottom: 8px; }
  #detail p { font-size: 12px; color: #a0aec0; line-height: 1.6; }
  #detail .tag {
    display: inline-block; padding: 2px 8px; border-radius: 12px;
    font-size: 11px; margin: 4px 4px 0 0; background: #2d3748; color: #63b3ed;
  }
  #search {
    width: 100%; background: #0f1117; border: 1px solid #2d3748; color: #e2e8f0;
    padding: 8px 12px; border-radius: 6px; font-size: 12px; margin-bottom: 16px;
  }
  #search:focus { outline: none; border-color: #63b3ed; }
  .legend-item { display: flex; align-items: center; gap: 8px; font-size: 11px; color: #a0aec0; margin-bottom: 6px; }
  .legend-dot { width: 10px; height: 10px; border-radius: 50%; flex-shrink: 0; }
  .tooltip {
    position: fixed; background: #1a1d2e; border: 1px solid #2d3748;
    padding: 8px 12px; border-radius: 6px; font-size: 11px; color: #e2e8f0;
    pointer-events: none; opacity: 0; transition: opacity 0.15s; max-width: 200px;
  }
  svg text { pointer-events: none; }
</style>
</head>
<body>
<svg id="canvas"></svg>
<div id="panel">
  <h2>fishnet</h2>
  <input id="search" type="text" placeholder="Search nodes..." />
  <div id="stats">Loading...</div>
  <div id="detail">
    <h3 id="d-name"></h3>
    <p><span id="d-type" class="tag"></span></p>
    <p id="d-summary" style="margin-top:8px;"></p>
    <p id="d-community" style="margin-top:8px; color:#68d391;"></p>
  </div>
  <div id="legend" style="margin-top:20px; padding-top:20px; border-top:1px solid #2d3748;">
    <h2>Legend</h2>
    <div id="legend-items" style="margin-top:10px;"></div>
  </div>
</div>
<div class="tooltip" id="tooltip"></div>

<script src="https://cdnjs.cloudflare.com/ajax/libs/d3/7.8.5/d3.min.js"></script>
<script>
const colors = d3.scaleOrdinal([
  '#63b3ed','#68d391','#f6e05e','#fc8181','#b794f4',
  '#f6ad55','#76e4f7','#fbb6ce','#9ae6b4','#fbd38d'
]);

const width = window.innerWidth - 300;
const height = window.innerHeight;

const svg = d3.select('#canvas')
  .attr('width', width).attr('height', height);

const g = svg.append('g');

const zoom = d3.zoom()
  .scaleExtent([0.1, 4])
  .on('zoom', e => g.attr('transform', e.transform));
svg.call(zoom);

fetch('/api/graph').then(r => r.json()).then(data => {
  const nodes = data.nodes || [];
  const edges = data.edges || [];

  // Stats
  const types = [...new Set(nodes.map(n => n.type))];
  const comms = [...new Set(nodes.map(n => n.community).filter(c => c >= 0))];
  document.getElementById('stats').innerHTML =
    '<b>' + nodes.length + '</b> nodes &nbsp; <b>' + edges.length + '</b> edges &nbsp; <b>' + comms.length + '</b> communities';

  // Legend
  const legendEl = document.getElementById('legend-items');
  types.forEach(t => {
    legendEl.innerHTML += '<div class="legend-item"><div class="legend-dot" style="background:'+colors(t)+'"></div>' + t + '</div>';
  });

  // Build index
  const nodeById = {};
  nodes.forEach(n => nodeById[n.id] = n);

  // Links
  const link = g.append('g').selectAll('line')
    .data(edges).join('line')
    .attr('stroke', '#2d3748')
    .attr('stroke-width', d => Math.min(d.weight, 3))
    .attr('stroke-opacity', 0.6);

  // Link labels
  const linkLabel = g.append('g').selectAll('text')
    .data(edges).join('text')
    .attr('font-size', 8)
    .attr('fill', '#4a5568')
    .attr('text-anchor', 'middle')
    .text(d => d.type);

  // Nodes
  const node = g.append('g').selectAll('g')
    .data(nodes).join('g')
    .attr('cursor', 'pointer')
    .call(d3.drag()
      .on('start', dragstart)
      .on('drag', dragging)
      .on('end', dragend));

  node.append('circle')
    .attr('r', d => 6 + Math.min(degree(d.id, edges) * 1.5, 20))
    .attr('fill', d => colors(d.type))
    .attr('stroke', '#0f1117')
    .attr('stroke-width', 2)
    .attr('fill-opacity', 0.85);

  node.append('text')
    .attr('dy', d => -(8 + Math.min(degree(d.id, edges) * 1.5, 20)))
    .attr('text-anchor', 'middle')
    .attr('font-size', 10)
    .attr('fill', '#e2e8f0')
    .text(d => d.name.length > 20 ? d.name.slice(0, 20) + '…' : d.name);

  // Tooltip & detail
  const tooltip = document.getElementById('tooltip');
  node.on('mouseover', (e, d) => {
    tooltip.style.opacity = 1;
    tooltip.style.left = (e.clientX + 12) + 'px';
    tooltip.style.top = (e.clientY - 20) + 'px';
    tooltip.textContent = d.name + ' · ' + d.type;
  }).on('mouseout', () => {
    tooltip.style.opacity = 0;
  }).on('click', (e, d) => {
    const detail = document.getElementById('detail');
    detail.style.display = 'block';
    document.getElementById('d-name').textContent = d.name;
    document.getElementById('d-type').textContent = d.type;
    document.getElementById('d-summary').textContent = d.summary || '—';
    document.getElementById('d-community').textContent = d.community >= 0 ? 'Community ' + d.community : '';
  });

  // Simulation
  const sim = d3.forceSimulation(nodes)
    .force('link', d3.forceLink(edges)
      .id(d => d.id)
      .distance(80)
      .strength(0.5))
    .force('charge', d3.forceManyBody().strength(-200))
    .force('center', d3.forceCenter(width / 2, height / 2))
    .force('collision', d3.forceCollide(30))
    .on('tick', () => {
      link
        .attr('x1', d => d.source.x).attr('y1', d => d.source.y)
        .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
      linkLabel
        .attr('x', d => (d.source.x + d.target.x) / 2)
        .attr('y', d => (d.source.y + d.target.y) / 2);
      node.attr('transform', d => 'translate(' + d.x + ',' + d.y + ')');
    });

  // Search
  document.getElementById('search').addEventListener('input', function() {
    const q = this.value.toLowerCase();
    node.selectAll('circle').attr('fill-opacity', d =>
      q === '' || d.name.toLowerCase().includes(q) || d.type.toLowerCase().includes(q) ? 0.85 : 0.1
    );
    node.selectAll('text').attr('fill-opacity', d =>
      q === '' || d.name.toLowerCase().includes(q) ? 1 : 0.15
    );
  });

  function degree(id, edges) {
    return edges.filter(e => {
      const src = typeof e.source === 'object' ? e.source.id : e.source;
      const tgt = typeof e.target === 'object' ? e.target.id : e.target;
      return src === id || tgt === id;
    }).length;
  }

  function dragstart(e, d) {
    if (!e.active) sim.alphaTarget(0.3).restart();
    d.fx = d.x; d.fy = d.y;
  }
  function dragging(e, d) { d.fx = e.x; d.fy = e.y; }
  function dragend(e, d) {
    if (!e.active) sim.alphaTarget(0);
    d.fx = null; d.fy = null;
  }
});
</script>
</body>
</html>`
