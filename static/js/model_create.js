// Model Create page — form submission + randomise
(function () {
  "use strict";

  document.addEventListener("DOMContentLoaded", () => {
    const btn = document.getElementById("createModelBtn");

    btn.addEventListener("click", async () => {
      const name = document.getElementById("modelName").value.trim();
      const description = document.getElementById("description").value.trim();

      if (!name) {
        showToast("Please enter a name.", "error");
        return;
      }
      if (!description) {
        showToast("Please enter an appearance description.", "error");
        return;
      }

      btn.disabled = true;
      btn.textContent = "Creating...";

      const payload = {
        name: name,
        handle: document.getElementById("handle").value.trim() || name.toLowerCase().replace(/\s+/g, "."),
        bio: document.getElementById("bio").value.trim(),
        description: description,
        personality: document.getElementById("personality").value.trim(),
        style: document.getElementById("style").value,
        schedule: document.getElementById("schedule").value,
      };

      try {
        const resp = await fetch("/api/models", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        });
        const data = await resp.json();
        if (data.status === "success" && data.modelId) {
          await fetch(`/api/models/${data.modelId}/generate-refs`, { method: "POST" });
          window.location.href = "/models/" + data.modelId;
        } else {
          throw new Error(data.message || "Failed to create model.");
        }
      } catch (e) {
        showToast(e.message || "Connection error.", "error");
        btn.disabled = false;
        btn.textContent = "Create Model";
      }
    });

    // Randomise button.
    const randBtn = document.getElementById("randomiseBtn");
    if (randBtn) {
      randBtn.addEventListener("click", async () => {
        randBtn.disabled = true;
        const origText = randBtn.innerHTML;
        randBtn.textContent = "Thinking...";

        try {
          const resp = await fetch("/api/models/randomise", { method: "POST" });
          const data = await resp.json();
          if (data.status === "success" && data.model) {
            const m = data.model;
            if (m.name) document.getElementById("modelName").value = m.name;
            if (m.handle) document.getElementById("handle").value = m.handle;
            if (m.bio) document.getElementById("bio").value = m.bio;
            if (m.description) document.getElementById("description").value = m.description;
            if (m.personality) document.getElementById("personality").value = m.personality;
            if (m.style) {
              const sel = document.getElementById("style");
              for (const opt of sel.options) {
                if (opt.value === m.style) { sel.value = m.style; break; }
              }
            }
            document.querySelectorAll("textarea").forEach((ta) => {
              ta.dispatchEvent(new Event("input"));
            });
            showToast("Model profile generated!", "success");
          } else {
            throw new Error(data.message || "Failed to generate");
          }
        } catch (e) {
          showToast(e.message || "Generation failed", "error");
        } finally {
          randBtn.disabled = false;
          randBtn.innerHTML = origText;
        }
      });
    }
  });
})();
