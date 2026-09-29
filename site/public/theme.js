// Light / dark. Follows the system until the visitor picks one with the nav
// button; the pick is remembered. Loaded in <head>, not deferred, so a saved
// choice is applied before the first paint instead of flashing the other theme.
(function () {
  var root = document.documentElement, KEY = "reminal-theme";
  try { var saved = localStorage.getItem(KEY); if (saved === "light" || saved === "dark") root.setAttribute("data-theme", saved); } catch (e) {}
  function current() {
    return root.getAttribute("data-theme") || (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
  }
  function wire() {
    var btn = document.getElementById("th");
    if (!btn) return;
    var label = function () { btn.textContent = current() === "dark" ? "Light" : "Dark"; };
    label();
    btn.addEventListener("click", function () {
      var next = current() === "dark" ? "light" : "dark";
      root.setAttribute("data-theme", next);
      try { localStorage.setItem(KEY, next); } catch (e) {}
      label();
    });
    matchMedia("(prefers-color-scheme: dark)").addEventListener("change", label);
  }
  // Scroll-spy: a nav link to a section on this page is underlined while
  // that section is on screen.
  function spy() {
    var links = [].slice.call(document.querySelectorAll('.nav a.l[href^="#"]'));
    var secs = links.map(function (a) { return document.getElementById(a.getAttribute("href").slice(1)); });
    if (!links.length) return;
    var update = function () {
      var y = scrollY + 120, cur = -1;
      secs.forEach(function (s, i) { if (s && s.getBoundingClientRect().top + scrollY <= y) cur = i; });
      if (cur >= 0 && secs[cur] && secs[cur].getBoundingClientRect().bottom < 0) cur = -1;
      links.forEach(function (a, i) { a.classList.toggle("on", i === cur); });
    };
    addEventListener("scroll", update, { passive: true });
    update();
  }
  function init() { wire(); spy(); }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init); else init();
})();
