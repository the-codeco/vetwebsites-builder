(function () {
  'use strict';

  // ---- sidebar: a drawer on phones, collapsible to icons on desktop ----
  var body = document.body;
  var drawerBtn = document.querySelector('[data-sidebar-open]');
  var sidebar = document.getElementById('sidebar');
  var COLLAPSE_KEY = 'wb-admin-sidebar';

  function setDrawer(open, focusBack) {
    body.classList.toggle('sidebar-open', open);
    if (drawerBtn) drawerBtn.setAttribute('aria-expanded', open ? 'true' : 'false');
    if (open && sidebar) {
      var first = sidebar.querySelector('.nav-link');
      if (first) first.focus();
    } else if (focusBack && drawerBtn) {
      drawerBtn.focus();
    }
  }

  function setCollapsed(collapsed) {
    body.classList.toggle('sidebar-collapsed', collapsed);
    var btn = document.querySelector('[data-sidebar-collapse]');
    if (btn) {
      var label = collapsed ? 'Expand sidebar' : 'Collapse sidebar';
      btn.setAttribute('aria-label', label);
      btn.title = label;
    }
    try { localStorage.setItem(COLLAPSE_KEY, collapsed ? 'collapsed' : 'open'); } catch (e) { /* storage blocked: not essential */ }
  }

  if (sidebar) {
    try { if (localStorage.getItem(COLLAPSE_KEY) === 'collapsed') setCollapsed(true); } catch (e) { /* ignore */ }

    document.addEventListener('click', function (e) {
      var t = e.target.closest ? e.target.closest('[data-sidebar-open], [data-sidebar-close], [data-sidebar-collapse]') : null;
      if (!t) return;
      if (t.hasAttribute('data-sidebar-open')) setDrawer(!body.classList.contains('sidebar-open'));
      else if (t.hasAttribute('data-sidebar-close')) setDrawer(false, true);
      else setCollapsed(!body.classList.contains('sidebar-collapsed'));
    });

    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && body.classList.contains('sidebar-open')) setDrawer(false, true);
    });

    // rotating or resizing to a wide screen must not leave the drawer "open" behind the layout
    var wide = window.matchMedia('(min-width: 901px)');
    var onWide = function () { if (wide.matches) setDrawer(false); };
    if (wide.addEventListener) wide.addEventListener('change', onWide); else wide.addListener(onWide);
  }

  // Reload the live preview whenever the server says content changed (HX-Trigger: preview-refresh).
  document.addEventListener('preview-refresh', function () {
    var f = document.getElementById('preview');
    if (f && f.contentWindow) f.contentWindow.location.reload();
  });

  // ---- page editor: show a block's edits in the preview while they are typed, before they are saved ----
  // The form is sent to the server as a draft (nothing is saved); then the block's own piece of the preview is
  // swapped for the freshly drawn one, so the rest of the preview, its scroll position and its menus stay as they are.
  (function () {
    var frame = document.getElementById('preview');
    var csrf = document.querySelector('meta[name="csrf"]');
    if (!frame || !csrf || !document.getElementById('blocks')) return;
    var timers = {}, latest = {};

    function formOf(el) {
      var f = el && el.closest ? el.closest('details.blk form') : null;
      return f && /^\/admin\/blocks\/\d+$/.test(f.getAttribute('hx-post') || '') ? f : null;
    }

    function refresh(id) {
      var win = frame.contentWindow, doc = frame.contentDocument;
      if (!win || !doc) return;
      fetch(win.location.href, { credentials: 'same-origin', cache: 'no-store' })
        .then(function (r) { return r.ok ? r.text() : Promise.reject(); })
        .then(function (html) { // parsed by the preview's own window: its inline styles are judged by the preview's policy, not the admin's
          var fresh = new win.DOMParser().parseFromString(html, 'text/html').getElementById('block-' + id);
          var old = doc.getElementById('block-' + id);
          if (fresh && old) old.replaceWith(doc.importNode(fresh, true));
          else win.location.reload(); // not on this page (or not drawn yet): start the preview again
        })
        .catch(function () {});
    }

    function send(form, id) {
      var n = latest[id] = (latest[id] || 0) + 1;
      fetch('/admin/blocks/' + id + '/draft', {
        method: 'POST', credentials: 'same-origin',
        headers: { 'X-CSRF-Token': csrf.content },
        body: new URLSearchParams(new FormData(form))
      }).then(function (r) { if (r.ok && n === latest[id]) refresh(id); }).catch(function () {});
    }

    function schedule(form) {
      var id = form.getAttribute('hx-post').split('/').pop();
      clearTimeout(timers[id]);
      timers[id] = setTimeout(function () { send(form, id); }, 300);
    }

    ['input', 'change'].forEach(function (type) {
      document.addEventListener(type, function (e) {
        var form = formOf(e.target);
        if (form) schedule(form);
      });
    });
    // adding, removing or moving an item of a list changes the form without typing in it
    document.addEventListener('click', function (e) {
      var b = e.target.closest && e.target.closest('[data-rep]');
      var form = b && formOf(b);
      if (form) schedule(form);
    });
  })();


  // ---- rich text editor (Tiptap) ----
  // Every textarea marked data-richtext becomes a visual editor. The textarea stays in the form, hidden, and always
  // holds the editor's HTML, so saving, the live preview and the server's cleaning work as they always did. Without
  // JavaScript the textarea and its plain buttons are still there.
  (function () {
    var TT = window.WBTiptap;
    if (!TT) return;

    function esc(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); }

    // text written without tags ("a blank line starts a new paragraph") becomes paragraphs
    function toHTML(v) {
      v = (v || '').trim();
      if (!v || /<[a-z][\s\S]*>/i.test(v)) return v;
      return v.split(/\n{2,}/).map(function (p) { return '<p>' + esc(p).replace(/\n/g, '<br>') + '</p>'; }).join('');
    }

    var SEP = '|';
    var BUTTONS = [
      ['bold', '<b>B</b>', 'Bold'], ['italic', '<i>I</i>', 'Italic'], ['underline', '<u>U</u>', 'Underline'], SEP,
      ['h2', 'H2', 'Heading'], ['h3', 'H3', 'Subheading'], ['p', '¶', 'Normal text'], SEP,
      ['ul', '• List', 'Bulleted list'], ['ol', '1. List', 'Numbered list'], ['quote', '“ ”', 'Quote'], SEP,
      ['link', 'Link', 'Add or change a link'], ['unlink', 'Unlink', 'Remove the link'], ['image', 'Image', 'Insert an image from the library'], SEP,
      ['undo', '↶', 'Undo'], ['redo', '↷', 'Redo']
    ];

    function active(ed, k) {
      switch (k) {
        case 'bold': case 'italic': case 'underline': return ed.isActive(k);
        case 'h2': return ed.isActive('heading', { level: 2 });
        case 'h3': return ed.isActive('heading', { level: 3 });
        case 'ul': return ed.isActive('bulletList');
        case 'ol': return ed.isActive('orderedList');
        case 'quote': return ed.isActive('blockquote');
        case 'link': return ed.isActive('link');
      }
      return false;
    }

    function run(ed, k) {
      var c = ed.chain().focus();
      switch (k) {
        case 'bold': c.toggleBold().run(); break;
        case 'italic': c.toggleItalic().run(); break;
        case 'underline': c.toggleUnderline().run(); break;
        case 'h2': c.toggleHeading({ level: 2 }).run(); break;
        case 'h3': c.toggleHeading({ level: 3 }).run(); break;
        case 'p': c.setParagraph().run(); break;
        case 'ul': c.toggleBulletList().run(); break;
        case 'ol': c.toggleOrderedList().run(); break;
        case 'quote': c.toggleBlockquote().run(); break;
        case 'unlink': c.unsetLink().run(); break;
        case 'undo': c.undo().run(); break;
        case 'redo': c.redo().run(); break;
        case 'link':
          var url = window.prompt('Link address (e.g. /about or https://example.com). Leave empty to remove the link.', ed.getAttributes('link').href || '');
          if (url === null) return;
          if (url.trim() === '') c.unsetLink().run();
          else c.extendMarkRange('link').setLink({ href: url.trim() }).run();
          break;
      }
    }

    function init(ta) {
      if (ta.getAttribute('data-rich-ready')) return;
      var label = ta.closest('label');
      if (!label) return;
      ta.setAttribute('data-rich-ready', '1');
      var old = label.querySelector('.rtbar');
      if (old) old.remove();

      var wrap = document.createElement('div');
      wrap.className = 'tt-wrap';
      var bar = document.createElement('div');
      bar.className = 'tt-bar';
      bar.setAttribute('role', 'toolbar');
      bar.setAttribute('aria-label', 'Formatting');
      var edit = document.createElement('div');
      edit.className = 'tt-editor';
      var btns = {};
      BUTTONS.forEach(function (b) {
        if (b === SEP) { var s = document.createElement('span'); s.className = 'tt-sep'; bar.appendChild(s); return; }
        var el = document.createElement('button');
        el.type = 'button';
        el.className = 'ghost';
        el.innerHTML = b[1];
        el.title = b[2];
        el.setAttribute('aria-label', b[2]);
        el.setAttribute('data-tt', b[0]);
        if (b[0] === 'image') { el.setAttribute('data-pick', ''); el.setAttribute('data-insert', ''); }
        bar.appendChild(el);
        btns[b[0]] = el;
      });
      wrap.appendChild(bar);
      wrap.appendChild(edit);
      ta.classList.add('tt-source');
      ta.parentNode.insertBefore(wrap, ta);

      var name = (label.firstChild && label.firstChild.textContent || '').trim() || 'Content';
      var ed = new TT.Editor({
        element: edit,
        extensions: [
          TT.StarterKit.configure({
            heading: { levels: [2, 3] },
            trailingNode: false, // no empty paragraph kept at the end of the text
            link: { openOnClick: false, HTMLAttributes: { target: null, rel: null } }
          }),
          TT.Image
        ],
        content: toHTML(ta.value),
        editorProps: { attributes: { class: 'tt-content', role: 'textbox', 'aria-multiline': 'true', 'aria-label': name } },
        onUpdate: function () {
          var html = ed.isEmpty ? '' : ed.getHTML();
          if (ta.value !== html) {
            ta.value = html;
            ta.dispatchEvent(new Event('input', { bubbles: true }));
          }
        },
        onTransaction: function () {
          Object.keys(btns).forEach(function (k) {
            var on = active(ed, k);
            btns[k].setAttribute('aria-pressed', on ? 'true' : 'false');
            btns[k].classList.toggle('on', on);
          });
          btns.undo.disabled = !ed.can().undo();
          btns.redo.disabled = !ed.can().redo();
        }
      });
      ta.wbEditor = ed;

      // the editor sits inside the field's <label>: a click in it must not be passed on to the label's control
      wrap.addEventListener('click', function (e) {
        e.preventDefault();
        var b = e.target.closest && e.target.closest('button[data-tt]');
        if (b && b.getAttribute('data-tt') !== 'image') run(ed, b.getAttribute('data-tt'));
      });
      bar.addEventListener('mousedown', function (e) { e.preventDefault(); }); // keep the selection while a button is pressed
    }

    function initAll(root) {
      if (root.matches && root.matches('textarea[data-richtext]')) init(root);
      if (root.querySelectorAll) Array.prototype.forEach.call(root.querySelectorAll('textarea[data-richtext]'), init);
    }

    initAll(document);
    // blocks added, swapped in by htmx, or cloned for a new list item
    new MutationObserver(function (muts) {
      muts.forEach(function (m) { Array.prototype.forEach.call(m.addedNodes, function (n) { if (n.nodeType === 1) initAll(n); }); });
    }).observe(document.body, { childList: true, subtree: true });
  })();


  // Keep block cards open across list swaps (move/delete/add replace the whole list).
  var open = [];
  document.addEventListener('htmx:beforeRequest', function () {
    open = Array.prototype.map.call(document.querySelectorAll('details.blk[open]'), function (d) { return d.id; });
  });
  document.addEventListener('htmx:afterSwap', function () {
    open.forEach(function (id) { var d = document.getElementById(id); if (d) d.open = true; });
  });

  // Colour fields: keep the text box and the picker in sync.
  document.addEventListener('input', function (e) {
    var t = e.target;
    if (!t.matches) return;
    var row = t.closest('.colorrow');
    if (!row) return;
    var text = row.querySelector('[data-color-text]'), pick = row.querySelector('[data-color-pick]');
    if (t === pick) text.value = pick.value;
    else if (/^#[0-9a-fA-F]{6}$/.test(text.value)) pick.value = text.value;
  });

  // Theme swatches: colours come from data attributes and are applied through the
  // CSS object model, which a strict Content-Security-Policy (no inline styles) allows.
  document.querySelectorAll('[data-color]').forEach(function (el) {
    el.style.background = el.getAttribute('data-color');
  });

  // Read-only URL boxes: select everything when focused, ready to copy.
  document.addEventListener('focusin', function (e) {
    if (e.target.matches && e.target.matches('[data-select]')) e.target.select();
  });

  // Confirm destructive form submits: <form data-confirm="message">.
  document.addEventListener('submit', function (e) {
    var msg = e.target.getAttribute && e.target.getAttribute('data-confirm');
    if (msg && !window.confirm(msg)) e.preventDefault();
  });

  // ---- media: uploads and the image picker ----
  var MAX_BYTES = 10 * 1024 * 1024;
  function csrf() { var m = document.querySelector('meta[name=csrf]'); return m ? m.content : ''; }

  // Uploads files as JSON; resolves to {uploaded: [...], errors: [...]}.
  function upload(files) {
    var fd = new FormData(), errors = [];
    Array.prototype.forEach.call(files, function (f) {
      if (f.size > MAX_BYTES) errors.push(f.name + ': larger than 10 MB.');
      else fd.append('files', f);
    });
    if (!fd.has('files')) return Promise.resolve({ uploaded: [], errors: errors });
    return fetch('/admin/media', {
      method: 'POST', body: fd, credentials: 'same-origin',
      headers: { 'X-CSRF-Token': csrf(), 'Accept': 'application/json' }
    }).then(function (r) { return r.json(); })
      .catch(function () { return { uploaded: [], errors: ['Upload failed.'] }; })
      .then(function (j) { j.errors = errors.concat(j.errors || []); return j; });
  }

  var dialog = document.getElementById('media-picker'), target = null;
  var msg = document.getElementById('picker-msg');

  function loadPicker() { htmx.ajax('GET', '/admin/media/picker', { target: '#picker-body', swap: 'innerHTML' }); }

  // ---- rich text toolbar ----
  function attrEsc(s) { return String(s).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;'); }

  function insertAtCursor(ta, text) {
    var s = ta.selectionStart, e = ta.selectionEnd;
    ta.setRangeText(text, s, e, 'end');
    ta.focus();
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  }

  function formatText(ta, kind) {
    var sel = ta.value.slice(ta.selectionStart, ta.selectionEnd), out;
    var wrap = { strong: 'strong', em: 'em', h2: 'h2', h3: 'h3', quote: 'blockquote' }[kind];
    if (wrap) {
      out = '<' + wrap + '>' + (sel || 'text') + '</' + wrap + '>';
    } else if (kind === 'ul') {
      var lines = (sel || 'Item').split('\n').map(function (l) { return l.trim(); }).filter(Boolean);
      out = '<ul>\n' + lines.map(function (l) { return '  <li>' + l + '</li>'; }).join('\n') + '\n</ul>';
    } else if (kind === 'a') {
      var url = window.prompt('Link address (e.g. /about or https://example.com)');
      if (!url) return;
      out = '<a href="' + attrEsc(url) + '">' + (sel || attrEsc(url)) + '</a>';
    } else {
      return;
    }
    insertAtCursor(ta, out);
  }

  var insertMode = false;

  function choose(item) {
    var url = item.getAttribute('data-media-url');
    if (insertMode && target.wbEditor) { // the visual editor: put the picture where the cursor is
      target.wbEditor.chain().focus().setImage({ src: url, alt: item.getAttribute('data-media-alt') || '' }).run();
      dialog.close();
      return;
    }
    if (insertMode) { // toolbar "Image": put an <img> at the cursor
      insertAtCursor(target, '<img src="' + attrEsc(url) + '" alt="' + attrEsc(item.getAttribute('data-media-alt') || '') + '">');
      dialog.close();
      return;
    }
    if (target.tagName === 'TEXTAREA') {
      target.value = (target.value.trim() ? target.value.replace(/\s+$/, '') + '\n' : '') + url;
    } else {
      target.value = url;
    }
    // prefill the block's alt text from the library if it is still empty
    var form = target.closest('form');
    var alt = form && form.querySelector('input[name="p.alt"]');
    if (alt && !alt.value) alt.value = item.getAttribute('data-media-alt') || '';
    target.dispatchEvent(new Event('input', { bubbles: true })); // the live preview listens for typing
    dialog.close();
  }

  document.addEventListener('click', function (e) {
    var t = e.target.closest ? e.target : e.target.parentElement;
    if (!t) return;
    var fmt = t.closest('[data-rt]');
    if (fmt && !fmt.hasAttribute('data-pick')) {
      formatText(fmt.closest('label').querySelector('textarea'), fmt.getAttribute('data-rt'));
      return;
    }
    var pick = t.closest('[data-pick]');
    if (pick && dialog) {
      insertMode = pick.hasAttribute('data-insert');
      target = pick.closest('label').querySelector('input[type=text], textarea');
      msg.textContent = '';
      dialog.showModal();
      loadPicker();
      return;
    }
    var item = t.closest('[data-media-url]');
    if (item && dialog && target) { choose(item); return; }
    if (t.closest('[data-picker-close]') && dialog) dialog.close();
    if (t === dialog) dialog.close(); // click on the backdrop
  });

  var pickerUpload = document.getElementById('picker-upload');
  if (pickerUpload) {
    pickerUpload.addEventListener('change', function () {
      msg.textContent = 'Uploading…';
      upload(pickerUpload.files).then(function (j) {
        msg.textContent = (j.errors || []).join(' ');
        pickerUpload.value = '';
        loadPicker();
      });
    });
  }

  // Library page: drag files onto the drop zone.
  var zone = document.querySelector('[data-dropzone]');
  if (zone) {
    ['dragenter', 'dragover'].forEach(function (n) {
      zone.addEventListener(n, function (e) { e.preventDefault(); zone.classList.add('over'); });
    });
    ['dragleave', 'drop'].forEach(function (n) {
      zone.addEventListener(n, function (e) { e.preventDefault(); zone.classList.remove('over'); });
    });
    zone.addEventListener('drop', function (e) {
      zone.querySelector('p').textContent = 'Uploading…';
      upload(e.dataTransfer.files).then(function (j) {
        if ((j.errors || []).length) window.alert(j.errors.join('\n'));
        window.location.reload();
      });
    });
    var input = zone.querySelector('input[type=file]');
    input.addEventListener('change', function () { // catch oversize files before sending them
      var big = Array.prototype.filter.call(input.files, function (f) { return f.size > MAX_BYTES; });
      if (big.length) {
        window.alert(big.map(function (f) { return f.name + ' is larger than 10 MB.'; }).join('\n'));
        input.value = '';
      }
    });
  }

  // ---- appearance: show token edits in the preview immediately ----
  // Two forms feed the preview: the light palette (data-token fields) and the dark palette
  // (data-dark-token fields). The dark form shows the preview in dark mode, the light form in light mode.
  var tokenForm = document.querySelector('[data-token-form]');
  var darkForm = document.querySelector('[data-dark-form]');
  var previewFrame = document.getElementById('preview');
  if (tokenForm && previewFrame) {
    var previewMode = null; // null = leave the preview as it loaded
    var setVar = function (doc, name, v) {
      if (v === '') doc.documentElement.style.removeProperty('--' + name);
      else doc.documentElement.style.setProperty('--' + name, v);
    };
    // Shows every unsaved edit of the mode being edited; the other mode's edits are cleared from the page.
    var syncPreview = function (overrides) {
      var doc = previewFrame.contentDocument;
      if (!doc) return;
      overrides = overrides || {};
      if (previewMode) doc.documentElement.setAttribute('data-theme', previewMode);
      tokenForm.querySelectorAll('[data-token]').forEach(function (f) {
        var v = (overrides[f.name] !== undefined ? overrides[f.name] : f.value).trim();
        if (f.dataset.kind === 'font') v = f.options[f.selectedIndex].dataset.stack || '';
        setVar(doc, f.dataset.token, previewMode === 'dark' ? '' : v);
      });
      if (darkForm && previewMode === 'dark') {
        darkForm.querySelectorAll('[data-dark-token]').forEach(function (f) {
          setVar(doc, f.dataset.darkToken, (overrides[f.name] !== undefined ? overrides[f.name] : f.value).trim());
        });
      }
    };
    var onInput = function (mode) {
      return function (e) {
        var t = e.target, o = {};
        previewMode = mode;
        if (t.matches('[data-color-pick]')) { // the shared handler copies this into the text box afterwards
          var text = t.closest('.colorrow').querySelector('[data-color-text]');
          o[text.name] = t.value;
        }
        syncPreview(o);
      };
    };
    tokenForm.addEventListener('input', onInput('light'));
    if (darkForm) darkForm.addEventListener('input', onInput('dark'));
    // a theme/CSS change reloads the frame: put the unsaved edits back
    previewFrame.addEventListener('load', function () { syncPreview(); });
  }

  // ---- menu builder: links, dropdowns and mega menus ----
  // The page renders every saved row; this adds/moves/removes rows at any level and, on
  // save, writes the whole tree into the hidden "structure" field as JSON (the server
  // validates it again).
  function fieldOf(row, name) {
    return row.querySelector(':scope > .mi-fields [data-f="' + name + '"]');
  }
  function textOf(row, name) { var el = fieldOf(row, name); return el ? el.value : ''; }
  function checkOf(row, name) { var el = fieldOf(row, name); return !!(el && el.checked); }

  function readLink(row) {
    return {
      label: textOf(row, 'label'), url: textOf(row, 'url'), description: textOf(row, 'description'),
      new_tab: checkOf(row, 'new_tab'), hidden: checkOf(row, 'hidden')
    };
  }

  function readItem(li) {
    var kind = li.getAttribute('data-kind') || 'link';
    var item = { kind: kind, label: textOf(li, 'label'), url: '', new_tab: false, button: false, hidden: checkOf(li, 'hidden') };
    if (kind === 'link') {
      item.url = textOf(li, 'url');
      item.new_tab = checkOf(li, 'new_tab');
      item.button = checkOf(li, 'button');
    } else if (kind === 'dropdown') {
      item.children = Array.prototype.map.call(
        li.querySelectorAll(':scope > .mi-dropdown [data-sub="children"] > [data-list] > .mi-link'), readLink);
    } else if (kind === 'mega') {
      item.columns = Array.prototype.map.call(
        li.querySelectorAll(':scope > .mi-mega [data-sub="columns"] > [data-list] > .mi-col'), function (col) {
          return {
            heading: textOf(col, 'heading'),
            url: textOf(col, 'url'),
            links: Array.prototype.map.call(
              col.querySelectorAll(':scope > [data-sub="links"] > [data-list] > .mi-link'), readLink)
          };
        });
    }
    return item;
  }

  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form.matches || !form.matches('[data-menu-form]')) return;
    var rows = form.querySelectorAll('[data-sub="items"] > [data-list] > .mi');
    form.elements.structure.value = JSON.stringify(Array.prototype.map.call(rows, readItem));
  });

  document.addEventListener('change', function (e) {
    var sel = e.target;
    if (sel.matches && sel.matches('.mi > .mi-fields select[data-f="kind"]')) {
      sel.closest('.mi').setAttribute('data-kind', sel.value);
    }
  });

  document.addEventListener('click', function (e) {
    var btn = e.target.closest && e.target.closest('[data-menu-builder] button[data-act], [data-footer-builder] button[data-act]');
    if (!btn) return;
    var act = btn.getAttribute('data-act');
    if (act === 'add') {
      var tpl = document.getElementById(btn.getAttribute('data-tpl'));
      var list = btn.parentElement.querySelector(':scope > [data-list]');
      list.appendChild(tpl.content.cloneNode(true));
      var added = list.lastElementChild.querySelector('input');
      if (added) added.focus();
      return;
    }
    var row = btn.closest('.mi-link, .mi-col, .mi, .fb-widget, .fb-row'); // the innermost row this button belongs to
    if (!row) return;
    if (act === 'find') { toggleFind(row, btn); return; }
    if (act === 'remove') row.remove();
    else if (act === 'up' && row.previousElementSibling) row.parentNode.insertBefore(row, row.previousElementSibling);
    else if (act === 'down' && row.nextElementSibling) row.parentNode.insertBefore(row.nextElementSibling, row);
  });

  // "Find…" on a menu or footer link row: search the site's pages and posts (listed in the
  // builder's data-destinations) and fill in the row's label (a section's title) and address.
  var menuDests = null;
  function destinations() {
    if (menuDests) return menuDests;
    var b = document.querySelector('[data-destinations]');
    try { menuDests = JSON.parse(b.getAttribute('data-destinations') || '[]') || []; } catch (err) { menuDests = []; }
    return menuDests;
  }

  function closeFind(row) {
    var box = row.querySelector(':scope > .mi-search');
    if (!box) return;
    box.remove();
    var btn = row.querySelector(':scope > .mi-fields [data-act="find"]');
    if (btn) btn.setAttribute('aria-expanded', 'false');
  }

  function toggleFind(row, btn) {
    if (row.querySelector(':scope > .mi-search')) { closeFind(row); btn.focus(); return; }
    var box = document.createElement('div');
    box.className = 'mi-search';
    var input = document.createElement('input');
    input.type = 'search';
    input.placeholder = 'Search pages and posts…';
    input.setAttribute('aria-label', 'Search pages and posts');
    var list = document.createElement('ul');
    list.className = 'mi-results';
    box.appendChild(input);
    box.appendChild(list);
    row.querySelector(':scope > .mi-fields').after(box);
    btn.setAttribute('aria-expanded', 'true');

    function pick(d) {
      var name = fieldOf(row, 'heading') ? 'heading' : 'label';
      fieldOf(row, name).value = d.title;
      fieldOf(row, 'url').value = d.url;
      closeFind(row);
      btn.focus();
    }
    function show() {
      var words = input.value.toLowerCase().split(/\s+/).filter(Boolean);
      var found = destinations().filter(function (d) {
        var hay = (d.title + ' ' + d.url).toLowerCase();
        return words.every(function (w) { return hay.indexOf(w) !== -1; });
      }).slice(0, 12);
      list.textContent = '';
      if (!found.length) {
        var none = document.createElement('li');
        none.className = 'mi-r-none';
        none.textContent = 'No pages or posts match.';
        list.appendChild(none);
      }
      found.forEach(function (d) {
        var li = document.createElement('li');
        var b = document.createElement('button');
        b.type = 'button';
        var t = document.createElement('span');
        t.className = 'mi-r-title';
        t.textContent = d.title;
        var m = document.createElement('span');
        m.className = 'mi-r-meta';
        m.textContent = d.type + (d.draft ? ' (draft)' : '') + ' · ' + d.url;
        b.appendChild(t);
        b.appendChild(m);
        b.addEventListener('click', function () { pick(d); });
        li.appendChild(b);
        list.appendChild(li);
      });
      return found;
    }
    input.addEventListener('input', show);
    box.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') { e.preventDefault(); closeFind(row); btn.focus(); return; }
      if (e.target === input && e.key === 'Enter') { // pick the first match; never submit the menu form
        e.preventDefault();
        var first = show()[0];
        if (first) pick(first);
      } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        var btns = Array.prototype.slice.call(list.querySelectorAll('button'));
        var at = btns.indexOf(document.activeElement);
        e.preventDefault();
        if (e.key === 'ArrowDown') (btns[at + 1] || btns[0] || input).focus();
        else (at > 0 ? btns[at - 1] : input).focus();
      }
    });
    show();
    input.focus();
  }

  // ---- footer builder: rows of columns of widgets ----
  // Rows and the links inside a links widget use the shared add/up/down/remove buttons above;
  // this adds widgets, moves them between columns, keeps a row's columns in step with its
  // "Columns" choice and, on save, writes the whole footer into the hidden "structure" field.
  function fbCols(row) { return row.querySelectorAll(':scope > .fb-cols > .fb-col'); }

  function fbSetCount(row, n) {
    var box = row.querySelector(':scope > .fb-cols'), cols = fbCols(row);
    for (var i = cols.length; i < n; i++) box.appendChild(document.getElementById('tpl-fb-col').content.cloneNode(true));
    cols = fbCols(row);
    if (cols.length > n) { // widgets of the columns that go away join the end of the last one kept
      var keep = cols[n - 1].querySelector(':scope > [data-list]');
      for (var j = n; j < cols.length; j++) {
        cols[j].querySelectorAll(':scope > [data-list] > .fb-widget').forEach(function (w) { keep.appendChild(w); });
        cols[j].remove();
      }
    }
    box.setAttribute('data-cols', n);
  }

  function fbReadWidget(el) {
    var f = function (name) { var x = el.querySelector(':scope > .fb-body [data-f="' + name + '"]'); return x ? (x.type === 'checkbox' ? x.checked : x.value) : ''; };
    var w = { type: el.getAttribute('data-type'), heading: f('heading') || '', text: f('text') || '' };
    if (w.type === 'links') {
      w.inline = !!f('inline');
      w.links = Array.prototype.map.call(el.querySelectorAll(':scope > .fb-body .fb-links > [data-list] > .mi-link'), function (l) {
        return { label: textOf(l, 'label'), url: textOf(l, 'url'), new_tab: checkOf(l, 'new_tab') };
      });
    } else if (w.type === 'contact') {
      w.parts = Array.prototype.filter.call(el.querySelectorAll(':scope > .fb-body [data-part]'), function (c) { return c.checked; })
        .map(function (c) { return c.getAttribute('data-part'); });
    } else if (w.type === 'newsletter') {
      w.button = f('button'); w.success = f('success');
    }
    return w;
  }

  function fbRead(form) {
    var val = function (name) { return form.querySelector('[data-fb="' + name + '"]').value; };
    return {
      version: 2,
      bottom: val('bottom'),
      style: { background: val('background'), text: val('text'), padding: val('padding') },
      rows: Array.prototype.map.call(form.querySelectorAll('.fb-rows > .fb-row'), function (row) {
        var head = row.querySelector(':scope > .fb-row-head');
        return {
          ratio: head.querySelector('[data-f="ratio"]').value,
          align: head.querySelector('[data-f="align"]').value,
          columns: Array.prototype.map.call(fbCols(row), function (col) {
            return { widgets: Array.prototype.map.call(col.querySelectorAll(':scope > [data-list] > .fb-widget'), fbReadWidget) };
          })
        };
      })
    };
  }

  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (form.matches && form.matches('[data-footer-form]')) form.elements.structure.value = JSON.stringify(fbRead(form));
  });

  document.addEventListener('change', function (e) {
    var sel = e.target;
    if (sel.matches && sel.matches('.fb-row-head [data-f="count"]')) fbSetCount(sel.closest('.fb-row'), parseInt(sel.value, 10) || 1);
  });

  document.addEventListener('click', function (e) {
    var btn = e.target.closest && e.target.closest('[data-footer-builder] button[data-act]');
    if (!btn) return;
    var act = btn.getAttribute('data-act');
    if (act === 'add-widget') {
      var col = btn.closest('.fb-col');
      var type = col.querySelector(':scope > .fb-add [data-add-widget]').value;
      var list = col.querySelector(':scope > [data-list]');
      list.appendChild(document.getElementById('tpl-fb-w-' + type).content.cloneNode(true));
      var first = list.lastElementChild.querySelector('input, textarea');
      if (first) first.focus();
    } else if (act === 'left' || act === 'right') {
      var w = btn.closest('.fb-widget'), from = w.closest('.fb-col');
      var to = act === 'left' ? from.previousElementSibling : from.nextElementSibling;
      if (to) { to.querySelector(':scope > [data-list]').appendChild(w); btn.focus(); }
    }
  });

  // ---- search & sharing: length counters and a live search-result preview ----
  // Guidance only: nothing here blocks a save. Ranges match internal/seo (title 15-60, description 70-160).
  function seoCount(span, n, min, max) {
    span.textContent = n === 0 ? '' : n + ' characters';
    span.className = 'count' + (n === 0 ? '' : (n < min || n > max) ? ' warn' : ' ok');
  }
  function seoClip(s, max) {
    s = s.replace(/\s+/g, ' ').trim();
    var chars = Array.from(s);
    return chars.length > max ? chars.slice(0, max - 1).join('').replace(/\s+\S*$/, '') + '…' : s;
  }
  document.querySelectorAll('form').forEach(function (form) {
    var titleIn = form.elements['seo_title'], descIn = form.elements['seo_description'];
    if (!descIn) return;
    var box = form.querySelector('[data-seo]');
    var counts = {};
    form.querySelectorAll('[data-count-for]').forEach(function (s) { counts[s.getAttribute('data-count-for')] = s; });
    var ownTitle = form.elements['title'];
    function update() {
      var desc = descIn.value.trim();
      if (counts.seo_description) seoCount(counts.seo_description, Array.from(desc).length, 70, 160);
      if (!box) return;
      var site = box.getAttribute('data-site'), heading = box.getAttribute('data-heading');
      // the home page's heading is empty on purpose: its tab title is just the site name
      var own = heading !== '' && ownTitle ? ownTitle.value.trim() : heading;
      var title = titleIn.value.trim() || (own ? own + ' · ' + site : site);
      if (counts.seo_title) seoCount(counts.seo_title, Array.from(title).length, 15, 60);
      var path = box.getAttribute('data-path');
      box.querySelector('[data-snippet=url]').textContent = box.getAttribute('data-host') + (path && path !== '/' ? ' › ' + path.replace(/^\//, '').split('/').join(' › ') : '');
      box.querySelector('[data-snippet=title]').textContent = seoClip(title, 60);
      box.querySelector('[data-snippet=desc]').textContent = seoClip(desc || descIn.placeholder, 160);
    }
    form.addEventListener('input', update);
    update();
  });
  // Repeating lists in the block editor: add, remove and reorder items in the browser. Every change renumbers the
  // fields (p.cards.0.title, p.cards.1.title…) in the order shown, which is the order the server saves.
  function repRenumber(rep) {
    var list = rep.querySelector('[data-rep-list]');
    var items = list.querySelectorAll(':scope > [data-rep-item]');
    Array.prototype.forEach.call(items, function (item, i) {
      var n = item.querySelector('[data-rep-n]');
      if (n) n.textContent = String(i + 1);
      Array.prototype.forEach.call(item.querySelectorAll('[name]'), function (el) {
        el.name = el.name.replace(/\.(\d+|__N__)\./, '.' + i + '.');
      });
    });
    var max = parseInt(rep.getAttribute('data-max'), 10) || 200;
    var add = rep.querySelector('[data-rep="add"]');
    if (add) add.disabled = items.length >= max;
  }

  document.addEventListener('click', function (e) {
    var btn = e.target.closest ? e.target.closest('[data-rep]') : null;
    var rep = btn && btn.closest('[data-repeater]');
    if (!rep) return;
    var list = rep.querySelector('[data-rep-list]');
    var what = btn.getAttribute('data-rep');
    if (what === 'add') {
      var tpl = rep.querySelector('template[data-rep-template]');
      if (!tpl || btn.disabled) return;
      list.appendChild(tpl.content.cloneNode(true));
      repRenumber(rep);
      var added = list.lastElementChild && list.lastElementChild.querySelector('input, textarea, select');
      if (added) added.focus();
      return;
    }
    var item = btn.closest('[data-rep-item]');
    if (!item) return;
    if (what === 'remove') item.remove();
    else if (what === 'up' && item.previousElementSibling) list.insertBefore(item, item.previousElementSibling);
    else if (what === 'down' && item.nextElementSibling) list.insertBefore(item.nextElementSibling, item);
    repRenumber(rep);
    if (what !== 'remove') btn.focus();
  });

  document.addEventListener('DOMContentLoaded', function () {
    Array.prototype.forEach.call(document.querySelectorAll('[data-repeater]'), repRenumber);
  });
  document.addEventListener('htmx:afterSwap', function () {
    Array.prototype.forEach.call(document.querySelectorAll('[data-repeater]'), repRenumber);
  });

  // "Publish automatically on": show the saved time in the person's own time zone and send the chosen time as UTC.
  function pad2(n) { return (n < 10 ? '0' : '') + n; }
  function localInput(d) {
    return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()) + 'T' + pad2(d.getHours()) + ':' + pad2(d.getMinutes());
  }
  function initSchedule(box) {
    var input = box.querySelector('input[type="datetime-local"]');
    var hidden = box.querySelector('input[type="hidden"]');
    if (!input || !hidden) return;
    var utc = input.getAttribute('data-utc');
    if (utc) {
      var d = new Date(utc);
      if (!isNaN(d.getTime())) input.value = localInput(d);
    }
    function sync() {
      var d = input.value ? new Date(input.value) : null;
      hidden.value = d && !isNaN(d.getTime()) ? d.toISOString() : '';
    }
    input.addEventListener('input', sync);
    input.addEventListener('change', sync);
    if (input.form) input.form.addEventListener('submit', sync);
    sync();
  }
  Array.prototype.forEach.call(document.querySelectorAll('[data-schedule]'), initSchedule);

})();
