use zed_extension_api::{
    self as zed, serde_json, DebugAdapterBinary, DebugConfig, DebugRequest, DebugScenario,
    DebugTaskDefinition, StartDebuggingRequestArguments, StartDebuggingRequestArgumentsRequest,
    TaskTemplate,
};

/// The debug adapter's name, as extension.toml registers it and a
/// debug.json scenario's `adapter` names it.
const ADAPTER: &str = "Nomi";

struct NomiExtension;

impl zed::Extension for NomiExtension {
    fn new() -> Self {
        NomiExtension
    }

    fn language_server_command(
        &mut self,
        _language_server_id: &zed::LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<zed::Command, String> {
        let path = worktree.which("nomi-lsp").ok_or_else(|| {
            "nomi-lsp not found on PATH. Install with: go install ./cmd/nomi-lsp/".to_string()
        })?;

        Ok(zed::Command {
            command: path,
            args: vec![],
            env: Default::default(),
        })
    }

    /// The debug adapter is `nomi dap`, speaking DAP over stdio.
    fn get_dap_binary(
        &mut self,
        adapter_name: String,
        config: DebugTaskDefinition,
        user_provided_debug_adapter_path: Option<String>,
        worktree: &zed::Worktree,
    ) -> Result<DebugAdapterBinary, String> {
        if adapter_name != ADAPTER {
            return Err(format!("the Nomi extension has no debug adapter {adapter_name}"));
        }
        let command = match user_provided_debug_adapter_path {
            Some(path) => path,
            None => worktree.which("nomi").ok_or_else(|| {
                "nomi not found on PATH. Install with: go install ./cmd/nomi/".to_string()
            })?,
        };
        let request = self.dap_request_kind(
            adapter_name,
            serde_json::from_str(&config.config).map_err(|e| format!("the debug configuration does not parse: {e}"))?,
        )?;
        Ok(DebugAdapterBinary {
            command: Some(command),
            arguments: vec!["dap".to_string()],
            envs: vec![],
            cwd: Some(worktree.root_path()),
            connection: None,
            request_args: StartDebuggingRequestArguments {
                configuration: config.config,
                request,
            },
        })
    }

    /// Every Nomi session launches its program.
    fn dap_request_kind(
        &mut self,
        _adapter_name: String,
        config: serde_json::Value,
    ) -> Result<StartDebuggingRequestArgumentsRequest, String> {
        match config.get("request").and_then(|r| r.as_str()) {
            None | Some("launch") => Ok(StartDebuggingRequestArgumentsRequest::Launch),
            Some(other) => Err(format!(
                "nomi dap launches programs; `request: \"{other}\"` is not supported"
            )),
        }
    }

    /// A session from Zed's new-session dialog runs the named program.
    fn dap_config_to_scenario(&mut self, config: DebugConfig) -> Result<DebugScenario, String> {
        let launch = match config.request {
            DebugRequest::Launch(launch) => launch,
            DebugRequest::Attach(_) => {
                return Err("nomi dap launches programs; attaching is not supported".to_string())
            }
        };
        let mut json = serde_json::json!({
            "request": "launch",
            "mode": "run",
            "program": launch.program,
            "args": launch.args,
        });
        if let Some(cwd) = launch.cwd {
            json["cwd"] = serde_json::Value::String(cwd);
        }
        if let Some(stop) = config.stop_on_entry {
            json["stopOnEntry"] = serde_json::Value::Bool(stop);
        }
        Ok(DebugScenario {
            label: config.label,
            adapter: ADAPTER.to_string(),
            build: None,
            config: json.to_string(),
            tcp_connection: None,
        })
    }

    /// The locator turns the extension's `nomi run` and `nomi test` tasks
    /// (languages/nomi/tasks.json) into debug sessions, so each has a debug
    /// entry beside it in Zed's task and debug pickers.
    fn dap_locator_create_scenario(
        &mut self,
        _locator_name: String,
        build_task: TaskTemplate,
        resolved_label: String,
        _debug_adapter_name: String,
    ) -> Option<DebugScenario> {
        let config = scenario_config(&build_task)?;
        Some(DebugScenario {
            label: format!("Debug: {resolved_label}"),
            adapter: ADAPTER.to_string(),
            build: None,
            config: config.to_string(),
            tcp_connection: None,
        })
    }
}

/// The launch configuration a `nomi run FILE` or `nomi test FILE [--line N]`
/// task debugs, or None for any other task.
fn scenario_config(task: &TaskTemplate) -> Option<serde_json::Value> {
    if task.command != "nomi" && !task.command.ends_with("/nomi") {
        return None;
    }
    let (mode, rest) = task.args.split_first()?;
    if mode != "run" && mode != "test" {
        return None;
    }
    let program = rest.first()?;
    if program.starts_with('-') {
        return None;
    }
    let mut json = serde_json::json!({
        "request": "launch",
        "mode": mode,
        "program": program,
    });
    if let Some(cwd) = &task.cwd {
        json["cwd"] = serde_json::Value::String(cwd.clone());
    }
    if mode == "test" {
        if let Some(i) = rest.iter().position(|a| a == "--line") {
            let line = rest.get(i + 1)?;
            json["line"] = match line.parse::<u64>() {
                Ok(n) => serde_json::Value::from(n),
                // An unresolved `$ZED_ROW`, which Zed substitutes in the
                // configuration before the adapter reads it.
                Err(_) => serde_json::Value::String(line.clone()),
            };
        }
    } else if rest.len() > 1 {
        json["args"] = serde_json::Value::from(rest[1..].to_vec());
    }
    Some(json)
}

zed::register_extension!(NomiExtension);

#[cfg(test)]
mod tests {
    use super::*;

    fn task(args: &[&str]) -> TaskTemplate {
        TaskTemplate {
            label: "t".to_string(),
            command: "nomi".to_string(),
            args: args.iter().map(|a| a.to_string()).collect(),
            env: vec![],
            cwd: None,
        }
    }

    #[test]
    fn a_test_task_debugs_its_line() {
        let got = scenario_config(&task(&["test", "/w/a_test.nomi", "--line", "12"])).unwrap();
        assert_eq!(
            got,
            serde_json::json!({"request": "launch", "mode": "test", "program": "/w/a_test.nomi", "line": 12})
        );
    }

    #[test]
    fn a_run_task_debugs_its_program_with_its_arguments() {
        let got = scenario_config(&task(&["run", "/w/main.nomi", "x"])).unwrap();
        assert_eq!(
            got,
            serde_json::json!({"request": "launch", "mode": "run", "program": "/w/main.nomi", "args": ["x"]})
        );
    }

    #[test]
    fn other_tasks_are_not_debugged() {
        assert!(scenario_config(&task(&["test"])).is_none());
        assert!(scenario_config(&task(&["fmt", "/w/a.nomi"])).is_none());
    }
}
