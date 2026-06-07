'use strict';

// Doneness presets. Target tip temperatures in Celsius (beef-oriented, with a
// poultry-safe option and two low-and-slow BBQ modes). Each posts its Celsius
// value to the API.
const PRESETS = [
	{ name: 'Rare', c: 52 },
	{ name: 'Med-Rare', c: 57 },
	{ name: 'Medium', c: 63 },
	{ name: 'Med-Well', c: 68 },
	{ name: 'Well', c: 74 },
	{ name: 'Poultry', c: 74 },
	{ name: 'Pulled Pork', c: 95 },
	{ name: 'Brisket', c: 96 },
];

const RING_CIRCUMFERENCE = 2 * Math.PI * 86; // matches the SVG radius

// Default ambient alert thresholds in Celsius, plus the "almost done" timer.
const DEFAULT_ALERT = { low: 110, high: 125, enabled: false, etaEnabled: true, etaMinutes: 30 };

const el = (id) => document.getElementById(id);

// Active service-worker registration, used to raise system-level notifications
// that survive the tab being backgrounded. Null until registered (HTTPS only).
let swRegistration = null;

const state = {
	unit: localStorage.getItem('unit') || 'C',
	last: null,
	etaSeconds: -1,
	etaTickedAt: 0,
	series: [], // { t: ms, tip: °C, amb: °C }
	alert: loadAlertConfig(),
	alertState: 'ok', // 'ok' | 'low' | 'high'
	etaWarned: false, // whether the "almost done" alert has fired this cook
	hover: null, // { x } in canvas CSS px while pointer is over the chart
	viewingCookId: null, // when set, the chart shows a saved cook instead of live
	viewSeries: [], // points of the cook being viewed
	viewMeta: null, // { name, startedAt } of the cook being viewed
	cookName: '', // last cook name seen from the server
};

// Chart plot geometry from the last draw, used for pointer hit-testing.
let chartGeom = null;

function cToUnit(c) {
	return state.unit === 'C' ? c : c * 9 / 5 + 32;
}

function unitToC(v) {
	return state.unit === 'C' ? v : (v - 32) * 5 / 9;
}


function tempIn(status, baseField) {
	return state.unit === 'C' ? status[baseField + 'Celsius'] : status[baseField + 'Fahrenheit'];
}

function fmtTemp(v) {
	if (v === null || v === undefined || Number.isNaN(v)) return '--';
	return Math.round(v * 10) / 10;
}

// fmtRate keeps two decimals so slow cooking rates (e.g. a stall at
// ~0.04 °/min) stay visible instead of rounding to zero.
function fmtRate(v) {
	if (v === null || v === undefined || Number.isNaN(v)) return '--';
	return (Math.round(v * 100) / 100).toFixed(2);
}

function fmtDuration(seconds) {
	if (seconds < 0) return '--:--';
	seconds = Math.max(0, Math.round(seconds));
	const h = Math.floor(seconds / 3600);
	const m = Math.floor((seconds % 3600) / 60);
	const s = seconds % 60;
	if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
	return `${m}:${String(s).padStart(2, '0')}`;
}

function buildPresets() {
	const row = el('preset-row');
	row.innerHTML = '';
	for (const p of PRESETS) {
		const btn = document.createElement('button');
		btn.type = 'button';
		btn.className = 'preset';
		btn.dataset.c = p.c;
		const shown = state.unit === 'C' ? p.c : Math.round(p.c * 9 / 5 + 32);
		btn.innerHTML = `<div class="p-name">${p.name}</div><div class="p-temp">${shown}°${state.unit}</div>`;
		btn.addEventListener('click', () => setTarget(p.c));
		row.appendChild(btn);
	}
}

async function setTarget(celsius) {
	try {
		await fetch('/api/target', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ celsius }),
		});
	} catch (err) {
		console.error('set target failed', err);
	}
}

