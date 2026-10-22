// Run by TestConsentScript: checks the cookie-notice script against a tiny fake browser.
// Usage: node consent_check.js <path to consent.js>
const fs = require('fs'), vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
let failures = 0;
function check(name, got, want) { if (got !== want) { failures++; console.log('FAIL', name, '- got', JSON.stringify(got), 'want', JSON.stringify(want)); } else console.log('ok  ', name); }

function run({ present = true, version = 'v1', stored, withReopen = true, storageBlocked = false }) {
  const attrs = {};
  const store = stored !== undefined ? { 'wb-consent': stored } : {};
  const events = [];
  const handlers = {};
  const mkBtn = (state) => ({ getAttribute: (k) => (k === 'data-consent' ? state : null), closest() { return this; } });
  const box = {
    hidden: true,
    getAttribute: (k) => (k === 'data-consent-version' ? version : null),
    addEventListener: (e, f) => { handlers.box = f; },
    querySelector: () => first,
  };
  let focused = false;
  const first = { focus() { focused = true; } };
  const reopen = { hidden: true, addEventListener: (e, f) => { handlers.reopen = f; } };
  const ctx = {
    CustomEvent: function (name, init) { this.name = name; this.detail = init.detail; },
    document: {
      documentElement: { setAttribute: (k, v) => { attrs[k] = v; }, removeAttribute: (k) => { delete attrs[k]; } },
      getElementById: () => (present ? box : null),
      querySelector: (s) => (s === '[data-consent-reopen]' && withReopen ? reopen : null),
      dispatchEvent: (ev) => { events.push(ev.detail.state); },
    },
    localStorage: storageBlocked
      ? { getItem() { throw new Error('blocked'); }, setItem() { throw new Error('blocked'); }, removeItem() { throw new Error('blocked'); } }
      : { getItem: (k) => store[k] ?? null, setItem: (k, v) => { store[k] = v; }, removeItem: (k) => { delete store[k]; } },
  };
  vm.runInNewContext(src, ctx);
  return { box, reopen, attrs, store, events, focused: () => focused, click: (state) => handlers.box({ target: mkBtn(state) }), reopenClick: () => handlers.reopen() };
}

let t = run({});
check('first visit: the notice shows', t.box.hidden, false);
check('first visit: nothing published yet', t.attrs['data-consent'], undefined);
check('first visit: no reopen link yet', t.reopen.hidden, true);

t.click('accepted');
check('accept hides the notice', t.box.hidden, true);
check('accept is stored with the notice version', t.store['wb-consent'], 'accepted:v1');
check('accept is published on <html>', t.attrs['data-consent'], 'accepted');
check('accept fires an event', t.events.join(), 'accepted');
check('the reopen link appears', t.reopen.hidden, false);

t.reopenClick();
check('reopening shows the notice again', t.box.hidden, false);
check('reopening forgets the answer', t.store['wb-consent'], undefined);
check('reopening clears the published answer', t.attrs['data-consent'], undefined);
check('reopening fires an "unset" event', t.events.join(), 'accepted,unset');
check('reopening moves focus into the notice', t.focused(), true);
t.click('declined');
check('decline is published', t.attrs['data-consent'], 'declined');

t = run({ stored: 'declined:v1' });
check('a saved answer hides the notice', t.box.hidden, true);
check('a saved answer is published on load', t.attrs['data-consent'], 'declined');
check('a saved answer shows the reopen link', t.reopen.hidden, false);

t = run({ stored: 'accepted:v0', version: 'v1' });
check('a changed notice asks again', t.box.hidden, false);
check('a stale answer is not published', t.attrs['data-consent'], undefined);

for (const junk of ['accepted', 'maybe:v1', ':v1', 'accepted:v1:extra', '<script>:v1']) {
  t = run({ stored: junk });
  check('junk value ' + JSON.stringify(junk) + ' is ignored', t.box.hidden, false);
}

t = run({ stored: 'ok:v1' });
check('a plain notice stays closed once dismissed', t.box.hidden, true);
check('a plain notice publishes nothing', t.attrs['data-consent'], undefined);
t = run({ withReopen: false });
t.click('ok');
check('a plain notice closes on "Got it"', t.box.hidden, true);
check('a plain notice stores the dismissal', t.store['wb-consent'], 'ok:v1');
check('a plain notice fires no event', t.events.length, 0);

t = run({ storageBlocked: true });
check('blocked storage: the notice shows', t.box.hidden, false);
t.click('declined');
check('blocked storage: it still closes for this page', t.box.hidden, true);
check('blocked storage: the answer is still published', t.attrs['data-consent'], 'declined');

t = run({ present: false });
check('no notice on the page: the script does nothing', t.events.length + (t.attrs['data-consent'] ? 1 : 0), 0);
process.exit(failures ? 1 : 0);
