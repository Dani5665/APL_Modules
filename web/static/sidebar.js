/* Side menu: collapsible on desktop (the choice is remembered), an off-canvas
 * drawer on small screens. Loaded synchronously in <head> so the remembered
 * state is applied before the first paint. */
(function () {
  var root = document.documentElement;
  var KEY = 'sidebar';
  var narrow = window.matchMedia('(max-width: 800px)');

  try {
    if (localStorage.getItem(KEY) === 'collapsed') root.classList.add('sb-collapsed');
  } catch (e) { /* storage may be blocked; the menu just starts open */ }

  function isOpen() {
    return narrow.matches ? root.classList.contains('sb-open') : !root.classList.contains('sb-collapsed');
  }

  function sync() {
    var open = isOpen();
    document.querySelectorAll('[data-sidebar-toggle]').forEach(function (b) {
      b.setAttribute('aria-expanded', open ? 'true' : 'false');
    });
  }

  function toggle() {
    if (narrow.matches) {
      root.classList.toggle('sb-open');
    } else {
      root.classList.toggle('sb-collapsed');
      try {
        localStorage.setItem(KEY, root.classList.contains('sb-collapsed') ? 'collapsed' : 'open');
      } catch (e) { /* ignore */ }
    }
    sync();
  }

  function closeDrawer() {
    root.classList.remove('sb-open');
    sync();
  }

  document.addEventListener('click', function (e) {
    if (e.target.closest('[data-sidebar-toggle]')) {
      toggle();
    } else if (e.target.closest('[data-sidebar-close]') ||
               (narrow.matches && e.target.closest('.sidebar a'))) {
      closeDrawer();
    }
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && narrow.matches && root.classList.contains('sb-open')) closeDrawer();
  });

  narrow.addEventListener('change', closeDrawer);
  document.addEventListener('DOMContentLoaded', sync);
})();
