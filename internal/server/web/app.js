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

// How much history to keep on the chart (milliseconds).
const CHART_WINDOW_MS = 60 * 60 * 1000;

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
};

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

	// Connection pill.
	const pill = el('status-pill');
	const txt = el('status-text');
	if (status.connected && status.hasReading) {
		pill.className = 'pill pill-on';
		txt.textContent = 'live';
	} else if (status.connected) {
		pill.className = 'pill pill-on';
		txt.textContent = 'connected';
	} else {
		pill.className = 'pill pill-off';
		txt.textContent = 'no probe';
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
		? `${rate > 0 ? '+' : ''}${fmtTemp(rate)} <span class="unit">°/min</span>`
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

function pushPoint(status) {
	const t = status.updatedAt ? new Date(status.updatedAt).getTime() : Date.now();
	const last = state.series[state.series.length - 1];
	if (last && t <= last.t) return; // ignore duplicates / out-of-order
	state.series.push({ t, tip: status.tipCelsius, amb: status.ambientCelsius });
	const cutoff = Date.now() - CHART_WINDOW_MS;
	while (state.series.length && state.series[0].t < cutoff) state.series.shift();
}

async function loadHistory() {
	try {
		const res = await fetch('/api/history');
		const points = await res.json();
		state.series = (points || []).map((p) => ({
			t: new Date(p.at).getTime(),
			tip: p.tipCelsius,
			amb: p.ambientCelsius,
		}));
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

	const series = state.series;
	if (series.length < 2) {
		ctx.fillStyle = 'rgba(138,150,176,.7)';
		ctx.font = '13px sans-serif';
		ctx.textAlign = 'center';
		ctx.fillText('Collecting data…', cssW / 2, cssH / 2);
		return;
	}

	const t0 = series[0].t;
	const t1 = series[series.length - 1].t;
	const tSpan = Math.max(1, t1 - t0);

	// Y range from data + target, in the active unit, with padding.
	let lo = Infinity, hi = -Infinity;
	for (const p of series) {
		lo = Math.min(lo, p.tip, p.amb);
		hi = Math.max(hi, p.tip, p.amb);
	}
	if (state.last) {
		lo = Math.min(lo, state.last.targetCelsius);
		hi = Math.max(hi, state.last.targetCelsius);
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
	if (state.alert.enabled) {
		drawHLine(ctx, padL, cssW - padR, y(state.alert.low), 'rgba(61,169,255,.55)');
		drawHLine(ctx, padL, cssW - padR, y(state.alert.high), 'rgba(224,83,61,.55)');
	}
	// Target line.
	if (state.last) {
		drawHLine(ctx, padL, cssW - padR, y(state.last.targetCelsius), 'rgba(54,211,153,.6)');
	}

	drawSeries(ctx, series, x, y, 'amb', getCss('--cool', '#3da9ff'));
	drawSeries(ctx, series, x, y, 'tip', getCss('--accent', '#ff6b3d'));
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
	registerServiceWorker();
	for (const ev of ['pointerdown', 'keydown', 'touchstart']) {
		window.addEventListener(ev, unlockAudio, { passive: true });
	}
	loadHistory();
	connectStream();
	setInterval(updateEta, 1000); // smooth local countdown between updates
	window.addEventListener('resize', drawChart);
}

document.addEventListener('DOMContentLoaded', init);
