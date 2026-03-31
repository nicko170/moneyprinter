// Create Job page — form submission + redirect
(function () {
  "use strict";

  document.addEventListener("DOMContentLoaded", () => {
    const btn = document.getElementById("generateButton");
    const subject = document.getElementById("videoSubject");

    btn.addEventListener("click", async () => {
      const text = subject.value.trim();
      if (!text) {
        showToast("Please enter a video subject.", "error");
        return;
      }

      btn.disabled = true;
      btn.textContent = "Submitting…";

      const payload = {
        videoSubject: text,
        voice: document.getElementById("voice").value,
        paragraphNumber: parseInt(document.getElementById("paragraphNumber").value) || 1,
        subtitlesPosition: document.getElementById("subtitlesPosition").value,
        color: document.getElementById("subtitlesColor").value,
        useMusic: document.getElementById("useMusicToggle").checked,
        customPrompt: document.getElementById("customPrompt").value,
        context: document.getElementById("context").value,
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

      // Handle logo upload — if a file is selected, upload it first.
      const logoInput = document.getElementById("endCardLogo");
      if (logoInput && logoInput.files.length > 0) {
        const formData = new FormData();
        formData.append("logo", logoInput.files[0]);
        try {
          const uploadResp = await fetch("/api/upload-logo", { method: "POST", body: formData });
          const uploadData = await uploadResp.json();
          if (uploadData.path) {
            payload.endCardLogoPath = uploadData.path;
          }
        } catch (e) {
          showToast("Logo upload failed, continuing without", "warning");
        }
      }

      try {
        const resp = await fetch("/api/generate", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        });
        const data = await resp.json();
        if (data.status === "success" && data.jobId) {
          window.location.href = "/jobs/" + data.jobId;
        } else {
          showToast(data.message || "Failed to start generation.", "error");
          btn.disabled = false;
          btn.textContent = "Generate";
        }
      } catch (e) {
        showToast("Connection error. Is the server running?", "error");
        btn.disabled = false;
        btn.textContent = "Generate";
      }
    });

    // Enter in subject = generate
    subject.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        btn.click();
      }
    });
  });
})();
