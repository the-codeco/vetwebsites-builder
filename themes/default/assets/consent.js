// The cookie notice. The visitor's answer is kept in this browser only (localStorage, together with
// the version of the notice they answered, so a changed notice asks again). Nothing is sent anywhere.
//
// When visitors are asked to choose, the answer is published for any feature that needs it:
//   <html data-consent="accepted">  or  "declined"  (absent until they choose: treat as declined)
//   document event "wb:consent" with detail.state = "accepted" | "declined" | "unset"
(function () {
  'use strict';
  var box = document.getElementById('consent');
  if (!box) return;

  var root = document.documentElement;
  var KEY = 'wb-consent';
  var version = box.getAttribute('data-consent-version') || '';
  var reopen = document.querySelector('[data-consent-reopen]');

  function read() {
    try {
      var v = localStorage.getItem(KEY) || '';
      var i = v.indexOf(':');
      if (i < 0 || v.slice(i + 1) !== version) return null; // never answered, or the notice has changed
      var state = v.slice(0, i);
      return state === 'ok' || state === 'accepted' || state === 'declined' ? state : null;
    } catch (e) { return null; } // storage can be blocked: the notice just shows on every page
  }
  function write(state) {
    try { localStorage.setItem(KEY, state + ':' + version); } catch (e) { /* answer lasts for this page */ }
  }
  function clear() {
    try { localStorage.removeItem(KEY); } catch (e) { /* nothing stored to remove */ }
  }
  function publish(state) {
    if (state === 'ok') return; // a plain notice has no choice to publish
    if (state === 'accepted' || state === 'declined') root.setAttribute('data-consent', state);
    else root.removeAttribute('data-consent');
    var ev;
    try { ev = new CustomEvent('wb:consent', { detail: { state: state } }); } catch (e) { return; }
    document.dispatchEvent(ev);
  }

  var current = read();
  if (current) {
    publish(current);
    box.hidden = true;
    if (reopen) reopen.hidden = false;
  } else {
    box.hidden = false;
  }

  box.addEventListener('click', function (e) {
    var btn = e.target.closest ? e.target.closest('[data-consent]') : null;
    if (!btn) return;
    var state = btn.getAttribute('data-consent');
    write(state);
    publish(state);
    box.hidden = true;
    if (reopen) reopen.hidden = false;
  });

  if (reopen) {
    reopen.addEventListener('click', function () {
      clear();
      publish('unset');
      box.hidden = false;
      var first = box.querySelector('button');
      if (first) first.focus(); // they asked to change their answer: take them to the choice
    });
  }
})();
