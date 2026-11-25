/**
 * The TOML files the language server reads: each module's nomi.toml, and
 * its phrase catalogs, the TOML files a module's `phrase` translations
 * live in, under the directory `[i18n].catalogs` names in its nomi.toml
 * (`locales` unless it says otherwise). The language client attaches to
 * them, and to no other TOML file, through the document filters this
 * builds from each module's manifest (tomlFilters).
 */

/** A module's manifest: the directory holding it and its text. */
export interface Manifest {
  dir: string;
  text: string;
}

/**
 * The catalog directory a manifest names, relative to its module root, or
 * undefined when its `[i18n]` table lists no locales, so it has no
 * catalogs. Only the keys catalogs need are read: `locales` and `catalogs`
 * in `[i18n]`.
 */
export function catalogDir(manifest: string): string | undefined {
  let inI18n = false;
  let listed = false;
  let dir = "locales";
  for (const raw of manifest.split(/\r?\n/)) {
    const line = raw.replace(/#.*$/, "").trim();
    const table = /^\[\s*([^\]]+?)\s*\]$/.exec(line);
    if (table) {
      inI18n = table[1] === "i18n";
      continue;
    }
    if (!inI18n) continue;
    if (/^locales\s*=/.test(line)) listed = true;
    const value = /^catalogs\s*=\s*["']([^"']*)["']/.exec(line);
    if (value) dir = value[1];
  }
  if (!listed) return undefined;
  return dir.replace(/^\.\//, "").replace(/\/+$/, "");
}

/**
 * The document filters of every catalog file of the modules whose manifests
 * are given: `<module>/<catalogs>/**\/*.toml` for each module with locales.
 */
export function catalogFilters(manifests: Manifest[]): { scheme: string; pattern: string }[] {
  const out: { scheme: string; pattern: string }[] = [];
  const seen = new Set<string>();
  for (const m of manifests) {
    const dir = catalogDir(m.text);
    if (dir === undefined) continue;
    const root = m.dir.replace(/\\/g, "/").replace(/\/+$/, "");
    const pattern = `${root}/${dir}/**/*.toml`;
    if (!seen.has(pattern)) {
      seen.add(pattern);
      out.push({ scheme: "file", pattern });
    }
  }
  return out;
}

/**
 * The document filters of the TOML files the client sends the server: every
 * nomi.toml, whose errors and [effects] lists the server checks, every
 * nomi-workspace.toml, whose members it checks, and the catalogs of the
 * modules whose manifests are given (catalogFilters).
 */
export function tomlFilters(manifests: Manifest[]): { scheme: string; pattern: string }[] {
  return [
    { scheme: "file", pattern: "**/nomi.toml" },
    { scheme: "file", pattern: "**/nomi-workspace.toml" },
    ...catalogFilters(manifests),
  ];
}
