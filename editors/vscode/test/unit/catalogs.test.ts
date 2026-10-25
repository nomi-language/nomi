import * as assert from "node:assert/strict";
import { test } from "node:test";
import { catalogDir, catalogFilters } from "../../src/catalogs";

test("a module with locales has its catalogs under `locales` unless [i18n] names another directory", () => {
  assert.equal(catalogDir('[module]\nname = "shop"\n\n[i18n]\nlocales = ["en", "pl"]\n'), "locales");
  assert.equal(catalogDir('[i18n]\nlocales = ["en", "pl"]\ncatalogs = "text/catalogs/"  # moved\n'), "text/catalogs");
  assert.equal(catalogDir("[i18n]\ncatalogs = './tr'\nlocales = ['en']\n"), "tr");
});

test("a module with no locales has no catalogs", () => {
  assert.equal(catalogDir('[module]\nname = "app"\n'), undefined);
  assert.equal(catalogDir('[module]\nlocales = ["en"]\n'), undefined);
  assert.equal(catalogDir('[i18n]\ncatalogs = "locales"\n'), undefined);
});

test("the client attaches to each module's catalogs, and to no other TOML file", () => {
  const filters = catalogFilters([
    { dir: "/w/shop", text: '[i18n]\nlocales = ["en", "pl"]\n' },
    { dir: "/w/admin", text: '[i18n]\nlocales = ["en"]\ncatalogs = "i18n"\n' },
    { dir: "/w/tool", text: '[module]\nname = "tool"\n' },
    { dir: "C:\\w\\web", text: '[i18n]\nlocales = ["en"]\n' },
  ]);
  assert.deepEqual(filters, [
    { scheme: "file", pattern: "/w/shop/locales/**/*.toml" },
    { scheme: "file", pattern: "/w/admin/i18n/**/*.toml" },
    { scheme: "file", pattern: "C:/w/web/locales/**/*.toml" },
  ]);
});
