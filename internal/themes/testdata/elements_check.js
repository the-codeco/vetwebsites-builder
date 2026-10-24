// Run by TestElementsScript: checks the site-wide elements script against a tiny fake browser with a clock we control.
// Usage: node elements_check.js <path to elements.js>
const fs = require('fs'), vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
let failures = 0;
function check(name, got, want) { if (got !== want) { failures++; console.log('FAIL', name, '- got', JSON.stringify(got), 'want', JSON.stringify(want)); } else console.log('ok  ', name); }

const T0 = Date.parse('2026-06-01T10:00:00Z');
const MIN = 60000;

// els: [{id, kind, version, days, start, end, preview, trigger, delay, scroll, dismissible}]
function run(els, { now = T0, local = {}, session = {}, blocked = false, hover = true } = {}) {
  let clock = now;
  const timers = [];
  const docH = {}, winH = {};
  const handlers = { document: {}, window: {} };
  const classes = new Set();
  let focused = null;
  const dialogFocus = [];
  function fake(e) {
    const attrs = {
      'data-el': String(e.id), 'data-el-kind': e.kind, 'data-el-version': e.version || 'v1', 'data-el-days': String(e.days ?? 0),
    };
    if (e.start) attrs['data-el-start'] = e.start;
    if (e.end) attrs['data-el-end'] = e.end;
    if (e.preview) attrs['data-el-preview'] = '';
    if (e.trigger) attrs['data-el-trigger'] = e.trigger;
    if (e.delay !== undefined) attrs['data-el-delay'] = String(e.delay);
    if (e.scroll !== undefined) attrs['data-el-scroll'] = String(e.scroll);
    const dialog = { focus() { focused = 'dialog:' + e.id; dialogFocus.push(e.id); }, querySelectorAll: () => [] };
    const box = {
      hidden: true,
      getAttribute: (k) => (k in attrs ? attrs[k] : null),
      querySelector: (s) => (s === '.el-dialog' ? dialog : null),
      closest(s) { return s === '[data-el]' ? box : null; },
    };
    return box;
  }
  const boxes = els.map(fake);
  const store = (o) => blocked
    ? { getItem() { throw new Error('blocked'); }, setItem() { throw new Error('blocked'); } }
    : { getItem: (k) => (k in o ? o[k] : null), setItem: (k, v) => { o[k] = v; } };
  const ctx = {
    document: {
      documentElement: { classList: { add: (c) => classes.add(c), remove: (c) => classes.delete(c) }, scrollHeight: 3000, scrollTop: 0 },
      querySelectorAll: (s) => (s === '[data-el]' ? boxes : []),
      addEventListener: (ev, f) => { (handlers.document[ev] = handlers.document[ev] || []).push(f); },
      removeEventListener: (ev, f) => { handlers.document[ev] = (handlers.document[ev] || []).filter((x) => x !== f); },
      get activeElement() { return { focus() { focused = 'opener'; } }; },
    },
    window: {
      innerHeight: 1000, pageYOffset: 0,
      matchMedia: () => ({ matches: hover }),
      addEventListener: (ev, f) => { (handlers.window[ev] = handlers.window[ev] || []).push(f); },
      removeEventListener: (ev, f) => { handlers.window[ev] = (handlers.window[ev] || []).filter((x) => x !== f); },
    },
    localStorage: store(local), sessionStorage: store(session),
    Date: { now: () => clock, parse: Date.parse },
    setTimeout: (fn, ms) => { timers.push({ at: clock + ms, fn }); return timers.length; },
    parseInt, isNaN, Math,
  };
  ctx.window.document = ctx.document;
  vm.runInNewContext(src, ctx);
  const api = {
    boxes, local, session, classes, ctx,
    focused: () => focused,
    advance(ms) { // move the clock and run the timers that fall due, in order
      const end = clock + ms;
      for (;;) {
        timers.sort((a, b) => a.at - b.at);
        const next = timers.find((t) => t.at <= end);
        if (!next) break;
        timers.splice(timers.indexOf(next), 1);
        clock = Math.max(clock, next.at);
        next.fn();
      }
      clock = end;
    },
    fire(target, ev, e) { (handlers[target][ev] || []).slice().forEach((f) => f(e)); },
    click(box) { api.fire('document', 'click', { target: { closest: (s) => (s === '[data-el-close]' ? { closest: () => box } : null) } }); },
    key(k) { api.fire('document', 'keydown', { key: k, preventDefault() {} }); },
    scrollTo(y) { ctx.window.pageYOffset = y; api.fire('window', 'scroll', {}); },
    visible: (i) => !boxes[i].hidden,
  };
  return api;
}