function render(status) {
	state.last = status;

	// Session control button (Start / Stop).
	updateSessionButton(status);

	// Connection pill.
	const pill = el('status-pill');
	const txt = el('status-text');
	if (!status.running) {
		pill.className = 'pill pill-off';
		txt.textContent = 'stopped';
	} else if (status.connected && status.hasReading) {
		pill.className = 'pill pill-on';
		txt.textContent = 'live';
	} else if (status.connected) {
		pill.className = 'pill pill-on';
		txt.textContent = 'connected';
	} else {
		pill.className = 'pill pill-off';
		txt.textContent = 'searching…';
	}

	// Temperatures.
	const tip = tempIn(status, 'tip');
	const ambient = tempIn(status, 'ambient');
	const target = tempIn(status, 'target');
	el('tip-temp').innerHTML = `${fmtTemp(tip)}<span class="unit">°</span>`;
	el('ambient-temp').innerHTML = `${fmtTemp(ambient)}<span class="unit">°</span>`;
	el('target-readout').textContent = `target ${fmtTemp(target)}°${state.unit}`;

	// Rate.
	const rate = state.unit === 'C'
		? status.rateCelsiusPerMin
		: status.rateCelsiusPerMin * 9 / 5;
	el('rate').innerHTML = status.hasReading
		? `${rate > 0 ? '+' : ''}${fmtRate(rate)} <span class="unit">°/min</span>`
		: `-- <span class="unit">°/min</span>`;

	// Ring progress (fraction of target reached).
	const frac = status.hasReading && status.targetCelsius > 0
		? Math.max(0, Math.min(1, status.tipCelsius / status.targetCelsius))
		: 0;
	const ring = el('ring-progress');
	ring.style.strokeDashoffset = String(RING_CIRCUMFERENCE * (1 - frac));
	ring.style.stroke = status.state === 'ready' ? 'var(--good)' : 'var(--accent)';

	// State badge.
	const badge = el('state-badge');
	badge.className = `state-badge state-${status.state}`;
	badge.textContent = status.state;

	// ETA.
	state.etaSeconds = status.etaSeconds;
	state.etaTickedAt = Date.now();
	updateEta();

	// Active preset highlight.
	for (const btn of document.querySelectorAll('.preset')) {
		btn.classList.toggle('active', Math.abs(Number(btn.dataset.c) - status.targetCelsius) < 0.5);
	}

	// Footer.
	if (status.updatedAt && status.hasReading) {
		const d = new Date(status.updatedAt);
		el('updated').textContent = `updated ${d.toLocaleTimeString()}`;
	} else {
		el('updated').textContent = 'waiting for probe…';
	}

	// Current cook name: reflect server value without clobbering active typing.
	state.cookName = status.cookName || '';
	const nameInput = el('cook-name-input');
	if (nameInput && document.activeElement !== nameInput) {
		nameInput.value = state.cookName;
	}

	// Chart + ambient alerts.
	if (status.hasReading) {
		pushPoint(status);
		evaluateAlerts(status);
		evaluateEtaAlert(status);
	}
	drawChart();
}

function updateEta() {
	const etaEl = el('eta');
	const readyEl = el('eta-ready');
	const s = state.last && state.last.state;

	if (s === 'ready') {
		etaEl.textContent = 'Done';
		readyEl.classList.remove('hidden');
		return;
	}
	readyEl.classList.add('hidden');

	if (state.etaSeconds < 0 || !state.last || !state.last.hasReading) {
		etaEl.textContent = '--:--';
		return;
	}
	const elapsed = (Date.now() - state.etaTickedAt) / 1000;
	etaEl.textContent = fmtDuration(state.etaSeconds - elapsed);
}

function toggleUnit() {
	state.unit = state.unit === 'C' ? 'F' : 'C';
	localStorage.setItem('unit', state.unit);
	el('unit-toggle').textContent = '°' + state.unit;
	buildPresets();
	syncAlertInputs();
	if (state.last) render(state.last);
}

// ---- Chart -------------------------------------------------------------

// Chart points are averaged into fixed time buckets so a long cook stays light
// to draw and hover over, no matter how fast the probe reports. The raw samples
// are still kept at full resolution in the database.
const BUCKET_MS = 5 * 60 * 1000; // 5 minutes

