// Motion for the pages that had none: sections and cards rise in as they
// scroll into view, cards lift on hover, a thin bar shows scroll progress and
// a faint glow drifts behind the page.
//
// Rules it keeps:
// - Nothing is hidden unless this script runs, so the page reads fine without it.
// - Content already on screen at load is never hidden, so there is no flash.
// - Only opacity and transform change, so nothing shifts the layout.
// - Visitors who ask for reduced motion get none of it.
(function () {
  'use strict';
  if (window.__siteMotion) return;
  window.__siteMotion = true;

  var reduce = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  if (reduce || !('IntersectionObserver' in window)) return;

  var css = [
    '.sm-hide{opacity:0;transform:translateY(18px)}',
    '.sm-move{transition:opacity .7s ease,transform .8s cubic-bezier(.16,1,.3,1);transition-delay:var(--sm-delay,0ms)}',
    '.sm-card{transition:transform .25s ease,border-color .25s ease,box-shadow .25s ease}',
    '@media (hover:hover) and (pointer:fine){.sm-card:hover{transform:translateY(-3px);border-color:rgba(16,185,129,.45);box-shadow:0 12px 32px -18px rgba(16,185,129,.55)}}',
    '.sm-bar{position:fixed;top:0;left:0;height:2px;width:100%;transform:scaleX(0);transform-origin:0 50%;background:linear-gradient(90deg,#10b981,#22d3ee);z-index:60;pointer-events:none}',
    '.sm-glow{position:fixed;inset:-20% -10% auto -10%;height:70vh;z-index:-1;pointer-events:none;opacity:.5;' +
      'background:radial-gradient(40% 50% at 25% 30%,rgba(16,185,129,.10),transparent 70%),radial-gradient(35% 45% at 75% 20%,rgba(34,211,238,.07),transparent 70%);' +
      'animation:sm-drift 22s ease-in-out infinite alternate}',
    '@keyframes sm-drift{from{transform:translate3d(-2%,0,0)}to{transform:translate3d(2%,3%,0)}}',
    '@media print{.sm-hide{opacity:1!important;transform:none!important}.sm-bar,.sm-glow{display:none}}'
  ].join('');
  var style = document.createElement('style');
  style.textContent = css;
  document.head.appendChild(style);

  function start() {
    var fold = window.innerHeight * 0.92;
    var targets = [];
    var seen = new Set();
    function add(el, delay) {
      if (seen.has(el) || el.closest('[data-no-motion], form') || el.matches('label,button,input,select,textarea,a')) return;
      seen.add(el);
      if (el.getBoundingClientRect().top < fold) return; // already on screen: leave it alone
      if (delay) el.style.setProperty('--sm-delay', delay + 'ms');
      el.classList.add('sm-hide');
      targets.push(el);
    }
    document.querySelectorAll('main section, main > article, main > div, body > footer').forEach(function (el) { add(el, 0); });
    // Cards in a grid follow each other in, one beat apart.
    document.querySelectorAll('main section [class*="grid-cols"]').forEach(function (grid) {
      Array.prototype.forEach.call(grid.children, function (child, i) { add(child, Math.min(i, 5) * 70); });
    });

    function reveal(el) {
      if (!el.classList.contains('sm-hide') || el.classList.contains('sm-move')) return;
      io.unobserve(el);
      el.classList.add('sm-move');
      // Two frames so the transition starts from the hidden state.
      requestAnimationFrame(function () { requestAnimationFrame(function () { el.classList.remove('sm-hide'); }); });
    }
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) { if (e.isIntersecting || e.boundingClientRect.top < 0) reveal(e.target); });
    }, { threshold: 0.06 });
    targets.forEach(function (el) { io.observe(el); });
    // The observer can miss an element, for example one that was not rendered
    // when it was observed. On scroll, reveal anything hidden that is on screen.
    function sweep() {
      var h = window.innerHeight;
      targets.forEach(function (el) {
        if (!el.classList.contains('sm-hide') || el.classList.contains('sm-move')) return;
        var r = el.getBoundingClientRect();
        // Also reveal sections already scrolled past: a fast flick can skip them before the observer reports.
        if (r.height > 0 && r.top < h) reveal(el);
      });
    }

    // If anything is still hidden when the visitor jumps by link, prints or the
    // observer misses, show it. Content must never stay invisible.
    function showAll() { targets.forEach(function (el) { el.classList.remove('sm-hide'); }); }
    window.addEventListener('beforeprint', showAll);
    window.addEventListener('hashchange', function () {
      var el = location.hash.length > 1 && document.getElementById(decodeURIComponent(location.hash.slice(1)));
      for (var n = el; n && n !== document.body; n = n.parentElement) n.classList.remove('sm-hide');
      if (el) el.querySelectorAll('.sm-hide').forEach(function (c) { c.classList.remove('sm-hide'); });
    });

    // Cards lift on hover. Links and buttons keep their own hover styles.
    document.querySelectorAll('main [class*="rounded-xl"][class*="border"], main [class*="rounded-2xl"][class*="border"]').forEach(function (el) {
      if (el.matches('a,button,input,textarea,select,pre,dl') || el.closest('[data-no-motion]')) return;
      if (el.querySelector('[class*="rounded-xl"][class*="border"], [class*="rounded-2xl"][class*="border"], canvas, form')) return;
      el.classList.add('sm-card');
    });

    var glow = document.createElement('div');
    glow.className = 'sm-glow';
    glow.setAttribute('aria-hidden', 'true');
    document.body.appendChild(glow);

    var bar = document.createElement('div');
    bar.className = 'sm-bar';
    bar.setAttribute('aria-hidden', 'true');
    document.body.appendChild(bar);
    var ticking = false;
    function progress() {
      ticking = false;
      var max = document.documentElement.scrollHeight - window.innerHeight;
      bar.style.transform = 'scaleX(' + (max > 0 ? Math.min(1, window.scrollY / max) : 0) + ')';
      sweep();
    }
    window.addEventListener('scroll', function () {
      if (!ticking) { ticking = true; requestAnimationFrame(progress); }
    }, { passive: true });
    progress();
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
})();
