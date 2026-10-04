use zed_extension_api as zed;

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
}

zed::register_extension!(NomiExtension);
