// Dashboard page — auto-refresh when active jobs exist
(function () {
  "use strict";

  async function checkForActive() {
    try {
      const resp = await fetch("/api/jobs");
      if (!resp.ok) return;
      const data = await resp.json();
      const hasActive = (data.jobs || []).some(
        (j) => j.status === "running" || j.status === "queued"
      );
      if (hasActive) {
        window.location.reload();
      }
    } catch (e) {
      // ignore
    }
  }

  document.addEventListener("DOMContentLoaded", () => {
    // Check API for active jobs; if any, poll every 5s.
    fetch("/api/jobs")
      .then((r) => r.json())
      .then((data) => {
        const hasActive = (data.jobs || []).some(
          (j) => j.status === "running" || j.status === "queued"
        );
        if (hasActive) {
          setInterval(checkForActive, 5000);
        }
      })
      .catch(() => {});
  });
})();
