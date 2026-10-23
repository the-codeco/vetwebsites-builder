// Run by TestColorModeScript: checks the colour-mode script against a tiny fake browser. Usage: node colormode_check.js <path to colormode.js>
const fs = require('fs'), vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
let failures = 0;
function check(name, got, want) { if (got !== want) { failures++; console.log('FAIL', name, '- got', got, 'want', want); } else console.log('ok  ', name); }

function run({ mode, saved, systemDark, storageBlocked }) {
  const attrs = { 'data-color-mode': mode };
  const store = saved ? { 'wb-color': saved } : {};
  let clickHandler, domReady, mqListener;
  const btn = { attrs: {}, setAttribute(k, v) { this.attrs[k] = v; }, querySelector: () => null, addEventListener(e, f) { if (e === 'click') clickHandler = f; } };
  const root = { getAttribute: k => attrs[k] ?? null, setAttribute: (k, v) => { attrs[k] = v; } };
  const mq = { matches: systemDark, addEventListener: (e, f) => { mqListener = f; } };
  const ctx = {
    document: { documentElement: root, querySelector: () => btn, querySelectorAll: () => [btn], addEventListener: (e, f) => { domReady = f; } },
    window: { matchMedia: () => mq },
    localStorage: storageBlocked
      ? { getItem() { throw new Error('blocked'); }, setItem() { throw new Error('blocked'); } }
      : { getItem: k => store[k] ?? null, setItem: (k, v) => { store[k] = v; } },
  };
  vm.runInNewContext(src, ctx);
  domReady && domReady();
  return { attrs, store, btn, click: () => clickHandler(), systemChange: (dark) => { mq.matches = dark; mqListener(); } };
}

let t = run({ mode: 'toggle', systemDark: true });
check('toggle mode ignores the system setting', t.attrs['data-theme'], 'light');
t.click(); check('click switches to dark', t.attrs['data-theme'], 'dark');
check('choice is saved', t.store['wb-color'], 'dark');
check('button says pressed', t.btn.attrs['aria-pressed'], 'true');
check('button offers light mode', t.btn.attrs['aria-label'], 'Switch to light mode');
t.click(); check('second click switches back', t.attrs['data-theme'], 'light');

t = run({ mode: 'auto', systemDark: true });
check('auto follows a dark system', t.attrs['data-theme'], 'dark');
t = run({ mode: 'auto', systemDark: false });
check('auto follows a light system', t.attrs['data-theme'], 'light');
t.systemChange(true); check('auto reacts when the system changes', t.attrs['data-theme'], 'dark');
t.click(); check('visitor overrides the system', t.attrs['data-theme'], 'light');
t.systemChange(true); check('a saved choice beats later system changes', t.attrs['data-theme'], 'light');

t = run({ mode: 'auto', systemDark: true, saved: 'light' });
check('a saved light choice beats a dark system', t.attrs['data-theme'], 'light');
t = run({ mode: 'toggle', systemDark: false, saved: 'dark' });
check('a saved dark choice applies on load', t.attrs['data-theme'], 'dark');
t = run({ mode: 'toggle', systemDark: false, saved: 'evil"><script>' });
check('a junk saved value is ignored', t.attrs['data-theme'], 'light');

t = run({ mode: 'toggle', systemDark: false, storageBlocked: true });
check('blocked storage: still starts light', t.attrs['data-theme'], 'light');
t.click(); check('blocked storage: the switch still works for this page', t.attrs['data-theme'], 'dark');

t = run({ mode: null, systemDark: true });
check('no mode attribute: the script does nothing', t.attrs['data-theme'], undefined);
process.exit(failures ? 1 : 0);
