// Series Create page — research & plan series
(function () {
  "use strict";

  document.addEventListener("DOMContentLoaded", () => {
    const btn = document.getElementById("createSeriesButton");

    btn.addEventListener("click", async () => {
      const theme = document.getElementById("theme").value.trim();
      const count = parseInt(document.getElementById("episodeCount").value) || 5;

      if (!theme) {
        showToast("Please enter a series theme.", "error");
        return;
      }

      btn.disabled = true;
      btn.textContent = "Starting research…";

      const sharedParams = {
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
          if (uploadData.path) sharedParams.endCardLogoPath = uploadData.path;
        } catch (e) {
          showToast("Logo upload failed, continuing without", "warning");
        }
      }

      try {
        const resp = await fetch("/api/shorts/series-drafts", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            theme,
            episodeCount: count,
            sharedParams,
          }),
        });
        const data = await resp.json();
        if (data.status === "success" && data.seriesDraftId) {
          window.location.href = "/shorts/series-drafts/" + data.seriesDraftId;
        } else {
          showToast(data.message || "Failed to start series research.", "error");
          btn.disabled = false;
          btn.textContent = "Research & Plan Series";
        }
      } catch (e) {
        showToast("Connection error. Is the server running?", "error");
        btn.disabled = false;
        btn.textContent = "Research & Plan Series";
      }
    });
  });
})();
