// Post-build CSP audit: make `public/_headers` an enforced invariant of the
// built site instead of a hand-maintained guess. Invoked by `npm run build`
// after `prehighlight.mjs`, so it sees the HTML that actually ships.
//
//   node csp-audit.mjs [dist-dir]
//
// Why this exists: the site's CSP allows inline <script> by sha256 hash
// rather than `'unsafe-inline'`, and every one of those hashes belongs to a
// Starlight boot script we do not author. An Astro/Starlight upgrade that
// changes one byte of such a script would silently break the theme picker,
// the mobile menu or the sidebar state on the deployed site and nowhere else
// — a build succeeds either way, and a browser only reports it in the
// console. This turns that into a build failure that prints the corrected
// `script-src` to paste into `public/_headers`.
//
// It checks four things:
//   1. dist/_headers is byte-identical to public/_headers (so the policy
//      audited is the policy served).
//   2. Every executable inline <script> in the build is covered by
//      script-src: either an exact sha256 hash, or `'unsafe-inline'`.
//   3. No hash in script-src is stale (nothing in the build produces it).
//   4. No inline <style> element exists unless the effective style-src for
//      elements allows `'unsafe-inline'`.
//
// Inline `style="..."` ATTRIBUTES are out of scope: the build emits
// thousands of them (Starlight's `--depth` / `--sl-icon-size`, Shiki's
// per-token `color:`), they are governed by `style-src-attr`, and CSP has no
// practical hash mechanism for them.
import { readFileSync, readdirSync, statSync } from "node:fs";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";

// `type` values a browser executes as a classic or module script. Anything
// else (`application/json`, `application/ld+json`, `text/template`, ...) is a
// data block: not executed, and therefore not gated by script-src.
const EXECUTABLE_TYPES = new Set([
  "",
  "module",
  "text/javascript",
  "application/javascript",
  "application/ecmascript",
  "text/ecmascript",
  "importmap",
  "speculationrules",
]);

const SCRIPT = /<script\b([^>]*)>([\s\S]*?)<\/script>/gi;
const STYLE_ELEMENT = /<style\b[^>]*>([\s\S]*?)<\/style>/gi;
const SRC_ATTR = /\bsrc\s*=/i;
const TYPE_ATTR = /\btype\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))/i;
const typeOf = (attrs) => {
  const m = TYPE_ATTR.exec(attrs);
  return m ? (m[2] ?? m[3] ?? m[4]).trim().toLowerCase() : "";
};

function hashOf(body) {
  return `sha256-${createHash("sha256").update(body, "utf8").digest("base64")}`;
}

/** Directive -> array of source expressions, from the `_headers` CSP line. */
function parseCsp(headersText) {
  const line = headersText
    .split("\n")
    .find((l) => /^\s*Content-Security-Policy\s*:/i.test(l));
  if (!line) return null;
  const policy = line.slice(line.indexOf(":") + 1).trim();
  const directives = new Map();
  for (const part of policy.split(";")) {
    const tokens = part.trim().split(/\s+/).filter(Boolean);
    if (tokens.length) directives.set(tokens[0].toLowerCase(), tokens.slice(1));
  }
  return directives;
}

function htmlFiles(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const p = `${dir}/${entry}`;
    if (statSync(p).isDirectory()) htmlFiles(p, out);
    else if (p.endsWith(".html")) out.push(p);
  }
  return out;
}

const distDir = fileURLToPath(new URL(process.argv[2] || "dist", import.meta.url));
const publicHeaders = fileURLToPath(new URL("./public/_headers", import.meta.url));
const errors = [];

// 1. The policy audited must be the policy served.
const publicText = readFileSync(publicHeaders, "utf8");
let distText = null;
try {
  distText = readFileSync(`${distDir}/_headers`, "utf8");
} catch {
  errors.push(`${distDir}/_headers is missing — Astro did not copy public/_headers.`);
}
if (distText !== null && distText !== publicText) {
  errors.push("dist/_headers differs from public/_headers; the served policy is not the audited one.");
}

const csp = parseCsp(publicText);
if (!csp) errors.push("public/_headers has no Content-Security-Policy line.");

