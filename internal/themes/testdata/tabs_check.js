// Run by TestTabsScript: checks the tabs part of menu.js against a tiny fake page.
// Usage: node tabs_check.js <path to menu.js>
const fs = require('fs'), vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
let failures = 0;
function check(name, got, want) { if (JSON.stringify(got) !== JSON.stringify(want)) { failures++; console.log('FAIL', name, '- got', JSON.stringify(got), 'want', JSON.stringify(want)); } else console.log('ok  ', name); }

function el(tag) {
  const e = {
    tag, attrs: {}, children: [], listeners: {}, hidden: false, className: '', tabIndex: undefined, textContent: '', _focused: false,
    classList: { add(c) { e.className += ' ' + c; }, remove() {}, toggle() {}, contains(c) { return (' ' + e.className + ' ').includes(' ' + c + ' '); } },
    setAttribute(k, v) { e.attrs[k] = String(v); }, getAttribute(k) { return k in e.attrs ? e.attrs[k] : null; },
    appendChild(c) { e.children.push(c); return c; },
    insertBefore(c, ref) { e.children.splice(Math.max(e.children.indexOf(ref), 0), 0, c); return c; },
    get firstChild() { return e.children[0] || null; },
    addEventListener(ev, f) { (e.listeners[ev] = e.listeners[ev] || []).push(f); },
    focus() { e._focused = true; },
    style: { setProperty() {} },
    querySelector() { return null; }, querySelectorAll() { return []; },
  };
  return e;
}

function page(titles) {
  const panels = titles.map((title, i) => {
    const p = el('section'); p.id = 'tab-7-' + (i + 1);
    const h = el('h3'); h.textContent = title; p.querySelector = (s) => (s === '.tab-title' ? h : null);
    return p;
  });
  const box = el('div');
  box.children = panels.slice();
  box.querySelectorAll = (s) => (s === ':scope > .tab-panel' ? panels : []);
  const doc = {
    documentElement: el('html'), createElement: el,
    querySelector: () => null,
    querySelectorAll: (s) => (s === '[data-tabs]' ? [box] : []),
    addEventListener() {},
  };
  const ctx = { document: doc, window: { matchMedia: () => ({ matches: false, addEventListener() {} }), addEventListener() {} }, ResizeObserver: undefined, Array };
  vm.runInNewContext(src, ctx);
  const list = box.children[0];
  return { box, panels, list, tabs: list.children };
}

let p = page(['One', 'Two', 'Three']);
check('a tab list is put before the panels', p.list.attrs.role, 'tablist');
check('one tab per panel, titled from the heading', p.tabs.map((t) => t.textContent), ['One', 'Two', 'Three']);
check('tabs are linked to their panels', [p.tabs[1].attrs['aria-controls'], p.panels[1].attrs['aria-labelledby']], ['tab-7-2', 'tab-7-2-tab']);
check('only the first panel shows', p.panels.map((x) => x.hidden), [false, true, true]);
check('only the first tab is selected', p.tabs.map((t) => t.attrs['aria-selected']), ['true', 'false', 'false']);
check('only the selected tab is in the tab order', p.tabs.map((t) => t.tabIndex), [0, -1, -1]);
check('the box is marked as tabbed', p.box.className.includes('is-tabbed'), true);

p.tabs[2].listeners.click[0]();
check('clicking a tab shows its panel', p.panels.map((x) => x.hidden), [true, true, false]);
check('and selects it', p.tabs.map((t) => t.attrs['aria-selected']), ['false', 'false', 'true']);

let prevented = 0;
const key = (i, k) => p.tabs[i].listeners.keydown[0]({ key: k, preventDefault() { prevented++; } });
key(2, 'ArrowRight');
check('arrow right wraps round to the first tab', p.panels.map((x) => x.hidden), [false, true, true]);
check('and moves focus', p.tabs[0]._focused, true);
key(0, 'ArrowLeft');
check('arrow left wraps to the last', p.panels.map((x) => x.hidden), [true, true, false]);
key(2, 'Home');
check('Home goes to the first', p.panels[0].hidden, false);
key(0, 'End');
check('End goes to the last', p.panels[2].hidden, false);
const before = prevented;
key(0, 'a');
check('other keys are left alone', prevented, before);

// a single tab needs no tab list
p = page(['Only']);
check('one panel stays a plain section', [p.box.children.length, p.box.className.includes('is-tabbed')], [1, false]);

if (failures) { console.log(failures + ' check(s) failed'); process.exit(1); }
