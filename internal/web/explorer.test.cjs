// Small browser-boundary tests using Node's built-in runner; no frontend packages.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const vm = require('node:vm');

function browserFixture(fields = {}) {
  const events = {}, requests = [], timers = new Map();
  const values = { q: '', stock: '', archived: 'false', limit: '50', ...fields };
  const element = {
    classList: { add() {}, toggle() {} }, setAttribute() {},
    querySelectorAll: () => [],
  };
  const document = {
    addEventListener: (name, fn) => { events[name] = fn; },
    querySelector: () => element, querySelectorAll: () => [],
  };
  let timerID = 0;
  vm.runInNewContext(readFileSync(__dirname + '/explorer.js', 'utf8'), {
    document, URL, URLSearchParams, AbortController,
    window: { innerWidth: 1920, addEventListener() {}, matchMedia: () => ({ addEventListener() {} }) },
    localStorage: { getItem: () => null, setItem() {} },
    FormData: class { get(key) { return values[key]; } [Symbol.iterator]() { return Object.entries(values)[Symbol.iterator](); } },
    setTimeout: fn => { timers.set(++timerID, fn); return timerID; },
    clearTimeout: id => timers.delete(id),
    fetch: (url, options) => { requests.push({ url, signal: options.signal }); return new Promise(() => {}); },
  });
  const click = href => events.click({
    button: 0, preventDefault() {},
    target: { closest: selector => selector === 'a[data-view]' ? { href: 'http://localhost' + href } : null },
  });
  return { click, events, requests, timers, values };
}

test('pagination retains offset when default state is omitted from the URL', () => {
  const b = browserFixture(); b.click('/items?limit=50&offset=50');
  const url = new URL(b.requests[0].url);
  assert.equal(url.searchParams.get('offset'), '50');
  assert.equal(url.searchParams.get('archived'), 'false');
});

test('tree and sorting links compose with newly entered search and filters', () => {
  const b = browserFixture({ q: 'MOSFET', stock: 'in' });
  b.click('/items?category_id=3&location_id=7&sort=quantity&direction=desc&offset=50&limit=50');
  const q = new URL(b.requests[0].url).searchParams;
  for (const [key,value] of Object.entries({q:'MOSFET',stock:'in',category_id:'3',location_id:'7',sort:'quantity',direction:'desc'})) assert.equal(q.get(key),value);
  assert.equal(q.has('offset'),false);
});

test('All items/reset clears pending filters', () => {
  const b=browserFixture({q:'MOSFET',stock:'in'}); b.click('/items');
  assert.equal(new URL(b.requests[0].url).search,'');
});

test('typing debounces requests and cancels an obsolete request', () => {
  const b=browserFixture(); b.click('/items?category_id=3');
  const target={matches:()=>true};
  b.values.q='M'; b.events.input({target,isComposing:false});
  b.values.q='MOSFET'; b.events.input({target,isComposing:false});
  assert.equal(b.requests[0].signal.aborted,true);
  assert.equal(b.requests.length,1);
  assert.equal(b.timers.size,1);
  [...b.timers.values()][0]();
  assert.equal(new URL(b.requests[1].url,'http://localhost').searchParams.get('q'),'MOSFET');
});
