// The contact and newsletter forms of a published copy of the website. The copy is plain files with no server of its
// own, so each form is sent to the builder (the address in the form's action). When the page opens, a fresh
// token is fetched for the forms; when one is sent, the answer is shown in place of the form.
(function () {
  'use strict';
  var forms = document.querySelectorAll('form[data-wb-form]');
  if (!forms.length || !window.fetch || !window.URLSearchParams) return;

  function tokenURL(form) { return form.action.replace(/\/(contact|subscribe)$/, '/token'); }

  function note(form, text, ok) {
    var old = form.parentNode.querySelector('.wb-form-note');
    if (old) old.remove();
    var p = document.createElement('p');
    p.className = 'wb-form-note ' + (ok ? 'form-notice' : 'form-error');
    p.setAttribute('role', ok ? 'status' : 'alert');
    p.textContent = text;
    if (ok) form.parentNode.replaceChild(p, form);
    else form.insertBefore(p, form.firstChild);
  }

  var tokens = {};
  function getTokens(url) {
    if (!tokens[url]) {
      tokens[url] = fetch(url, { headers: { Accept: 'application/json' }, credentials: 'omit' })
        .then(function (r) { if (!r.ok) throw new Error('token'); return r.json(); });
    }
    return tokens[url];
  }

  Array.prototype.forEach.call(forms, function (form) {
    var kind = form.getAttribute('data-wb-form');
    var field = form.querySelector('input[name="t"]');
    var button = form.querySelector('button[type="submit"]');
    var ready = getTokens(tokenURL(form)).then(function (t) {
      if (field && t[kind]) field.value = t[kind];
    }, function () {});

    form.addEventListener('submit', function (e) {
      e.preventDefault();
      if (button) button.disabled = true;
      ready.then(function () {
        if (!field || !field.value) throw new Error('offline');
        return fetch(form.action, {
          method: 'POST', credentials: 'omit', headers: { Accept: 'application/json' },
          body: new URLSearchParams(new FormData(form))
        });
      }).then(function (r) {
        return r.json().catch(function () { return { ok: false }; });
      }).then(function (res) {
        if (res.ok) {
          note(form, form.getAttribute('data-success') || 'Thank you.', true);
        } else {
          if (button) button.disabled = false;
          note(form, res.error || 'Sorry, that could not be sent. Please try again in a moment.', false);
        }
      }).catch(function () {
        if (button) button.disabled = false;
        note(form, 'Sorry, that could not be sent. Please check your connection and try again.', false);
      });
    });
  });
})();
