// Job Detail page — polling, log viewer, cancel, retry
(function () {
  "use strict";

  const jobId =
    document.currentScript.getAttribute("data-job-id") || window.__jobId;
  if (!jobId) return;

  let lastEventId = 0;
  let pollHandle = null;

  const logBody = document.getElementById("logViewerBody");
  const clearBtn = document.getElementById("logClearBtn");
  const cancelBtn = document.getElementById("cancelButton");
  const retryBtn = document.getElementById("retryButton");

  function appendLog(message, level) {
    if (!logBody) return;
    const entry = document.createElement("div");
    const time = new Date().toLocaleTimeString("en-GB", { hour12: false });
    const colors = {
      success: "text-green-400",
      error: "text-red-400",
      warning: "text-yellow-400",
      info: "text-zinc-400",
    };
    entry.innerHTML = `<span class="text-zinc-600">${time}</span> <span class="${colors[level] || colors.info}">${message}</span>`;
    logBody.appendChild(entry);
    logBody.scrollTop = logBody.scrollHeight;
  }

  async function poll() {
    try {
      const evResp = await fetch(
        `/api/jobs/${jobId}/events?after=${lastEventId}`
      );
      if (evResp.ok) {
        const data = await evResp.json();
        if (data.events) {
          for (const ev of data.events) {
            appendLog(ev.message, ev.level);
            if (ev.id > lastEventId) lastEventId = ev.id;
          }
        }
      }

      const jobResp = await fetch(`/api/jobs/${jobId}`);
      if (jobResp.ok) {
        const data = await jobResp.json();
        const job = data.job;
        if (
          job &&
          (job.status === "completed" ||
            job.status === "failed" ||
            job.status === "cancelled")
        ) {
          stopPolling();
          // Reload page to show final state rendered by server.
          setTimeout(() => window.location.reload(), 500);
        }
      }
    } catch (e) {
      // Ignore transient errors
    }
  }

  function startPolling() {
    pollHandle = setInterval(poll, 1200);
    poll();
  }

  function stopPolling() {
    if (pollHandle) {
      clearInterval(pollHandle);
      pollHandle = null;
    }
  }

  // Clear log
  if (clearBtn) {
    clearBtn.addEventListener("click", () => {
      if (logBody) logBody.innerHTML = "";
    });
  }

  // Cancel
  if (cancelBtn) {
    cancelBtn.addEventListener("click", async () => {
      cancelBtn.disabled = true;
      cancelBtn.textContent = "Cancelling…";
      try {
        await fetch(`/api/jobs/${jobId}/cancel`, { method: "POST" });
        showToast("Cancellation requested.", "info");
      } catch (e) {
        showToast("Failed to cancel.", "error");
        cancelBtn.disabled = false;
        cancelBtn.textContent = "Cancel";
      }
    });
  }

  // Retry (re-submit same job)
  if (retryBtn) {
    retryBtn.addEventListener("click", async () => {
      try {
        const jobResp = await fetch(`/api/jobs/${jobId}`);
        const data = await jobResp.json();
        if (data.job && data.job.payload) {
          const payload = typeof data.job.payload === "string"
            ? JSON.parse(data.job.payload)
            : data.job.payload;
          payload.force = true;
          const resp = await fetch("/api/generate", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          });
          const result = await resp.json();
          if (result.jobId) {
            window.location.href = "/jobs/" + result.jobId;
          }
        }
      } catch (e) {
        showToast("Failed to retry.", "error");
      }
    });
  }

  // Start polling if job is active
  document.addEventListener("DOMContentLoaded", () => {
    const statusCard = document.getElementById("statusCard");
    if (statusCard && statusCard.querySelector(".animate-pulse")) {
      startPolling();
    }
  });
})();
