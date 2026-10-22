// Run by TestImageEditScript: checks the crop geometry of imageedit.js without a browser.
// Usage: node imageedit_check.js <path to imageedit.js>
const fs = require('fs'), vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const ctx = {};
vm.runInNewContext(src + '\nthis.WBCrop = WBCrop;', ctx); // no document: only the geometry loads
const C = ctx.WBCrop;
let failures = 0;
function check(name, got, want) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g !== w) { failures++; console.log('FAIL', name, '- got', g, 'want', w); } else console.log('ok  ', name);
}
const near = (a, b) => Math.abs(a - b) < 1e-6;
const B = { w: 400, h: 300 };

check('a quarter turn swaps the sides', [C.turnedSize(40, 20, 90), C.turnedSize(40, 20, 180), C.turnedSize(40, 20, 270), C.turnedSize(40, 20, 0)],
  [{ w: 20, h: 40 }, { w: 40, h: 20 }, { w: 20, h: 40 }, { w: 40, h: 20 }]);

check('free shape fills the picture', C.biggest(B, 0), { x: 0, y: 0, w: 400, h: 300 });
check('square is centred and as big as fits', C.biggest(B, 1), { x: 50, y: 0, w: 300, h: 300 });
check('wide shape fills the width', C.biggest(B, 16 / 9).w, 400);
let tall = C.biggest({ w: 300, h: 400 }, 16 / 9);
check('wide shape in a tall picture is centred', [tall.w, near(tall.h, 300 * 9 / 16), near(tall.y, (400 - tall.h) / 2)], [300, true, true]);

check('moving stays inside (right/bottom)', C.move({ x: 100, y: 100, w: 100, h: 100 }, 999, 999, B), { x: 300, y: 200, w: 100, h: 100 });
check('moving stays inside (left/top)', C.move({ x: 100, y: 100, w: 100, h: 100 }, -999, -999, B), { x: 0, y: 0, w: 100, h: 100 });
check('moving keeps the size', C.move({ x: 10, y: 10, w: 50, h: 60 }, 5, 7, B), { x: 15, y: 17, w: 50, h: 60 });

const r0 = { x: 100, y: 100, w: 100, h: 100 };
check('dragging the bottom-right corner grows the box', C.resize(r0, 'se', 30, 20, B, 0, 16), { x: 100, y: 100, w: 130, h: 120 });
check('dragging the top-left corner moves the other corner away', C.resize(r0, 'nw', -40, -10, B, 0, 16), { x: 60, y: 90, w: 140, h: 110 });
check('dragging the top-right corner', C.resize(r0, 'ne', 20, -20, B, 0, 16), { x: 100, y: 80, w: 120, h: 120 });
check('dragging the bottom-left corner', C.resize(r0, 'sw', -20, 20, B, 0, 16), { x: 80, y: 100, w: 120, h: 120 });
check('the opposite corner never moves (se)', (function () { var r = C.resize(r0, 'se', 55, -33, B, 0, 16); return [r.x, r.y]; })(), [100, 100]);
check('the opposite corner never moves (nw)', (function () { var r = C.resize(r0, 'nw', 33, 22, B, 0, 16); return [r.x + r.w, r.y + r.h]; })(), [200, 200]);
check('cannot grow past the picture', C.resize(r0, 'se', 9999, 9999, B, 0, 16), { x: 100, y: 100, w: 300, h: 200 });
check('cannot grow past the top-left edge', C.resize(r0, 'nw', -9999, -9999, B, 0, 16), { x: 0, y: 0, w: 200, h: 200 });
check('cannot shrink below the minimum', C.resize(r0, 'se', -95, -95, B, 0, 16), { x: 100, y: 100, w: 16, h: 16 });
check('dragging a long way past the opposite corner stays inside', C.resize(r0, 'se', -9999, -9999, B, 0, 16), { x: 0, y: 0, w: 100, h: 100 });
check('dragging past the opposite corner flips the box', (function () { var r = C.resize(r0, 'se', -150, -150, B, 0, 16); return [r.x, r.y, r.w, r.h]; })(), [50, 50, 50, 50]);

// keeping a shape
const sq = { x: 100, y: 100, w: 100, h: 100 };
const keep = C.resize(sq, 'se', 60, 10, B, 1, 16);
check('a square stays square', [near(keep.w, keep.h), keep.x, keep.y], [true, 100, 100]);
const wide = C.resize({ x: 0, y: 0, w: 160, h: 90 }, 'se', 80, 0, B, 16 / 9, 16);
check('a 16:9 box stays 16:9', near(wide.w / wide.h, 16 / 9), true);
const pinned = C.resize({ x: 200, y: 150, w: 100, h: 100 }, 'se', 9999, 9999, B, 1, 16);
check('a square stops at the nearest edge', [near(pinned.w, pinned.h), pinned.x + pinned.w <= 400 + 1e-6, pinned.y + pinned.h <= 300 + 1e-6, near(pinned.h, 150)], [true, true, true, true]);
const small = C.resize({ x: 100, y: 100, w: 100, h: 100 }, 'se', -500, -500, B, 2, 16);
check('the minimum applies with a shape too', [small.w >= 16 - 1e-6, small.h >= 16 - 1e-6, near(small.w / small.h, 2)], [true, true, true]);

check('whole pixels of the real image', C.toPixels({ x: 50, y: 25, w: 100, h: 50 }, 0.5, { w: 800, h: 400 }), { x: 100, y: 50, w: 200, h: 100 });
check('never outside the real image', C.toPixels({ x: 399.9, y: 299.9, w: 50, h: 50 }, 1, B), { x: 400 - 1, y: 300 - 1, w: 1, h: 1 });
check('at least one pixel', C.toPixels({ x: 0, y: 0, w: 0.1, h: 0.1 }, 1, B), { x: 0, y: 0, w: 1, h: 1 });
check('there and back', C.toScreen({ x: 100, y: 50, w: 200, h: 100 }, 0.5), { x: 50, y: 25, w: 100, h: 50 });
check('clamp', [C.clamp(5, 0, 3), C.clamp(-5, 0, 3), C.clamp(2, 0, 3)], [3, 0, 2]);

if (failures) { console.log(failures + ' check(s) failed'); process.exit(1); }