// addSample folds one reading into a 5-minute bucket, updating the running
// average for the current bucket or starting a new one.
function addSample(series, t, tip, amb) {
	if (tip === null || tip === undefined || Number.isNaN(tip)) return;
	const bucket = Math.floor(t / BUCKET_MS);
	const last = series[series.length - 1];
	if (last && bucket === last.bucket) {
		if (t < last.lastT) return; // out of order within the bucket
		last.n += 1;
		last.sumTip += tip;
		last.sumAmb += amb;
		last.tip = last.sumTip / last.n;
		last.amb = last.sumAmb / last.n;
		last.lastT = t;
		return;
	}
	if (last && bucket < last.bucket) return; // out of order across buckets
	series.push({
		bucket,
		n: 1,
		sumTip: tip,
		sumAmb: amb,
		t: bucket * BUCKET_MS + BUCKET_MS / 2, // plot at the bucket centre
		tip,
		amb,
		lastT: t,
	});
}

// bucketize averages a list of raw {t, tip, amb} points into 5-minute buckets.
function bucketize(raw) {
	const out = [];
	for (const p of raw) addSample(out, p.t, p.tip, p.amb);
	return out;
}

function pushPoint(status) {
	const t = status.updatedAt ? new Date(status.updatedAt).getTime() : Date.now();
	addSample(state.series, t, status.tipCelsius, status.ambientCelsius);
}

async function loadHistory() {
	try {
		const res = await fetch('/api/history');
		const points = await res.json();
		state.series = bucketize((points || []).map((p) => ({
			t: new Date(p.at).getTime(),
			tip: p.tipCelsius,
			amb: p.ambientCelsius,
		})));
		drawChart();
	} catch (err) {
		console.error('history load failed', err);
	}
}

function drawChart() {
	const canvas = el('chart');
	if (!canvas) return;
	const ctx = canvas.getContext('2d');
	const dpr = window.devicePixelRatio || 1;
	const cssW = canvas.clientWidth || 600;
	const cssH = canvas.clientHeight || 240;
	if (canvas.width !== cssW * dpr || canvas.height !== cssH * dpr) {
		canvas.width = cssW * dpr;
		canvas.height = cssH * dpr;
	}
	ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
	ctx.clearRect(0, 0, cssW, cssH);

	const padL = 44, padR = 12, padT = 12, padB = 24;
	const plotW = cssW - padL - padR;
	const plotH = cssH - padT - padB;

	const viewing = state.viewingCookId != null;
	const series = viewing ? state.viewSeries : state.series;
	if (series.length < 2) {
		chartGeom = null;
		ctx.fillStyle = 'rgba(138,150,176,.7)';
		ctx.font = '13px sans-serif';
		ctx.textAlign = 'center';
		ctx.fillText(viewing ? 'No data for this cook' : 'Collecting data…', cssW / 2, cssH / 2);
		return;
	}

	const t0 = series[0].t;
	const t1 = series[series.length - 1].t;
	const tSpan = Math.max(1, t1 - t0);

	// Target temperature for this view (live status, or the saved cook's target).
	const targetC = viewing
		? (state.viewMeta && typeof state.viewMeta.target === 'number' ? state.viewMeta.target : null)
		: (state.last ? state.last.targetCelsius : null);

	// Y range from data + target, in the active unit, with padding.
	let lo = Infinity, hi = -Infinity;
	for (const p of series) {
		lo = Math.min(lo, p.tip, p.amb);
		hi = Math.max(hi, p.tip, p.amb);
	}
	if (targetC != null) {
		lo = Math.min(lo, targetC);
		hi = Math.max(hi, targetC);
	}
	if (state.alert.enabled) {
		lo = Math.min(lo, state.alert.low);
		hi = Math.max(hi, state.alert.high);
	}
	// Anchor the chart at 0 so the curve is easy to read; pad the top only.
	lo = 0;
	hi += Math.max(2, hi * 0.1);
	const ySpan = Math.max(1, hi - lo);

	const x = (t) => padL + ((t - t0) / tSpan) * plotW;
	const y = (c) => padT + (1 - (c - lo) / ySpan) * plotH;

	// Grid + Y labels.
	ctx.strokeStyle = 'rgba(255,255,255,.06)';
	ctx.fillStyle = 'rgba(138,150,176,.8)';
	ctx.font = '11px sans-serif';
	ctx.textAlign = 'right';
	ctx.textBaseline = 'middle';
	ctx.lineWidth = 1;
	for (let i = 0; i <= 4; i++) {
		const c = lo + (ySpan * i) / 4;
		const py = y(c);
		ctx.beginPath();
		ctx.moveTo(padL, py);
		ctx.lineTo(cssW - padR, py);
		ctx.stroke();
		ctx.fillText(`${Math.round(cToUnit(c))}°`, padL - 6, py);
	}

	// X time labels.
	ctx.textAlign = 'center';
	ctx.textBaseline = 'top';
	for (let i = 0; i <= 3; i++) {
		const t = t0 + (tSpan * i) / 3;
		const px = x(t);
		ctx.fillText(new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }), px, cssH - padB + 6);
	}

	// Threshold lines.
	if (state.alert.enabled && !viewing) {
		drawHLine(ctx, padL, cssW - padR, y(state.alert.low), 'rgba(61,169,255,.55)');
		drawHLine(ctx, padL, cssW - padR, y(state.alert.high), 'rgba(224,83,61,.55)');
	}
	// Target line.
	if (targetC != null) {
		drawHLine(ctx, padL, cssW - padR, y(targetC), 'rgba(54,211,153,.6)');
	}

	drawSeries(ctx, series, x, y, 'amb', getCss('--cool', '#3da9ff'));
	drawSeries(ctx, series, x, y, 'tip', getCss('--accent', '#ff6b3d'));

	// Remember geometry for pointer hit-testing, then draw the hover overlay.
	chartGeom = { x, y, series, padL, padR, padT, padB, plotW, plotH, t0, t1, cssW, cssH, targetC };
	drawHover(ctx);
}

