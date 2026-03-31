// MoneyPrinter — shared toast utility (loaded on all pages via layout)
window.showToast = function (message, type = "info") {
  const container = document.getElementById("toastContainer");
  if (!container) return;
  const colors = {
    success: "border-green-500 bg-green-50 text-green-800",
    error: "border-red-500 bg-red-50 text-red-800",
    warning: "border-yellow-500 bg-yellow-50 text-yellow-800",
    info: "border-zinc-300 bg-zinc-50 text-zinc-800",
  };
  const toast = document.createElement("div");
  toast.className = `flex items-center gap-3 rounded-lg border-l-4 px-4 py-3 text-sm shadow-lg max-w-sm transition-all duration-300 translate-x-full opacity-0 ${colors[type] || colors.info}`;
  toast.innerHTML = `<span class="flex-1">${message}</span><button class="text-current opacity-50 hover:opacity-100" onclick="this.parentElement.remove()">&times;</button>`;
  container.appendChild(toast);
  requestAnimationFrame(() =>
    requestAnimationFrame(() =>
      toast.classList.remove("translate-x-full", "opacity-0")
    )
  );
  setTimeout(() => {
    toast.classList.add("translate-x-full", "opacity-0");
    setTimeout(() => toast.remove(), 300);
  }, 5000);
};
