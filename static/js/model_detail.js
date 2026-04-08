// Model Detail page — live updates, event polling, pause/resume, trigger
(function () {
  "use strict";

  const modelId =
    document.currentScript.getAttribute("data-model-id") || window.__modelId;
  if (!modelId) return;

  let lastEventId = 0;
  let pollHandle = null;
  let knownPosts = {}; // index → status, tracks what we've rendered
  const logBody = document.getElementById("logViewerBody");
  const postGrid = document.getElementById("postGrid");
  const statusArea = document.getElementById("postStatusArea");

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

  // Build a post card HTML string for a completed post.
  function postCardHTML(post) {
    const idx = String(post.index).padStart(3, "0");
    const imgFile = post.imagePaths[0].split("/").pop();
    const imgSrc = `/model-images/${modelId}/posts/${idx}/${imgFile}`;
    const hashtags = (post.hashtags || []).map((t) => "#" + t).join(" ");
    return `<div class="rounded-lg border border-border/60 overflow-hidden bg-card" data-post-index="${post.index}">
      <div class="aspect-[4/5] overflow-hidden bg-muted">
        <img src="${imgSrc}" alt="${post.scene || ""}" class="w-full h-full object-cover"/>
      </div>
      <div class="p-3">
        <p class="text-sm leading-relaxed line-clamp-3">${post.caption || ""}</p>
        ${hashtags ? `<p class="text-xs text-muted-foreground mt-1.5">${hashtags}</p>` : ""}
      </div>
    </div>`;
  }

  // Build a status row HTML for an in-progress/planned post.
  function statusRowHTML(post) {
    const badgeClasses = {
      planned: "bg-secondary text-secondary-foreground",
      captioning: "bg-purple-100 text-purple-800 border-purple-200",
      generating: "bg-blue-100 text-blue-800 border-blue-200",
      failed: "bg-destructive text-destructive-foreground",
    };
    const badgeLabels = {
      planned: "Planned",
      captioning: "Writing",
      generating: "Generating",
      failed: "Failed",
    };
    const cls = badgeClasses[post.status] || badgeClasses.planned;
    const label = badgeLabels[post.status] || post.status;
    const scene = post.scene
      ? `<span class="text-sm truncate">${post.scene}</span>`
      : "";
    const error = post.error
      ? `<span class="text-xs text-red-500 truncate">${post.error}</span>`
      : "";
    return `<div class="flex items-center gap-3 rounded-lg border border-border/60 px-4 py-3 mb-2" data-post-status="${post.index}">
      <span class="text-xs text-muted-foreground">Post ${post.index}</span>
      <span class="inline-flex items-center rounded-md border px-2.5 py-0.5 text-xs font-semibold ${cls}">${label}</span>
      ${scene}${error}
    </div>`;
  }

  // Sync DOM with current post data from API.
  function syncPosts(posts) {
    if (!posts || !postGrid) return;

    for (const post of posts) {
      const prev = knownPosts[post.index];

      if (
        post.status === "completed" &&
        post.imagePaths &&
        post.imagePaths.length > 0 &&
        prev !== "completed"
      ) {
        // New completed post — add card to grid (prepend = newest first).
        postGrid.insertAdjacentHTML("afterbegin", postCardHTML(post));
        // Remove its status row if exists.
        const row = statusArea?.querySelector(
          `[data-post-status="${post.index}"]`
        );
        if (row) row.remove();
      } else if (post.status !== "completed" && prev !== post.status) {
        // Status changed for in-progress post — update or add status row.
        const existing = statusArea?.querySelector(
          `[data-post-status="${post.index}"]`
        );
        if (existing) {
          existing.outerHTML = statusRowHTML(post);
        } else if (statusArea) {
          statusArea.insertAdjacentHTML("afterbegin", statusRowHTML(post));
        }
      }

      knownPosts[post.index] = post.status;
    }
  }

  // Seed knownPosts from the server-rendered DOM so we don't re-add existing cards.
  function seedKnownPosts() {
    if (postGrid) {
      postGrid.querySelectorAll("[data-post-index]").forEach((el) => {
        knownPosts[el.dataset.postIndex] = "completed";
      });
    }
    if (statusArea) {
      statusArea.querySelectorAll("[data-post-status]").forEach((el) => {
        // We don't know the exact status from DOM, just mark as seen.
        knownPosts[el.dataset.postStatus] = "pending-placeholder";
      });
    }
  }

  async function loadAllEvents() {
    try {
      const resp = await fetch(`/api/models/${modelId}/events?after=0`);
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
      // Fetch new log events.
      const evResp = await fetch(
        `/api/models/${modelId}/events?after=${lastEventId}`
      );
      if (evResp.ok) {
        const data = await evResp.json();
        if (data.events && data.events.length > 0) {
          for (const ev of data.events) {
            appendLog(ev.message, ev.level, ev.timestamp);
            if (ev.id > lastEventId) lastEventId = ev.id;
          }
        }
      }

      // Fetch model state and sync posts.
      const mResp = await fetch(`/api/models/${modelId}`);
      if (mResp.ok) {
        const data = await mResp.json();
        if (data.model && data.model.posts) {
          syncPosts(data.model.posts);
        }
      }
    } catch (e) {}
  }

  function startPolling() {
    pollHandle = setInterval(poll, 3000);
    poll();
  }

  // Action buttons.
  function wireButton(id, endpoint) {
    const btn = document.getElementById(id);
    if (!btn) return;
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      try {
        const resp = await fetch(`/api/models/${modelId}/${endpoint}`, {
          method: "POST",
        });
        const data = await resp.json();
        if (resp.ok) {
          showToast("Done", "success");
          // Trigger an immediate poll to pick up changes.
          poll();
          btn.disabled = false;
        } else {
          showToast(data.message || "Action failed", "error");
          btn.disabled = false;
        }
      } catch (e) {
        showToast("Request failed", "error");
        btn.disabled = false;
      }
    });
  }

  async function init() {
    seedKnownPosts();
    await loadAllEvents();
    wireButton("pauseBtn", "pause");
    wireButton("resumeBtn", "resume");
    wireButton("triggerBtn", "trigger");
    wireButton("genRefsBtn", "generate-refs");
    startPolling();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