// drawHover renders a crosshair, markers, and a tooltip at the sample nearest to
// the pointer so you can read which temperature was reached at which time.
function drawHover(ctx) {
	if (!state.hover || !chartGeom) return;
	const g = chartGeom;
	const hx = state.hover.x;
	if (hx < g.padL || hx > g.cssW - g.padR) return;

	// Find the sample nearest the pointer in screen-x.
	let best = null, bestDx = Infinity;
	for (const p of g.series) {
		const dx = Math.abs(g.x(p.t) - hx);
		if (dx < bestDx) { bestDx = dx; best = p; }
	}
	if (!best) return;

	const px = g.x(best.t);
	const yTip = g.y(best.tip);
	const yAmb = g.y(best.amb);

	// Crosshair.
	ctx.save();
	ctx.strokeStyle = 'rgba(255,255,255,.25)';
	ctx.lineWidth = 1;
	ctx.beginPath();
	ctx.moveTo(px, g.padT);
	ctx.lineTo(px, g.cssH - g.padB);
	ctx.stroke();

	// Markers.
	const tipColor = getCss('--accent', '#ff6b3d');
	const ambColor = getCss('--cool', '#3da9ff');
	const tgtColor = getCss('--good', '#36d399');
	const markers = [[yTip, tipColor], [yAmb, ambColor]];
	if (g.targetC != null) markers.push([g.y(g.targetC), tgtColor]);
	for (const [yy, col] of markers) {
		ctx.beginPath();
		ctx.arc(px, yy, 3.5, 0, Math.PI * 2);
		ctx.fillStyle = col;
		ctx.fill();
		ctx.lineWidth = 1.5;
		ctx.strokeStyle = 'rgba(11,15,23,.9)';
		ctx.stroke();
	}

	// Tooltip box. Each row is [text, color].
	const time = new Date(best.t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
	const rows = [
		[time, 'rgba(225,231,245,.95)'],
		[`Internal ${Math.round(cToUnit(best.tip) * 10) / 10}°${state.unit}`, tipColor],
		[`Ambient ${Math.round(cToUnit(best.amb) * 10) / 10}°${state.unit}`, ambColor],
	];
	if (g.targetC != null) {
		rows.push([`Target ${Math.round(cToUnit(g.targetC) * 10) / 10}°${state.unit}`, tgtColor]);
	}
	ctx.font = '11px sans-serif';
	let boxW = 0;
	for (const [text] of rows) boxW = Math.max(boxW, ctx.measureText(text).width);
	boxW += 16;
	const boxH = 12 + rows.length * 15;
	let bx = px + 10;
	if (bx + boxW > g.cssW - g.padR) bx = px - 10 - boxW;
	const by = g.padT + 6;

	ctx.fillStyle = 'rgba(20,26,38,.95)';
	ctx.strokeStyle = 'rgba(255,255,255,.12)';
	ctx.lineWidth = 1;
	roundRect(ctx, bx, by, boxW, boxH, 6);
	ctx.fill();
	ctx.stroke();

	ctx.textAlign = 'left';
	ctx.textBaseline = 'top';
	rows.forEach(([text, col], i) => {
		ctx.fillStyle = col;
		ctx.fillText(text, bx + 8, by + 7 + i * 15);
	});
	ctx.restore();
}

function roundRect(ctx, x, y, w, h, r) {
	ctx.beginPath();
	ctx.moveTo(x + r, y);
	ctx.arcTo(x + w, y, x + w, y + h, r);
	ctx.arcTo(x + w, y + h, x, y + h, r);
	ctx.arcTo(x, y + h, x, y, r);
	ctx.arcTo(x, y, x + w, y, r);
	ctx.closePath();
}

function drawSeries(ctx, series, x, y, key, color) {
	ctx.strokeStyle = color;
	ctx.lineWidth = 2;
	ctx.lineJoin = 'round';
	ctx.beginPath();
	series.forEach((p, i) => {
		const px = x(p.t), py = y(p[key]);
		if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py);
	});
	ctx.stroke();
}

