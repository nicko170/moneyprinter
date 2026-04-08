// Dashboard page — auto-refresh when active jobs or research exist
(function () {
  "use strict";

  async function hasActiveWork() {
    try {
      const resp = await fetch("/api/shorts/jobs");
      if (resp.ok) {
        const data = await resp.json();
        const active = (data.jobs || []).some(
          (j) => j.status === "running" || j.status === "queued"
        );
        if (active) return true;
      }
    } catch (e) {}
    return false;
  }

  document.addEventListener("DOMContentLoaded", async () => {
    if (await hasActiveWork()) {
      setInterval(async () => {
        if (await hasActiveWork()) window.location.reload();
      }, 5000);
    }
  });
})();
