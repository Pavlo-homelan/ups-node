(() => {
  'use strict';

  const csrf = document.querySelector('meta[name="csrf-token"]')?.content || '';
  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  const history = { battery: [], output: [] };
  const displayed = new Map();
  let stream;

  const byId = (id) => document.getElementById(id);
  const number = (value, fallback = 0) => Number.isFinite(Number(value)) ? Number(value) : fallback;
  const text = (id, value) => { const target = byId(id); if (target) target.textContent = value; };

  function setConnection(state, label) {
    const target = byId('connection-state');
    if (target) target.dataset.state = state;
    text('connection-label', label);
  }

  function animateValue(key, value, digits) {
    const targets = document.querySelectorAll(`[data-number="${key}"]`);
    if (!targets.length) return;
    const finish = number(value);
    const start = displayed.has(key) ? displayed.get(key) : finish;
    displayed.set(key, finish);
    if (reducedMotion || Math.abs(finish - start) < 0.001) {
      targets.forEach((target) => { target.textContent = finish.toFixed(digits); });
      return;
    }
    const began = performance.now();
    const duration = 420;
    function frame(now) {
      const progress = Math.min(1, (now - began) / duration);
      const eased = 1 - Math.pow(1 - progress, 3);
      const current = start + (finish - start) * eased;
      targets.forEach((target) => { target.textContent = current.toFixed(digits); });
      if (progress < 1) requestAnimationFrame(frame);
    }
    requestAnimationFrame(frame);
  }

  function updateSparkline(id, values, incoming) {
    values.push(number(incoming));
    if (values.length > 48) values.shift();
    const polyline = byId(id);
    if (!polyline || values.length < 2) return;
    const low = Math.min(...values);
    const high = Math.max(...values);
    const span = Math.max(0.001, high - low);
    const points = values.map((value, index) => {
      const x = index * (400 / Math.max(1, values.length - 1));
      const y = 65 - ((value - low) / span) * 54;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    });
    polyline.setAttribute('points', points.join(' '));
  }

  function statusSummary(data) {
    const state = String(data.state || '').toUpperCase();
    if (state === 'DISCHARGE') return 'AC немає · батарея';
    if (state === 'CHARGE') return 'AC є · заряд';
    if (state === 'CHARGED') return 'AC є · заряджено';
    if (state === 'ERROR') return 'Помилка апаратної частини';
    return 'Очікування телеметрії';
  }

  function applyStatus(data) {
    const state = String(data.state || 'INIT').toUpperCase();
    const mode = state.toLowerCase();
    const percent = Math.max(0, Math.min(100, Math.round(number(data.percent))));
    const batteryStatus = String(data.battery_status || '--');
    const connected = String(data.status || '') !== 'degraded';

    setConnection(connected ? 'open' : 'error', connected ? 'Live' : 'Часткові дані');
    text('updated-time', data.updated_at ? new Date(data.updated_at).toLocaleTimeString() : '--:--:--');
    text('system-summary', statusSummary(data));
    text('ups-state', state);
    byId('ups-state')?.setAttribute('data-state', mode);
    byId('power-map')?.setAttribute('data-mode', mode);
    text('flow-mains-label', data.ac ? 'ONLINE' : 'LOST');
    text('flow-ups-label', state);
    text('flow-battery-label', `${percent}%`);

    text('battery-percent', `${percent}%`);
    const fill = byId('battery-fill');
    if (fill) {
      fill.style.height = `${percent}%`;
      fill.style.background = percent < 15 ? 'linear-gradient(0deg,#d9485a,#ff6b78)' : percent < 40 ? 'linear-gradient(0deg,#d89f36,#ffd166)' : 'linear-gradient(0deg,#29bd7d,#6cf2ad)';
    }
    text('battery-status', batteryStatus);
    byId('battery-status')?.setAttribute('data-status', batteryStatus.toLowerCase());
    text('output-channel', String(data.output_channel || 2));

    animateValue('v', data.v, 2);
    animateValue('current_ma', data.current_ma, 0);
    animateValue('power_w', data.power_w, 2);
    animateValue('output_v', data.output_v, 2);
    animateValue('output_i', data.output_i, 0);
    animateValue('output_w', data.output_w, 2);
    animateValue('cpu_temp', data.cpu_temp, 1);
    animateValue('ram_used', data.ram_used, 0);
    animateValue('ram_percent', data.ram_percent, 1);

    updateSparkline('battery-history', history.battery, data.v);
    updateSparkline('output-history', history.output, data.output_i);
    text('wifi-ssid', data.connected_ssid || (data.portal_mode === 'ap' ? 'Fallback AP' : '--'));
    text('wifi-ip', data.ip_address || '--');
    text('wifi-mode', data.portal_mode === 'client' ? 'Client' : data.portal_mode === 'ap' ? 'Access point' : data.wifi_state || '--');

    const diagnostic = byId('diagnostic-error');
    const error = data.hardware_error || data.display_error || data.collection_error || '';
    if (diagnostic) {
      diagnostic.hidden = !error;
      diagnostic.textContent = error;
    }
  }

  async function api(path, options = {}) {
    const headers = new Headers(options.headers || {});
    headers.set('Accept', 'application/json');
    if (options.method && options.method !== 'GET') headers.set('X-CSRF-Token', csrf);
    const response = await fetch(path, { ...options, headers, credentials: 'same-origin' });
    const payload = await response.json().catch(() => ({}));
    if (response.status === 401) {
      location.assign('/login');
      throw new Error('authentication required');
    }
    if (!response.ok) throw new Error(payload.error || `HTTP ${response.status}`);
    return payload;
  }

  async function initialStatus() {
    try { applyStatus(await api('/api/v1/status')); } catch (error) { setConnection('error', 'Немає даних'); }
  }

  function openStream() {
    if (stream) stream.close();
    stream = new EventSource('/api/v1/events');
    stream.addEventListener('status', (event) => {
      try { applyStatus(JSON.parse(event.data)); } catch (error) { setConnection('error', 'Помилка даних'); }
    });
    stream.onopen = () => setConnection('open', 'Live');
    stream.onerror = () => setConnection('error', 'Перепідключення…');
  }

  function setupNetworkDialog() {
    const dialog = byId('network-dialog');
    byId('open-network')?.addEventListener('click', () => dialog?.showModal());
    byId('scan-network')?.addEventListener('click', async () => {
      const list = byId('network-list');
      if (list) list.textContent = 'Сканування…';
      try {
        const result = await api('/api/v1/wifi/scan', { method: 'POST' });
        if (!list) return;
        list.textContent = '';
        const networks = Array.isArray(result.networks) ? result.networks : [];
        if (!networks.length) list.textContent = 'Мережі не знайдено.';
        networks.forEach((network) => {
          const button = document.createElement('button');
          button.type = 'button';
          button.className = 'network-option';
          button.dataset.connected = String(Boolean(network.connected));
          const name = document.createElement('strong');
          name.textContent = network.ssid || 'Прихована мережа';
          const security = document.createElement('small');
          security.textContent = network.security || 'Open';
          const signal = document.createElement('span');
          signal.className = 'network-signal';
          signal.textContent = `${number(network.signal).toFixed(0)}%`;
          button.append(name, security, signal);
          button.addEventListener('click', () => { const input = byId('connect-ssid'); if (input) input.value = network.ssid || ''; });
          list.append(button);
        });
      } catch (error) { if (list) list.textContent = error.message; }
    });
    byId('connect-form')?.addEventListener('submit', async (event) => {
      event.preventDefault();
      const message = byId('network-message');
      if (message) { message.className = 'form-message'; message.textContent = 'Підключення…'; }
      const payload = {
        ssid: byId('connect-ssid')?.value || '',
        password: byId('connect-password')?.value || '',
        hidden: Boolean(byId('connect-hidden')?.checked)
      };
      try {
        const result = await api('/api/v1/wifi/connect', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) });
        if (message) message.textContent = result.message || 'Команду підключення передано. Адреса ноди може змінитися.';
        const password = byId('connect-password'); if (password) password.value = '';
      } catch (error) {
        if (message) { message.className = 'form-message error'; message.textContent = error.message; }
      }
    });
  }

  document.addEventListener('DOMContentLoaded', () => {
    try {
      const initial = byId('initial-status')?.textContent;
      if (initial) applyStatus(JSON.parse(initial));
    } catch (error) {
      setConnection('error', 'Помилка даних');
    }
    initialStatus();
    openStream();
    setupNetworkDialog();
    window.addEventListener('pagehide', () => stream?.close(), { once: true });
  });
})();
