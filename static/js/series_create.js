// Series Create page — form submission + redirect
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
      btn.textContent = "Generating topics…";

      try {
        const controller = new AbortController();
        const timeout = setTimeout(() => controller.abort(), 300000); // 5 min
        const resp = await fetch("/api/series", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          signal: controller.signal,
          body: JSON.stringify({
            theme: theme,
            episodeCount: count,
            voice: document.getElementById("voice").value,
            context: document.getElementById("seriesContext").value,
            hookStyle: document.getElementById("hookStyle").value,
            customHook: document.getElementById("customHook").value,
            tonePreset: document.getElementById("tonePreset").value,
            subtitlesPosition: document.getElementById("subtitlesPosition").value,
            color: document.getElementById("subtitlesColor").value,
            paragraphNumber: parseInt(document.getElementById("paragraphNumber").value) || 1,
            useMusic: document.getElementById("useMusicToggle").checked,
            videoEffects: [
              ...(document.getElementById("effectSlowmo").checked ? ["slowmo"] : []),
              ...(document.getElementById("effectKenburns").checked ? ["kenburns"] : []),
            ],
            ...(document.getElementById("enableEndCard").checked ? {
              endCardBgColor: document.getElementById("endCardBgColor").value,
              endCardCTAText: document.getElementById("endCardCTAText").value,
              endCardDuration: parseInt(document.getElementById("endCardDuration").value) || 4,
            } : {}),
          }),
        });
        clearTimeout(timeout);
        const data = await resp.json();
        if (data.status === "success" && data.seriesId) {
          showToast(
            `Series created with ${data.topics.length} episodes!`,
            "success"
          );
          window.location.href = "/series/" + data.seriesId;
        } else {
          showToast(data.message || "Failed to create series.", "error");
          btn.disabled = false;
          btn.textContent = "Generate Series";
        }
      } catch (e) {
        showToast("Connection error. Is the server running?", "error");
        btn.disabled = false;
        btn.textContent = "Generate Series";
      }
    });
  });
})();
