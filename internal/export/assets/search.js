// Search for a published copy of the website. The question is sent to the builder (the address in the search form's
// data-wb-search), which searches the website's own database the way the live site does. If the builder can't be
// reached, or none is set up, the words of every page and post in search-index.json (written when the site was built)
// are searched in the visitor's own browser instead. The address carries the question (search/?q=...), exactly as on
// the live site.
(function () {
  'use strict';
  var form = document.querySelector('.search-form');
  if (!form) return;
  var script = document.currentScript;
  var base = '', indexURL = '/search-index.json';
  if (script && script.src) {
    base = new URL(script.src).pathname.replace(/\/search\.js$/, '');
    indexURL = script.src.replace(/search\.js(\?.*)?$/, 'search-index.json');
  }
  var remote = form.getAttribute('data-wb-search');
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

  // show one page of results. Each hit is {path, title, kind, parts: [{t: text, m: true when matched}]}; the text is
  // only ever put on the page as text, never as markup
  function show(hits, total, pages) {
    box.textContent = '';
    if (!total) { note('No results for “' + q + '”'); return; }
    page = Math.min(page, pages);
    note(total + (total === 1 ? ' result for “' : ' results for “') + q + '”');
    var ol = document.createElement('ol');
    ol.className = 'search-results';
    hits.forEach(function (h) {
      var li = document.createElement('li');
      var a = document.createElement('a');
      a.className = 'search-title';
      a.href = h.path;
      a.textContent = h.title;
      var kind = document.createElement('span');
      kind.className = 'search-kind';
      kind.textContent = h.kind === 'post' ? 'Post' : 'Page';
      var url = document.createElement('p');
      url.className = 'search-url';
      url.textContent = h.path;
      var snip = document.createElement('p');
      snip.className = 'search-snippet';
      (h.parts || []).forEach(function (part) {
        if (part.m) {
          var m = document.createElement('mark');
          m.textContent = part.t;
          snip.appendChild(m);
        } else {
          snip.appendChild(document.createTextNode(part.t));
        }
      });
      li.appendChild(a); li.appendChild(document.createTextNode(' ')); li.appendChild(kind); li.appendChild(url); li.appendChild(snip);
      ol.appendChild(li);
    });
    box.appendChild(ol);
    if (pages > 1) {
      var nav = document.createElement('nav');
      nav.className = 'pager';
      nav.setAttribute('aria-label', 'Search pages');
      var link = function (label, p, rel) {
        var a = document.createElement('a');
        var qs = new URLSearchParams({ q: q });
        if (p > 1) qs.set('page', String(p));
        a.href = location.pathname + '?' + qs.toString();
        a.textContent = label;
        a.rel = rel;
        return a;
      };
      if (page > 1) nav.appendChild(link('← Previous', page - 1, 'prev'));
      var span = document.createElement('span');
      span.textContent = 'Page ' + page + ' of ' + pages;
      nav.appendChild(span);
      if (page < pages) nav.appendChild(link('Next →', page + 1, 'next'));
      box.appendChild(nav);
    }
  }

  // --- on the builder ---
  function searchOnBuilder() {
    var qs = new URLSearchParams({ q: q });
    if (page > 1) qs.set('page', String(page));
    return fetch(remote + '?' + qs.toString(), { headers: { Accept: 'application/json' }, credentials: 'omit' })
      .then(function (r) {
        if (r.status === 429) { box.textContent = ''; note('You’ve searched a lot in a short time. Please wait a minute and try again.'); return; }
        if (!r.ok) throw new Error('search');
        return r.json().then(function (res) {
          page = res.page || page;
          show((res.items || []).map(function (it) {
            return { path: base + it.path, title: it.title, kind: it.kind, parts: it.snippet };
          }), res.total || 0, res.pages || 1);
        });
      });
  }

  // --- in the browser, from the index file ---
  function words(s) { return (s.toLowerCase().match(/[\p{L}\p{N}]+/gu) || []); }
  var terms = words(q);

  function snippet(text) {
    var lower = text.toLowerCase(), at = -1, i;
    for (i = 0; i < terms.length && at < 0; i++) at = lower.indexOf(terms[i]);
    if (at < 0) return text.slice(0, 160);
    var start = Math.max(at - 70, 0), end = Math.min(start + 180, text.length);
    return (start > 0 ? '…' : '') + text.slice(start, end) + (end < text.length ? '…' : '');
  }

  function parts(text) {
    var lower = text.toLowerCase(), pos = 0, out = [];
    while (pos < text.length) {
      var next = -1, len = 0;
      terms.forEach(function (t) {
        var i = lower.indexOf(t, pos);
        if (i >= 0 && (next < 0 || i < next)) { next = i; len = t.length; }
      });
      if (next < 0) break;
      out.push({ t: text.slice(pos, next) });
      out.push({ t: text.slice(next, next + len), m: true });
      pos = next + len;
    }
    out.push({ t: text.slice(pos) });
    return out;
  }

  function searchInBrowser() {
    if (!terms.length) { show([], 0, 1); return Promise.resolve(); }
    return fetch(indexURL, { credentials: 'omit' }).then(function (r) {
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
      var pages = Math.max(Math.ceil(hits.length / PER_PAGE), 1);
      page = Math.min(page, pages);
      show(hits.slice((page - 1) * PER_PAGE, page * PER_PAGE).map(function (h) {
        return { path: h.e.p, title: h.e.t, kind: h.e.k, parts: parts(snippet(h.e.x || '')) };
      }), hits.length, pages);
    });
  }

  var done = remote ? searchOnBuilder().catch(searchInBrowser) : searchInBrowser();
  done.catch(function () {
    box.textContent = '';
    note('Search is not available right now.');
  });
})();
