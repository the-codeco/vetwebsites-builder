// Visit counting for a published copy of the website. The copy is plain files, so the builder counts its visits: when a
// page opens, this tells the builder which page it was and where the visitor came from (the address in data-endpoint).
// Nothing is stored in the browser and no cookie is set; the builder keeps no address, only a daily anonymous count.
// Visitors who send Do Not Track or Global Privacy Control are not counted at all.
(function () {
  'use strict';
  var script = document.currentScript;
  var endpoint = script && script.getAttribute('data-endpoint');
  if (!endpoint || !window.fetch || !window.URLSearchParams) return;
  if (navigator.doNotTrack === '1' || window.doNotTrack === '1' || navigator.globalPrivacyControl) return;
  try {
    // the copy shown from the builder itself (the "preview before publishing") is the owner looking, not a visitor
    if (new URL(endpoint).origin === location.origin) return;
  } catch (e) { return; }

  // a copy published in a folder (example.com/shop) reports its pages as the website knows them: /about, not /shop/about
  var base = script.src ? new URL(script.src).pathname.replace(/\/beacon\.js$/, '') : '';
  var path = location.pathname;
  if (base && path.indexOf(base) === 0) path = path.slice(base.length) || '/';

  fetch(endpoint, {
    method: 'POST', credentials: 'omit', keepalive: true, headers: { Accept: 'application/json' },
    body: new URLSearchParams({ p: path, r: document.referrer || '' })
  }).catch(function () { /* counting is never worth bothering a visitor */ });
})();
