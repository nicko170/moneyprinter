// Series Detail page — poll for episode progress and log events
(function () {
  "use strict";

  const seriesId =
    document.currentScript.getAttribute("data-series-id") || window.__seriesId;
  if (!seriesId) return;

  let lastEventId = 0;
  let pollHandle = null;
  const logBody = document.getElementById("logViewerBody");

  function appendLog(message, level, timestamp) {
    if (!logBody) return;
    const entry = document.createElement("div");
    const d = timestamp ? new Date(timestamp) : new Date(); const time = d.toLocaleTimeString("en-GB", { hour12: false });
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

  async function loadAllEvents() {
    try {
      const resp = await fetch(`/api/shorts/series/${seriesId}/events?after=0`);
      if (!resp.ok) return;
      const data = await resp.json();
      if (data.events) {
        for (const ev of data.events) {
          appendLog(ev.message, ev.level, ev.timestamp);
          if (ev.id > lastEventId) lastEventId = ev.id;
        }
      }
    } catch (e) {}
  }

  async function poll() {
    try {
      // Fetch new events.
      const evResp = await fetch(`/api/shorts/series/${seriesId}/events?after=${lastEventId}`);
      if (evResp.ok) {
        const data = await evResp.json();
        if (data.events) {
          for (const ev of data.events) {
            appendLog(ev.message, ev.level, ev.timestamp);
            if (ev.id > lastEventId) lastEventId = ev.id;
          }
        }
      }

      // Check series status — reload page if episodes changed.
      const serResp = await fetch(`/api/shorts/series/${seriesId}`);
      if (serResp.ok) {
        const data = await serResp.json();
        const s = data.series;
        if (s && (s.status === "completed" || s.status === "failed")) {
          stopPolling();
          setTimeout(() => window.location.reload(), 1000);
        }
        // Reload if any episode status changed (e.g. new research complete).
        if (s && s.episodes) {
          const changed = s.episodes.some((ep) => {
            const badge = document.getElementById(`ep-${ep.index}-badge`);
            return badge && badge.textContent.trim().toLowerCase() !== ep.status;
          });
          if (changed) {
            setTimeout(() => window.location.reload(), 500);
          }
        }
      }
    } catch (e) {}
  }

  function startPolling() {
    pollHandle = setInterval(poll, 3000);
    poll();
  }

  function stopPolling() {
    if (pollHandle) {
      clearInterval(pollHandle);
      pollHandle = null;
    }
  }

  async function init() {
    await loadAllEvents();
    // Poll if series is still active.
    const resp = await fetch(`/api/shorts/series/${seriesId}`);
    if (resp.ok) {
      const data = await resp.json();
      if (data.series && data.series.status === "running") {
        startPolling();
      }
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
