/* ===== 共用布局 / 鉴权 / API ===== */
const TOKEN_KEY = 'adskull_token';

function getToken() { return localStorage.getItem(TOKEN_KEY) || ''; }
function setToken(t) { localStorage.setItem(TOKEN_KEY, t); }
function clearToken() { localStorage.removeItem(TOKEN_KEY); }

function logout() {
  clearToken();
  location.href = '/login';
}

/* api 请求封装：自动带 token；401 跳登录；收到 X-New-Token 自动换新 token（旧的已作废） */
async function api(path, opts = {}) {
  opts.headers = Object.assign({}, opts.headers, { Authorization: 'Bearer ' + getToken() });
  const r = await fetch(path, opts);
  const nt = r.headers.get('X-New-Token');
  if (nt) setToken(nt);
  if (r.status === 401) {
    clearToken();
    location.href = '/login';
    throw new Error('unauthorized');
  }
  return r;
}

function toast(msg, err) {
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.className = 'toast' + (err ? ' err' : '');
  t.style.display = 'block';
  setTimeout(() => (t.style.display = 'none'), 2200);
}

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

function closeModal(id) {
  document.getElementById(id).style.display = 'none';
}

function fmtTime(t) {
  return t ? new Date(t).toLocaleString() : '';
}

/* ===== 自定义下拉框（替换原生 select） ===== */
function initSelects(root) {
  (root || document).querySelectorAll('select.px-input:not([data-enhanced])').forEach(sel => {
    sel.dataset.enhanced = '1';
    const wrap = document.createElement('div');
    wrap.className = 'px-select';
    sel.parentNode.insertBefore(wrap, sel);
    wrap.appendChild(sel);

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'px-select-btn';
    const label = document.createElement('span');
    label.className = 'px-select-label';
    btn.appendChild(label);
    btn.insertAdjacentHTML('beforeend',
      '<svg class="px-select-arrow" viewBox="0 0 24 24"><path d="M7.41 8.59 12 13.17l4.59-4.58L18 10l-6 6-6-6z"/></svg>');
    wrap.appendChild(btn);

    const menu = document.createElement('div');
    menu.className = 'px-select-menu';
    wrap.appendChild(menu);

    function sync() {
      const opt = sel.options[sel.selectedIndex];
      label.textContent = opt ? opt.textContent : '';
      menu.querySelectorAll('.px-select-opt').forEach(o => {
        o.classList.toggle('active', o.dataset.value === sel.value);
      });
    }
    function build() {
      menu.innerHTML = '';
      [...sel.options].forEach(opt => {
        const item = document.createElement('div');
        item.className = 'px-select-opt';
        item.dataset.value = opt.value;
        item.textContent = opt.textContent;
        item.addEventListener('click', () => {
          sel.value = opt.value;
          sync();
          wrap.classList.remove('open');
          sel.dispatchEvent(new Event('change', { bubbles: true }));
        });
        menu.appendChild(item);
      });
      sync();
    }
    build();
    sel._rebuild = build;
    sel._sync = sync;

    btn.addEventListener('click', e => {
      e.stopPropagation();
      document.querySelectorAll('.px-select.open').forEach(w => { if (w !== wrap) w.classList.remove('open'); });
      wrap.classList.toggle('open');
    });
  });
}
document.addEventListener('click', () => {
  document.querySelectorAll('.px-select.open').forEach(w => w.classList.remove('open'));
});
/* 代码里改了 select.value 之后调用，刷新自定义下拉显示 */
function syncSelect(id) {
  const sel = document.getElementById(id);
  if (sel && sel._sync) sel._sync();
}

