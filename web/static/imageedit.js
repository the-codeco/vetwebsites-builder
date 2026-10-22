// Image editor: turn, mirror and crop an image in the media library, with a live preview.
// The form works without this script (numbers for the crop box); the script adds a picture you can drag.
// The geometry is kept in plain functions (WBCrop) so it can be checked without a browser.
var WBCrop = (function () {
  'use strict';

  function clamp(v, lo, hi) { return Math.min(Math.max(v, lo), hi); }

  // The size of an image once turned: a quarter turn swaps width and height.
  function turnedSize(w, h, rot) { return rot % 180 === 0 ? { w: w, h: h } : { w: h, h: w }; }

  // The biggest centred box of a given shape (ratio = width / height; 0 = free) that fits in bounds.
  function biggest(bounds, ratio) {
    if (!ratio) return { x: 0, y: 0, w: bounds.w, h: bounds.h };
    var w = bounds.w, h = w / ratio;
    if (h > bounds.h) { h = bounds.h; w = h * ratio; }
    return { x: (bounds.w - w) / 2, y: (bounds.h - h) / 2, w: w, h: h };
  }

  // Slides a box without letting it leave the picture.
  function move(r, dx, dy, bounds) {
    return { x: clamp(r.x + dx, 0, bounds.w - r.w), y: clamp(r.y + dy, 0, bounds.h - r.h), w: r.w, h: r.h };
  }

  // Drags one corner ('nw', 'ne', 'sw' or 'se') by (dx, dy); the opposite corner stays put, and dragging past it
  // turns the box over to the other side. With a ratio the box keeps its shape. It stays inside the picture and
  // never gets smaller than min.
  function resize(r, corner, dx, dy, bounds, ratio, min) {
    var west = corner.indexOf('w') >= 0, north = corner.indexOf('n') >= 0;
    var ax = west ? r.x + r.w : r.x, ay = north ? r.y + r.h : r.y; // the fixed corner
    var px = clamp((west ? r.x : r.x + r.w) + dx, 0, bounds.w);
    var py = clamp((north ? r.y : r.y + r.h) + dy, 0, bounds.h);
    // which side of the fixed corner the dragged one is on (a tie keeps the side it started on)
    var right = px === ax ? !west : px > ax, down = py === ay ? !north : py > ay;
    var w = Math.max(Math.abs(px - ax), min), h = Math.max(Math.abs(py - ay), min);
    var roomX = right ? bounds.w - ax : ax, roomY = down ? bounds.h - ay : ay;
    if (ratio) {
      if (w / h > ratio) h = w / ratio; else w = h * ratio;
      var shrink = Math.min(1, roomX / w, roomY / h);
      w *= shrink; h *= shrink;
      if (w < min) { w = min; h = min / ratio; }
      if (h < min) { h = min; w = min * ratio; }
    } else {
      w = Math.min(w, roomX); h = Math.min(h, roomY);
    }
    return { x: right ? ax : ax - w, y: down ? ay : ay - h, w: w, h: h };
  }

  // The box in whole pixels of the real image (the picture on screen is drawn at `scale` of full size).
  function toPixels(r, scale, full) {
    var x = clamp(Math.round(r.x / scale), 0, full.w - 1), y = clamp(Math.round(r.y / scale), 0, full.h - 1);
    var w = clamp(Math.round(r.w / scale), 1, full.w - x), h = clamp(Math.round(r.h / scale), 1, full.h - y);
    return { x: x, y: y, w: w, h: h };
  }

  // The box on screen for a box in real pixels.
  function toScreen(p, scale) { return { x: p.x * scale, y: p.y * scale, w: p.w * scale, h: p.h * scale }; }

  return { clamp: clamp, turnedSize: turnedSize, biggest: biggest, move: move, resize: resize, toPixels: toPixels, toScreen: toScreen };
})();

