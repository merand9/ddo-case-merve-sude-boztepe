// DamarBağış ön yüzü: derleme adımı olmayan, bağımlılıksız tek sayfa uygulama.
// Her ekran gerçek API verisiyle çalışır ve yükleniyor / boş / hata durumlarını ele alır.

const $app = document.getElementById('app');
const ROLE_TR = { donor: 'Bağışçı', nurse: 'Hemşire', lab: 'Laboratuvar', bank: 'Kan bankası', hospital: 'Hastane', admin: 'Yönetici' };
const STATUS_TR = {
  pending_test: 'Test bekliyor', available: 'Stokta', reserved: 'Rezerve', issued: 'Dağıtıldı', transfused: 'Transfüze',
  quarantined: 'Karantina', discarded: 'İmha', expired: 'SKT doldu', split: 'Bölündü',
  open: 'Açık', partial: 'Kısmi', cancelled: 'İptal', active: 'Aktif', released: 'Serbest', preempted: 'Devredildi',
  booked: 'Randevulu', completed: 'Tamamlandı', no_show: 'Gelmedi', pending: 'Bekliyor', approved: 'Onaylandı', rejected: 'Reddedildi',
};
const URG_TR = { routine: 'Rutin', urgent: 'Acil', emergency: 'Çok acil' };
const COMP_TR = { RBC: 'Eritrosit', PLT: 'Trombosit', FFP: 'Plazma' };
const GROUPS = ['O-', 'O+', 'A-', 'A+', 'B-', 'B+', 'AB-', 'AB+'];
const DEMO = [
  ['admin@damarbagis.local', 'Yönetici'], ['hemsire@damarbagis.local', 'Hemşire'], ['lab@damarbagis.local', 'Laboratuvar uzmanı'],
  ['lab2@damarbagis.local', 'Laboratuvar (2. onay)'], ['bankasorumlusu@damarbagis.local', 'Kan bankası sorumlusu'],
  ['hastane@damarbagis.local', 'Hastane'], ['bagisci@damarbagis.local', 'Bağışçı'],
];

// ---------- yardımcılar ----------
const esc = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const fmtDate = (s) => (s ? new Date(s).toLocaleDateString('tr-TR') : '—');
const fmtDT = (s) => (s ? new Date(s).toLocaleString('tr-TR', { dateStyle: 'short', timeStyle: 'short' }) : '—');
const fmtTime = (s) => (s ? new Date(s).toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '');
const grp = (abo, rh) => `${abo}${rh ? '+' : '-'}`;
const badge = (status) => {
  const cls = { available: 'b-ok', approved: 'b-ok', issued: 'b-info', transfused: 'b-info', completed: 'b-ok', reserved: 'b-warn', partial: 'b-warn',
    pending_test: 'b-warn', pending: 'b-warn', open: 'b-warn', booked: 'b-info', quarantined: 'b-bad', discarded: 'b-bad', expired: 'b-bad',
    cancelled: '', rejected: 'b-bad', preempted: 'b-bad' }[status] || '';
  return `<span class="badge ${cls}">${esc(STATUS_TR[status] || status)}</span>`;
};
const idemKey = () => (crypto.randomUUID ? crypto.randomUUID() : String(Date.now()) + Math.random().toString(16).slice(2));
const isoDay = (d = new Date()) => d.toISOString().slice(0, 10);

function toast(msg, bad = false) {
  let box = document.getElementById('toast');
  if (!box) { box = document.createElement('div'); box.id = 'toast'; document.body.appendChild(box); }
  const el = document.createElement('div');
  if (bad) el.className = 'bad';
  el.textContent = msg;
  box.appendChild(el);
  setTimeout(() => el.remove(), 4500);
}

// ---------- oturum ve API ----------
const session = {
  get() { try { return JSON.parse(sessionStorage.getItem('db.session') || 'null'); } catch { return null; } },
  set(v) { try { sessionStorage.setItem('db.session', JSON.stringify(v)); } catch { /* yoksay */ } },
  clear() { try { sessionStorage.removeItem('db.session'); } catch { /* yoksay */ } },
};

class ApiError extends Error {
  constructor(status, body) {
    super(body?.error?.message || `HTTP ${status}`);
    this.status = status; this.code = body?.error?.code; this.details = body?.error?.details;
  }
}

async function api(method, path, body, opts = {}) {
  const s = session.get();
  const headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) };
  if (s?.access_token) headers.Authorization = `Bearer ${s.access_token}`;
  if (method === 'POST' && opts.idem !== false) headers['Idempotency-Key'] = opts.key || idemKey();
  const res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  if (res.status === 401 && s?.refresh_token && !opts.retried) {
    const r = await fetch('/auth/refresh', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ refresh_token: s.refresh_token }) });
    if (r.ok) { session.set(await r.json()); return api(method, path, body, { ...opts, retried: true }); }
    session.clear(); location.hash = '#/login'; render();
    throw new ApiError(401, { error: { message: 'Oturum süresi doldu, yeniden giriş yapın' } });
  }
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) throw new ApiError(res.status, data);
  return data;
}

const errorBox = (e) => {
  let extra = '';
  if (e.details?.reasons) extra = `<ul class="reasons">${e.details.reasons.map((r) => `<li>${esc(r.message)}${r.until ? ` (uygun: ${fmtDate(r.until)})` : ''}</li>`).join('')}</ul>`;
  else if (Array.isArray(e.details)) extra = `<ul class="reasons">${e.details.map((d) => `<li>${esc(d)}</li>`).join('')}</ul>`;
  return `<div class="state error"><b>Hata${e.status ? ` (${e.status})` : ''}:</b> ${esc(e.message)}${extra}</div>`;
};
const loading = (n = 4) => `<div class="card">${'<div class="skeleton"></div>'.repeat(n)}</div>`;
const empty = (msg) => `<div class="state">${esc(msg)}</div>`;

// Bir bölgeyi yükleme → veri / boş / hata akışıyla doldurur.
async function section(el, loader, draw, emptyMsg = 'Kayıt yok') {
  el.innerHTML = loading();
  try {
    const data = await loader();
    const isEmpty = data == null || (Array.isArray(data) && data.length === 0) || (data.items && data.items.length === 0);
    el.innerHTML = isEmpty ? empty(emptyMsg) : draw(data);
  } catch (e) {
    el.innerHTML = errorBox(e);
  }
}

// Form gönderimi: düğmeyi kilitler, hatayı formun altında gösterir.
function onSubmit(form, handler) {
  form.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const btn = form.querySelector('button[type=submit]');
    const out = form.querySelector('.out') || form.appendChild(Object.assign(document.createElement('div'), { className: 'out' }));
    btn && (btn.disabled = true);
    out.innerHTML = '';
    try {
      const msg = await handler(Object.fromEntries(new FormData(form)), out);
      if (msg) out.innerHTML = `<div class="notice ok">${msg}</div>`;
    } catch (e) {
      out.innerHTML = errorBox(e);
    } finally {
      btn && (btn.disabled = false);
    }
  });
}

// Tablo içi eylem düğmeleri (data-act).
function onAct(root, handlers) {
  root.addEventListener('click', async (ev) => {
    const b = ev.target.closest('[data-act]');
    if (!b || !handlers[b.dataset.act]) return;
    b.disabled = true;
    try { await handlers[b.dataset.act](b.dataset, b); } catch (e) { toast(e.message, true); } finally { b.disabled = false; }
  });
}

// ---------- yönlendirme ----------
const ROUTES = {
  live: { title: 'Canlı pano', roles: ['nurse', 'lab', 'bank', 'admin'], view: viewLive },
  donors: { title: 'Bağışçılar', roles: ['nurse', 'bank', 'admin'], view: viewDonors },
  donor: { title: 'Bağışçı', roles: ['nurse', 'admin'], view: viewDonor, hidden: true },
  appointments: { title: 'Randevular', roles: ['nurse', 'admin'], view: viewAppointments },
  lab: { title: 'Laboratuvar', roles: ['lab', 'admin'], view: viewLab },
  stock: { title: 'Stok', roles: ['lab', 'bank', 'admin'], view: viewStock },
  requests: { title: 'Talepler', roles: ['bank', 'admin', 'hospital'], view: viewRequests },
  hproducts: { title: 'Ürünlerimiz', roles: ['hospital'], view: viewHospitalProducts },
  forecast: { title: 'Tahmin ve çağrı', roles: ['bank', 'admin'], view: viewForecast },
  trace: { title: 'Geri izleme', roles: ['bank', 'admin'], view: viewTrace },
  reports: { title: 'Raporlar', roles: ['bank', 'admin'], view: viewReports },
  admin: { title: 'Yönetim', roles: ['admin'], view: viewAdmin },
  me: { title: 'Bağışlarım', roles: ['donor'], view: viewMe },
};
const HOME = { donor: 'me', nurse: 'donors', lab: 'lab', bank: 'live', hospital: 'requests', admin: 'live' };
let refreshTimer = null;

