import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

// The statistics panel is rendered by functions living inside mount(); this
// harness extracts the stats block and executes it against a minimal DOM
// shim. It caught a real TypeError where svgNode('title') was called without
// an attribute object, killing the whole render.
const src = fs.readFileSync(new URL('../../internal/console/web/console.js', import.meta.url), 'utf8');
const start = src.indexOf('// —— 用量统计区块 ——');
const end = src.indexOf("  $('auth-form')");
if (start < 0 || end < 0) throw new Error('stats block markers not found');

const fake = () => {
  const el = {
    children: [], attrs: {}, style: { setProperty(k, v) { this[k] = v; } }, dataset: {}, className: '', hidden: false, disabled: false,
    _handlers: {}, _text: undefined,
  };
  Object.defineProperty(el, 'textContent', {
    get() { return el._text !== undefined ? el._text : el.children.map(c => typeof c === 'string' ? c : (c.textContent ?? '')).join(''); },
    set(v) { el._text = String(v); el.children = []; },
  });
  const classes = new Set();
  el.classList = {
    add: c => classes.add(c),
    remove: c => classes.delete(c),
    toggle: (c, on) => { const v = on === undefined ? !classes.has(c) : !!on; if (v) classes.add(c); else classes.delete(c); return v; },
    contains: c => classes.has(c),
  };
  el.append = (...xs) => { el.children.push(...xs); };
  el.replaceChildren = (...xs) => { el.children = xs; };
  el.setAttribute = (k, v) => { el.attrs[k] = String(v); if (k === 'class') el.className = String(v); };
  el.getAttribute = k => el.attrs[k] ?? null;
  el.addEventListener = (t, fn) => { (el._handlers[t] = el._handlers[t] || []).push(fn); };
  el.dispatch = (t, ev) => { for (const fn of el._handlers[t] || []) fn(ev); };
  el.querySelectorAll = sel => {
    if (sel !== '[data-hl-key]') return [];
    const out = [];
    const walk = n => { for (const c of n.children || []) { if (c.attrs && c.attrs['data-hl-key'] !== undefined) out.push(c); walk(c); } };
    walk(el);
    return out;
  };
  return el;
};

function render(payload, accounts = []) {
  const byId = new Map();
  const $ = id => { if (!byId.has(id)) byId.set(id, fake()); return byId.get(id); };
  const node = (tag, text, cls) => { const el = fake(); if (text !== undefined) el.textContent = String(text); if (cls) el.className = cls; return el; };
  const renderText = (el, v) => { el.textContent = String(v ?? ''); };
  let block = src.slice(start, end);
  block += '\nstatsPayload = TEST_PAYLOAD;\nrenderStats();\n';
  const fn = new Function('$', 'node', 'renderText', 'message', 'when', 'quota', 'api', 'document', 'snapshot', 'TEST_PAYLOAD', block);
  const shim = {
    createElementNS: () => fake(), createElement: () => fake(), querySelectorAll: () => [], addEventListener() {},
    getElementById: id => $(id),
  };
  fn($, node, renderText, k => 'msg:' + String(k), v => 'when:' + String(v), n => 'quota:' + String(n ?? 0),
    { request: () => Promise.resolve({}) }, shim, { accounts }, payload);
  return { byId, el: $ };
}

const series = [];
const day = 1794816000;
for (let i = 0; i < 7; i++) {
  series.push({ start: day + i * 86400, tokens: [{ model: 'gpt-6.1-sol', tokens: 1000000 + i * 1000 }, { model: 'gpt-6-astra', tokens: 500000 }] });
}
const stats = { range: '7d', start: day, end: day + 7 * 86400, synced_at: '2026-11-16T09:50:00Z', truncated: false, partial_cost: false,
  totals: { calls: 6181, prompt_tokens: 1090000000, completion_tokens: 4200000, total_tokens: 1094201953, cache_tokens: 1063386432, quota: 500000000, cost_usd: 584.33 },
  accounts: [{ account_id: 'a'.repeat(64), label: 'HappyCoding', site: 'https://happycoding.xyz', calls: 3531, prompt_tokens: 680000000, completion_tokens: 1322350, total_tokens: 681322350, cache_tokens: 668124928, quota: 7025033, per_unit: 500000, cost_usd: 14.05 }],
  models: [
    { model: 'deepseek-v4.1-flash', calls: 3531, prompt_tokens: 680000000, completion_tokens: 1322350, total_tokens: 681322350, cache_tokens: 668124928, quota: 7025033, cost_usd: 14.05 },
    { model: 'gpt-6.1-sol', calls: 1200, prompt_tokens: 300000000, completion_tokens: 1000000, total_tokens: 301000000, cache_tokens: 290000000, quota: 40000000, cost_usd: 80 },
    { model: 'gpt-6-astra', calls: 900, prompt_tokens: 100000000, completion_tokens: 800000, total_tokens: 100800000, cache_tokens: 96000000, quota: 20000000, cost_usd: 40 },
  ],
  series, failed: {} };

test('TestStatsRenderDrawsCharts', () => {
  const { el } = render({ from_cache: false, stats });
  assert.ok(!el('stats-meta').textContent.includes('统计失败'), el('stats-meta').textContent);
  const donut = el('stats-donut');
  assert.equal(donut.children.length, 1, 'donut svg missing');
  assert.ok(donut.children[0].children.length >= 3, 'donut segments missing');
  const bars = el('stats-bars');
  assert.equal(bars.children.length, 1, 'bars svg missing');
  assert.ok(bars.children[0].children.length >= 7, 'bar segments missing');
  const list = el('stats-account-list');
  assert.ok(list.children.some(c => c.className === 'rank-item'), 'account ranking rows missing');
  const models = el('stats-model-list');
  assert.equal(models.children.filter(c => c.className === 'rank-item').length, 3);
});

