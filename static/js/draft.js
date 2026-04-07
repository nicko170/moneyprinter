// Draft detail page — research log polling + approve
(function () {
  "use strict";

  const draftId = document.currentScript.getAttribute("data-draft-id");
  if (!draftId) return;

  let lastEventId = 0;
  let pollHandle = null;

  const logBody = document.getElementById("logViewerBody");
  const clearBtn = document.getElementById("logClearBtn");
  const approveBtn = document.getElementById("approveButton");
  const redraftBtn = document.getElementById("redraftButton");

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
      const evResp = await fetch(`/api/drafts/${draftId}/events?after=${lastEventId}`);
      if (evResp.ok) {
        const data = await evResp.json();
        if (data.events) {
          for (const ev of data.events) {
            appendLog(ev.message, ev.level);
            if (ev.id > lastEventId) lastEventId = ev.id;
          }
        }
      }

      const draftResp = await fetch(`/api/drafts/${draftId}`);
      if (draftResp.ok) {
        const data = await draftResp.json();
        const d = data.draft;
        if (d && (d.status === "done" || d.status === "failed")) {
          stopPolling();
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
        const resp = await fetch(`/api/drafts/${draftId}/approve`, { method: "POST" });
        const data = await resp.json();
        if (data.status === "success" && data.jobId) {
          window.location.href = "/jobs/" + data.jobId;
        } else {
          if (typeof showToast === "function") showToast(data.message || "Approval failed.", "error");
          approveBtn.disabled = false;
          approveBtn.textContent = "Approve & Generate Video";
        }
      } catch (e) {
        if (typeof showToast === "function") showToast("Connection error.", "error");
        approveBtn.disabled = false;
        approveBtn.textContent = "Approve & Generate Video";
      }
    });
  }

  if (redraftBtn) {
    redraftBtn.addEventListener("click", () => {
      window.location.href = "/jobs/create";
    });
  }

  async function loadAllEvents() {
    try {
      const evResp = await fetch(`/api/drafts/${draftId}/events?after=0`);
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
    if (document.querySelector(".animate-pulse")) {
      startPolling();
    }
  });
})();
