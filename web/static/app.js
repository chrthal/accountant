// kmu-buchhaltung: the only script, and every part of it is optional (ADR 0013).
//
// It is loaded in <head> without `defer` so the theme is applied before first
// paint, which avoids a flash of the wrong theme. Everything that touches the
// DOM waits for DOMContentLoaded. Without this file every page still works:
// the theme follows the OS, and invoice positions come as a fixed set of rows.

(function () {
  var root = document.documentElement;
  root.classList.add('js');

  // --- theme -------------------------------------------------------------
  // The stored choice is 'light', 'dark' or absent (= follow the system).
  // Storage can be unavailable (private mode, blocked site data), so every
  // access is guarded and the app falls back to the system theme.
  var KEY = 'theme';
  function readChoice() {
    try { return localStorage.getItem(KEY) || 'system'; } catch (e) { return 'system'; }
  }
  function applyChoice(choice) {
    if (choice === 'light' || choice === 'dark') {
      root.setAttribute('data-theme', choice);
    } else {
      root.removeAttribute('data-theme');
      choice = 'system';
    }
    root.setAttribute('data-theme-choice', choice);
  }
  applyChoice(readChoice());

  // The page carries the labels in the UI language (ADR 0024); German is the
  // fallback for a page without them.
  var LABELS = { system: 'Design: System', light: 'Design: Hell', dark: 'Design: Dunkel', switch: '(wechseln)' };
  function label(btn, key) { return btn.getAttribute('data-label-' + key) || LABELS[key]; }
  var NEXT = { system: 'light', light: 'dark', dark: 'system' };

  document.addEventListener('DOMContentLoaded', function () {
    document.querySelectorAll('[data-theme-toggle]').forEach(function (btn) {
      var sync = function () {
        var c = root.getAttribute('data-theme-choice') || 'system';
        btn.setAttribute('aria-label', label(btn, c) + ' ' + label(btn, 'switch'));
        btn.setAttribute('title', label(btn, c));
      };
      sync();
      btn.addEventListener('click', function () {
        var next = NEXT[root.getAttribute('data-theme-choice') || 'system'];
        try {
          if (next === 'system') localStorage.removeItem(KEY); else localStorage.setItem(KEY, next);
        } catch (e) { /* not persisted, still applied for this page */ }
        applyChoice(next);
        document.querySelectorAll('[data-theme-toggle]').forEach(function (b) {
          var c = root.getAttribute('data-theme-choice');
          b.setAttribute('aria-label', label(b, c) + ' ' + label(b, 'switch'));
          b.setAttribute('title', label(b, c));
        });
      });
    });

    // --- confirmations ---------------------------------------------------
    // A Storno cannot be undone, so the form asks first. Without script the
    // form simply submits. The action stays correctable by another Storno.
    document.querySelectorAll('form[data-confirm]').forEach(function (form) {
      form.addEventListener('submit', function (ev) {
        if (!window.confirm(form.getAttribute('data-confirm'))) ev.preventDefault();
      });
    });

    // A single button can ask too, e.g. "Rechnung ausstellen" next to
    // "Als Entwurf speichern" in the same form.
    document.querySelectorAll('button[data-confirm]').forEach(function (btn) {
      btn.addEventListener('click', function (ev) {
        if (!window.confirm(btn.getAttribute('data-confirm'))) ev.preventDefault();
      });
    });

    // --- invoice positions -----------------------------------------------
    // The server renders a few blank rows, and this button clones a <template>
    // for more. Field names carry an index, and the server reads every index
    // it finds (up to a cap), so no JSON and no partial rendering are needed.
    document.querySelectorAll('[data-add-line]').forEach(function (btn) {
      var list = document.getElementById(btn.getAttribute('data-add-line'));
      var tpl = document.getElementById(btn.getAttribute('data-template'));
      if (!list || !tpl) return;
      btn.hidden = false;
      btn.addEventListener('click', function () {
        var idx = list.querySelectorAll('[data-line]').length;
        var html = tpl.innerHTML.replace(/__i__/g, String(idx)).replace(/__n__/g, String(idx + 1));
        list.insertAdjacentHTML('beforeend', html);
        var first = list.lastElementChild && list.lastElementChild.querySelector('input');
        if (first) first.focus();
      });
    });

    // --- category default VAT (ADR 0021) ---------------------------------
    // Choosing a category with a default VAT rate sets the rate field. The
    // server sends the same default when it prefills the form, so this only
    // spares a second click when the user picks a category by hand.
    document.querySelectorAll('select[data-sets-vat]').forEach(function (sel) {
      var target = sel.form && sel.form.elements[sel.getAttribute('data-sets-vat')];
      if (!target) return;
      sel.addEventListener('change', function () {
        var opt = sel.options[sel.selectedIndex];
        var vat = opt && opt.getAttribute('data-vat');
        if (vat !== null && vat !== undefined && vat !== '') target.value = vat;
      });
    });

    // --- long uploads and background reading (ADR 0042) -------------------
    // A large upload gives no sign of life until it is sent, so the button
    // says so. The button is disabled only after the submit has started:
    // a disabled button before it would leave the form unsent.
    document.querySelectorAll('form[data-busy]').forEach(function (form) {
      form.addEventListener('submit', function (ev) {
        if (ev.defaultPrevented) return;
        var btn = form.querySelector('button[type="submit"]');
        if (!btn) return;
        setTimeout(function () {
          btn.disabled = true;
          btn.textContent = form.getAttribute('data-busy');
        }, 0);
      });
    });
    // While Belege are read, the list reloads itself, but never over what
    // the user has typed: then it only offers to reload.
    document.querySelectorAll('[data-refresh]').forEach(function (el) {
      var dirty = false;
      document.addEventListener('input', function () { dirty = true; });
      setTimeout(function () {
        if (!dirty) { window.location.reload(); return; }
        var stale = el.querySelector('[data-refresh-stale]');
        if (stale) stale.hidden = false;
      }, (parseInt(el.getAttribute('data-refresh'), 10) || 5) * 1000);
    });

    // --- private share (ADR 0041) -----------------------------------------
    // Only an expense has a private share; the server sets income to 100 %
    // anyway, so this only tidies the form.
    document.querySelectorAll('[data-outgoing-only]').forEach(function (el) {
      var form = el.closest('form');
      if (!form) return;
      var sync = function () {
        var dir = form.querySelector('input[name="direction"]:checked');
        el.hidden = !!dir && dir.value === 'income';
      };
      form.addEventListener('change', sync);
      sync();
    });

    // --- date fields -------------------------------------------------------
    // The server renders dates as text in the chosen format, because a browser
    // draws <input type="date"> in its own locale. This adds a button that
    // opens the native picker and writes the choice back in that format.
    // data-date holds the Go layout: 2006 is the year, 01 the month, 02 the day.
    document.querySelectorAll('input[data-date]').forEach(function (input) {
      var layout = input.getAttribute('data-date');
      var native = document.createElement('input');
      native.type = 'date';
      if (native.type !== 'date' || typeof native.showPicker !== 'function') return;
      var order = layout.match(/2006|01|02/g) || [];
      var pattern = new RegExp('^' + layout.replace(/[.*+?^${}()|[\]\\/-]/g, '\\$&')
        .replace(/2006|01|02/g, function (m) { return m === '2006' ? '(\\d{4})' : '(\\d{1,2})'; }) + '$');
      var pad = function (n) { return (n.length < 2 ? '0' : '') + n; };
      var toISO = function (v) {
        var m = v.trim().match(/^(\d{4})-(\d{2})-(\d{2})$/);
        if (m) return v.trim();
        m = v.trim().match(pattern);
        if (!m) return '';
        var p = {};
        order.forEach(function (k, i) { p[k] = m[i + 1]; });
        return p['2006'] + '-' + pad(p['01']) + '-' + pad(p['02']);
      };
      var fromISO = function (iso) {
        var p = iso.split('-');
        return layout.replace(/2006|01|02/g, function (m) { return m === '2006' ? p[0] : m === '01' ? p[1] : p[2]; });
      };

      var wrap = document.createElement('span');
      wrap.className = 'date-wrap';
      input.parentNode.insertBefore(wrap, input);
      wrap.appendChild(input);
      native.className = 'date-native';
      native.tabIndex = -1;
      native.setAttribute('aria-hidden', 'true');
      wrap.appendChild(native);
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'ghost';
      btn.setAttribute('aria-label', input.getAttribute('data-date-label') || '');
      btn.setAttribute('title', input.getAttribute('data-date-label') || '');
      btn.innerHTML = '<svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><use href="#i-calendar"/></svg>';
      wrap.appendChild(btn);

      btn.addEventListener('click', function () {
        native.value = toISO(input.value);
        try { native.showPicker(); } catch (e) { input.focus(); }
      });
      native.addEventListener('change', function () {
        if (native.value) input.value = fromISO(native.value);
        input.dispatchEvent(new Event('change', { bubbles: true }));
      });
    });

    // --- auto-submit filters ---------------------------------------------
    document.querySelectorAll('form[data-autosubmit] select').forEach(function (sel) {
      sel.addEventListener('change', function () { sel.form.requestSubmit ? sel.form.requestSubmit() : sel.form.submit(); });
    });
  });

  // --- service worker ----------------------------------------------------
  // Registered from the root so its scope covers the whole app. It only ever
  // serves the cached shell (see /sw.js).
  if ('serviceWorker' in navigator) {
    window.addEventListener('load', function () { navigator.serviceWorker.register('/sw.js'); });
  }
})();
