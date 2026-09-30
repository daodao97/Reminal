// The signup form (#signup): an email, who it's for, and for a team, how
// many people. Each page's form says what it is for in its own markup:
//   data-group  the list it joins in Loops (its userGroup), e.g. "waitlist"
//   data-done   what to say once it has gone through
// Everything else, from checking the address to the fallback when the
// network fails, is the same wherever the form appears.
(function () {
  // Loops' endpoint for a self-built form (Forms → Create → "Use my own
  // form"). Public by design: it only accepts subscribes, so there is no
  // secret to leak.
  var LOOPS_ENDPOINT = "https://app.loops.so/api/newsletter-form/cmswt702j18uc0j0uaj8c1b84";

  var form = document.getElementById("signup");
  if (!form) return;
  var emailEl = document.getElementById("email");
  var submitEl = document.getElementById("submit");
  var msgEl = document.getElementById("msg");
  var sizeRow = document.getElementById("sizerow");
  var sizeEl = document.getElementById("teamsize");

  // Who it's for. The headcount only exists once someone says "my team", so an
  // individual signing up still answers exactly one question.
  var audience = "individual";
  document.querySelectorAll(".whobtn").forEach(function (btn) {
    btn.addEventListener("click", function () {
      audience = btn.dataset.who;
      document.querySelectorAll(".whobtn").forEach(function (b) {
        b.setAttribute("aria-checked", String(b === btn));
      });
      if (sizeRow) sizeRow.hidden = audience !== "team";
    });
  });

  function say(text, kind) {
    msgEl.textContent = text;
    msgEl.className = "formmsg" + (kind ? " " + kind : "");
  }

  form.addEventListener("submit", async function (event) {
    event.preventDefault();
    var email = emailEl.value.trim();
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) {
      say("That doesn't look like an email address.", "err");
      emailEl.focus();
      return;
    }
    submitEl.disabled = true;
    say("Adding you…");
    try {
      // Loops' form endpoint wants form-encoded, and answers opaquely on
      // some plans, so a network-level success is treated as a success.
      var res = await fetch(LOOPS_ENDPOINT, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({
          email: email,
          userGroup: form.dataset.group,
          source: "reminal.app" + location.pathname.replace(/\/$/, ""),
          audience: audience,
          teamSize: audience === "team" && sizeEl ? sizeEl.value : "",
        }),
      });
      if (!res.ok) throw new Error("HTTP " + res.status);
      form.hidden = true;
      say(form.dataset.done, "ok");
    } catch (err) {
      submitEl.disabled = false;
      say("That didn't go through. Email mail@harshalgajjar.com and I'll add you by hand.", "err");
    }
  });
})();
