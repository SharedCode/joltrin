// Sound for the Private Intent and Agents pages. Every sound is synthesized here, so there are no audio files.
// Browsers only allow audio after a click, tap or key press, so sound starts at the visitor's first one.
// It only listens to what the pages already do: sections rising into view, the verdict banner, and the barrier log.
(function () {
  var AC = window.AudioContext || window.webkitAudioContext;
  if (!AC || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
  var ctx = null, on = true, bus = null, last = {};

  var css = document.createElement('style');
  css.textContent =
    '.site-sound{position:fixed;left:12px;bottom:12px;z-index:40;display:flex;align-items:center;justify-content:center;gap:.4rem;min-width:44px;height:44px;padding:0;border-radius:999px;border:1px solid #3f3f46;background:rgba(15,15,20,.88);backdrop-filter:blur(8px);-webkit-backdrop-filter:blur(8px);color:#e4e4e7;font:600 .72rem "JetBrains Mono",ui-monospace,monospace;letter-spacing:.06em;text-transform:uppercase;cursor:pointer}' +
    '.site-sound:hover,.site-sound:focus-visible{border-color:#a1a1aa;color:#fff}' +
    '.site-sound[aria-pressed="true"]{color:#34d399;border-color:#34d399}' +
    '.site-sound svg{width:20px;height:20px}' +
    '.site-sound .st{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);clip-path:inset(50%);white-space:nowrap}' +
    '@media(min-width:640px){.site-sound{padding:0 .85rem}.site-sound .st{position:static;width:auto;height:auto;clip:auto;clip-path:none}.site-sound svg{display:none}}';
  document.head.appendChild(css);

  var SPEAKER = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M11 5 6 9H3v6h3l5 4z"/><path d="M15.5 8.5a5 5 0 0 1 0 7"/><path d="M18.5 5.5a9 9 0 0 1 0 13"/></svg>';
  var btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'site-sound';
  function live() { return !!(on && ctx && ctx.state === 'running'); }
  function paint() {
    btn.setAttribute('aria-pressed', String(live()));
    btn.innerHTML = SPEAKER + '<span class="st">' + (!on ? 'Sound off' : live() ? 'Sound on' : 'Tap for sound') + '</span>';
  }
  function audio() {
    if (!ctx) { ctx = new AC(); ctx.addEventListener('statechange', paint); }
    if (ctx.state !== 'running') ctx.resume().then(paint, function () {});
    return ctx;
  }
  // It never creates the context itself. Only a click, tap or key press does, so page motion cannot start audio alone.
  function ready() { return on && !!ctx && ctx.state === 'running'; }

  var livePress = null;
  ['pointerdown', 'keydown', 'touchstart'].forEach(function (ev) {
    window.addEventListener(ev, function () { livePress = live(); }, { passive: true, capture: true });
  });
  ['pointerup', 'click', 'keydown', 'touchend'].forEach(function (ev) {
    window.addEventListener(ev, function () { if (on) audio(); }, { passive: true, capture: true });
  });
  btn.addEventListener('click', function (e) {
    e.stopPropagation();
    var wasLive = livePress === null ? live() : livePress;
    livePress = null;
    if (on && !wasLive) { audio().resume().then(function () { paint(); voice(660, 0, 0.12, 0.05, 'sine', 990); }, function () {}); return; }
    on = !on;
    paint();
    if (on) voice(660, 0, 0.12, 0.05, 'sine', 990);
  });
  paint();
  document.body.appendChild(btn);

  function gate(name, gap) {
    var now = performance.now();
    if (last[name] && now - last[name] < gap) return false;
    last[name] = now;
    return true;
  }
  // One shared bus: a limiter so nothing clips, and a short filtered echo for air.
  function out(c) {
    if (bus) return bus;
    var comp = c.createDynamicsCompressor(), dry = c.createGain(), wet = c.createGain();
    comp.threshold.value = -18; comp.ratio.value = 8; comp.attack.value = 0.003; comp.release.value = 0.2;
    dry.gain.value = 0.9; wet.gain.value = 0.28;
    var delay = c.createDelay(0.5), fb = c.createGain(), lp = c.createBiquadFilter();
    delay.delayTime.value = 0.17; fb.gain.value = 0.32; lp.type = 'lowpass'; lp.frequency.value = 2400;
    dry.connect(comp);
    wet.connect(delay); delay.connect(lp); lp.connect(fb); fb.connect(delay); lp.connect(comp);
    comp.connect(c.destination);
    bus = { dry: dry, wet: wet };
    return bus;
  }
  // A soft pluck: two slightly detuned oscillators, a quick attack and an exponential decay.
  function voice(freq, at, dur, vol, type, glideTo) {
    if (!ready()) return;
    var c = audio(), b = out(c), t = c.currentTime + at;
    [-4, 4].forEach(function (cents) {
      var o = c.createOscillator(), g = c.createGain();
      o.type = type || 'sine';
      o.detune.value = cents;
      o.frequency.setValueAtTime(freq, t);
      if (glideTo) o.frequency.exponentialRampToValueAtTime(glideTo, t + dur);
      g.gain.setValueAtTime(0.0001, t);
      g.gain.exponentialRampToValueAtTime(vol / 2, t + 0.008);
      g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
      o.connect(g); g.connect(b.dry); g.connect(b.wet);
      o.start(t);
      o.stop(t + dur + 0.03);
    });
  }
  // A smooth, quiet rise of filtered air.
  function sweep(dur, vol) {
    if (!ready()) return;
    var c = audio(), b = out(c), t = c.currentTime, n = Math.floor(c.sampleRate * dur);
    var buf = c.createBuffer(1, n, c.sampleRate), d = buf.getChannelData(0);
    for (var i = 0; i < n; i++) d[i] = Math.random() * 2 - 1;
    var src = c.createBufferSource(), f = c.createBiquadFilter(), g = c.createGain();
    src.buffer = buf;
    f.type = 'bandpass'; f.Q.value = 0.7;
    f.frequency.setValueAtTime(500, t);
    f.frequency.exponentialRampToValueAtTime(2600, t + dur);
    g.gain.setValueAtTime(0.0001, t);
    g.gain.exponentialRampToValueAtTime(vol, t + dur * 0.5);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    src.connect(f); f.connect(g); g.connect(b.dry);
    src.start(t);
  }
  var play = {
    reveal: function () { if (gate('reveal', 700)) sweep(0.5, 0.025); },
    step: function () { if (gate('step', 200)) { voice(660, 0, 0.16, 0.045); voice(990, 0.05, 0.2, 0.03); } },
    // Two short falling taps, muffled: reads as "no" without a harsh buzz.
    block: function () { if (gate('verdict', 150)) { voice(311, 0, 0.14, 0.07, 'triangle'); voice(233, 0.1, 0.24, 0.07, 'triangle'); } },
    // A quick rising major arpeggio with a soft tail.
    allow: function () { if (gate('verdict', 150)) { voice(523.25, 0, 0.18, 0.05); voice(659.25, 0.07, 0.18, 0.05); voice(783.99, 0.14, 0.34, 0.055); } },
    // A short tick for a button press.
    tick: function () { if (gate('tick', 60)) voice(1318.5, 0, 0.06, 0.02); }
  };
  window.JoltrinSound = { play: play };

  // Sections rising into view: the Private Intent page adds "in" to ".rise" elements.
  new MutationObserver(function (list) {
    if (!on) return;
    list.forEach(function (m) {
      var cl = m.target.classList;
      if (cl && cl.contains('rise') && cl.contains('in') && m.oldValue.indexOf('in') === -1) play.reveal();
    });
  }).observe(document.body, { attributes: true, attributeFilter: ['class'], attributeOldValue: true, subtree: true });

  // Private Intent demo: the verdict banner changes to BLOCK, ALLOW or REQUIRE_APPROVAL.
  var title = document.getElementById('banner-title');
  if (title) {
    new MutationObserver(function () {
      var v = (title.textContent || '').trim().toUpperCase();
      if (v === 'ALLOW') play.allow();
      else if (v === 'BLOCK') play.block();
      else if (v === 'REQUIRE_APPROVAL') play.step();
    }).observe(title, { childList: true, characterData: true, subtree: true });
  }

  // Agents page barrier: lines appear in the log as steps are blocked or committed.
  var log = document.getElementById('exec-log');
  if (log) {
    new MutationObserver(function (list) {
      list.forEach(function (m) {
        m.addedNodes.forEach(function (n) {
          var t = n.textContent || '';
          if (/BLOCKED/.test(t)) play.block();
          else if (/ALLOWED/.test(t)) play.allow();
          else if (/committed/.test(t)) play.step();
        });
      });
    }).observe(log, { childList: true });
  }

  // A tick when a demo button is pressed, so the interface answers before the verdict arrives.
  document.addEventListener('click', function (e) {
    var b = e.target.closest && e.target.closest('main button, #demo button, .step-card button, [id^="b-"]');
    if (b && b !== btn) play.tick();
  }, true);
})();
