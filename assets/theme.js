(function () {
  var dark = false;
  try {
    var saved = localStorage.getItem("theme");
    if (saved === "dark" || saved === "light") {
      dark = saved === "dark";
    } else {
      dark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    }
  } catch (_) {
    dark = window.matchMedia("(prefers-color-scheme: dark)").matches;
  }
  if (dark) {
    document.documentElement.classList.add("dark");
  }
})();

const ICON_SUN =
  '<svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/></svg>';
const ICON_MOON =
  '<svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/></svg>';

function effectiveDark() {
  return document.documentElement.classList.contains("dark");
}

function renderThemeIcon() {
  const el = document.querySelector("[data-theme-icon]");
  if (el) el.innerHTML = effectiveDark() ? ICON_SUN : ICON_MOON;
}

function toggleTheme() {
  const dark = !effectiveDark();
  document.documentElement.classList.toggle("dark", dark);
  try {
    localStorage.setItem("theme", dark ? "dark" : "light");
  } catch (_) {}
  renderThemeIcon();
}

// Sync the theme icon on load
document.addEventListener("DOMContentLoaded", renderThemeIcon);
