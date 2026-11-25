-- Default client config for `vim.lsp.enable('nomi')` (Neovim 0.11+).
-- Override any field with `vim.lsp.config('nomi', { ... })`.

-- The server is `nomi lsp`, from the `nomi` on PATH, as the runner and the
-- debugger find it.

---@type vim.lsp.Config
return {
  cmd = { 'nomi', 'lsp' },
  -- A TOML buffer only when it is a module's nomi.toml, a workspace's
  -- nomi-workspace.toml or one of a module's phrase catalogs.
  filetypes = { 'nomi', 'toml' },
  root_dir = function(bufnr, on_dir)
    if vim.bo[bufnr].filetype == 'toml' then
      local root = require('nomi.catalogs').root(vim.api.nvim_buf_get_name(bufnr))
      if root then
        on_dir(root)
      end
      return
    end
    -- Priority order: the nearest nomi.toml wins over an enclosing .git.
    on_dir(vim.fs.root(bufnr, { 'nomi.toml', '.git' }))
  end,
}
