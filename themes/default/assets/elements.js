// Site-wide elements: notice bars, emergency alerts, popups and the contact button.
//
// Each element is in the page hidden. This script shows it only inside its start and end time, measured by the
// visitor's own clock: a published copy of a website has no server that could switch anything on or off at the
// right minute. A visitor who closes an element does not see it again for the number of days set on it (or until
// the browser is closed), unless it is edited: its version changes and it is shown again. Closing is remembered in
// this browser only; nothing is sent anywhere.
//
// Markup: [data-el="id"] with data-el-kind (bar, alert, popup, contact_button), data-el-version, data-el-days,
// optional data-el-start / data-el-end (RFC 3339), data-el-preview (the owner is looking at it: show it now), and for
// popups data-el-trigger (delay, scroll or exit), data-el-delay (seconds) and data-el-scroll (percent).
(function () {
  'use strict';
  var all = document.querySelectorAll ? document.querySelectorAll('[data-el]') : [];
  if (!all || !all.length) return;

  var root = document.documentElement;
  var DAY = 86400000, MAX_TIMER = 2147483647;
  var items = [];
  for (var i = 0; i < all.length; i++) {
    var el = all[i];
    items.push({
      el: el,
      id: el.getAttribute('data-el'),
      kind: el.getAttribute('data-el-kind'),
      version: el.getAttribute('data-el-version') || '',
      days: parseInt(el.getAttribute('data-el-days'), 10) || 0,
      start: time(el.getAttribute('data-el-start')),
      end: time(el.getAttribute('data-el-end')),
      preview: el.getAttribute('data-el-preview') !== null,
      open: false
    });
  }
  var openPopup = null, lastFocus = null;

  function time(s) {
    if (!s) return null;
    var t = Date.parse(s);
    return isNaN(t) ? null : t;
  }
  function key(it) { return 'wb-el-' + it.id; }
  function inWindow(it, t) {
    if (it.preview) return true;
    return (it.start === null || t >= it.start) && (it.end === null || t < it.end);
  }

  // closed by the visitor? The value is "version:expiry" (expiry 0 = this browser session).
  function closed(it, t) {
    if (it.gone) return true; // closed on this page, whether or not storage could remember it
    if (it.preview) return false;
    var v;
    try { v = localStorage.getItem(key(it)); } catch (e) { v = null; }
    if (v) {
      var i = v.lastIndexOf(':');
      if (i > 0 && v.slice(0, i) === it.version && parseInt(v.slice(i + 1), 10) > t) return true;
    }
    try { v = sessionStorage.getItem(key(it)); } catch (e) { v = null; }
    return !!v && v === it.version + ':0';
  }
  function close(it) {
    it.gone = true;
    if (it.preview) return;
    var t = Date.now();
    try {
      if (it.days > 0) localStorage.setItem(key(it), it.version + ':' + (t + it.days * DAY));
      else sessionStorage.setItem(key(it), it.version + ':0');
    } catch (e) { /* storage blocked: it comes back on the next page */ }
  }

  // bars, alerts and the contact button are shown whenever they are inside their window and not closed; a bar gives
  // way to an alert, so there is only ever one strip of news at the top
  function sync() {
    var t = Date.now(), alertShown = false, k;
    for (k = 0; k < items.length; k++) {
      var it = items[k];
      if (it.kind === 'popup') continue;
      var on = inWindow(it, t) && !closed(it, t);
      if (it.kind === 'alert') alertShown = alertShown || on;
      it.want = on;
    }
    for (k = 0; k < items.length; k++) {
      var x = items[k];
      if (x.kind === 'popup') continue;
      x.el.hidden = !(x.want && !(x.kind === 'bar' && alertShown));
    }
    if (openPopup && !inWindow(openPopup, t)) closePopup(openPopup, false);
  }

  function at(when, fn) {
    var d = when - Date.now();
    if (d > 0 && d < MAX_TIMER) setTimeout(fn, d + 50);
  }

  // popups
  function dialogOf(it) { return it.el.querySelector ? it.el.querySelector('.el-dialog') : null; }
  function showPopup(it) {
    if (openPopup || it.open) return;
    var t = Date.now();
    if (!inWindow(it, t) || closed(it, t)) return;
    it.open = true;
    openPopup = it;
    lastFocus = document.activeElement || null;
    it.el.hidden = false;
    if (root.classList) root.classList.add('el-popup-open');
    var d = dialogOf(it);
    if (d && d.focus) d.focus();
  }
  function closePopup(it, remember) {
    it.el.hidden = true;
    if (root.classList) root.classList.remove('el-popup-open');
    if (remember !== false) close(it);
    if (openPopup === it) openPopup = null;
    if (lastFocus && lastFocus.focus) lastFocus.focus();
    lastFocus = null;
  }

  function arm(it) {
    var t = Date.now();
    if (it.preview) { showPopup(it); return; }
    if (it.end !== null && t >= it.end) return;
    if (closed(it, t)) return;
    var delay = Math.max(0, parseInt(it.el.getAttribute('data-el-delay'), 10) || 0) * 1000;
    var trigger = it.el.getAttribute('data-el-trigger') || 'delay';
    var go = function () { showPopup(it); };
    // a popup that is waiting for its start time begins waiting for its trigger then
    var begin = function () {
      if (trigger === 'scroll') {
        var pct = parseInt(it.el.getAttribute('data-el-scroll'), 10) || 50;
        var check = function () {
          var doc = root, max = (doc.scrollHeight || 0) - (window.innerHeight || 0);
          var y = window.pageYOffset || doc.scrollTop || 0;
          if (max > 0 && y / max * 100 >= pct) { window.removeEventListener('scroll', check); go(); }
        };
        window.addEventListener('scroll', check, { passive: true });
        setTimeout(function () { if (!it.open) { window.removeEventListener('scroll', check); go(); } }, 20000); // a page too short to scroll
      } else if (trigger === 'exit') {
        var hover = window.matchMedia && window.matchMedia('(hover: hover)').matches;
        if (hover) {
          var armedAt = Date.now() + delay;
          var out = function (e) {
            if (Date.now() >= armedAt && !e.relatedTarget && e.clientY <= 0) { document.removeEventListener('mouseout', out); go(); }
          };
          document.addEventListener('mouseout', out);
        } else {
          setTimeout(go, Math.max(delay, 20000)); // a phone has no mouse to watch: ask after a while instead
        }
      } else {
        setTimeout(go, delay);
      }
    };
    if (it.start !== null && t < it.start) at(it.start, begin); else begin();
  }

  document.addEventListener('click', function (e) {
    var btn = e.target && e.target.closest ? e.target.closest('[data-el-close]') : null;
    if (!btn) return;
    var box = btn.closest('[data-el]');
    for (var k = 0; k < items.length; k++) {
      if (items[k].el === box) {
        if (items[k].kind === 'popup') closePopup(items[k]);
        else { close(items[k]); sync(); }
      }
    }
  });
  document.addEventListener('keydown', function (e) {
    if (!openPopup) return;
    if (e.key === 'Escape' || e.key === 'Esc') { closePopup(openPopup); return; }
    if (e.key === 'Tab') { // keep the keyboard inside the open popup
      var box = dialogOf(openPopup);
      var f = box && box.querySelectorAll ? box.querySelectorAll('a[href], button, input, [tabindex]:not([tabindex="-1"])') : [];
      if (!f.length) { e.preventDefault(); return; }
      var first = f[0], last = f[f.length - 1], cur = document.activeElement;
      if (e.shiftKey && (cur === first || cur === box)) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && cur === last) { e.preventDefault(); first.focus(); }
    }
  });
  // signing up from a popup counts as answering it: it does not come back
  document.addEventListener('submit', function (e) {
    var box = e.target && e.target.closest ? e.target.closest('[data-el]') : null;
    for (var k = 0; k < items.length; k++) if (items[k].el === box && items[k].kind === 'popup') close(items[k]);
  });

  sync();
  for (var n = 0; n < items.length; n++) {
    var it2 = items[n];
    if (it2.kind === 'popup') arm(it2);
    else if (it2.start !== null) at(it2.start, sync);
    if (it2.end !== null) at(it2.end, sync);
  }
})();
