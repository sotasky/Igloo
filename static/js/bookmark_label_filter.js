(function () {
  function closeFilter(root) {
    var panel = root.querySelector('[data-bookmark-label-panel]');
    var toggle = root.querySelector('[data-bookmark-label-toggle]');
    if (panel) panel.classList.add('hidden');
    if (toggle) toggle.setAttribute('aria-expanded', 'false');
  }

  function openFilter(root) {
    var panel = root.querySelector('[data-bookmark-label-panel]');
    var toggle = root.querySelector('[data-bookmark-label-toggle]');
    var input = root.querySelector('[data-bookmark-label-search]');
    if (panel) panel.classList.remove('hidden');
    if (toggle) toggle.setAttribute('aria-expanded', 'true');
    if (input) {
      input.value = '';
      filterRows(root, '');
      setTimeout(function () { input.focus(); }, 0);
    }
  }

  function filterRows(root, query) {
    var q = String(query || '').trim().toLowerCase();
    var rows = Array.prototype.slice.call(root.querySelectorAll('[data-bookmark-label-row]'));
    var empty = root.querySelector('[data-bookmark-label-empty]');
    var visible = 0;
    rows.forEach(function (row) {
      var label = String(row.getAttribute('data-label') || '').toLowerCase();
      var show = !q || label.indexOf(q) !== -1;
      row.classList.toggle('hidden', !show);
      if (show) visible += 1;
    });
    if (empty) empty.classList.toggle('hidden', visible > 0);
  }

  function init() {
    var filters = Array.prototype.slice.call(document.querySelectorAll('[data-bookmark-label-filter]'));
    filters.forEach(function (root) {
      var toggle = root.querySelector('[data-bookmark-label-toggle]');
      var input = root.querySelector('[data-bookmark-label-search]');
      if (toggle) {
        toggle.addEventListener('click', function (event) {
          event.preventDefault();
          var panel = root.querySelector('[data-bookmark-label-panel]');
          if (panel && panel.classList.contains('hidden')) openFilter(root);
          else closeFilter(root);
        });
      }
      if (input) {
        input.addEventListener('input', function () { filterRows(root, input.value); });
      }
    });

    document.addEventListener('mousedown', function (event) {
      filters.forEach(function (root) {
        if (!root.contains(event.target)) closeFilter(root);
      });
    });
    document.addEventListener('keydown', function (event) {
      if (event.key !== 'Escape') return;
      filters.forEach(closeFilter);
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init, { once: true });
  } else {
    init();
  }
})();
