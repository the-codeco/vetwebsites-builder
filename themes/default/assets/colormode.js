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

  // the switch can be a floating button or a link in the footer (or absent): every one there is kept in step
  function toggles() { return document.querySelectorAll('[data-color-toggle]'); }

  function show(theme) {
    root.setAttribute('data-theme', theme);
    Array.prototype.forEach.call(toggles(), function (btn) {
      btn.setAttribute('aria-pressed', theme === 'dark' ? 'true' : 'false');
      btn.setAttribute('aria-label', theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode');
      var label = btn.querySelector('[data-color-label]'); // the footer link says it in words
      if (label) label.textContent = theme === 'dark' ? 'Light mode' : 'Dark mode';
    });
  }

  show(saved() || system());

  document.addEventListener('DOMContentLoaded', function () {
    show(root.getAttribute('data-theme') || 'light'); // the switches exist now: label them
    Array.prototype.forEach.call(toggles(), function (btn) {
      btn.addEventListener('click', function () {
        var next = root.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
        try { localStorage.setItem(KEY, next); } catch (e) { /* the choice just lasts for this page */ }
        show(next);
      });
    });
  });

  // until the visitor makes a choice, follow the system as it changes (e.g. at sunset)
  if (query && query.addEventListener) {
    query.addEventListener('change', function () {
      if (!saved()) show(system());
    });
  }
})();
