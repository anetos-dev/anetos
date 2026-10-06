// The admin's script: confirmations, selecting all rows, and filters that
// apply as they change. No inline scripts, so the pages' CSP stays strict.
(function () {
  "use strict";
  document.addEventListener("submit", function (e) {
    var el = e.submitter && e.submitter.hasAttribute("data-confirm") ? e.submitter : e.target;
    var msg = el.getAttribute && el.getAttribute("data-confirm");
    if (msg && !window.confirm(msg)) {
      e.preventDefault();
      e.stopImmediatePropagation();
    }
  }, true);
  document.addEventListener("change", function (e) {
    var t = e.target;
    if (t.matches("[data-select-all]")) {
      t.closest("form").querySelectorAll('input[name="ids"]').forEach(function (b) { b.checked = t.checked; });
    } else if (t.matches("[data-autosubmit] select")) {
      t.form.requestSubmit();
    }
  });
})();