const iso = (ms) => new Date(ms).toISOString();

// --- bars and the window in time
let t = run([{ id: 1, kind: 'bar', start: iso(T0 - MIN), end: iso(T0 + 10 * MIN) }]);
check('a bar inside its window shows', t.visible(0), true);
t.advance(11 * MIN);
check('a bar hides itself when its end passes, with the page still open', t.visible(0), false);

t = run([{ id: 1, kind: 'bar', start: iso(T0 + 5 * MIN) }]);
check('a bar before its start stays hidden', t.visible(0), false);
t.advance(6 * MIN);
check('a bar shows itself when its start arrives', t.visible(0), true);

t = run([{ id: 1, kind: 'bar', end: iso(T0 - MIN) }]);
check('an ended bar never shows', t.visible(0), false);
t = run([{ id: 1, kind: 'bar' }]);
check('a bar with no window shows', t.visible(0), true);
t = run([{ id: 1, kind: 'bar', start: 'garbage', end: 'nonsense' }]);
check('an unreadable window is treated as open', t.visible(0), true);

// --- closing and remembering
t = run([{ id: 7, kind: 'bar', version: 'v1', days: 3 }]);
t.click(t.boxes[0]);
check('closing hides the bar', t.visible(0), false);
check('closing is remembered for the days set', t.local['wb-el-7'], 'v1:' + (T0 + 3 * 86400000));
t = run([{ id: 7, kind: 'bar', version: 'v1', days: 3 }], { local: { 'wb-el-7': 'v1:' + (T0 + 86400000) } });
check('a closed bar stays away', t.visible(0), false);
t = run([{ id: 7, kind: 'bar', version: 'v1', days: 3 }], { local: { 'wb-el-7': 'v1:' + (T0 - 1) } });
check('after the days are over it comes back', t.visible(0), true);
t = run([{ id: 7, kind: 'bar', version: 'v2', days: 3 }], { local: { 'wb-el-7': 'v1:' + (T0 + 86400000) } });
check('an edited element comes back for someone who closed the old one', t.visible(0), true);
t = run([{ id: 7, kind: 'bar', version: 'v1', days: 0 }]);
t.click(t.boxes[0]);
check('zero days lasts for the browser session only', t.session['wb-el-7'], 'v1:0');
check('and is not kept in localStorage', t.local['wb-el-7'], undefined);
t = run([{ id: 7, kind: 'bar', version: 'v1', days: 0 }], { session: { 'wb-el-7': 'v1:0' } });
check('a session close hides it on the next page', t.visible(0), false);
t = run([{ id: 7, kind: 'bar', days: 3 }], { blocked: true });
check('blocked storage: it still shows', t.visible(0), true);
t.click(t.boxes[0]);
check('blocked storage: it still closes for this page', t.visible(0), false);
for (const junk of ['v1', 'v1:abc', ':5', 'v1:']) {
  t = run([{ id: 7, kind: 'bar', version: 'v1', days: 3 }], { local: { 'wb-el-7': junk } });
  check('a junk stored value ' + JSON.stringify(junk) + ' is ignored', t.visible(0), true);
}

// --- an alert wins over a bar
t = run([{ id: 1, kind: 'bar' }, { id: 2, kind: 'alert' }]);
check('with an alert up, the bar gives way', t.visible(0), false);
check('and the alert shows', t.visible(1), true);
t = run([{ id: 1, kind: 'bar' }, { id: 2, kind: 'alert', start: iso(T0 + 5 * MIN) }]);
check('an alert that has not started does not hide the bar', t.visible(0), true);
t.advance(6 * MIN);
check('when it starts the bar gives way', t.visible(0) === false && t.visible(1) === true, true);

// --- the owner's preview ignores the window and any earlier close
t = run([{ id: 1, kind: 'bar', preview: true, end: iso(T0 - MIN), version: 'v1' }], { local: { 'wb-el-1': 'v1:' + (T0 + 86400000) } });
check('a preview shows an ended, closed bar', t.visible(0), true);
t.click(t.boxes[0]);
check('closing a preview remembers nothing new', t.local['wb-el-1'], 'v1:' + (T0 + 86400000));
check('but does hide it for this page', t.visible(0), false);

// --- the contact button has no close
t = run([{ id: 3, kind: 'contact_button', start: iso(T0 - MIN), end: iso(T0 + MIN) }]);
check('the contact button shows in its window', t.visible(0), true);
t.advance(2 * MIN);
check('and goes when it ends', t.visible(0), false);