test('TestStatsRenderEmptyPayload', () => {
  const empty = { range: '24h', start: 0, end: 1, synced_at: '2026-11-16T09:50:00Z', truncated: false, partial_cost: false,
    totals: {}, accounts: [], models: [], series: [], failed: {} };
  const { el } = render({ from_cache: true, stats: empty });
  assert.ok(!el('stats-meta').textContent.includes('统计失败'), el('stats-meta').textContent);
  assert.ok(el('stats-account-list').children.some(c => c.textContent === '暂无数据'));
  assert.ok(el('stats-bars').children.some(c => c.textContent === '暂无数据'));
});

const walkAll = (n, out = []) => {
  for (const c of n.children || []) { out.push(c); walkAll(c, out); }
  return out;
};

test('TestStatsDonutLabelsAndHoverTooltip', () => {
  const { el } = render({ from_cache: false, stats });
  const svg = el('stats-donut').children[0];
  const nodes = walkAll(svg);
  // 扇区外侧引线标签：带百分比的 text 与 polyline
  const labels = nodes.filter(n => n.attrs.class === 'donut-label');
  assert.ok(labels.length >= 1, 'donut leader labels missing');
  assert.ok(labels.some(l => l.textContent.includes('%')), 'label lacks share percentage');
  assert.ok(nodes.some(n => n.attrs.class === 'donut-label-line'), 'leader lines missing');
  // 悬浮：提示框出现、扇区高亮/其余变暗
  const circles = nodes.filter(n => n.attrs.class === 'donut-seg');
  assert.ok(circles.length >= 1, 'donut segments missing');
  const first = circles[0];
  first.dispatch('mousemove', { clientX: 100, clientY: 100 });
  const tip = el('stats-tooltip');
  assert.equal(tip.hidden, false, 'hover tooltip not shown');
  assert.ok(tip.children[0].textContent.includes('HappyCoding'), tip.children[0].textContent);
  first.dispatch('mouseenter');
  assert.ok(first.classList.contains('is-hl'), 'hovered segment not highlighted');
  first.dispatch('mouseleave');
  assert.equal(el('stats-tooltip').hidden, true, 'tooltip not hidden on leave');
});

test('TestStatsRowsHighlightCharts', () => {
  const { el } = render({ from_cache: false, stats });
  const list = el('stats-account-list');
  const row = list.children.find(c => c.className === 'rank-item');
  assert.ok(row, 'ranking row missing');
  row.dispatch('mouseenter');
  const svg = el('stats-donut').children[0];
  const circles = walkAll(svg).filter(n => n.attrs.class === 'donut-seg');
  assert.ok(circles.some(c => c.classList.contains('is-hl')), 'row hover did not highlight donut');
  row.dispatch('mouseleave');
  assert.ok(circles.every(c => !c.classList.contains('is-dim')), 'row leave left segments dimmed');
});

test('TestStatsRankingCardsMatchReference', () => {
  const { el } = render({ from_cache: false, stats });
  const rows = el('stats-account-list').children.filter(c => c.className === 'rank-item');
  assert.equal(rows.length, 1);
  const top = rows[0].children.find(c => c.className === 'rank-top');
  assert.equal(top.children[0].textContent, '1', 'rank number missing');
  assert.equal(top.children[2].textContent, 'HappyCoding', 'name missing');
  assert.ok(top.children[4].textContent.endsWith('%'), 'share missing');
  const track = rows[0].children.find(c => c.className === 'rank-track');
  const pct = parseFloat(track.children[0].style.width);
  assert.ok(pct > 60 && pct < 65, `share bar width wrong: ${pct}`);
  const metrics = rows[0].children.find(c => c.className === 'rank-metrics');
  const text = metrics.children.map(c => c.textContent).join(' | ');
  for (const label of ['缓存率', '请求数', '总成本', 'RPM', 'TPM']) {
    assert.ok(text.includes(label), `metric label ${label} missing: ${text}`);
  }
  const modelRows = el('stats-model-list').children.filter(c => c.className === 'rank-item');
  assert.equal(modelRows.length, 3);
});

test('TestStatsUsesRenamedAccountLabel', () => {
  const accounts = [{ account_id: 'a'.repeat(64), label: '重命名后' }];
  const { el } = render({ from_cache: true, stats }, accounts);
  const rows = el('stats-account-list').children.filter(c => c.className === 'rank-item');
  assert.equal(rows.length, 1);
  const top = rows[0].children.find(c => c.className === 'rank-top');
  assert.equal(top.children[2].textContent, '重命名后', 'cached stats label won over the live account label');
});

test('TestStatsLongAccountNameKeepsBothEnds', () => {
  const long = 'account-with-a-very-long-name-2026';
  const accounts = [{ account_id: 'a'.repeat(64), label: long }];
  const { el } = render({ from_cache: true, stats }, accounts);
  const rows = el('stats-account-list').children.filter(c => c.className === 'rank-item');
  const top = rows[0].children.find(c => c.className === 'rank-top');
  const shown = top.children[2].textContent;
  assert.ok(shown.includes('…'), `middle ellipsis missing: ${shown}`);
  assert.ok(shown.startsWith('account-'), `prefix lost: ${shown}`);
  assert.ok(shown.endsWith('2026'), `suffix lost: ${shown}`);
  assert.ok(top.children[2].attrs.title.includes(long), 'hover title should keep the full name');
});
