// Series Detail page — auto-refresh while jobs are active
(function () {
  "use strict";

  document.addEventListener("DOMContentLoaded", () => {
    // If page contains running/queued badges, auto-refresh
    const html = document.body.innerHTML;
    if (html.includes("Running") || html.includes("Queued")) {
      setTimeout(() => window.location.reload(), 5000);
    }
  });
})();