function drawHLine(ctx, x0, x1, py, color) {
	ctx.save();
	ctx.strokeStyle = color;
	ctx.lineWidth = 1.5;
	ctx.setLineDash([5, 4]);
	ctx.beginPath();
	ctx.moveTo(x0, py);
	ctx.lineTo(x1, py);
	ctx.stroke();
	ctx.restore();
}

function getCss(varName, fallback) {
	const v = getComputedStyle(document.documentElement).getPropertyValue(varName).trim();
	return v || fallback;
}

// ---- Cooks (naming, new cook, saved history) ---------------------------

async function saveCookName() {
	const name = el('cook-name-input').value.trim();
	try {
		await fetch('/api/cook/name', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ name }),
		});
		loadCooks();
	} catch (err) {
		console.error('save cook name failed', err);
	}
}

// Start probe discovery and a fresh cook. The current name field is sent so the
// new cook is created with it.
async function startSession() {
	const name = el('cook-name-input').value.trim();
	try {
		await fetch('/api/session/start', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ name }),
		});
		state.series = [];
		state.viewingCookId = null;
		state.etaWarned = false;
		updateLiveButton();
		drawChart();
		loadCooks();
	} catch (err) {
		console.error('start session failed', err);
	}
}

// Stop probe discovery and end the current cook.
async function stopSession() {
	try {
		await fetch('/api/session/stop', { method: 'POST' });
		loadCooks();
	} catch (err) {
		console.error('stop session failed', err);
	}
}

// toggleSession starts or stops discovery based on the current running state.
function toggleSession() {
	if (state.last && state.last.running) {
		stopSession();
	} else {
		startSession();
	}
}

// updateSessionButton reflects the running state on the Start/Stop button.
function updateSessionButton(status) {
	const btn = el('session-toggle');
	if (!btn) return;
	if (status.running) {
		btn.textContent = 'Stop';
		btn.classList.add('session-stop');
	} else {
		btn.textContent = 'Start';
		btn.classList.remove('session-stop');
	}
}

function fmtCookSpan(c) {
	const start = new Date(c.startedAt);
	const end = c.endedAt ? new Date(c.endedAt) : null;
	const day = start.toLocaleDateString([], { month: 'short', day: 'numeric' });
	const t = start.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
	let dur = '';
	if (end) {
		const secs = Math.max(0, Math.round((end - start) / 1000));
		dur = ' · ' + fmtDuration(secs);
	}
	return `${day} ${t}${dur}`;
}

