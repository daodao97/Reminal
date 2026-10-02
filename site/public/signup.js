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

  // Anything else the page put in the form as a hidden field (context a page
  // adds to its signup), sent along as it is, each cut to
  // what Loops keeps (a few hundred characters; 400 is safe).
  function extras() {
    var out = {};
    form.querySelectorAll('input[type="hidden"][name]').forEach(function (i) {
      // Empty ones too: a list keeps a property it is not sent, so a value
      // an earlier signup set is cleared, not kept.
      out[i.name] = i.value.slice(0, 400);
    });
    return out;
  }

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
      // The page's own step before sending (anything it must
      // store first). Its failure never stops the signup.
      if (typeof form.reminalBefore === "function") {
        try { await form.reminalBefore(); } catch (e) {}
      }
      var base = {
        email: email,
        userGroup: form.dataset.group,
        source: "reminal.app" + location.pathname.replace(/\/$/, ""),
        audience: audience,
        teamSize: audience === "team" && sizeEl ? sizeEl.value : "",
      };
      var send = function (fields) {
        return fetch(LOOPS_ENDPOINT, {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams(fields),
        });
      };
      var res = await send(Object.assign({}, base, extras()));
      // Loops refuses the whole signup when any value is too long; the
      // address matters more than the extras, so it goes again without them.
      if (res.status === 400) {
        var why = await res.clone().json().catch(function () { return {}; });
        if (/longer than allowable/i.test(why.message || "")) res = await send(base);
      }
      if (!res.ok) throw new Error("HTTP " + res.status);
      form.hidden = true;
      say(form.dataset.done, "ok");
    } catch (err) {
      submitEl.disabled = false;
      say("That didn't go through. Email mail@harshalgajjar.com and I'll add you by hand.", "err");
    }
  });
})();
