// Navigation behaviour for every theme: dropdowns and mega menus, the mobile menu,
// and the offset that lets a frozen header sit below a frozen top bar.
// Without this script the menus still work: panels open on hover and keyboard focus,
// and on small screens everything is shown expanded.
(function () {
  'use strict';

  var root = document.documentElement;
  root.classList.add('js');

  var nav = document.querySelector('.menu-main');
  var phone = window.matchMedia('(max-width: 800px)');

  // ---- dropdown / mega panels ----
  var items = nav ? Array.prototype.slice.call(nav.querySelectorAll('.has-panel')) : [];

  function toggleOf(li) { return li.querySelector(':scope > .menu-toggle'); }

  function close(li) {
    li.classList.remove('open');
    li._byHover = false;
    toggleOf(li).setAttribute('aria-expanded', 'false');
  }

  function closeAll(except) {
    items.forEach(function (li) { if (li !== except) close(li); });
  }

  function open(li, byHover) {
    closeAll(li);
    li.classList.add('open');
    li._byHover = !!byHover;
    toggleOf(li).setAttribute('aria-expanded', 'true');
  }

  items.forEach(function (li) {
    var toggle = toggleOf(li);

    toggle.addEventListener('click', function () {
      if (li.classList.contains('open') && !li._byHover) close(li);
      else if (li.classList.contains('open')) li._byHover = false; // a click on a hover-opened panel keeps it open
      else open(li, false);
    });

    // hover opens on desktop with a mouse; a short delay stops it flickering shut
    li.addEventListener('pointerenter', function (e) {
      if (phone.matches || e.pointerType !== 'mouse') return;
      clearTimeout(li._timer);
      if (!li.classList.contains('open')) open(li, true);
    });
    li.addEventListener('pointerleave', function (e) {
      if (phone.matches || e.pointerType !== 'mouse' || !li._byHover) return;
      li._timer = setTimeout(function () { close(li); }, 160);
    });

    // tabbing out of the panel closes it
    li.addEventListener('focusout', function (e) {
      if (e.relatedTarget && !li.contains(e.relatedTarget)) close(li);
    });
  });

  // Escape closes whatever is open and puts focus back on its button
  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape') return;
    var openItem = items.filter(function (li) { return li.classList.contains('open'); })[0];
    if (openItem) {
      var hadFocus = openItem.contains(document.activeElement);
      close(openItem);
      if (hadFocus) toggleOf(openItem).focus();
    }
    if (nav && nav.classList.contains('is-open')) setBurger(false, true);
  });

  document.addEventListener('click', function (e) {
    if (nav && !nav.contains(e.target)) { closeAll(); if (phone.matches) setBurger(false); }
  });

  // ---- mobile menu ----
  var burger = nav && nav.querySelector('.menu-burger');

  function setBurger(openIt, refocus) {
    if (!burger) return;
    nav.classList.toggle('is-open', openIt);
    burger.setAttribute('aria-expanded', openIt ? 'true' : 'false');
    if (!openIt) closeAll();
    if (refocus) burger.focus();
  }

  if (burger) {
    burger.addEventListener('click', function () { setBurger(!nav.classList.contains('is-open')); });
    var onChange = function () { if (!phone.matches) setBurger(false); };
    if (phone.addEventListener) phone.addEventListener('change', onChange);
    else phone.addListener(onChange);
  }

  // ---- a frozen header must sit below a frozen top bar ----
  var bar = document.querySelector('.menu-topbar.is-sticky');
  if (bar) {
    var measure = function () { root.style.setProperty('--topbar-h', bar.offsetHeight + 'px'); };
    measure();
    if (window.ResizeObserver) new ResizeObserver(measure).observe(bar);
    else window.addEventListener('resize', measure);
  }

  // ---- tabs blocks: the page holds every tab as a titled section; here they become real tabs ----
  Array.prototype.forEach.call(document.querySelectorAll('[data-tabs]'), function (box) {
    var panels = Array.prototype.slice.call(box.querySelectorAll(':scope > .tab-panel'));
    if (panels.length < 2) return;

    var list = document.createElement('div');
    list.className = 'tablist';
    list.setAttribute('role', 'tablist');
    var tabs = panels.map(function (panel) {
      var title = panel.querySelector('.tab-title');
      var tab = document.createElement('button');
      tab.type = 'button';
      tab.className = 'tab';
      tab.id = panel.id + '-tab';
      tab.setAttribute('role', 'tab');
      tab.setAttribute('aria-controls', panel.id);
      tab.textContent = title.textContent;
      panel.setAttribute('role', 'tabpanel');
      panel.setAttribute('aria-labelledby', tab.id);
      panel.tabIndex = 0;
      list.appendChild(tab);
      return tab;
    });

    function select(i, focus) {
      tabs.forEach(function (tab, j) {
        var on = i === j;
        tab.setAttribute('aria-selected', on ? 'true' : 'false');
        tab.tabIndex = on ? 0 : -1;
        panels[j].hidden = !on;
      });
      if (focus) tabs[i].focus();
    }

    tabs.forEach(function (tab, i) {
      tab.addEventListener('click', function () { select(i); });
      tab.addEventListener('keydown', function (e) {
        var to = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: tabs.length - 1 }[e.key];
        if (to === undefined) return;
        e.preventDefault();
        select((to + tabs.length) % tabs.length, true);
      });
    });

    box.insertBefore(list, box.firstChild);
    box.classList.add('is-tabbed');
    select(0);
  });
})();
