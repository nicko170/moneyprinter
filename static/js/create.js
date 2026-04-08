// Create page — handles both single draft and series draft creation
(function () {
  "use strict";

  document.addEventListener("DOMContentLoaded", () => {
    const btn = document.getElementById("researchButton");
    const subject = document.getElementById("videoSubject");

    btn.addEventListener("click", async () => {
      const text = subject.value.trim();
      const isSeries = document.getElementById("seriesToggle").checked;

      if (!text) {
        showToast(isSeries ? "Please enter a series theme." : "Please enter a video subject.", "error");
        return;
      }

      btn.disabled = true;
      btn.textContent = "Starting research…";

      // Collect shared params.
      const params = {
        voice: document.getElementById("voice").value,
        paragraphNumber: parseInt(document.getElementById("paragraphNumber").value) || 1,
        subtitlesPosition: document.getElementById("subtitlesPosition").value,
        color: document.getElementById("subtitlesColor").value,
        useMusic: document.getElementById("useMusicToggle").checked,
        hookStyle: document.getElementById("hookStyle").value,
        customHook: document.getElementById("customHook").value,
        tonePreset: document.getElementById("tonePreset").value,
        videoEffects: [
          ...(document.getElementById("effectSlowmo").checked ? ["slowmo"] : []),
          ...(document.getElementById("effectKenburns").checked ? ["kenburns"] : []),
        ],
        ...(document.getElementById("enableEndCard").checked ? {
          endCardBgColor: document.getElementById("endCardBgColor").value,
          endCardCTAText: document.getElementById("endCardCTAText").value,
          endCardDuration: parseInt(document.getElementById("endCardDuration").value) || 4,
        } : {}),
      };

      // Handle logo upload if present.
      const logoInput = document.getElementById("endCardLogo");
      if (logoInput && logoInput.files.length > 0) {
        const formData = new FormData();
        formData.append("logo", logoInput.files[0]);
        try {
          const uploadResp = await fetch("/api/upload-logo", { method: "POST", body: formData });
          const uploadData = await uploadResp.json();
          if (uploadData.path) params.endCardLogoPath = uploadData.path;
        } catch (e) {
          showToast("Logo upload failed, continuing without", "warning");
        }
      }

      try {
        if (isSeries) {
          const count = parseInt(document.getElementById("episodeCount").value) || 5;
          const schedule = document.getElementById("schedule").value;
          const resp = await fetch("/api/shorts/series", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              theme: text,
              episodeCount: count,
              schedule: schedule,
              params: params,
            }),
          });
          const data = await resp.json();
          if (data.status === "success" && data.seriesId) {
            window.location.href = "/shorts/series/" + data.seriesId;
          } else {
            throw new Error(data.message || "Failed to create series.");
          }
        } else {
          params.videoSubject = text;
          params.customPrompt = document.getElementById("customPrompt").value;
          const resp = await fetch("/api/shorts/drafts", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(params),
          });
          const data = await resp.json();
          if (data.status === "success" && data.draftId) {
            window.location.href = "/shorts/drafts/" + data.draftId;
          } else {
            throw new Error(data.message || "Failed to start research.");
          }
        }
      } catch (e) {
        showToast(e.message || "Connection error.", "error");
        btn.disabled = false;
        btn.textContent = "Research & Draft";
      }
    });

    // Enter in subject = research
    subject.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        btn.click();
      }
    });
  });
})();
