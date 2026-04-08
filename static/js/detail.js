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
        `/api/shorts/jobs/${jobId}/events?after=${lastEventId}`
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

      const jobResp = await fetch(`/api/shorts/jobs/${jobId}`);
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
        await fetch(`/api/shorts/jobs/${jobId}/cancel`, { method: "POST" });
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
        const jobResp = await fetch(`/api/shorts/jobs/${jobId}`);
        const data = await jobResp.json();
        if (data.job && data.job.payload) {
          const payload = typeof data.job.payload === "string"
            ? JSON.parse(data.job.payload)
            : data.job.payload;
          payload.force = true;
          const resp = await fetch("/api/shorts/generate", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          });
          const result = await resp.json();
          if (result.jobId) {
            window.location.href = "/shorts/jobs/" + result.jobId;
          }
        }
      } catch (e) {
        showToast("Failed to retry.", "error");
      }
    });
  }

  // Load social metadata if available
  async function loadMetadata() {
    const card = document.getElementById("metadataCard");
    if (!card) return;
    try {
      const resp = await fetch(`/api/shorts/jobs/${jobId}/metadata`);
      if (!resp.ok) return;
      const meta = await resp.json();
      document.getElementById("metaTitle").textContent = meta.title || "";
      document.getElementById("metaDescription").textContent = meta.description || "";
      const tags = (meta.hashtags || []).map(t => "#" + t).join("  ");
      document.getElementById("metaHashtags").textContent = tags;
      card.classList.remove("hidden");

      // Click-to-copy on each field
      card.querySelectorAll("[id^=meta]").forEach(el => {
        el.addEventListener("click", () => {
          navigator.clipboard.writeText(el.textContent);
          showToast("Copied to clipboard", "success");
        });
      });
    } catch (e) {
      // No metadata available — card stays hidden
    }
  }

  // Load all historical events (e.g. on page reload after completion).
  async function loadAllEvents() {
    try {
      const resp = await fetch(`/api/shorts/jobs/${jobId}/events?after=0`);
      if (!resp.ok) return;
      const data = await resp.json();
      if (data.events) {
        for (const ev of data.events) {
          appendLog(ev.message, ev.level);
          if (ev.id > lastEventId) lastEventId = ev.id;
        }
      }
    } catch (e) {
      // ignore
    }
  }

  // Load and render the script if available.
  async function loadScript() {
    try {
      const resp = await fetch(`/api/shorts/jobs/${jobId}/script`);
      if (!resp.ok) return;
      const data = await resp.json();
      if (!data.script) return;
      const container = document.getElementById("scriptCard");
      if (!container) return;
      document.getElementById("scriptText").textContent = data.script;
      container.classList.remove("hidden");
    } catch (e) {
      // no script available
    }
  }

  // Start polling if job is active
  async function init() {
    await loadAllEvents();
    const statusCard = document.getElementById("statusCard");
    if (statusCard && statusCard.querySelector(".animate-pulse")) {
      startPolling();
    }
    loadMetadata();
    loadScript();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
