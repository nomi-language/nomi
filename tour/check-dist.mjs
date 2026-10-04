// Post-build size check: fail `npm run build` on a dist/ that Cloudflare Pages
// would refuse at upload. Invoked by `npm run build` after csp-audit.mjs.
//
//   node check-dist.mjs [dist-dir]
//
// Pages rejects any single file over 25 MiB, and it does so only at the end of
// `wrangler pages deploy`, after the whole build has run. The Nomi wasm is
// about 27 MiB uncompressed, so scripts/build-tour-wasm.sh stages it as
// nomi.wasm.gz and run-worker.js decompresses it in the browser. This check
// fails on:
//   1. any file in dist/ over 25 MiB, naming each one and its size;
//   2. an uncompressed nomi.wasm anywhere in dist/, which means something
//      staged the raw module again, even if it has not yet crossed the limit.
import { readdirSync, statSync } from "node:fs";
import { join, relative, basename } from "node:path";

const LIMIT = 25 * 1024 * 1024;
const dist = process.argv[2] ?? "dist";

function* walk(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    const st = statSync(p);
    if (st.isDirectory()) yield* walk(p);
    else yield [p, st.size];
  }
}

const mib = (n) => (n / 1024 / 1024).toFixed(1) + " MiB";
const problems = [];
let largest = ["", 0];
for (const [p, size] of walk(dist)) {
  if (size > largest[1]) largest = [p, size];
  if (size > LIMIT) {
    problems.push(`${relative(dist, p)} is ${mib(size)}; Cloudflare Pages refuses files over 25 MiB`);
  }
  if (basename(p) === "nomi.wasm") {
    problems.push(
      `${relative(dist, p)} is the uncompressed Nomi wasm; the site serves nomi.wasm.gz (see scripts/build-tour-wasm.sh)`,
    );
  }
}

if (problems.length) {
  console.error("check-dist: dist/ cannot be deployed to Cloudflare Pages:");
  for (const m of problems) console.error("  " + m);
  process.exit(1);
}
console.log(`check-dist: ok, largest file ${relative(dist, largest[0])} (${mib(largest[1])})`);
