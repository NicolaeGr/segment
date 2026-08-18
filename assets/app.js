// app.js — the two client-side hooks that make the segment stack work.
// 1. attach the currently-mounted segment stack to every htmx request
// 2. adopt the new stack after each navigation (drives body[data-stack]).
// The client reports no modal state: modal context is baked into links by the
// server (X-Modal headers + hx-target).

// The server stamps the true mounted stack onto <body data-stack> on full
// loads, so initialize from the DOM (a direct reload of /dashboard/settings
// has root,dashboard,settings mounted).
let mounted = (document.body.dataset.stack || "root").split(",");

document.body.addEventListener("htmx:configRequest", (e) => {
  // X-Drop-Segments (baked into the expand button) names segments that are
  // mounted only inside the modal; drop them so the server reloads them for
  // the full page.
  const drop = e.detail.headers["X-Drop-Segments"];
  if (drop) {
    const dropped = new Set(drop.split(","));
    mounted = mounted.filter((id) => !dropped.has(id));
  }

  e.detail.headers["X-Mounted-Segments"] = mounted.join(",");

  // The expand button carries the modal's open URL, but the user may have
  // navigated within the modal — request the live path instead.
  if (e.detail.headers["X-Modal"] === "none") {
    e.detail.path = location.pathname + location.search;
  }
});

document.body.addEventListener("segments", (e) => {
  mounted = e.detail.value.split(",");
  document.body.dataset.stack = e.detail.value;
  document.body.dataset.mounted = mounted.join(",");
});

// Any handler can trigger `showToast` to surface feedback.
let toastTimer = null;
document.body.addEventListener("showToast", (e) => {
  const msg = e.detail?.value || "Done";
  let toast = document.getElementById("app-toast");
  if (!toast) {
    toast = document.createElement("div");
    toast.id = "app-toast";
    toast.className = "app-toast";
    document.body.appendChild(toast);
  }
  toast.textContent = msg;
  toast.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.remove("show"), 2000);
});
