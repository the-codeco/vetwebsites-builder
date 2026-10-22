// Loads the analytics services the site owner has set up, and only when the visitor allows it.
//
// The page puts the settings on this script's own tag, so no inline script is needed:
//   data-ga4="G-XXXX"            Google Analytics 4
//   data-plausible="example.com" Plausible
//   data-wait="1"                wait until the visitor accepts the cookie notice (see consent.js)
(function () {
  'use strict';
  var tag = document.currentScript;
  if (!tag) return;
  var ga4 = tag.getAttribute('data-ga4') || '';
  var plausible = tag.getAttribute('data-plausible') || '';
  var wait = tag.getAttribute('data-wait') === '1';
  var started = false;

  function add(src, attrs) {
    var s = document.createElement('script');
    s.async = true;
    s.src = src;
    for (var k in attrs) { if (Object.prototype.hasOwnProperty.call(attrs, k)) s.setAttribute(k, attrs[k]); }
    document.head.appendChild(s);
  }

  function start() {
    if (started) return;
    started = true;
    if (ga4 && /^G-[A-Z0-9]{4,14}$/.test(ga4)) {
      window.dataLayer = window.dataLayer || [];
      window.gtag = function () { window.dataLayer.push(arguments); };
      window.gtag('js', new Date());
      window.gtag('config', ga4, { anonymize_ip: true });
      add('https://www.googletagmanager.com/gtag/js?id=' + encodeURIComponent(ga4), {});
    }
    if (plausible && /^[a-z0-9.\-]+$/.test(plausible)) {
      add('https://plausible.io/js/script.js', { defer: '', 'data-domain': plausible });
    }
  }

  if (!wait) { start(); return; }
  // wait for "Accept": consent.js publishes the answer on <html data-consent> and as an event
  if (document.documentElement.getAttribute('data-consent') === 'accepted') { start(); return; }
  document.addEventListener('wb:consent', function (e) {
    if (e.detail && e.detail.state === 'accepted') start();
  });
})();