async function loadCooks() {
	const list = el('cooks-list');
	if (!list) return;
	try {
		const res = await fetch('/api/cooks');
		const cooks = await res.json();
		list.innerHTML = '';
		if (!cooks || cooks.length === 0) {
			const li = document.createElement('li');
			li.className = 'cooks-empty';
			li.textContent = 'No saved cooks yet.';
			list.appendChild(li);
			return;
		}
		for (const c of cooks) {
			const li = document.createElement('li');
			li.className = 'cook-item' + (c.active ? ' cook-active' : '');
			if (state.viewingCookId === c.id) li.classList.add('cook-viewing');
			const name = (c.name && c.name.trim()) || 'Cook #' + c.id;
			const maxTip = Math.round(cToUnit(c.maxTipCelsius));
			li.innerHTML = `
				<div class="cook-main">
					<span class="cook-item-name">${escapeHtml(name)}</span>
					<span class="cook-meta">${fmtCookSpan(c)}</span>
				</div>
				<div class="cook-side">
					<span class="cook-max">max ${maxTip}°${state.unit}</span>
					${c.active ? '<span class="cook-badge">live</span>' : ''}
				</div>`;
			li.addEventListener('click', () => viewCook(c.id, name, c.targetCelsius));
			list.appendChild(li);
		}
	} catch (err) {
		console.error('load cooks failed', err);
	}
}

async function viewCook(id, name, targetCelsius) {
	try {
		const res = await fetch('/api/cooks/' + id);
		const data = await res.json();
		state.viewSeries = bucketize((data.points || []).map((p) => ({
			t: new Date(p.at).getTime(),
			tip: p.tipCelsius,
			amb: p.ambientCelsius,
		})));
		state.viewingCookId = id;
		state.viewMeta = { name, target: typeof targetCelsius === 'number' ? targetCelsius : null };
		updateLiveButton();
		drawChart();
		loadCooks();
	} catch (err) {
		console.error('view cook failed', err);
	}
}

function backToLive() {
	state.viewingCookId = null;
	state.viewMeta = null;
	updateLiveButton();
	drawChart();
	loadCooks();
}

function updateLiveButton() {
	const btn = el('chart-live');
	const title = el('chart-title');
	if (!btn || !title) return;
	if (state.viewingCookId != null) {
		btn.classList.remove('hidden');
		title.textContent = state.viewMeta && state.viewMeta.name ? state.viewMeta.name : 'Saved cook';
	} else {
		btn.classList.add('hidden');
		title.textContent = 'Temperature over time';
	}
}