const scriptSrc = csp?.get("script-src") ?? [];
const scriptInlineAllowed = scriptSrc.includes("'unsafe-inline'");
const listedHashes = new Set(
  scriptSrc.filter((s) => /^'sha(256|384|512)-/.test(s)).map((s) => s.slice(1, -1)),
);
// style-src-elem overrides style-src for <style> elements where supported;
// a browser without it falls back to style-src, so the element source list
// only allows inline if BOTH do.
const styleElemSrc = csp?.get("style-src-elem") ?? csp?.get("style-src") ?? [];
const styleSrc = csp?.get("style-src") ?? [];
const styleElementInlineAllowed =
  styleElemSrc.includes("'unsafe-inline'") && styleSrc.includes("'unsafe-inline'");

const seenHashes = new Map(); // hash -> { count, file, body }
let dataBlocks = 0;
let styleElements = 0;
let externalScripts = 0;
const files = htmlFiles(distDir);

for (const file of files) {
  const html = readFileSync(file, "utf8");
  SCRIPT.lastIndex = 0;
  for (let m; (m = SCRIPT.exec(html)); ) {
    const [, attrs, body] = m;
    if (SRC_ATTR.test(attrs)) {
      externalScripts++;
      continue;
    }
    if (!body.trim()) continue;
    if (!EXECUTABLE_TYPES.has(typeOf(attrs))) {
      dataBlocks++;
      continue;
    }
    const h = hashOf(body);
    const prev = seenHashes.get(h);
    if (prev) prev.count++;
    else seenHashes.set(h, { count: 1, file, body });
  }
  STYLE_ELEMENT.lastIndex = 0;
  for (let m; (m = STYLE_ELEMENT.exec(html)); ) {
    if (m[1].trim()) styleElements++;
  }
}

// 2 + 3. Inline scripts must be exactly the ones script-src names.
if (!scriptInlineAllowed) {
  const missing = [...seenHashes].filter(([h]) => !listedHashes.has(h));
  const stale = [...listedHashes].filter((h) => !seenHashes.has(h));
  if (missing.length) {
    errors.push(
      `${missing.length} inline <script>(s) in the build are not covered by script-src ` +
        `and will be BLOCKED in the browser:\n` +
        missing
          .map(
            ([h, v]) =>
              `    ${h}  (x${v.count}, first in ${v.file})\n` +
              `      ${v.body.trim().split("\n")[0].slice(0, 110)}`,
          )
          .join("\n"),
    );
  }
  if (stale.length) {
    errors.push(
      `${stale.length} sha256 hash(es) in script-src match nothing in the build:\n` +
        stale.map((h) => `    ${h}`).join("\n"),
    );
  }
  if (missing.length || stale.length) {
    const keywords = scriptSrc.filter((s) => !/^'sha(256|384|512)-/.test(s));
    errors.push(
      "Corrected directive for public/_headers:\n    script-src " +
        [...keywords, ...[...seenHashes.keys()].map((h) => `'${h}'`)].join(" "),
    );
  }
}

// 4. Inline <style> elements must be allowed if any exist.
if (styleElements && !styleElementInlineAllowed) {
  errors.push(
    `${styleElements} inline <style> element(s) in the build, but the effective ` +
      `style source list for elements does not allow 'unsafe-inline' ` +
      `(style-src-elem: ${styleElemSrc.join(" ") || "<unset>"}). They will be BLOCKED.`,
  );
}

const summary =
  `csp-audit: ${files.length} html, ${seenHashes.size} distinct inline script(s) ` +
  `(${[...seenHashes.values()].reduce((n, v) => n + v.count, 0)} occurrences), ` +
  `${externalScripts} external script(s), ${dataBlocks} non-executable data block(s), ` +
  `${styleElements} inline <style> element(s); script-src ` +
  `${scriptInlineAllowed ? "allows 'unsafe-inline'" : `pins ${listedHashes.size} hash(es)`}`;

if (errors.length) {
  console.error(summary);
  for (const e of errors) console.error(`csp-audit: ERROR ${e}`);
  process.exit(1);
}
console.log(`${summary} — ok`);
