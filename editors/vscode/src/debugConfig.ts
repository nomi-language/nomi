// Debug configurations for `nomi dap`, free of the vscode module so the unit
// tests can run them under plain node.

/** A launch configuration of type `nomi`, as launch.json spells it. */
export interface NomiDebugConfiguration {
  type: string;
  request: string;
  name: string;
  /** "run" runs the file's `main`; "test" runs its tests. */
  mode?: "run" | "test";
  program?: string;
  /** In test mode, the line a test starts on, as `nomi test --line` takes it. */
  line?: number;
  args?: string[];
  cwd?: string;
  env?: Record<string, string>;
  console?: "integratedTerminal" | "internalConsole" | "externalTerminal";
  justMyCode?: boolean;
  stopOnEntry?: boolean;
  [key: string]: unknown;
}

/** The configuration that debugs `main` of file. */
export function debugMainConfiguration(file: string, cwd?: string): NomiDebugConfiguration {
  return { type: "nomi", request: "launch", name: `Debug ${basename(file)}`, mode: "run", program: file, cwd };
}

/**
 * The configuration that debugs the tests of file: the one test (or group,
 * or `//!` attached test) starting on line when it is given, else them all.
 */
export function debugTestConfiguration(file: string, line?: number, cwd?: string): NomiDebugConfiguration {
  const config: NomiDebugConfiguration = {
    type: "nomi",
    request: "launch",
    name: line === undefined ? `Debug tests in ${basename(file)}` : `Debug test at ${basename(file)}:${line}`,
    mode: "test",
    program: file,
    cwd,
  };
  if (line !== undefined) {
    config.line = line;
  }
  return config;
}

/** The configurations a new launch.json starts with. */
export function initialConfigurations(): NomiDebugConfiguration[] {
  return [
    { type: "nomi", request: "launch", name: "Debug current file", mode: "run", program: "${file}" },
    { type: "nomi", request: "launch", name: "Debug tests in current file", mode: "test", program: "${file}" },
  ];
}

/** Whether source declares `fn main` at the top level. */
export function declaresMain(source: string): boolean {
  return /^(pub\s+)?fn\s+main\s*\(/m.test(source);
}

/**
 * Fills in a configuration F5 started: with no launch.json the configuration
 * is empty, and the active Nomi file is debugged, its `main` when it has one
 * and its tests otherwise. A configuration with no program gets the active
 * file. Answers undefined when there is nothing to debug.
 */
export function resolveConfiguration(
  config: Partial<NomiDebugConfiguration>,
  active: { file: string; source: string } | undefined,
  cwd: string | undefined,
): NomiDebugConfiguration | undefined {
  if (!config.type && !config.request && !config.name) {
    if (!active) {
      return undefined;
    }
    const isTest = active.file.endsWith("_test.nomi") || !declaresMain(active.source);
    return isTest ? debugTestConfiguration(active.file, undefined, cwd) : debugMainConfiguration(active.file, cwd);
  }
  const out = { ...config, type: "nomi", request: config.request ?? "launch", name: config.name ?? "Debug" } as NomiDebugConfiguration;
  if (!out.program) {
    if (!active) {
      return undefined;
    }
    out.program = active.file;
  }
  if (!out.cwd && cwd) {
    out.cwd = cwd;
  }
  return out;
}

function basename(file: string): string {
  const parts = file.split(/[\\/]/);
  return parts[parts.length - 1];
}