function escapeHtml(s) {
	return String(s).replace(/[&<>"']/g, (c) => (
		{ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
	));
}

// setupHover wires pointer interaction on the chart for the crosshair tooltip.
function setupHover() {
	const canvas = el('chart');
	if (!canvas) return;
	const move = (clientX) => {
		const rect = canvas.getBoundingClientRect();
		state.hover = { x: clientX - rect.left };
		drawChart();
	};
	canvas.addEventListener('mousemove', (e) => move(e.clientX));
	canvas.addEventListener('mouseleave', () => { state.hover = null; drawChart(); });
	canvas.addEventListener('touchstart', (e) => { if (e.touches[0]) move(e.touches[0].clientX); }, { passive: true });
	canvas.addEventListener('touchmove', (e) => { if (e.touches[0]) move(e.touches[0].clientX); }, { passive: true });
	canvas.addEventListener('touchend', () => { state.hover = null; drawChart(); });
}

// ---- Ambient alerts ----------------------------------------------------

function loadAlertConfig() {
	try {
		const saved = JSON.parse(localStorage.getItem('alertConfig'));
		if (saved && typeof saved.low === 'number' && typeof saved.high === 'number') {
			return {
				low: saved.low,
				high: saved.high,
				enabled: !!saved.enabled,
				etaEnabled: saved.etaEnabled !== undefined ? !!saved.etaEnabled : DEFAULT_ALERT.etaEnabled,
				etaMinutes: typeof saved.etaMinutes === 'number' ? saved.etaMinutes : DEFAULT_ALERT.etaMinutes,
			};
		}
	} catch { /* ignore */ }
	return { ...DEFAULT_ALERT };
}

function saveAlertConfig() {
	localStorage.setItem('alertConfig', JSON.stringify(state.alert));
}

// syncAlertInputs reflects the stored Celsius thresholds in the current unit.
function syncAlertInputs() {
	el('alert-low').value = Math.round(cToUnit(state.alert.low));
	el('alert-high').value = Math.round(cToUnit(state.alert.high));
	el('alerts-enabled').checked = state.alert.enabled;
	el('eta-enabled').checked = state.alert.etaEnabled;
	el('eta-minutes').value = state.alert.etaMinutes;
	for (const u of document.querySelectorAll('.alert-unit')) u.textContent = '°' + state.unit;
}

function evaluateAlerts(status) {
	if (!state.alert.enabled) {
		state.alertState = 'ok';
		hideBanner();
		return;
	}
	const amb = status.ambientCelsius;
	let next = 'ok';
	if (amb < state.alert.low) next = 'low';
	else if (amb > state.alert.high) next = 'high';

	if (next !== state.alertState) {
		state.alertState = next;
		if (next === 'ok') {
			hideBanner();
		} else {
			const shown = Math.round(cToUnit(next === 'low' ? state.alert.low : state.alert.high));
			const ambShown = Math.round(cToUnit(amb));
			const msg = next === 'low'
				? `Ambient ${ambShown}°${state.unit} is below ${shown}°${state.unit}`
				: `Ambient ${ambShown}°${state.unit} is above ${shown}°${state.unit}`;
			triggerAlert(next, msg);
		}
	}
}

// evaluateEtaAlert fires a one-shot notification when the estimated finish time
// drops to (or below) the configured warning window, e.g. 30 minutes to go.
function evaluateEtaAlert(status) {
	if (!state.alert.etaEnabled || !status.hasReading) return;
	const eta = status.etaSeconds;
	const threshold = state.alert.etaMinutes * 60;
	if (status.state === 'cooking' && eta > 0 && eta <= threshold) {
		if (!state.etaWarned) {
			state.etaWarned = true;
			const mins = Math.max(1, Math.round(eta / 60));
			triggerAlert('eta', `Almost done — about ${mins} min to target`);
		}
	} else if (eta > threshold || status.state === 'ready' || status.state === 'disconnected') {
		state.etaWarned = false;
	}
}

function triggerAlert(kind, msg) {
	showBanner(kind, msg);
	beep(kind === 'high' ? 880 : kind === 'eta' ? 660 : 440);
	const title = kind === 'eta' ? 'MEATER cooking timer' : 'MEATER ambient alert';
	notify(title, msg, 'meater-' + kind);
}

// notify raises a system notification, preferring the service worker so it is
// system-level and survives the tab being backgrounded; falls back to a page
// Notification when no worker is registered.
function notify(title, body, tag) {
	if (!('Notification' in window) || Notification.permission !== 'granted') return;
	const opts = { body, tag, renotify: true };
	if (swRegistration && swRegistration.showNotification) {
		swRegistration.showNotification(title, opts).catch(() => { });
		return;
	}
	try {
		new Notification(title, opts);
	} catch { /* ignore */ }
}

// registerServiceWorker installs sw.js (HTTPS / localhost only) so notifications
// can be shown by the worker even when the page is in the background.
function registerServiceWorker() {
	if (!('serviceWorker' in navigator) || !window.isSecureContext) return;
	navigator.serviceWorker.register('sw.js')
		.then((reg) => { swRegistration = reg; })
		.catch((err) => console.warn('service worker registration failed', err));
}

// unlockAudio resumes the AudioContext on the first user gesture so the alert
// beep can actually play (browsers start audio suspended until interaction).
let audioUnlocked = false;
function unlockAudio() {
	if (audioUnlocked) return;
	try {
		audioCtx = audioCtx || new (window.AudioContext || window.webkitAudioContext)();
		if (audioCtx.state === 'suspended') audioCtx.resume();
		audioUnlocked = true;
	} catch { /* audio not available */ }
}

function showBanner(kind, msg) {
	const banner = el('alert-banner');
	banner.classList.remove('hidden', 'alert-low');
	if (kind === 'low') banner.classList.add('alert-low');
	el('alert-text').textContent = msg;
}

function hideBanner() {
	el('alert-banner').classList.add('hidden');
}

let audioCtx = null;
function beep(freq) {
	try {
		audioCtx = audioCtx || new (window.AudioContext || window.webkitAudioContext)();
		const osc = audioCtx.createOscillator();
		const gain = audioCtx.createGain();
		osc.type = 'sine';
		osc.frequency.value = freq;
		gain.gain.setValueAtTime(0.001, audioCtx.currentTime);
		gain.gain.exponentialRampToValueAtTime(0.2, audioCtx.currentTime + 0.02);
		gain.gain.exponentialRampToValueAtTime(0.001, audioCtx.currentTime + 0.5);
		osc.connect(gain).connect(audioCtx.destination);
		osc.start();
		osc.stop(audioCtx.currentTime + 0.5);
	} catch { /* audio not available */ }
}

function commitThreshold() {
	const low = parseFloat(el('alert-low').value);
	const high = parseFloat(el('alert-high').value);
	if (!Number.isNaN(low)) state.alert.low = unitToC(low);
	if (!Number.isNaN(high)) state.alert.high = unitToC(high);
	saveAlertConfig();
	state.alertState = 'ok'; // re-arm so the new range is re-evaluated
	if (state.last) evaluateAlerts(state.last);
	drawChart();
}

function setupAlerts() {
	syncAlertInputs();
	el('alert-low').addEventListener('change', commitThreshold);
	el('alert-high').addEventListener('change', commitThreshold);
	el('alerts-enabled').addEventListener('change', (e) => {
		state.alert.enabled = e.target.checked;
		saveAlertConfig();
		state.alertState = 'ok';
		if (state.last) evaluateAlerts(state.last);
		drawChart();
	});
	el('alert-dismiss').addEventListener('click', hideBanner);

	el('eta-enabled').addEventListener('change', (e) => {
		state.alert.etaEnabled = e.target.checked;
		saveAlertConfig();
		state.etaWarned = false;
	});
	el('eta-minutes').addEventListener('change', (e) => {
		const m = parseInt(e.target.value, 10);
		if (!Number.isNaN(m) && m > 0) state.alert.etaMinutes = m;
		saveAlertConfig();
		state.etaWarned = false;
		syncAlertInputs();
	});

	const notifBtn = el('notif-enable');
	if (!('Notification' in window)) {
		notifBtn.textContent = 'Notifications unsupported';
		notifBtn.disabled = true;
	} else if (!window.isSecureContext) {
		notifBtn.textContent = 'Notifications need HTTPS';
		notifBtn.title = 'Open this page over https:// (or localhost) to enable system notifications.';
		notifBtn.disabled = true;
	} else {
		reflectNotifButton();
		notifBtn.addEventListener('click', async () => {
			await Notification.requestPermission();
			reflectNotifButton();
		});
	}
}

function reflectNotifButton() {
	const btn = el('notif-enable');
	if (Notification.permission === 'granted') {
		btn.textContent = 'Browser notifications on';
		btn.classList.add('on');
	} else if (Notification.permission === 'denied') {
		btn.textContent = 'Notifications blocked';
	} else {
		btn.textContent = 'Enable browser notifications';
	}
}

function connectStream() {
	const es = new EventSource('/api/stream');
	es.onmessage = (e) => {
		try {
			render(JSON.parse(e.data));
		} catch (err) {
			console.error('bad event', err);
		}
	};
	es.onerror = () => {
		// EventSource auto-reconnects; reflect the gap in the UI.
		const pill = el('status-pill');
		pill.className = 'pill pill-off';
		el('status-text').textContent = 'reconnecting…';
	};
}

function init() {
	el('unit-toggle').textContent = '°' + state.unit;
	el('unit-toggle').addEventListener('click', toggleUnit);
	el('custom-form').addEventListener('submit', (e) => {
		e.preventDefault();
		const raw = parseFloat(el('custom-input').value);
		if (Number.isNaN(raw)) return;
		const celsius = state.unit === 'C' ? raw : (raw - 32) * 5 / 9;
		setTarget(celsius);
		el('custom-input').value = '';
	});
	buildPresets();
	setupAlerts();
	setupHover();
	el('cook-name-form').addEventListener('submit', (e) => {
		e.preventDefault();
		saveCookName();
		el('cook-name-input').blur();
	});
	el('session-toggle').addEventListener('click', toggleSession);
	el('chart-live').addEventListener('click', backToLive);
	el('cooks-refresh').addEventListener('click', loadCooks);
	registerServiceWorker();
	for (const ev of ['pointerdown', 'keydown', 'touchstart']) {
		window.addEventListener(ev, unlockAudio, { passive: true });
	}
	loadHistory();
	loadCooks();
	connectStream();
	setInterval(updateEta, 1000); // smooth local countdown between updates
	window.addEventListener('resize', drawChart);
}

document.addEventListener('DOMContentLoaded', init);
