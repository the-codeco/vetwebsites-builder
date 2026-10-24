// Run by TestBeaconScript: checks beacon.js against a tiny fake browser. Usage: node beacon_check.js <path to beacon.js>
const fs = require('fs'), vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
let failures = 0;
function check(name, got, want) { if (got !== want) { failures++; console.log('FAIL', name, '- got', got, 'want', want); } else console.log('ok  ', name); }

function run({ endpoint, scriptSrc, pathname, origin, referrer, dnt, gpc, noFetch }) {
  const calls = [];
  const script = { src: scriptSrc, getAttribute: (k) => (k === 'data-endpoint' ? endpoint : null) };
  const ctx = {
    document: { currentScript: script, referrer: referrer || '' },
    location: { pathname, origin },
    navigator: { doNotTrack: dnt ? '1' : null, globalPrivacyControl: !!gpc },
    window: { fetch: undefined },
    URL, URLSearchParams,
    fetch: noFetch ? undefined : (url, opts) => { calls.push({ url, opts, body: opts.body.toString() }); return Promise.resolve({}); },
  };
  ctx.window.fetch = ctx.fetch;
  ctx.window.URLSearchParams = URLSearchParams;
  vm.runInNewContext(src, ctx);
  return calls;
}

const ep = 'https://builder.example/f/tok/b';
let c = run({ endpoint: ep, scriptSrc: 'https://www.shop.example/beacon.js', pathname: '/about-us', origin: 'https://www.shop.example', referrer: 'https://google.com/x' });
check('one report', c.length, 1);
check('goes to the builder', c[0].url, ep);
check('sends the path and the referrer', c[0].body, 'p=%2Fabout-us&r=https%3A%2F%2Fgoogle.com%2Fx');
check('no cookies', c[0].opts.credentials, 'omit');

c = run({ endpoint: ep, scriptSrc: 'https://www.shop.example/shop/beacon.js', pathname: '/shop/about-us', origin: 'https://www.shop.example' });
check('a folder is taken off the path', c[0].body.startsWith('p=%2Fabout-us&'), true);
c = run({ endpoint: ep, scriptSrc: 'https://www.shop.example/shop/beacon.js', pathname: '/shop/', origin: 'https://www.shop.example' });
check('the home page of a folder is /', c[0].body.startsWith('p=%2F&'), true);

check('Do Not Track: nothing sent', run({ endpoint: ep, scriptSrc: 'https://a.example/beacon.js', pathname: '/', origin: 'https://a.example', dnt: true }).length, 0);
check('Global Privacy Control: nothing sent', run({ endpoint: ep, scriptSrc: 'https://a.example/beacon.js', pathname: '/', origin: 'https://a.example', gpc: true }).length, 0);
check('the builder showing its own preview: nothing sent', run({ endpoint: ep, scriptSrc: 'https://builder.example/preview/build/beacon.js', pathname: '/preview/build/', origin: 'https://builder.example' }).length, 0);
check('no endpoint: nothing sent', run({ endpoint: null, scriptSrc: 'https://a.example/beacon.js', pathname: '/', origin: 'https://a.example' }).length, 0);
check('a junk endpoint: nothing sent', run({ endpoint: 'not a url', scriptSrc: 'https://a.example/beacon.js', pathname: '/', origin: 'https://a.example' }).length, 0);
check('an old browser without fetch: nothing sent', run({ endpoint: ep, scriptSrc: 'https://a.example/beacon.js', pathname: '/', origin: 'https://a.example', noFetch: true }).length, 0);
process.exit(failures ? 1 : 0);
