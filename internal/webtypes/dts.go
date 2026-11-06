package webtypes

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/irbuild"
)

// Declarations is nomi.d.ts for a program whose entry file is named entry
// (`hangman.nomi`), whose exported functions are exports, and whose files
// are mods: the loader's API (load, run, mount), and a typed `call`
// overload for each exported function, with the types they name.
func Declarations(entry string, exports []irbuild.Export, mods []Module) string {
	w := NewWriter(IntsExact)
	// The loader's own types, and the DOM's that its API names.
	w.Reserve("NomiProgram", "NomiExport", "NomiLoadOptions", "NomiRunOptions", "NomiRunResult",
		"Promise", "URL", "Element", "AbortSignal", "Uint8Array")
	p := w.AddProgram(mods)
	for _, e := range exports {
		for _, prm := range e.Params {
			w.Visit(p, prm.Type)
		}
		if !analysis.IsUnitType(e.Result) {
			w.Visit(p, e.Result)
		}
	}
	w.Seal()
	var calls []string
	for _, e := range exports {
		var params []string
		for i, prm := range e.Params {
			params = append(params, fmt.Sprintf("%s: %s", paramName(prm.Name, i), w.TS(p, prm.Type)))
		}
		result := "void"
		if !analysis.IsUnitType(e.Result) {
			result = w.TS(p, e.Result)
		}
		args := ""
		if len(params) > 0 {
			args = ", " + strings.Join(params, ", ")
		}
		calls = append(calls, fmt.Sprintf("  call(name: %s%s): Promise<%s>;", quote(e.Name), args, result))
	}
	if len(calls) == 0 {
		calls = append(calls, "  call(name: string, ...args: unknown[]): Promise<unknown>;")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "// nomi.d.ts, written by `nomi build --web` for %s: the types of nomi.js's\n", entry)
	b.WriteString("// API, with a typed `call` for each exported function.\n\n")
	b.WriteString(w.Declarations())
	b.WriteString("/** A program `load` started, whose exported functions a page calls. */\n")
	b.WriteString("export interface NomiProgram {\n")
	b.WriteString("  /** Calls an exported function. Rejects with the Nomi diagnostic when it fails; the program stays loaded. */\n")
	for _, c := range calls {
		b.WriteString(c + "\n")
	}
	b.WriteString(api)
	return b.String()
}

// api is the rest of nomi.js's API, after NomiProgram's call overloads.
const api = `  /** The exported functions, with their Nomi signatures. */
  exports(): Promise<NomiExport[]>;
  /** Stops the program's worker. Pending calls reject. */
  close(): void;
}

export interface NomiExport {
  name: string;
  params: { name: string; type: string }[];
  /** The result type as Nomi writes it, or "" for a Unit result. */
  result: string;
  /** The declaration without ` + "`export fn`" + `: "guess(game: Game, letter: String): Game". */
  signature: string;
}

export interface NomiLoadOptions {
  /** Startup.args. */
  args?: string[];
  /** Startup.env; NOMI_COLOR=never unless set here. */
  env?: Record<string, string>;
  /** Output as written; default console.log per line. */
  stdout?: (text: string) => void;
  /** Default console.error per line. */
  stderr?: (text: string) => void;
  /** The folder Locale.load fetches <tag>.nomil from; default locales/ beside the module. */
  locales?: string | URL;
  /** A module imported where the program runs, its default export awaited before the program starts. */
  setup?: string | URL;
}

export interface NomiRunOptions extends NomiLoadOptions {
  stdin?: string | (() => Promise<string | null> | string | null);
  timeoutMs?: number;
  signal?: AbortSignal;
  worker?: boolean;
  /** The files the program's in-memory file system starts with, by path from its working directory. */
  files?: Record<string, string | Uint8Array>;
}

export interface NomiRunResult {
  exitCode: number;
  fatal?: string;
  /** Every file in the program's file system when it ended, by path; absent when it was stopped. */
  files?: Record<string, Uint8Array>;
}

/** Starts the program at url in a worker, runs its main when it has one, and resolves when its exported functions can be called. */
export function load(url: string | URL, options?: NomiLoadOptions): Promise<NomiProgram>;

/** Runs the program at url to its end. */
export function run(url: string | URL, options?: NomiRunOptions): Promise<NomiRunResult>;

/** Mounts a terminal on el and runs the program its data-nomi-program names. */
export function mount(el: Element, url?: string): void;
`

// tsReserved are the words a TypeScript parameter cannot be named.
var tsReserved = map[string]bool{
	"break": true, "case": true, "catch": true, "class": true, "const": true, "continue": true,
	"debugger": true, "default": true, "delete": true, "do": true, "else": true, "enum": true,
	"export": true, "extends": true, "false": true, "finally": true, "for": true, "function": true,
	"if": true, "import": true, "in": true, "instanceof": true, "new": true, "null": true,
	"return": true, "super": true, "switch": true, "this": true, "throw": true, "true": true,
	"try": true, "typeof": true, "var": true, "void": true, "while": true, "with": true,
	"yield": true, "let": true, "static": true, "implements": true, "interface": true,
	"package": true, "private": true, "protected": true, "public": true, "await": true, "name": true,
}

// paramName is a Nomi parameter name as a TypeScript one: `?` and any other
// character an identifier cannot hold become `_`, a reserved word (and
// `name`, the overload's first parameter) gains a trailing `_`, and a
// destructured parameter is `argN`.
func paramName(name string, i int) string {
	if name == "" || name == "_" {
		return fmt.Sprintf("arg%d", i+1)
	}
	var b strings.Builder
	for _, r := range name {
		if r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if tsReserved[out] {
		out += "_"
	}
	return out
}
