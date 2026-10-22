// Dark mode for the public site. Loaded in <head>, before the page paints, so a visitor who chose
// dark never sees a white flash. The choice lives only in this browser (localStorage); nothing is
// sent anywhere and no cookie is set.
(function () {
  'use strict';
  var root = document.documentElement;
  var mode = root.getAttribute('data-color-mode'); // "toggle" or "auto", set by the server
  if (!mode) return;

  var KEY = 'wb-color';
  var query = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;

  function saved() {
    try {
      var v = localStorage.getItem(KEY);
      return v === 'dark' || v === 'light' ? v : null;
    } catch (e) { return null; } // storage can be blocked (private windows, strict settings)
  }
  function system() { return mode === 'auto' && query && query.matches ? 'dark' : 'light'; }

  function show(theme) {
    root.setAttribute('data-theme', theme);
    var btn = document.querySelector('[data-color-toggle]');
    if (btn) {
      btn.setAttribute('aria-pressed', theme === 'dark' ? 'true' : 'false');
      btn.setAttribute('aria-label', theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode');
    }
  }

  show(saved() || system());

  document.addEventListener('DOMContentLoaded', function () {
    var btn = document.querySelector('[data-color-toggle]');
    show(root.getAttribute('data-theme') || 'light'); // the button exists now: label it
    if (!btn) return;
    btn.addEventListener('click', function () {
      var next = root.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
      try { localStorage.setItem(KEY, next); } catch (e) { /* the choice just lasts for this page */ }
      show(next);
    });
  });

  // until the visitor makes a choice, follow the system as it changes (e.g. at sunset)
  if (query && query.addEventListener) {
    query.addEventListener('change', function () {
      if (!saved()) show(system());
    });
  }
})();
