// Series Draft detail page — live episode status updates + approve
(function () {
  "use strict";

  const script = document.currentScript;
  const seriesDraftId = script.getAttribute("data-series-draft-id");
  const episodeCount = parseInt(script.getAttribute("data-episode-count")) || 0;
  if (!seriesDraftId) return;

  let lastEventId = 0;
  let pollHandle = null;

  const logBody = document.getElementById("logViewerBody");
  const clearBtn = document.getElementById("logClearBtn");
  const approveBtn = document.getElementById("approveButton");
  const progressFill = document.getElementById("progressFill");
  const progressText = document.getElementById("progressText");

  const statusLabels = {
    queued: "Queued",
    researching: "Researching",
    done: "Done",
    failed: "Failed",
  };
  const statusClasses = {
    queued: "inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold",
    researching: "inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold bg-primary text-primary-foreground border-transparent",
    done: "inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold bg-green-500 text-white border-transparent",
    failed: "inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold bg-destructive text-destructive-foreground border-transparent",
  };

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

  function updateEpisodeBadge(index, status) {
    const badgeEl = document.getElementById(`ep-${index}-badge`);
    if (!badgeEl) return;
    const label = statusLabels[status] || status;
    const cls = statusClasses[status] || statusClasses.queued;
    badgeEl.innerHTML = `<span class="${cls}">${label}</span>`;
  }

  function updateProgress(episodes) {
    if (!progressFill || !progressText || !episodes) return;
    const done = episodes.filter(e => e.status === "done" || e.status === "failed").length;
    const pct = episodeCount > 0 ? Math.round((done / episodeCount) * 100) : 0;
    progressFill.style.width = pct + "%";
    progressText.textContent = `${done} / ${episodeCount} episodes done`;

    // Update each episode badge.
    for (const ep of episodes) {
      updateEpisodeBadge(ep.index, ep.status);
    }
  }

  async function poll() {
    try {
      // Fetch new log events.
      const evResp = await fetch(`/api/shorts/series-drafts/${seriesDraftId}/events?after=${lastEventId}`);
      if (evResp.ok) {
        const data = await evResp.json();
        if (data.events) {
          for (const ev of data.events) {
            appendLog(ev.message, ev.level);
            if (ev.id > lastEventId) lastEventId = ev.id;
          }
        }
      }

      // Fetch full draft state for episode progress.
      const draftResp = await fetch(`/api/shorts/series-drafts/${seriesDraftId}`);
      if (draftResp.ok) {
        const data = await draftResp.json();
        const sd = data.draft;
        if (!sd) return;

        if (sd.episodes) updateProgress(sd.episodes);

        if (sd.status === "ready" || sd.status === "failed") {
          stopPolling();
          setTimeout(() => window.location.reload(), 500);
        }
      }
    } catch (e) {
      // Ignore transient errors
    }
  }

  function startPolling() {
    pollHandle = setInterval(poll, 1500);
    poll();
  }

  function stopPolling() {
    if (pollHandle) {
      clearInterval(pollHandle);
      pollHandle = null;
    }
  }

  if (clearBtn) {
    clearBtn.addEventListener("click", () => {
      if (logBody) logBody.innerHTML = "";
    });
  }

  if (approveBtn) {
    approveBtn.addEventListener("click", async () => {
      approveBtn.disabled = true;
      approveBtn.textContent = "Submitting…";
      try {
        const resp = await fetch(`/api/shorts/series-drafts/${seriesDraftId}/approve`, { method: "POST" });
        const data = await resp.json();
        if (data.status === "success" && data.seriesId) {
          window.location.href = "/shorts/series/" + data.seriesId;
        } else {
          if (typeof showToast === "function") showToast(data.message || "Approval failed.", "error");
          approveBtn.disabled = false;
          approveBtn.textContent = "Approve All & Generate Series";
        }
      } catch (e) {
        if (typeof showToast === "function") showToast("Connection error.", "error");
        approveBtn.disabled = false;
        approveBtn.textContent = "Approve All & Generate Series";
      }
    });
  }

  async function loadAllEvents() {
    try {
      const evResp = await fetch(`/api/shorts/series-drafts/${seriesDraftId}/events?after=0`);
      if (!evResp.ok) return;
      const data = await evResp.json();
      if (data.events) {
        for (const ev of data.events) {
          appendLog(ev.message, ev.level);
          if (ev.id > lastEventId) lastEventId = ev.id;
        }
      }
    } catch (e) {}
  }

  document.addEventListener("DOMContentLoaded", async () => {
    await loadAllEvents();
    if (document.querySelector(".animate-pulse") || document.getElementById("progressBar")) {
      startPolling();
    }
  });
})();