function render() {
  clearInterval(refreshTimer);
  const s = session.get();
  const [route, arg] = location.hash.replace(/^#\/?/, '').split('/');
  if (!s) return viewLogin();
  const role = s.user.role;
  const name = ROUTES[route] && ROUTES[route].roles.includes(role) ? route : HOME[role];
  if (name !== route) { location.hash = `#/${name}`; return; }
  const nav = Object.entries(ROUTES).filter(([, r]) => !r.hidden && r.roles.includes(role))
    .map(([k, r]) => `<a href="#/${k}" class="${k === name || (name === 'donor' && k === 'donors') ? 'active' : ''}">${r.title}</a>`).join('');
  $app.innerHTML = `
    <header class="top"><div class="top-inner">
      <a class="logo" href="#/"><svg viewBox="0 0 32 32" aria-hidden="true"><path d="M16 3C16 3 6 14 6 20a10 10 0 0 0 20 0C26 14 16 3 16 3z" fill="#b4232c"/></svg>DamarBağış</a>
      <nav class="nav">${nav}</nav>
      <div class="who"><span class="live-dot" title="Sistem canlı"></span><span class="name">${esc(s.user.full_name)}</span>
        <span class="badge">${ROLE_TR[role]}</span><button class="small" id="logout">Çıkış</button></div>
    </div></header>
    <main id="main"></main>`;
  document.getElementById('logout').onclick = async () => {
    try { await api('POST', '/auth/logout', { refresh_token: s.refresh_token }, { idem: false }); } catch { /* yoksay */ }
    session.clear(); location.hash = '#/login'; render();
  };
  document.title = `${ROUTES[name].title} — DamarBağış`;
  ROUTES[name].view(document.getElementById('main'), arg, s.user);
}
window.addEventListener('hashchange', render);

// ---------- giriş ----------
function viewLogin() {
  $app.innerHTML = `
  <div class="login">
    <div class="card">
      <h1>DamarBağış</h1>
      <p class="sub">Bölge kan merkezi: bağışçı kabulü, test, üretim, stok ve hastane talepleri.</p>
      <form id="lf" class="grid" style="margin-top:1rem">
        <div class="field"><label for="em">E-posta</label><input id="em" name="email" type="email" autocomplete="username" required></div>
        <div class="field"><label for="pw">Parola</label><input id="pw" name="password" type="password" autocomplete="current-password" required></div>
        <button class="primary" type="submit">Giriş yap</button>
      </form>
    </div>
    <div class="card demo-list">
      <h3>Demo hesapları</h3>
      <p class="sub" style="margin-bottom:.6rem">Parola: <span class="mono">Demo1234!</span> — tıklayınca form dolar.</p>
      ${DEMO.map(([e, r]) => `<button type="button" data-email="${e}"><span>${r}</span><small>${e}</small></button>`).join('')}
    </div>
  </div>`;
  const f = document.getElementById('lf');
  document.querySelectorAll('[data-email]').forEach((b) => (b.onclick = () => { f.email.value = b.dataset.email; f.password.value = 'Demo1234!'; f.password.focus(); }));
  onSubmit(f, async (v) => {
    const t = await api('POST', '/auth/login', v, { idem: false });
    session.set(t);
    location.hash = `#/${HOME[t.user.role]}`;
    render();
  });
}

// ---------- canlı pano ----------
function stockMatrix(rows) {
  const comps = ['RBC', 'PLT', 'FFP'];
  let html = `<div class="matrix"><div class="h rh"></div>${GROUPS.map((g) => `<div class="h grp">${g}</div>`).join('')}`;
  for (const c of comps) {
    html += `<div class="h rh">${COMP_TR[c]}</div>`;
    for (const g of GROUPS) {
      const r = rows.find((x) => x.component === c && x.group === g) || { available: 0 };
      html += `<div class="cell ${r.alert || ''}" title="Rezerve ${r.reserved ?? 0} · Test bekleyen ${r.pending_test ?? 0} · 48 saatte dolacak ${r.expiring_48h ?? 0}">
        <b>${r.available}</b><small>${r.reserved ? `+${r.reserved} rez.` : '&nbsp;'}</small></div>`;
    }
  }
  return html + '</div>';
}

function viewLive(main) {
  main.innerHTML = `
    <div class="page-head"><div><h1>Canlı pano</h1><p class="sub">5 saniyede bir yenilenir. Simülatör sürekli olay üretir.</p></div></div>
    <div id="lc" class="grid g3"></div>
    <div class="card" style="margin-top:1rem"><h2>Stok matrisi (kullanılabilir ünite)</h2><div id="lm"></div>
      <p class="sub" style="margin-top:.5rem"><span class="badge b-warn">Düşük</span> <span class="badge b-bad">Kritik</span> stok alarmı, tahmini tüketime göre kaç gün yeteceğine bakılarak verilir.</p></div>
    <div class="grid g2" style="margin-top:1rem">
      <div class="card"><h2>Son ürün olayları</h2><div id="le"></div></div>
      <div class="card"><h2>Son kritik işlemler</h2><div id="la"></div></div>
    </div>`;
  const load = async (first) => {
    try {
      const [live, stock] = await Promise.all([api('GET', '/api/v1/live'), api('GET', '/api/v1/stock')]);
      const c = live.last_5_minutes;
      document.getElementById('lc').innerHTML = [
        ['Bağış', c.donations], ['Uygunsuz deneme', c.ineligible_attempts], ['Hastane talebi', c.requests], ['Dağıtılan ünite', c.issued],
      ].map(([k, v]) => `<div class="card stat"><b>${v}</b><span>${k} · son 5 dk</span></div>`).join('');
      document.getElementById('lm').innerHTML = stockMatrix(stock);
      document.getElementById('le').innerHTML = live.product_events.length ? `<ul class="feed">${live.product_events.map((e) =>
        `<li><time>${fmtTime(e.at)}</time><span class="mono">${esc(e.product_no)}</span><span class="grp">${esc(e.group)}</span>${esc(COMP_TR[e.component])} ${e.from ? badge(e.from) + ' → ' : ''}${badge(e.to)}</li>`).join('')}</ul>` : empty('Henüz olay yok');
      document.getElementById('la').innerHTML = live.actions.length ? `<ul class="feed">${live.actions.map((a) =>
        `<li><time>${fmtTime(a.at)}</time><span class="mono">${esc(a.action)}</span><span>${esc(a.entity)} ${esc(a.entity_id || '')}</span></li>`).join('')}</ul>` : empty('Henüz işlem yok');
    } catch (e) {
      if (first) main.querySelector('#lc').innerHTML = errorBox(e);
      else toast('Pano yenilenemedi: ' + e.message, true);
    }
  };
  ['lc', 'lm', 'le', 'la'].forEach((id) => (document.getElementById(id).innerHTML = loading(3)));
  load(true);
  refreshTimer = setInterval(load, 5000);
}

// ---------- bağışçılar ----------
function viewDonors(main, _arg, user) {
  const canCreate = ['nurse', 'admin'].includes(user.role);
  main.innerHTML = `
    <div class="page-head"><div><h1>Bağışçılar</h1><p class="sub">Ad, soyad ya da bağışçı no ile arayın.</p></div></div>
    <form id="sf" class="inline card">
      <div class="field" style="flex:1"><label>Arama</label><input name="q" placeholder="ör. Yılmaz ya da D0001234"></div>
      <div class="field"><label>Kan grubu</label><select name="group"><option value="">Tümü</option>${GROUPS.map((g) => `<option>${g}</option>`).join('')}</select></div>
      <button type="submit" class="primary">Ara</button>
    </form>
    <div id="dl" style="margin-top:1rem"></div>
    <div id="more"></div>
    ${canCreate ? `<div class="card" style="margin-top:1rem"><h2>Yeni bağışçı kaydı</h2>
      <form id="nf"><div class="form-grid">
        <div class="field"><label>Ad</label><input name="first_name" required maxlength="80"></div>
        <div class="field"><label>Soyad</label><input name="last_name" required maxlength="80"></div>
        <div class="field"><label>Cinsiyet</label><select name="sex"><option value="F">Kadın</option><option value="M">Erkek</option></select></div>
        <div class="field"><label>Doğum tarihi</label><input name="birth_date" type="date" required></div>
        <div class="field"><label>Kan grubu</label><select name="group">${GROUPS.map((g) => `<option>${g}</option>`).join('')}</select></div>
        <div class="field"><label>Telefon</label><input name="phone"></div>
        <div class="field"><label>Kullandığı ilaçlar (virgülle)</label><input name="meds" placeholder="şifreli saklanır"></div>
        <div class="field"><label>Kronik hastalıklar (virgülle)</label><input name="conds" placeholder="şifreli saklanır"></div>
      </div><div style="margin-top:.75rem"><button class="primary" type="submit">Kaydet</button></div></form></div>` : ''}`;
  let params = {};
  let cursor = null;
  const list = document.getElementById('dl');
  const draw = (items) => `<div class="table-wrap"><table><thead><tr><th>No</th><th>Ad soyad</th><th>Grup</th><th>Cinsiyet</th><th>Doğum</th><th></th></tr></thead><tbody>
    ${items.map((d) => `<tr><td class="mono">${esc(d.donor_no)}</td><td>${esc(d.first_name)} ${esc(d.last_name)}</td><td class="grp">${d.group}</td>
      <td>${d.sex === 'F' ? 'K' : 'E'}</td><td>${fmtDate(d.birth_date)}</td>
      <td>${canCreate ? `<a href="#/donor/${d.id}">Aç →</a>` : ''}</td></tr>`).join('')}</tbody></table></div>`;
  const load = async (append) => {
    const q = new URLSearchParams({ ...params, limit: 25, ...(append && cursor ? { cursor } : {}) });
    if (!append) list.innerHTML = loading();
    try {
      const res = await api('GET', `/api/v1/donors?${q}`);
      cursor = res.next_cursor;
      if (!append) list.innerHTML = res.items.length ? draw(res.items) : empty('Eşleşen bağışçı yok');
      else list.querySelector('tbody').insertAdjacentHTML('beforeend', draw(res.items).match(/<tbody>([\s\S]*)<\/tbody>/)[1]);
      document.getElementById('more').innerHTML = cursor ? '<button id="mb" style="margin-top:.5rem">Daha fazla</button>' : '';
      if (cursor) document.getElementById('mb').onclick = () => load(true);
    } catch (e) { list.innerHTML = errorBox(e); }
  };
  onSubmit(document.getElementById('sf'), async (v) => { params = Object.fromEntries(Object.entries(v).filter(([, x]) => x)); cursor = null; await load(false); });
  load(false);
  if (canCreate) onSubmit(document.getElementById('nf'), async (v) => {
    const split = (s) => (s || '').split(',').map((x) => x.trim()).filter(Boolean);
    const d = await api('POST', '/api/v1/donors', {
      first_name: v.first_name, last_name: v.last_name, sex: v.sex, birth_date: v.birth_date, abo: v.group.slice(0, -1),
      rh_positive: v.group.endsWith('+'), phone: v.phone, health: { medications: split(v.meds), conditions: split(v.conds) },
    });
    location.hash = `#/donor/${d.id}`;
    return `Kaydedildi: ${d.donor_no}`;
  });
}

function decisionBox(d) {
  if (d.eligible) return `<div class="notice ok"><b>Uygun.</b> Kural sürümü ${esc(d.rule_version)}.</div>`;
  return `<div class="notice bad"><b>Uygun değil.</b>${d.next_eligible_at ? ` En erken: <b>${fmtDate(d.next_eligible_at)}</b>.` : ' Kalıcı / ölçüm kaynaklı red.'}
    <ul class="reasons">${d.reasons.map((r) => `<li><span class="mono">${esc(r.code)}</span> — ${esc(r.message)}${r.until ? ` (bitiş ${fmtDate(r.until)})` : ''}</li>`).join('')}</ul>
    <small>Kural sürümü ${esc(d.rule_version || '—')}</small></div>`;
}

function viewDonor(main, id) {
  main.innerHTML = `<p><a href="#/donors">← Bağışçılar</a></p><div id="dh">${loading(2)}</div>
  <div class="grid g2" style="margin-top:1rem">
    <div class="card"><h2>Uygunluk kontrolü</h2>
      <form id="ef"><div class="form-grid">
        <div class="field"><label>Bağış türü</label><select name="donation_type"><option value="whole_blood">Tam kan</option><option value="platelet_apheresis">Trombosit aferezi</option></select></div>
        <div class="field"><label>Hemoglobin (g/dL)</label><input name="hemoglobin" type="number" step="0.1" value="14.0"></div>
        <div class="field"><label>Kilo (kg)</label><input name="weight_kg" type="number" step="0.1" value="70"></div>
        <div class="field"><label>Beyan (isteğe bağlı)</label><select name="decl"><option value="">Yok</option>
          <option value="travel_malaria">Sıtma bölgesine seyahat</option><option value="tattoo_piercing">Dövme / piercing</option><option value="antibiotic">Antibiyotik</option>
          <option value="aspirin">Aspirin</option><option value="dental">Diş tedavisi</option><option value="iv_drug_use">Damar içi madde</option></select></div>
        <div class="field"><label>Beyan tarihi</label><input name="decl_date" type="date" value="${isoDay()}"></div>
      </div>
      <div class="actions" style="margin-top:.75rem"><button class="primary" type="submit">Kontrol et</button><button type="button" id="rec">Bağışı kaydet</button></div>
      </form></div>
    <div class="card"><h2>Randevu</h2>
      <form id="af" class="inline"><div class="field"><label>Tarih-saat</label><input name="at" type="datetime-local" required></div>
        <div class="field"><label>Tür</label><select name="type"><option value="whole_blood">Tam kan</option><option value="platelet_apheresis">Trombosit</option></select></div>
        <button class="primary" type="submit">Randevu ver</button></form>
      <h2>Erteleme ekle</h2>
      <form id="xf" class="inline"><div class="field"><label>Neden</label><select name="reason_code">
        <option value="travel_malaria">Sıtma bölgesine seyahat (365 g)</option><option value="tattoo_piercing">Dövme / piercing (180 g)</option>
        <option value="surgery_major">Büyük cerrahi (180 g)</option><option value="antibiotic">Antibiyotik (14 g)</option><option value="isotretinoin">İzotretinoin (30 g)</option>
        <option value="dental">Diş tedavisi (7 g)</option><option value="iv_drug_use">Damar içi madde (kalıcı)</option></select></div>
        <div class="field"><label>Başlangıç</label><input name="starts_on" type="date" value="${isoDay()}"></div>
        <button type="submit">Ekle</button></form>
      <div id="dfl"></div></div>
  </div>
  <div class="card" style="margin-top:1rem"><h2>Sağlık beyanı (şifreli)</h2>
    <form id="hf" class="inline"><div class="field" style="flex:1"><label>Erişim gerekçesi (zorunlu, denetime yazılır)</label><input name="reason" minlength="10" required placeholder="ör. Bağış öncesi muayene ilaç kontrolü"></div>
    <button type="submit">Görüntüle</button></form><div id="hout"></div></div>
  <div class="card" style="margin-top:1rem"><h2>Bağış geçmişi</h2><div id="dn"></div></div>`;
  const drawHead = (d) => `<div class="card"><div class="page-head" style="margin:0"><div><h1>${esc(d.first_name)} ${esc(d.last_name)} <span class="grp">${d.group}</span></h1>
    <p class="sub"><span class="mono">${esc(d.donor_no)}</span> · ${d.sex === 'F' ? 'Kadın' : 'Erkek'} · Doğum ${fmtDate(d.birth_date)} · ${esc(d.phone || 'telefon yok')}</p></div></div></div>`;
  section(document.getElementById('dh'), () => api('GET', `/api/v1/donors/${id}`), drawHead);
  const loadDeferrals = () => section(document.getElementById('dfl'), () => api('GET', `/api/v1/donors/${id}/deferrals`), (rows) =>
    `<div class="table-wrap" style="margin-top:.6rem"><table><thead><tr><th>Neden</th><th>Başlangıç</th><th>Bitiş</th></tr></thead><tbody>
    ${rows.map((r) => `<tr><td class="mono">${esc(r.reason_code)}</td><td>${fmtDate(r.starts_on)}</td><td>${r.permanent ? '<span class="badge b-bad">Kalıcı</span>' : fmtDate(r.ends_on)}</td></tr>`).join('')}</tbody></table></div>`, 'Erteleme kaydı yok');
  const loadDonations = () => section(document.getElementById('dn'), () => api('GET', `/api/v1/donations?donor_id=${id}&limit=20`), (res) =>
    `<div class="table-wrap"><table><thead><tr><th>Torba</th><th>Tür</th><th>Tarih</th><th class="num">Hb</th><th>Test</th><th class="num">Ürün</th></tr></thead><tbody>
    ${res.items.map((d) => `<tr><td class="mono">${esc(d.bag_no)}</td><td>${d.donation_type === 'whole_blood' ? 'Tam kan' : 'Trombosit'}</td><td>${fmtDT(d.collected_at)}</td>
      <td class="num">${d.hemoglobin}</td><td>${d.test_outcome ? badge(d.test_outcome === 'positive' ? 'quarantined' : 'available').replace(/>[^<]+</, `>${d.test_outcome === 'positive' ? 'Pozitif' : 'Negatif'}<`) : badge('pending')}</td><td class="num">${d.products}</td></tr>`).join('')}</tbody></table></div>`, 'Henüz bağış yok');
  loadDeferrals(); loadDonations();
  const ef = document.getElementById('ef');
  const body = (v) => ({ donation_type: v.donation_type, measurements: { hemoglobin: +v.hemoglobin, weight_kg: +v.weight_kg },
    declarations: v.decl ? [{ code: v.decl, date: v.decl_date }] : [] });
  onSubmit(ef, async (v, out) => { const d = await api('POST', `/api/v1/donors/${id}/eligibility`, body(v)); out.innerHTML = decisionBox(d); });
  document.getElementById('rec').onclick = async (ev) => {
    const v = Object.fromEntries(new FormData(ef)); const out = ef.querySelector('.out') || ef.appendChild(Object.assign(document.createElement('div'), { className: 'out' }));
    ev.target.disabled = true;
    try {
      const b = body(v);
      const d = await api('POST', '/api/v1/donations', { donor_id: +id, donation_type: v.donation_type, volume_ml: v.donation_type === 'whole_blood' ? 450 : 250,
        hemoglobin: b.measurements.hemoglobin, weight_kg: b.measurements.weight_kg, declarations: b.declarations });
      out.innerHTML = `<div class="notice ok">Bağış kaydedildi: <span class="mono">${esc(d.bag_no)}</span>. Bileşen üretimi ve test laboratuvar ekranından yapılır.</div>`;
      loadDonations();
    } catch (e) { out.innerHTML = errorBox(e); } finally { ev.target.disabled = false; }
  };
  onSubmit(document.getElementById('af'), async (v) => {
    const a = await api('POST', '/api/v1/appointments', { donor_id: +id, scheduled_at: new Date(v.at).toISOString(), donation_type: v.type });
    return `Randevu oluşturuldu: ${fmtDT(a.scheduled_at)}`;
  });
  onSubmit(document.getElementById('xf'), async (v) => { await api('POST', `/api/v1/donors/${id}/deferrals`, v); loadDeferrals(); return 'Erteleme eklendi'; });
  onSubmit(document.getElementById('hf'), async (v, out) => {
    const h = await api('GET', `/api/v1/donors/${id}/health`, undefined, { headers: { 'X-Access-Reason': v.reason } });
    out.innerHTML = `<div class="notice info"><b>İlaçlar:</b> ${esc(h.medications.join(', ') || 'yok')}<br><b>Hastalıklar:</b> ${esc(h.conditions.join(', ') || 'yok')}${h.notes ? `<br><b>Not:</b> ${esc(h.notes)}` : ''}<br><small>Bu erişim gerekçesiyle denetim kaydına yazıldı.</small></div>`;
  });
}

function viewAppointments(main) {
  main.innerHTML = `<div class="page-head"><div><h1>Randevular</h1></div>
    <form id="df" class="inline"><div class="field"><label>Gün</label><input type="date" name="date" value="${isoDay()}"></div><button type="submit">Göster</button></form></div><div id="al"></div>`;
  const load = (date) => section(document.getElementById('al'), () => api('GET', `/api/v1/appointments?date=${date}`), (rows) =>
    `<div class="table-wrap"><table><thead><tr><th>Saat</th><th>Bağışçı</th><th>Tür</th><th>Durum</th><th></th></tr></thead><tbody>
    ${rows.map((a) => `<tr><td>${fmtDT(a.scheduled_at)}</td><td><a href="#/donor/${a.donor_id}">${esc(a.donor_name)}</a></td><td>${a.donation_type === 'whole_blood' ? 'Tam kan' : 'Trombosit'}</td>
      <td>${badge(a.status)}</td><td>${a.status === 'booked' ? `<button class="small" data-act="cancel" data-id="${a.id}">İptal</button>` : ''}</td></tr>`).join('')}</tbody></table></div>`, 'Bu gün için randevu yok');
  onSubmit(document.getElementById('df'), async (v) => load(v.date));
  onAct(document.getElementById('al'), { cancel: async (d) => { await api('POST', `/api/v1/appointments/${d.id}/cancel`, {}); toast('Randevu iptal edildi'); load(document.querySelector('#df [name=date]').value); } });
  load(isoDay());
}

// ---------- laboratuvar ----------
function viewLab(main) {
  main.innerHTML = `<div class="page-head"><div><h1>Laboratuvar</h1><p class="sub">Pozitif sonuç girildiğinde bağışın tüm türev ürünleri (bölünmüşler dahil) karantinaya alınır, bağışçı kalıcı reddedilir.</p></div></div>
    <div class="grid g2">
      <div class="card"><h2>Bileşen üretimi bekleyen bağışlar</h2><div id="pc"></div></div>
      <div class="card"><h2>Test sonucu bekleyen bağışlar</h2><div id="pt"></div></div>
    </div>
    <div class="card" style="margin-top:1rem"><h2>Test sonucu düzeltme (çift onay)</h2>
      <p class="sub">Talebi açan uzman onaylayamaz; ikinci bir laboratuvar uzmanı onaylar.</p>
      <form id="cf"><div class="form-grid">
        <div class="field"><label>Bağış no (id)</label><input name="donation_id" type="number" required></div>
        ${['hiv', 'hbv', 'hcv', 'syphilis'].map((m) => `<div class="field"><label>${m.toUpperCase()}</label><select name="${m}"><option value="false">Negatif</option><option value="true">Pozitif</option></select></div>`).join('')}
        <div class="field" style="grid-column:1/-1"><label>Gerekçe</label><input name="reason" required minlength="5"></div>
      </div><button class="primary" type="submit" style="margin-top:.6rem">Talep oluştur</button></form>
      <h3>Bekleyen talepler</h3><div id="cl"></div></div>`;
  const loadPC = () => section(document.getElementById('pc'), () => api('GET', '/api/v1/donations?pending=components&limit=20'), (res) =>
    `<div class="table-wrap"><table><thead><tr><th>Torba</th><th>Tür</th><th>Toplama</th><th></th></tr></thead><tbody>${res.items.map((d) =>
      `<tr><td class="mono">${esc(d.bag_no)}</td><td>${d.donation_type === 'whole_blood' ? 'Tam kan' : 'Trombosit'}</td><td>${fmtDT(d.collected_at)}</td>
      <td><button class="small primary" data-act="produce" data-id="${d.id}">Üret</button></td></tr>`).join('')}</tbody></table></div>`, 'Üretim bekleyen bağış yok');
  const loadPT = () => section(document.getElementById('pt'), () => api('GET', '/api/v1/donations?pending=tests&limit=20'), (res) =>
    `<div class="table-wrap"><table><thead><tr><th>Torba</th><th>Toplama</th><th class="num">Ürün</th><th>Sonuç</th></tr></thead><tbody>${res.items.map((d) =>
      `<tr><td class="mono">${esc(d.bag_no)} <small>#${d.id}</small></td><td>${fmtDT(d.collected_at)}</td><td class="num">${d.products}</td>
      <td class="actions"><button class="small" data-act="neg" data-id="${d.id}">Negatif</button>
      <select data-id="${d.id}" class="pos-sel"><option value="">Pozitif…</option><option>hiv</option><option>hbv</option><option>hcv</option><option>syphilis</option></select></td></tr>`).join('')}</tbody></table></div>`, 'Test bekleyen bağış yok');
  const loadCL = () => section(document.getElementById('cl'), () => api('GET', '/api/v1/test-changes?status=pending'), (rows) =>
    `<div class="table-wrap"><table><thead><tr><th>Talep</th><th>Bağış</th><th>Önerilen</th><th>Gerekçe</th><th>Açan</th><th></th></tr></thead><tbody>${rows.map((c) =>
      `<tr><td>#${c.id}</td><td>#${c.donation_id}</td><td class="mono">${Object.entries(c.proposed).filter(([, v]) => v).map(([k]) => k).join(', ') || 'tümü negatif'}</td>
      <td>${esc(c.reason)}</td><td>kullanıcı ${c.requested_by}</td>
      <td class="actions"><button class="small primary" data-act="approve" data-id="${c.id}">Onayla</button><button class="small" data-act="reject" data-id="${c.id}">Reddet</button></td></tr>`).join('')}</tbody></table></div>`, 'Bekleyen talep yok');
  loadPC(); loadPT(); loadCL();
  const result = async (id, m) => {
    const r = await api('POST', `/api/v1/donations/${id}/test-results`, { hiv: false, hbv: false, hcv: false, syphilis: false, ...m });
    toast(r.outcome === 'positive' ? `Pozitif: ${r.quarantined.length} ürün karantinada, ${r.recalls.length} geri çağırma, look-back ${r.lookback_donations.length} bağış` : `Negatif: ${r.released} ürün stoğa alındı`, r.outcome === 'positive');
    loadPT();
  };
  onAct(main, {
    produce: async (d) => { const ps = await api('POST', `/api/v1/donations/${d.id}/components`, {}); toast(`${ps.length} ürün üretildi`); loadPC(); },
    neg: (d) => result(d.id, {}),
    approve: async (d) => { const r = await api('POST', `/api/v1/test-changes/${d.id}/approve`, {}); toast(`Onaylandı: sonuç ${r.effect.outcome}`); loadCL(); },
    reject: async (d) => { await api('POST', `/api/v1/test-changes/${d.id}/reject`, {}); toast('Reddedildi'); loadCL(); },
  });
  main.addEventListener('change', (ev) => {
    if (ev.target.classList.contains('pos-sel') && ev.target.value && confirm(`Bağış #${ev.target.dataset.id} için ${ev.target.value.toUpperCase()} pozitif girilsin mi? Tüm türev ürünler karantinaya alınır.`)) {
      result(ev.target.dataset.id, { [ev.target.value]: true }).catch((e) => toast(e.message, true));
    }
  });
  onSubmit(document.getElementById('cf'), async (v) => {
    const proposed = Object.fromEntries(['hiv', 'hbv', 'hcv', 'syphilis'].map((m) => [m, v[m] === 'true']));
    const c = await api('POST', `/api/v1/donations/${v.donation_id}/test-changes`, { proposed, reason: v.reason });
    loadCL();
    return `Talep #${c.id} oluşturuldu; ikinci onay bekleniyor.`;
  });
}

// ---------- stok ----------
function viewStock(main, _a, user) {
  const isBank = user.role === 'bank';
  main.innerHTML = `<div class="page-head"><div><h1>Stok</h1><p class="sub">Varsayılan sıralama SKT'ye göre (FEFO).</p></div></div>
    <div class="card"><div id="sm"></div></div>
    <form id="pf" class="inline card" style="margin-top:1rem">
      <div class="field"><label>Durum</label><select name="status"><option value="available">Stokta</option><option value="reserved">Rezerve</option><option value="pending_test">Test bekliyor</option>
        <option value="quarantined">Karantina</option><option value="issued">Dağıtıldı</option><option value="expired">SKT doldu</option><option value="">Tümü</option></select></div>
      <div class="field"><label>Bileşen</label><select name="component"><option value="">Tümü</option><option value="RBC">Eritrosit</option><option value="PLT">Trombosit</option><option value="FFP">Plazma</option></select></div>
      <div class="field"><label>Grup</label><select name="group"><option value="">Tümü</option>${GROUPS.map((g) => `<option>${g}</option>`).join('')}</select></div>
      <button type="submit" class="primary">Listele</button>
    </form>
    <div id="pl" style="margin-top:1rem"></div>
    <div class="card" style="margin-top:1rem"><h2>İmzalı barkod doğrulama</h2>
      <form id="bf" class="inline"><div class="field" style="flex:1"><label>Barkod</label><input name="code" placeholder="P00001234.ABCDEFGH12" required></div><button type="submit">Doğrula</button></form></div>`;
  section(document.getElementById('sm'), () => api('GET', '/api/v1/stock'), stockMatrix);
  const load = (v) => {
    const q = new URLSearchParams({ ...Object.fromEntries(Object.entries(v).filter(([, x]) => x)), sort: 'expiry', limit: 100 });
    return section(document.getElementById('pl'), () => api('GET', `/api/v1/products?${q}`), (res) =>
      `<div class="table-wrap"><table><thead><tr><th>Ürün</th><th>Bileşen</th><th>Grup</th><th class="num">ml</th><th>SKT</th><th>Durum</th><th>Barkod</th><th></th></tr></thead><tbody>${res.items.map((p) =>
        `<tr><td class="mono">${esc(p.product_no)}${p.parent_id ? ' <small>(alt)</small>' : ''}</td><td>${COMP_TR[p.component]}</td><td class="grp">${grp(p.abo, p.rh_positive)}</td>
        <td class="num">${p.volume_ml}</td><td>${fmtDT(p.expires_at)}</td><td>${badge(p.status)}</td><td class="mono"><small>${esc(p.barcode)}</small></td>
        <td class="actions">${isBank && p.status === 'available' && p.component !== 'FFP' ? `<button class="small" data-act="split" data-id="${p.id}">Böl</button>` : ''}
        ${['available', 'quarantined', 'pending_test'].includes(p.status) ? `<button class="small" data-act="discard" data-id="${p.id}">İmha</button>` : ''}</td></tr>`).join('')}</tbody></table></div>`, 'Bu filtreyle ürün yok');
  };
  const pf = document.getElementById('pf');
  onSubmit(pf, async (v) => load(v));
  load(Object.fromEntries(new FormData(pf)));
  onAct(document.getElementById('pl'), {
    split: async (d) => { const k = await api('POST', `/api/v1/products/${d.id}/split`, { parts: 2 }); toast(`${k.length} alt ürün oluşturuldu`); load(Object.fromEntries(new FormData(pf))); },
    discard: async (d) => { const r = prompt('İmha gerekçesi:'); if (!r) return; await api('POST', `/api/v1/products/${d.id}/discard`, { reason: r }); toast('İmha edildi'); load(Object.fromEntries(new FormData(pf))); },
  });
  onSubmit(document.getElementById('bf'), async (v, out) => {
    const r = await api('GET', `/api/v1/products/verify?code=${encodeURIComponent(v.code.trim())}`);
    out.innerHTML = r.valid ? `<div class="notice ok">İmza geçerli: <b>${esc(r.product_no)}</b> · ${COMP_TR[r.component]} <span class="grp">${esc(r.group)}</span> · ${badge(r.status)} · SKT ${fmtDT(r.expires_at)}</div>`
      : `<div class="notice bad">İmza geçersiz${r.reason ? ': ' + esc(r.reason) : ''}. Ürün kullanılmamalı.</div>`;
  });
}

// ---------- talepler ----------
function viewRequests(main, _a, user) {
  const isHosp = user.role === 'hospital';
  const isBank = user.role === 'bank';
  main.innerHTML = `<div class="page-head"><div><h1>${isHosp ? 'Kan talepleri' : 'Hastane talepleri'}</h1>
    <p class="sub">Tahsis: önce tam uyum, sonra uyumlu alternatifler; her kademede en erken SKT (FEFO). Çok acil talepler rutin rezervasyonları devralabilir.</p></div>
    <form id="ff" class="inline"><div class="field"><label>Durum</label><select name="status"><option value="open,partial,reserved">Bekleyen</option><option value="issued">Dağıtılan</option><option value="cancelled">İptal</option><option value="">Tümü</option></select></div><button type="submit">Filtrele</button></form></div>
    ${isHosp ? `<div class="card"><h2>Yeni talep</h2><form id="rf"><div class="form-grid">
      <div class="field"><label>Bileşen</label><select name="component"><option value="RBC">Eritrosit</option><option value="PLT">Trombosit</option><option value="FFP">Plazma</option></select></div>
      <div class="field"><label>Hasta kan grubu</label><select name="group">${GROUPS.map((g) => `<option ${g === 'A+' ? 'selected' : ''}>${g}</option>`).join('')}</select></div>
      <div class="field"><label>Ünite</label><input name="units" type="number" min="1" max="20" value="1"></div>
      <div class="field"><label>Aciliyet</label><select name="urgency"><option value="routine">Rutin</option><option value="urgent">Acil</option><option value="emergency">Çok acil</option></select></div>
      <div class="field"><label>Hasta referansı (takma ad)</label><input name="patient_ref" required minlength="3" placeholder="ör. PRT-2026-0042"></div>
    </div><button class="primary" type="submit" style="margin-top:.6rem">Talep gönder</button></form></div>` : ''}
    <div id="rl" style="margin-top:1rem"></div>`;
  const ff = document.getElementById('ff');
  const load = () => {
    const st = new FormData(ff).get('status');
    return section(document.getElementById('rl'), () => api('GET', `/api/v1/requests?limit=50${st ? `&status=${st}` : ''}`), (res) =>
      `<div class="table-wrap"><table><thead><tr><th>#</th>${isHosp ? '' : '<th>Hastane</th>'}<th>Bileşen</th><th>Grup</th><th class="num">Ünite</th><th>Aciliyet</th><th>Hasta</th><th>Durum</th><th>Rezervasyon / ürün</th><th></th></tr></thead><tbody>
      ${res.items.map((q) => `<tr><td>${q.id}</td>${isHosp ? '' : `<td>${esc(q.hospital_name)}</td>`}<td>${COMP_TR[q.component]}</td><td class="grp">${q.group}</td><td class="num">${q.units}</td>
        <td>${q.urgency === 'emergency' ? '<span class="badge b-bad">Çok acil</span>' : q.urgency === 'urgent' ? '<span class="badge b-warn">Acil</span>' : 'Rutin'}</td>
        <td class="mono"><small>${esc(q.patient_ref)}</small></td><td>${badge(q.status)}</td>
        <td>${q.reservations.filter((r) => ['active', 'issued'].includes(r.status)).map((r) => `<div><span class="mono">${esc(r.product_no)}</span> <span class="grp">${r.group}</span>${r.exact_match ? '' : ' <span class="badge b-info">alternatif</span>'} ${badge(r.status)}${r.status === 'active' ? ` <small>→ ${fmtTime(r.hold_until)}</small>` : ''}</div>`).join('') || '—'}</td>
        <td class="actions">${isBank && ['open', 'partial'].includes(q.status) ? `<button class="small" data-act="alloc" data-id="${q.id}">Tahsis</button>` : ''}
          ${isBank && ['reserved', 'partial'].includes(q.status) ? `<button class="small primary" data-act="issue" data-id="${q.id}">Dağıt</button>` : ''}
          ${['open', 'partial', 'reserved'].includes(q.status) ? `<button class="small" data-act="cancel" data-id="${q.id}">İptal</button>` : ''}</td></tr>`).join('')}
      </tbody></table></div>`, 'Bu filtreyle talep yok');
  };
  onSubmit(ff, async () => load());
  load();
  onAct(document.getElementById('rl'), {
    alloc: async (d) => { const r = await api('POST', `/api/v1/requests/${d.id}/allocate`, {}); toast(`${r.reserved_product_ids.length} ürün rezerve edildi${r.missing_units ? `, ${r.missing_units} eksik` : ''}`); load(); },
    issue: async (d) => { await api('POST', `/api/v1/requests/${d.id}/issue`, {}); toast('Dağıtıldı'); load(); },
    cancel: async (d) => { if (!confirm(`Talep #${d.id} iptal edilsin mi?`)) return; await api('POST', `/api/v1/requests/${d.id}/cancel`, {}); toast('İptal edildi'); load(); },
  });
  if (isHosp) onSubmit(document.getElementById('rf'), async (v) => {
    const q = await api('POST', '/api/v1/requests', { ...v, units: +v.units });
    load();
    const got = q.reservations.filter((r) => r.status === 'active').length;
    return `Talep #${q.id}: ${got}/${q.units} ünite rezerve edildi (${STATUS_TR[q.status]}).`;
  });
}

function viewHospitalProducts(main) {
  main.innerHTML = `<div class="page-head"><div><h1>Hastanemize dağıtılan ürünler</h1><p class="sub">Bağışçı kimliği gösterilmez; yalnızca ürün bilgisi.</p></div></div>
    <div class="card"><h2>Transfüzyon kaydı</h2><form id="tf" class="inline">
      <div class="field"><label>Ürün no</label><input name="product_no" required placeholder="P00001234"></div>
      <div class="field"><label>Hasta referansı</label><input name="patient_ref" required minlength="3"></div><button class="primary" type="submit">Kaydet</button></form></div>
    <div id="hl" style="margin-top:1rem"></div>`;
  const load = () => section(document.getElementById('hl'), () => api('GET', '/api/v1/hospital/products'), (rows) =>
    `<div class="table-wrap"><table><thead><tr><th>Ürün</th><th>Bileşen</th><th>Grup</th><th>SKT</th><th>Durum</th><th>Talep</th><th>Hasta</th><th></th></tr></thead><tbody>${rows.map((p) =>
      `<tr><td class="mono">${esc(p.product_no)}</td><td>${COMP_TR[p.component]}</td><td class="grp">${p.group}</td><td>${fmtDT(p.expires_at)}</td><td>${badge(p.status)}</td>
      <td>#${p.request_id}</td><td class="mono"><small>${esc(p.patient_ref || '')}</small></td>
      <td>${p.status === 'issued' ? `<button class="small" data-act="fill" data-no="${esc(p.product_no)}">Transfüzyon</button>` : ''}</td></tr>`).join('')}</tbody></table></div>`, 'Henüz dağıtılan ürün yok');
  load();
  const tf = document.getElementById('tf');
  onAct(document.getElementById('hl'), { fill: async (d) => { tf.product_no.value = d.no; tf.patient_ref.focus(); } });
  onSubmit(tf, async (v) => { await api('POST', '/api/v1/transfusions', v); load(); return 'Transfüzyon kaydedildi'; });
}

// ---------- tahmin ----------
function viewForecast(main) {
  main.innerHTML = `<div class="page-head"><div><h1>Stok tahmini ve çağrı listesi</h1><p class="sub">28 günlük hareketli ortalama × haftanın günü çarpanı. SKT kaybı FEFO varsayımıyla simüle edilir.</p></div>
    <form id="cf" class="inline"><div class="field"><label>Bileşen</label><select name="component"><option value="RBC">Eritrosit</option><option value="PLT">Trombosit</option><option value="FFP">Plazma</option></select></div><button type="submit">Hesapla</button></form></div>
    <div class="grid g2"><div class="card"><h2>Açık alarmlar</h2><div id="al"></div></div><div class="card"><h2>Geriye dönük test (son 28 gün)</h2><div id="bt"></div></div></div>
    <div class="card" style="margin-top:1rem"><h2>7 günlük tahmin</h2><div id="fc"></div></div>
    <div class="card" style="margin-top:1rem"><h2>Çağrı listesi</h2>
      <form id="lf" class="inline"><div class="field"><label>Grup</label><select name="group"><option value="">Alarmdaki gruplar</option>${GROUPS.map((g) => `<option>${g}</option>`).join('')}</select></div><button type="submit">Listele</button></form>
      <div id="cl" style="margin-top:.6rem"></div></div>`;
  const load = (comp) => {
    section(document.getElementById('fc'), () => api('GET', `/api/v1/forecast?component=${comp}&horizon=7`), (r) => {
      const days = r.groups[0].forecast.map((_, i) => { const d = new Date(); d.setDate(d.getDate() + i); return d.toLocaleDateString('tr-TR', { weekday: 'short' }); });
      return `<div class="table-wrap"><table><thead><tr><th>Grup</th><th class="num">Stok</th><th class="num">Ort./gün</th>${days.map((d) => `<th class="num">${d}</th>`).join('')}<th class="num">Yeterlilik (gün)</th><th class="num">SKT kaybı</th><th></th></tr></thead><tbody>
      ${r.groups.map((g) => `<tr><td class="grp">${g.group}</td><td class="num">${g.available}</td><td class="num">${g.avg_daily_28d}</td>${g.forecast.map((v) => `<td class="num">${v.toFixed(1)}</td>`).join('')}
        <td class="num"><b>${g.days_of_supply >= 999 ? '∞' : g.days_of_supply}</b></td><td class="num">${g.expected_expiry_loss}</td>
        <td>${g.level === 'critical' ? '<span class="badge b-bad">Kritik</span>' : g.level === 'low' ? '<span class="badge b-warn">Düşük</span>' : ''}</td></tr>`).join('')}</tbody></table></div>`;
    });
    section(document.getElementById('bt'), () => api('GET', `/api/v1/forecast/backtest?component=${comp}&days=28`), (r) =>
      `<div class="grid g3"><div class="stat"><b>${r.total.mape}%</b><span>MAPE (toplam)</span></div><div class="stat"><b>${r.total.wape}%</b><span>WAPE (toplam)</span></div><div class="stat"><b>${r.total.bias}</b><span>Ortalama sapma (ünite/gün)</span></div></div>
      <div class="table-wrap" style="margin-top:.6rem"><table><thead><tr><th>Grup</th><th class="num">MAPE</th><th class="num">WAPE</th></tr></thead><tbody>${Object.entries(r.groups).map(([g, x]) => `<tr><td class="grp">${g}</td><td class="num">${x.mape}%</td><td class="num">${x.wape}%</td></tr>`).join('')}</tbody></table></div>
      <p class="sub" style="margin-top:.4rem">Düşük hacimli gruplarda MAPE yüksek çıkar (birkaç ünitelik hata büyük yüzdedir); bu yüzden WAPE de raporlanır.</p>`);
  };
  const loadAlerts = () => section(document.getElementById('al'), () => api('GET', '/api/v1/alerts'), (rows) =>
    `<div class="table-wrap"><table><thead><tr><th>Bileşen</th><th>Grup</th><th>Seviye</th><th class="num">Stok</th><th class="num">Gün</th></tr></thead><tbody>${rows.map((a) =>
      `<tr><td>${COMP_TR[a.component]}</td><td class="grp">${a.group}</td><td>${a.level === 'critical' ? '<span class="badge b-bad">Kritik</span>' : '<span class="badge b-warn">Düşük</span>'}</td><td class="num">${a.available_units}</td><td class="num">${a.days_of_supply}</td></tr>`).join('')}</tbody></table></div>`, 'Açık alarm yok');
  onSubmit(document.getElementById('cf'), async (v) => load(v.component));
  onSubmit(document.getElementById('lf'), async (v) => section(document.getElementById('cl'), () => api('GET', `/api/v1/call-list?limit=30${v.group ? `&group=${encodeURIComponent(v.group)}` : ''}`), (m) => {
    const entries = Object.entries(m);
    if (!entries.length) return empty('Alarmda grup yok; bir grup seçin.');
    return entries.map(([g, list]) => `<h3><span class="grp">${g}</span> · ${list.length} uygun bağışçı</h3>${list.length ? `<div class="table-wrap"><table><thead><tr><th>No</th><th>Ad</th><th>Telefon</th><th>Son bağış</th></tr></thead><tbody>
      ${list.map((c) => `<tr><td class="mono">${esc(c.donor_no)}</td><td>${esc(c.name)}</td><td>${esc(c.phone || '')}</td><td>${fmtDate(c.last_donation)}</td></tr>`).join('')}</tbody></table></div>` : empty('Uygun bağışçı yok')}`).join('');
  }));
  load('RBC'); loadAlerts();
}

// ---------- izleme ----------
function viewTrace(main) {
  main.innerHTML = `<div class="page-head"><div><h1>Geri izlenebilirlik</h1><p class="sub">Bağışçıdan hastaya ve hastadan bağışçıya (look-back). Bölünmüş ürünler recursive sorguyla izlenir.</p></div></div>
    <div class="grid g2">
      <div class="card"><h2>Bağışçı → hasta</h2><form id="ff" class="inline"><div class="field"><label>Bağışçı id</label><input name="id" type="number" required></div><button type="submit" class="primary">İzle</button></form><div id="fo"></div></div>
      <div class="card"><h2>Hasta → bağışçı</h2><form id="bf" class="inline"><div class="field"><label>Hasta referansı</label><input name="ref" required></div><button type="submit" class="primary">İzle</button></form><div id="bo"></div></div>
    </div>`;
  onSubmit(document.getElementById('ff'), async (v, out) => {
    const r = await api('GET', `/api/v1/trace/donors/${v.id}`);
    out.innerHTML = `<p><b>${esc(r.donor.donor_no)}</b> · ${r.products.length} ürün · hastalar: ${r.patients.map(esc).join(', ') || '—'}</p>
      <div class="table-wrap"><table><thead><tr><th>Ürün</th><th>Bileşen</th><th>Durum</th><th>Hastane</th><th>Hasta</th></tr></thead><tbody>${r.products.map((p) =>
        `<tr><td class="mono">${'&nbsp;&nbsp;'.repeat(p.depth)}${p.depth ? '└ ' : ''}${esc(p.product_no)}</td><td>${COMP_TR[p.component]}</td><td>${badge(p.status)}</td><td>${esc(p.hospital || '')}</td><td class="mono">${esc(p.patient_ref || '')}</td></tr>`).join('')}</tbody></table></div>`;
  });
  onSubmit(document.getElementById('bf'), async (v, out) => {
    const r = await api('GET', `/api/v1/trace/patients/${encodeURIComponent(v.ref)}`);
    out.innerHTML = `<div class="table-wrap"><table><thead><tr><th>Transfüze ürün</th><th>Kök ürün</th><th class="num">Adım</th><th>Bağış</th><th>Bağışçı</th></tr></thead><tbody>${r.links.map((l) =>
      `<tr><td class="mono">${esc(l.transfused_product_no)}</td><td class="mono">${esc(l.root_product_no)}</td><td class="num">${l.hops}</td><td>#${l.donation_id} · ${fmtDate(l.collected_at)}</td><td class="mono">${esc(l.donor_no)}</td></tr>`).join('')}</tbody></table></div>
      <p class="sub">Aynı bağışçılardan ürün alan diğer hastalar: ${r.other_recipients_of_same_donors.map(esc).join(', ') || 'yok'}</p>`;
  });
}

// ---------- raporlar ----------
function viewReports(main) {
  const from = new Date(); from.setDate(from.getDate() - 30);
  const to = new Date(); to.setDate(to.getDate() + 1);
  main.innerHTML = `<div class="page-head"><div><h1>Dağıtım ve raporlar</h1></div>
    <form id="rf" class="inline"><div class="field"><label>Başlangıç</label><input type="date" name="from" value="${isoDay(from)}"></div><div class="field"><label>Bitiş</label><input type="date" name="to" value="${isoDay(to)}"></div><button type="submit">Göster</button></form></div><div id="ro"></div>`;
  const load = (v) => section(document.getElementById('ro'), () => api('GET', `/api/v1/reports/summary?from=${v.from}&to=${v.to}`), (r) => {
    const kv = (o, tr = (x) => x) => Object.entries(o).map(([k, v]) => `<tr><td>${esc(tr(k))}</td><td class="num">${v}</td></tr>`).join('') || '<tr><td colspan="2">—</td></tr>';
    const t = (title, body) => `<div class="card"><h3>${title}</h3><div class="table-wrap"><table><tbody>${body}</tbody></table></div></div>`;
    return `<div class="grid g3">
      <div class="card stat"><b>${Object.values(r.donations).reduce((a, b) => a + b, 0)}</b><span>Bağış</span></div>
      <div class="card stat"><b>${r.tests_total ? ((100 * r.tests_positive) / r.tests_total).toFixed(2) : 0}%</b><span>Pozitif test oranı (${r.tests_positive}/${r.tests_total})</span></div>
      <div class="card stat"><b>${r.ineligible_attempts}</b><span>Uygunsuz bağış denemesi</span></div>
      <div class="card stat"><b>${r.emergency_median_fill_minutes != null ? r.emergency_median_fill_minutes.toFixed(1) + ' dk' : '—'}</b><span>Çok acil talepte medyan karşılama</span></div>
    </div><div class="grid g3" style="margin-top:1rem">
      ${t('Üretilen', kv(r.produced, (k) => COMP_TR[k]))}${t('Dağıtılan', kv(r.issued, (k) => COMP_TR[k]))}${t('SKT kaybı', kv(r.expired, (k) => COMP_TR[k]))}
      ${t('Talepler', kv(r.requests_by_status, (k) => STATUS_TR[k] || k))}
      ${t('Hastanelere dağıtım', r.issued_by_hospital.map((h) => `<tr><td>${esc(h.hospital)}</td><td class="num">${h.units}</td></tr>`).join('') || '<tr><td>—</td></tr>')}
    </div>`;
  });
  const rf = document.getElementById('rf');
  onSubmit(rf, async (v) => load(v));
  load(Object.fromEntries(new FormData(rf)));
}

// ---------- yönetim ----------
function viewAdmin(main) {
  main.innerHTML = `<div class="page-head"><div><h1>Yönetim</h1></div></div>
    <div class="grid g2"><div class="card"><h2>Kullanıcılar</h2><div id="ul"></div>
      <h3>Yeni kullanıcı</h3><form id="uf"><div class="form-grid">
        <div class="field"><label>E-posta</label><input name="email" type="email" required></div><div class="field"><label>Ad soyad</label><input name="full_name" required></div>
        <div class="field"><label>Parola</label><input name="password" type="password" minlength="8" required></div>
        <div class="field"><label>Rol</label><select name="role">${Object.entries(ROLE_TR).filter(([k]) => k !== 'donor').map(([k, v]) => `<option value="${k}">${v}</option>`).join('')}</select></div>
        <div class="field"><label>Hastane id (rol hastane ise)</label><input name="hospital_id" type="number"></div></div>
        <button type="submit" class="primary" style="margin-top:.6rem">Ekle</button></form></div>
    <div class="card"><h2>Uygunluk kural sürümleri</h2><p class="sub">Yeni sürüm yalnızca ileri bir tarihten geçerli olur; o güne kadar mevcut sürüm uygulanır.</p><div id="rl"></div>
      <form id="vf" style="margin-top:.6rem"><div class="field"><label>Geçerlilik başlangıcı</label><input type="date" name="valid_from" required></div>
      <div class="field"><label>Parametreler (JSON)</label><textarea name="params" rows="8"></textarea></div><div class="field"><label>Not</label><input name="note"></div>
      <button type="submit" class="primary" style="margin-top:.5rem">Sürüm ekle</button></form></div></div>
    <div class="card" style="margin-top:1rem"><h2>Denetim kaydı (eklemeli-yalnız)</h2>
      <form id="af" class="inline"><div class="field"><label>İşlem</label><input name="action" placeholder="ör. donor.health.read"></div><button type="submit">Filtrele</button></form><div id="aul" style="margin-top:.6rem"></div><div id="amore"></div></div>`;
  section(document.getElementById('ul'), () => api('GET', '/api/v1/users'), (rows) => `<div class="table-wrap"><table><thead><tr><th>E-posta</th><th>Rol</th><th>Ad</th></tr></thead><tbody>${rows.map((u) =>
    `<tr><td class="mono"><small>${esc(u.email)}</small></td><td>${ROLE_TR[u.role]}</td><td>${esc(u.full_name)}</td></tr>`).join('')}</tbody></table></div>`);
  const loadRules = () => section(document.getElementById('rl'), () => api('GET', '/api/v1/rules?code=eligibility'), (rows) => {
    const vf = document.getElementById('vf');
    if (!vf.params.value) vf.params.value = JSON.stringify(rows[rows.length - 1].params, null, 2);
    return `<div class="table-wrap"><table><thead><tr><th>Başlangıç</th><th>Bitiş</th><th>Not</th></tr></thead><tbody>${rows.map((r) => `<tr><td>${fmtDate(r.valid_from)}</td><td>${r.valid_to ? fmtDate(r.valid_to) : 'açık'}</td><td>${esc(r.note || '')}</td></tr>`).join('')}</tbody></table></div>`;
  });
  loadRules();
  onSubmit(document.getElementById('uf'), async (v) => { const u = await api('POST', '/api/v1/users', { ...v, hospital_id: v.hospital_id ? +v.hospital_id : null }); return `Eklendi: ${u.email}`; });
  onSubmit(document.getElementById('vf'), async (v) => {
    let params; try { params = JSON.parse(v.params); } catch { throw new Error('Parametreler geçerli JSON değil'); }
    await api('POST', '/api/v1/rules', { code: 'eligibility', valid_from: v.valid_from, note: v.note, params }); loadRules(); return 'Sürüm eklendi';
  });
  let filter = ''; let cursor = null;
  const auditRows = (items) => items.map((e) => `<tr><td>${fmtDT(e.at)}</td><td class="mono"><small>${esc(e.action)}</small></td><td>${esc(e.entity)} ${esc(e.entity_id || '')}</td><td>${e.actor_role ? ROLE_TR[e.actor_role] || e.actor_role : 'sistem'} ${e.actor_id ?? ''}</td><td>${esc(e.reason || '')}</td></tr>`).join('');
  const loadAudit = async (append) => {
    const el = document.getElementById('aul');
    if (!append) el.innerHTML = loading();
    try {
      const res = await api('GET', `/api/v1/audit?limit=30${filter ? `&action=${encodeURIComponent(filter)}` : ''}${append && cursor ? `&cursor=${cursor}` : ''}`);
      cursor = res.next_cursor;
      if (!append) el.innerHTML = res.items.length ? `<div class="table-wrap"><table><thead><tr><th>Zaman</th><th>İşlem</th><th>Varlık</th><th>Aktör</th><th>Gerekçe</th></tr></thead><tbody>${auditRows(res.items)}</tbody></table></div>` : empty('Kayıt yok');
      else el.querySelector('tbody').insertAdjacentHTML('beforeend', auditRows(res.items));
      document.getElementById('amore').innerHTML = cursor ? '<button id="amb" style="margin-top:.5rem">Daha fazla</button>' : '';
      if (cursor) document.getElementById('amb').onclick = () => loadAudit(true);
    } catch (e) { el.innerHTML = errorBox(e); }
  };
  onSubmit(document.getElementById('af'), async (v) => { filter = v.action.trim(); cursor = null; await loadAudit(false); });
  loadAudit(false);
}

// ---------- bağışçı kendi ekranı ----------
function viewMe(main) {
  main.innerHTML = `<div id="mo">${loading(5)}</div>`;
  const load = () => section(document.getElementById('mo'), () => api('GET', '/api/v1/me/donor'), (v) => {
    const upcoming = v.appointments.find((a) => a.status === 'booked');
    const days = upcoming ? Math.ceil((new Date(upcoming.scheduled_at) - new Date()) / 86400000) : null;
    return `<div class="page-head"><div><h1>Merhaba ${esc(v.donor.first_name)}</h1><p class="sub">Kan grubunuz <span class="grp">${v.donor.group}</span> · Bağışçı no <span class="mono">${esc(v.donor.donor_no)}</span></p></div></div>
      ${upcoming ? `<div class="notice info"><b>Hatırlatma:</b> ${fmtDT(upcoming.scheduled_at)} tarihinde randevunuz var${days != null ? ` (${days <= 0 ? 'bugün' : days + ' gün sonra'})` : ''}. Bağıştan önce iyi beslenin ve bol su için.</div>` : ''}
      <div class="grid g2"><div class="card"><h2>Bağışa uygunluk</h2>${decisionBox(v.eligibility)}</div>
      <div class="card"><h2>Randevu al</h2>${upcoming ? `<p>Açık randevunuz: <b>${fmtDT(upcoming.scheduled_at)}</b></p><button data-act="cancel" data-id="${upcoming.id}">Randevuyu iptal et</button>` :
        `<form id="bf" class="inline"><div class="field"><label>Tarih-saat</label><input type="datetime-local" name="at" required min="${new Date().toISOString().slice(0, 16)}"></div><button type="submit" class="primary">Randevu al</button></form>
         ${v.eligibility.next_eligible_at ? `<p class="sub">En erken uygun gün: <b>${fmtDate(v.eligibility.next_eligible_at)}</b></p>` : ''}`}</div></div>
      <div class="card" style="margin-top:1rem"><h2>Bağış geçmişim</h2>${v.donations.length ? `<div class="table-wrap"><table><thead><tr><th>Tarih</th><th>Tür</th><th class="num">ml</th></tr></thead><tbody>${v.donations.map((d) =>
        `<tr><td>${fmtDT(d.collected_at)}</td><td>${d.donation_type === 'whole_blood' ? 'Tam kan' : 'Trombosit'}</td><td class="num">${d.volume_ml}</td></tr>`).join('')}</tbody></table></div>` : empty('Henüz bağışınız yok — ilk bağışınız için randevu alabilirsiniz.')}</div>`;
  }).then(() => {
    const bf = document.getElementById('bf');
    if (bf) onSubmit(bf, async (v) => { await api('POST', '/api/v1/me/appointments', { scheduled_at: new Date(v.at).toISOString() }); load(); return 'Randevu alındı'; });
  });
  onAct(main, { cancel: async (d) => { await api('POST', `/api/v1/me/appointments/${d.id}/cancel`, {}); toast('Randevu iptal edildi'); load(); } });
  load();
}

render();