(function () {
  'use strict';
  if (typeof document === 'undefined') return;
  var canvas = document.getElementById('ie-canvas');
  if (!canvas) return;

  var MIN = 16; // pixels of the real image
  var stage = document.getElementById('ie-stage');
  var box = document.getElementById('ie-crop');
  var form = document.getElementById('ie-form');
  var img = new Image();
  var natural = { w: +canvas.getAttribute('data-w'), h: +canvas.getAttribute('data-h') };
  var f = {
    rotate: form.elements.rotate, flipH: form.elements.flip_h, flipV: form.elements.flip_v,
    x: form.elements.x, y: form.elements.y, w: form.elements.w, h: form.elements.h,
    ratio: document.getElementById('ie-ratio'), out: document.getElementById('ie-out')
  };
  var scale = 1, full = natural, rect = null, ctx = canvas.getContext('2d');

  function rot() { return parseInt(f.rotate.value, 10) || 0; }

  // draws the turned, mirrored picture and sizes the stage to it
  function draw() {
    full = WBCrop.turnedSize(natural.w, natural.h, rot());
    var avail = Math.max(200, Math.min(stage.parentNode.clientWidth || 720, 720));
    scale = Math.min(1, avail / full.w);
    var cw = Math.max(1, Math.round(full.w * scale)), ch = Math.max(1, Math.round(full.h * scale));
    canvas.width = cw; canvas.height = ch;
    stage.style.width = cw + 'px'; stage.style.height = ch + 'px';
    ctx.save();
    ctx.translate(cw / 2, ch / 2);
    ctx.scale(f.flipH.checked ? -1 : 1, f.flipV.checked ? -1 : 1); // turn first, then mirror
    ctx.rotate(rot() * Math.PI / 180);
    ctx.drawImage(img, -natural.w * scale / 2, -natural.h * scale / 2, natural.w * scale, natural.h * scale);
    ctx.restore();
  }

  function bounds() { return { w: canvas.width, h: canvas.height }; }
  function ratio() { return parseFloat(f.ratio.value) || 0; }
  function minScreen() { return Math.max(MIN * scale, 1); }

  function show() {
    if (!rect) { box.hidden = true; return; }
    box.hidden = false;
    box.style.left = rect.x + 'px'; box.style.top = rect.y + 'px';
    box.style.width = rect.w + 'px'; box.style.height = rect.h + 'px';
  }

  // box -> the number fields
  function publish() {
    if (!rect) { f.x.value = f.y.value = f.w.value = f.h.value = ''; report(); return; }
    var p = WBCrop.toPixels(rect, scale, full);
    f.x.value = p.x; f.y.value = p.y; f.w.value = p.w; f.h.value = p.h;
    report();
  }

  // the number fields -> box
  function adopt() {
    var p = { x: +f.x.value, y: +f.y.value, w: +f.w.value, h: +f.h.value };
    if (f.w.value === '' || f.h.value === '' || !(p.w > 0) || !(p.h > 0)) { rect = null; show(); report(); return; }
    p.x = WBCrop.clamp(p.x || 0, 0, full.w - MIN); p.y = WBCrop.clamp(p.y || 0, 0, full.h - MIN);
    p.w = WBCrop.clamp(p.w, MIN, full.w - p.x); p.h = WBCrop.clamp(p.h, MIN, full.h - p.y);
    rect = WBCrop.toScreen(p, scale);
    show(); report();
  }

  function report() {
    var p = rect ? WBCrop.toPixels(rect, scale, full) : { w: full.w, h: full.h };
    var w = p.w, h = p.h, max = parseInt(f.out.value, 10) || 0;
    if (max && w > max) { h = Math.round(h * max / w); w = max; }
    document.getElementById('ie-size').textContent = w + ' × ' + h + ' px';
  }

  function reset() {
    draw();
    var b = bounds();
    if (f.ratio.value === '') rect = null; // no crop
    else if (ratio()) rect = WBCrop.biggest(b, ratio());
    else rect = { x: b.w * 0.1, y: b.h * 0.1, w: b.w * 0.8, h: b.h * 0.8 }; // free shape: start inside the edges
    show(); publish();
  }

  // ---- turning and mirroring ----
  document.addEventListener('click', function (e) {
    var b = e.target.closest && e.target.closest('[data-ie]');
    if (!b) return;
    var act = b.getAttribute('data-ie');
    if (act === 'left') f.rotate.value = (rot() + 270) % 360;
    else if (act === 'right') f.rotate.value = (rot() + 90) % 360;
    else if (act === 'flip-h') f.flipH.checked = !f.flipH.checked;
    else if (act === 'flip-v') f.flipV.checked = !f.flipV.checked;
    else if (act === 'reset') { f.rotate.value = 0; f.flipH.checked = f.flipV.checked = false; f.ratio.value = ''; rect = null; }
    else return;
    rect = null;
    reset();
  });
  [f.rotate, f.flipH, f.flipV].forEach(function (el) { el.addEventListener('change', function () { rect = null; reset(); }); });
  f.ratio.addEventListener('change', function () { rect = null; reset(); });
  f.out.addEventListener('change', report);
  [f.x, f.y, f.w, f.h].forEach(function (el) { el.addEventListener('input', adopt); });

  // ---- dragging the box ----
  var drag = null;
  box.addEventListener('pointerdown', function (e) {
    if (!rect) return;
    var corner = e.target.getAttribute && e.target.getAttribute('data-corner');
    drag = { corner: corner, x: e.clientX, y: e.clientY, start: { x: rect.x, y: rect.y, w: rect.w, h: rect.h } };
    box.setPointerCapture(e.pointerId);
    e.preventDefault();
  });
  box.addEventListener('pointermove', function (e) {
    if (!drag) return;
    var dx = e.clientX - drag.x, dy = e.clientY - drag.y;
    rect = drag.corner
      ? WBCrop.resize(drag.start, drag.corner, dx, dy, bounds(), ratio(), minScreen())
      : WBCrop.move(drag.start, dx, dy, bounds());
    show(); publish();
  });
  function end() { drag = null; }
  box.addEventListener('pointerup', end);
  box.addEventListener('pointercancel', end);

  // arrow keys nudge a focused box; Shift moves ten at a time
  box.addEventListener('keydown', function (e) {
    var d = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[e.key];
    if (!d || !rect) return;
    var step = (e.shiftKey ? 10 : 1) * scale;
    rect = WBCrop.move(rect, d[0] * step, d[1] * step, bounds());
    e.preventDefault(); show(); publish();
  });

  img.addEventListener('load', function () { stage.classList.add('ready'); reset(); adopt(); });
  img.src = canvas.getAttribute('data-src');
  window.addEventListener('resize', function () { var r = rect && WBCrop.toPixels(rect, scale, full); draw(); rect = r && WBCrop.toScreen(r, scale); show(); });
})();
