// Series Detail page — auto-refresh while jobs are active, reburn support
(function () {
  "use strict";

  document.addEventListener("DOMContentLoaded", () => {
    // If page contains running/queued badges, auto-refresh
    const html = document.body.innerHTML;
    if (html.includes("Running") || html.includes("Queued")) {
      setTimeout(() => window.location.reload(), 5000);
    }

    // Reburn button — clears subtitle cache and re-queues all series jobs.
    const reburnBtn = document.getElementById("reburnButton");
    if (reburnBtn) {
      reburnBtn.addEventListener("click", async () => {
        const seriesId = window.location.pathname.split("/").pop();
        reburnBtn.disabled = true;
        reburnBtn.textContent = "Re-queuing...";
        try {
          const resp = await fetch(`/api/series/${seriesId}/reburn`, { method: "POST" });
          const data = await resp.json();
          if (data.status === "success") {
            window.location.reload();
          } else {
            alert(data.message || "Reburn failed");
            reburnBtn.disabled = false;
            reburnBtn.textContent = "Reburn Subtitles";
          }
        } catch (e) {
          alert("Reburn request failed");
          reburnBtn.disabled = false;
          reburnBtn.textContent = "Reburn Subtitles";
        }
      });
    }
  });
})();