// --- popups
t = run([{ id: 5, kind: 'popup', trigger: 'delay', delay: 5 }]);
check('a delayed popup is not there at first', t.visible(0), false);
t.advance(4000);
check('and not before its delay', t.visible(0), false);
t.advance(2000);
check('it opens after its delay', t.visible(0), true);
check('the page behind is locked', t.classes.has('el-popup-open'), true);
check('focus moves into the dialog', t.focused(), 'dialog:5');
t.key('Escape');
check('Escape closes it', t.visible(0), false);
check('and unlocks the page', t.classes.has('el-popup-open'), false);
check('and gives focus back', t.focused(), 'opener');
check('and remembers it was closed', t.session['wb-el-5'], 'v1:0');

t = run([{ id: 5, kind: 'popup', trigger: 'delay', delay: 1, days: 7 }]);
t.advance(2000);
t.click(t.boxes[0]);
check('closing a popup is remembered for the days set', t.local['wb-el-5'], 'v1:' + (T0 + 2000 + 7 * 86400000));
t = run([{ id: 5, kind: 'popup', trigger: 'delay', delay: 1, days: 7 }], { local: { 'wb-el-5': 'v1:' + (T0 + 86400000) } });
t.advance(5000);
check('a closed popup does not come back', t.visible(0), false);

t = run([{ id: 5, kind: 'popup', trigger: 'delay', delay: 1, start: iso(T0 + 10 * MIN) }]);
t.advance(5000);
check('a popup before its start stays shut', t.visible(0), false);
t.advance(10 * MIN);
check('and opens after its start (plus its delay)', t.visible(0), true);

t = run([{ id: 5, kind: 'popup', trigger: 'delay', delay: 1, end: iso(T0 + 3000) }]);
t.advance(2000);
check('a popup opens inside its window', t.visible(0), true);
t.advance(5000);
check('and shuts when the window ends while it is open', t.visible(0), false);
check('without counting that as the visitor closing it', t.session['wb-el-5'], undefined);

t = run([{ id: 5, kind: 'popup', trigger: 'delay', delay: 1, end: iso(T0 - MIN) }]);
t.advance(5000);
check('an ended popup never opens', t.visible(0), false);

t = run([{ id: 5, kind: 'popup', trigger: 'delay', delay: 1 }, { id: 6, kind: 'popup', trigger: 'delay', delay: 2 }]);
t.advance(5000);
check('only one popup at a time', t.visible(0) !== t.visible(1), true);

t = run([{ id: 5, kind: 'popup', trigger: 'scroll', scroll: 50 }]);
t.scrollTo(500);
check('a scroll popup waits for its depth', t.visible(0), false);
t.scrollTo(1100);
check('and opens once the visitor is past it', t.visible(0), true);

t = run([{ id: 5, kind: 'popup', trigger: 'exit', delay: 3 }]);
t.fire('document', 'mouseout', { relatedTarget: null, clientY: -5 });
check('exit intent is ignored in the first seconds', t.visible(0), false);
t.advance(4000);
t.fire('document', 'mouseout', { relatedTarget: {}, clientY: -5 });
check('a mouse moving between elements is not leaving', t.visible(0), false);
t.fire('document', 'mouseout', { relatedTarget: null, clientY: 300 });
check('leaving at the side is not exit intent', t.visible(0), false);
t.fire('document', 'mouseout', { relatedTarget: null, clientY: -5 });
check('leaving at the top opens it', t.visible(0), true);

t = run([{ id: 5, kind: 'popup', trigger: 'exit', delay: 3 }], { hover: false });
t.advance(10000);
check('on a phone exit intent waits, then asks after a while', t.visible(0), false);
t.advance(11000);
check('and does ask', t.visible(0), true);

t = run([{ id: 5, kind: 'popup', trigger: 'delay', delay: 1, preview: true }]);
check('a previewed popup opens at once', t.visible(0), true);

t = run([{ id: 5, kind: 'popup', trigger: 'delay', delay: 1, days: 2 }]);
t.advance(2000);
t.fire('document', 'submit', { target: { closest: (s) => (s === '[data-el]' ? t.boxes[0] : null) } });
check('signing up from a popup counts as answering it', t.local['wb-el-5'] !== undefined, true);

// nothing on the page: the script does nothing and does not fail
t = run([]);
check('a page with no elements is untouched', t.boxes.length, 0);

if (failures) { console.log(failures + ' check(s) failed'); process.exit(1); }
