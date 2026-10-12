// Progressive enhancement: links and GET forms remain usable without JavaScript.
(() => {
  'use strict';
  const read = (key, fallback) => { try { return JSON.parse(localStorage.getItem(key)) ?? fallback; } catch { return fallback; } };
  const save = (key, value) => { try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* Private browsing may disable storage. */ } };
  let branches = read('inventory.branches', {});
  if (!branches || typeof branches !== 'object' || Array.isArray(branches)) branches = {};
  let columns = read('inventory.columns', {});
  if (!columns || typeof columns !== 'object' || Array.isArray(columns)) columns = {};
  let timer, pending;
  function applyColumns() {
    document.querySelectorAll('[data-column-toggle]').forEach(input => {
      const key = input.dataset.columnToggle;
      const narrowDefault = window.innerWidth < 800 && ['manufacturer', 'category', 'location', 'state'].includes(key);
      input.checked = key === 'name' || (typeof columns[key] === 'boolean' ? columns[key] : input.hasAttribute('checked') && !narrowDefault);
      document.querySelectorAll(`[data-column="${key}"]`).forEach(cell => { cell.hidden = !input.checked; });
    });
  }
  function init() {
    const root = document.querySelector('#explorer');
    root.classList.add('enhanced');
    const collapsed = read('inventory.sidebarCollapsed', window.innerWidth < 800);
    root.classList.toggle('sidebar-collapsed', !!collapsed);
    document.querySelector('#sidebar-toggle').setAttribute('aria-expanded', String(!collapsed));
    root.querySelectorAll('[data-branch]').forEach(branch => {
      if (branch.dataset.selectedBranch !== 'true' && typeof branches[branch.dataset.branch] === 'boolean') branch.open = branches[branch.dataset.branch];
      branch.addEventListener('toggle', () => { branches[branch.dataset.branch] = branch.open; save('inventory.branches', branches); });
    });
    applyColumns();
  }
  async function navigate(url, mode = 'push') {
    clearTimeout(timer);
    pending?.abort();
    const controller = new AbortController(); pending = controller;
    const status = document.querySelector('#explorer-status');
    status.textContent = 'Loading inventory…';
    document.querySelector('.explorer-results').setAttribute('aria-busy', 'true');
    try {
      const response = await fetch(url, { signal: controller.signal, headers: { Accept: 'text/html' } });
      if (!response.ok) throw new Error('Request failed');
      const page = new DOMParser().parseFromString(await response.text(), 'text/html');
      const replacement = page.querySelector('#explorer');
      if (!replacement) { window.location.assign(response.url); return; }
      if (controller.signal.aborted) return;
      const focused = document.activeElement;
      const editingSearch = focused?.matches('input[name="q"]');
      const selection = editingSearch ? [focused.selectionStart, focused.selectionEnd] : null;
      document.querySelector('#explorer').replaceWith(replacement);
      if (mode === 'push') history.pushState(null, '', url);
      if (mode === 'replace') history.replaceState(null, '', url);
      init();
      if (editingSearch) {
        const input = document.querySelector('input[name="q"]'); input.focus(); input.setSelectionRange(...selection);
      } else {
        const resultStatus = document.querySelector('#explorer-status'); resultStatus.tabIndex = -1; resultStatus.focus({ preventScroll: true });
      }
    } catch (error) {
      if (error.name !== 'AbortError') {
        document.querySelector('.explorer-results').removeAttribute('aria-busy');
        status.textContent = 'Could not load inventory. Use Search to retry, or reload this page.';
      }
    }
  }
  const formURL = () => '/items?' + new URLSearchParams(new FormData(document.querySelector('#explorer-search')));
  document.addEventListener('click', event => {
    const toggle = event.target.closest('#sidebar-toggle');
    if (toggle) {
      const collapsed = document.querySelector('#explorer').classList.toggle('sidebar-collapsed');
      toggle.setAttribute('aria-expanded', String(!collapsed)); save('inventory.sidebarCollapsed', collapsed); return;
    }
    const link = event.target.closest('a[data-view]');
    if (link && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey && event.button === 0) {
      event.preventDefault();
      const url = new URL(link.href);
      // A click during the debounce window must retain the text/filters just entered.
      // Bare /items links intentionally reset the entire view.
      if (url.search) {
        const form = new FormData(document.querySelector('#explorer-search'));
        for (const key of ['q', 'stock', 'archived', 'limit']) {
          const value = String(form.get(key) ?? '');
          const defaultValue = key === 'archived' ? 'false' : key === 'limit' ? '50' : '';
          if (value !== (url.searchParams.get(key) || defaultValue)) url.searchParams.delete('offset');
          if (value) url.searchParams.set(key, value); else url.searchParams.delete(key);
        }
      }
      navigate(url.href);
    }
  });
  document.addEventListener('submit', event => {
    if (event.target.id === 'explorer-search') { event.preventDefault(); navigate(formURL()); }
  });
  document.addEventListener('input', event => {
    if (event.target.matches('#explorer-search input[name="q"]') && !event.isComposing) {
      clearTimeout(timer); pending?.abort(); timer = setTimeout(() => navigate(formURL(), 'replace'), 350);
    }
  });
  document.addEventListener('compositionstart', event => {
    if (event.target.matches('#explorer-search input[name="q"]')) { clearTimeout(timer); pending?.abort(); }
  });
  document.addEventListener('compositionend', event => {
    if (event.target.matches('#explorer-search input[name="q"]')) { clearTimeout(timer); timer = setTimeout(() => navigate(formURL(), 'replace'), 350); }
  });
  document.addEventListener('change', event => {
    if (event.target.matches('[data-column-toggle]')) {
      columns[event.target.dataset.columnToggle] = event.target.checked; save('inventory.columns', columns); applyColumns();
    } else if (event.target.matches('#explorer-search select')) navigate(formURL());
  });
  window.addEventListener('popstate', () => navigate(location.href, 'none'));
  window.matchMedia('(max-width: 799px)').addEventListener('change', applyColumns);
  init();
})();