/* ===== 页码分页条：1 2 3 4 ... 12 13 14 15，只有一页时隐藏 ===== */
function renderPager(elId, page, maxPage, go) {
  const el = document.getElementById(elId);
  if (!el) return;
  if (maxPage <= 1) { el.style.display = 'none'; el.innerHTML = ''; return; }
  el.style.display = '';
  const pages = [];
  for (let i = 1; i <= maxPage; i++) {
    if (i <= 2 || i > maxPage - 2 || Math.abs(i - page) <= 1) pages.push(i);
    else if (pages[pages.length - 1] !== '...') pages.push('...');
  }
  el.innerHTML =
    `<button class="pg-btn" ${page <= 1 ? 'disabled' : ''} data-p="${page - 1}">&lsaquo;</button>` +
    pages.map(p => p === '...'
      ? '<span class="pg-dots">···</span>'
      : `<button class="pg-btn${p === page ? ' active' : ''}" data-p="${p}">${p}</button>`).join('') +
    `<button class="pg-btn" ${page >= maxPage ? 'disabled' : ''} data-p="${page + 1}">&rsaquo;</button>`;
  el.querySelectorAll('.pg-btn[data-p]:not([disabled])').forEach(b => {
    b.addEventListener('click', () => go(parseInt(b.dataset.p, 10)));
  });
}

/* 注入侧边栏（每个页面一个 html，data-page 标记当前页） */
(function () {
  const page = document.body.dataset.page;
  if (!page) return;
  if (!getToken()) { location.href = '/login'; return; }

  const items = [
    ['dashboard', '仪表盘', 'M3 13h8V3H3v10zm0 8h8v-6H3v6zm10 0h8V11h-8v10zm0-18v6h8V3h-8z'],
    ['mailboxes', '邮箱管理', 'M20 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 4l-8 5-8-5V6l8 5 8-5v2z'],
    ['accounts', '账户管理', 'M12 12c2.21 0 4-1.79 4-4s-1.79-4-4-4-4 1.79-4 4 1.79 4 4 4zm0 2c-2.67 0-8 1.34-8 4v2h16v-2c0-2.66-5.33-4-8-4z'],
    ['settings', '系统设置', 'M19.14 12.94c.04-.3.06-.61.06-.94 0-.32-.02-.64-.07-.94l2.03-1.58c.18-.14.23-.41.12-.61l-1.92-3.32c-.12-.22-.37-.29-.59-.22l-2.39.96c-.5-.38-1.03-.7-1.62-.94l-.36-2.54c-.04-.24-.24-.41-.48-.41h-3.84c-.24 0-.43.17-.47.41l-.36 2.54c-.59.24-1.13.57-1.62.94l-2.39-.96c-.22-.08-.47 0-.59.22L2.74 8.87c-.12.21-.08.47.12.61l2.03 1.58c-.05.3-.09.63-.09.94s.02.64.07.94l-2.03 1.58c-.18.14-.23.41-.12.61l1.92 3.32c.12.22.37.29.59.22l2.39-.96c.5.38 1.03.7 1.62.94l.36 2.54c.05.24.24.41.48.41h3.84c.24 0 .44-.17.47-.41l.36-2.54c.59-.24 1.13-.56 1.62-.94l2.39.96c.22.08.47 0 .59-.22l1.92-3.32c.12-.22.07-.47-.12-.61l-2.01-1.58zM12 15.6c-1.98 0-3.6-1.62-3.6-3.6s1.62-3.6 3.6-3.6 3.6 1.62 3.6 3.6-1.62 3.6-3.6 3.6z'],
  ];
  const nav = items.map(([key, label, d]) => `
    <a class="menu-item${key === page ? ' active' : ''}" href="/${key}">
      <svg viewBox="0 0 24 24"><path d="${d}"/></svg>${label}
    </a>`).join('');

  const aside = document.createElement('aside');
  aside.className = 'sidebar px-panel';
  aside.innerHTML = `
    <div class="logo">
      <div class="logo-icon">C</div>
      <div class="logo-text">Grok</div>
    </div>
    <div class="menu-group">导航</div>
    <nav id="menu">${nav}</nav>
    <a class="menu-item logout-item" href="javascript:logout()">
      <svg viewBox="0 0 24 24"><path d="M17 7l-1.41 1.41L18.17 11H8v2h10.17l-2.58 2.58L17 17l5-5zM4 5h8V3H4c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h8v-2H4V5z"/></svg>退出登录
    </a>`;
  document.querySelector('.layout').prepend(aside);

  document.querySelectorAll('.modal-mask').forEach(m => {
    m.addEventListener('click', e => {
      if (e.target !== m) return;
      if (m.id === 'log-modal' && typeof closeLog === 'function') { closeLog(); return; }
      m.style.display = 'none';
    });
  });
})();

initSelects();
