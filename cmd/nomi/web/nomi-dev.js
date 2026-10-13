// nomi-dev.js is the page side of `nomi run --web`. The server adds it to
// every .html page it serves as /__nomi/dev.js?build=<n>&static=<m>; `nomi
// build --web` never writes it.
//
// It listens to /__nomi/events, a stream of the server's state as
// {"build": n, "static": m, "error": "..."}: n counts the program's builds
// and m the changes to the author's static files. A build or static number
// other than the page's own reloads the page; an error is shown over the
// page, which keeps running the last build that succeeded, and is cleared
// when a build succeeds.

const params = new URL(import.meta.url).searchParams;
const build = params.get("build");
const staticCount = params.get("static");
let overlay = null;

function show(error) {
  if (!error) {
    overlay?.remove();
    overlay = null;
    return;
  }
  if (!overlay) {
    overlay = document.createElement("div");
    overlay.setAttribute("role", "alert");
    Object.assign(overlay.style, {
      position: "fixed",
      inset: "0 0 auto 0",
      maxHeight: "60vh",
      overflow: "auto",
      zIndex: "2147483647",
      margin: "0",
      padding: "12px 16px",
      font: "13px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
      background: "light-dark(#fdecea, #3b1a17)",
      color: "light-dark(#5f1410, #ffd6d1)",
      borderBottom: "2px solid light-dark(#b3261e, #ff8a80)",
      colorScheme: "light dark",
    });
    const title = document.createElement("div");
    title.style.fontWeight = "bold";
    title.style.marginBottom = "8px";
    title.textContent = "nomi run --web cannot serve your latest change. The page runs the last build that succeeded.";
    const text = document.createElement("pre");
    Object.assign(text.style, { margin: "0", whiteSpace: "pre-wrap", overflowWrap: "anywhere" });
    overlay.append(title, text);
    document.body.append(overlay);
  }
  overlay.lastChild.textContent = error;
}

const events = new EventSource(new URL("./events", import.meta.url));
events.onmessage = (e) => {
  const state = JSON.parse(e.data);
  if (String(state.build) !== build || String(state.static) !== staticCount) {
    location.reload();
    return;
  }
  show(state.error);
};
