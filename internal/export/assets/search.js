// Search for a static copy of the site. The words of every page and post are in search-index.json (built when the site was
// published); this finds them in the visitor's own browser, so no server is involved. The address carries the question
// (search/?q=...), exactly as on the live site.
(function () {
  'use strict';
  var form = document.querySelector('.search-form');
  if (!form) return;
  var script = document.currentScript;
  var indexURL = script && script.src ? script.src.replace(/search\.js(\?.*)?$/, 'search-index.json') : '/search-index.json';
  var PER_PAGE = 10;

  var params = new URLSearchParams(location.search);
  var q = (params.get('q') || '').replace(/\s+/g, ' ').trim().slice(0, 100);
  var page = Math.max(parseInt(params.get('page') || '1', 10) || 1, 1);
  var input = form.querySelector('input[name="q"]');
  if (input) input.value = q;

  var box = document.createElement('div');
  box.className = 'search-js';
  form.parentNode.insertBefore(box, form.nextSibling);
  Array.prototype.forEach.call(document.querySelectorAll('.search-note'), function (n) { n.hidden = true; });

  function note(text) {
    var p = document.createElement('p');
    p.className = 'search-note';
    p.textContent = text;
    box.appendChild(p);
  }

  if (!q) { note('Type a word or phrase above to search pages and posts.'); return; }

  function words(s) { return (s.toLowerCase().match(/[\p{L}\p{N}]+/gu) || []); }
  var terms = words(q);
  if (!terms.length) { note('No results for “' + q + '”'); return; }

  function snippet(text) {
    var lower = text.toLowerCase(), at = -1, i;
    for (i = 0; i < terms.length && at < 0; i++) at = lower.indexOf(terms[i]);
    if (at < 0) return text.slice(0, 160);
    var start = Math.max(at - 70, 0), end = Math.min(start + 180, text.length);
    return (start > 0 ? '…' : '') + text.slice(start, end) + (end < text.length ? '…' : '');
  }

  // put <mark> around the matched words without ever treating the text as markup
  function highlight(el, text) {
    var lower = text.toLowerCase(), pos = 0;
    while (pos < text.length) {
      var next = -1, len = 0;
      terms.forEach(function (t) {
        var i = lower.indexOf(t, pos);
        if (i >= 0 && (next < 0 || i < next)) { next = i; len = t.length; }
      });
      if (next < 0) break;
      el.appendChild(document.createTextNode(text.slice(pos, next)));
      var m = document.createElement('mark');
      m.textContent = text.slice(next, next + len);
      el.appendChild(m);
      pos = next + len;
    }
    el.appendChild(document.createTextNode(text.slice(pos)));
  }

  fetch(indexURL, { credentials: 'omit' }).then(function (r) {
    if (!r.ok) throw new Error('index');
    return r.json();
  }).then(function (index) {
    var hits = [];
    index.forEach(function (e) {
      var title = (e.t || '').toLowerCase(), text = (e.x || '').toLowerCase(), score = 0;
      for (var i = 0; i < terms.length; i++) {
        var inTitle = title.indexOf(terms[i]) >= 0, inText = text.indexOf(terms[i]) >= 0;
        if (!inTitle && !inText) return;
        score += (inTitle ? 5 : 0) + (inText ? 1 : 0);
      }
      hits.push({ e: e, score: score });
    });
    hits.sort(function (a, b) { return b.score - a.score; });
    if (!hits.length) { note('No results for “' + q + '”'); return; }
    var pages = Math.ceil(hits.length / PER_PAGE);
    page = Math.min(page, pages);
    note(hits.length + (hits.length === 1 ? ' result for “' : ' results for “') + q + '”');
    var ol = document.createElement('ol');
    ol.className = 'search-results';
    hits.slice((page - 1) * PER_PAGE, page * PER_PAGE).forEach(function (h) {
      var li = document.createElement('li');
      var a = document.createElement('a');
      a.className = 'search-title';
      a.href = h.e.p;
      a.textContent = h.e.t;
      var kind = document.createElement('span');
      kind.className = 'search-kind';
      kind.textContent = h.e.k === 'post' ? 'Post' : 'Page';
      var url = document.createElement('p');
      url.className = 'search-url';
      url.textContent = h.e.p;
      var snip = document.createElement('p');
      snip.className = 'search-snippet';
      highlight(snip, snippet(h.e.x || ''));
      li.appendChild(a); li.appendChild(document.createTextNode(' ')); li.appendChild(kind); li.appendChild(url); li.appendChild(snip);
      ol.appendChild(li);
    });
    box.appendChild(ol);
    if (pages > 1) {
      var nav = document.createElement('nav');
      nav.className = 'pager';
      nav.setAttribute('aria-label', 'Search pages');
      function link(label, p, rel) {
        var a = document.createElement('a');
        var qs = new URLSearchParams({ q: q });
        if (p > 1) qs.set('page', String(p));
        a.href = location.pathname + '?' + qs.toString();
        a.textContent = label;
        a.rel = rel;
        return a;
      }
      if (page > 1) nav.appendChild(link('← Previous', page - 1, 'prev'));
      var span = document.createElement('span');
      span.textContent = 'Page ' + page + ' of ' + pages;
      nav.appendChild(span);
      if (page < pages) nav.appendChild(link('Next →', page + 1, 'next'));
      box.appendChild(nav);
    }
  }).catch(function () {
    note('Search is not available right now.');
  });
})();
