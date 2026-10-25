-- Default client config for `vim.lsp.enable('nomi')` (Neovim 0.11+).
-- Override any field with `vim.lsp.config('nomi', { ... })`.

-- Prefer `nomi-lsp` on PATH; fall back to where `go install` puts it.
local cmd = 'nomi-lsp'
if vim.fn.executable(cmd) == 0 then
  local gobin = vim.env.GOBIN
  if not gobin or gobin == '' then
    gobin = vim.fs.joinpath(vim.env.GOPATH or vim.fs.joinpath(vim.env.HOME, 'go'), 'bin')
  end
  local candidate = vim.fs.joinpath(gobin, 'nomi-lsp')
  if vim.fn.executable(candidate) == 1 then
    cmd = candidate
  end
end

---@type vim.lsp.Config
return {
  cmd = { cmd },
  -- A TOML buffer only when it is one of a module's message catalogs.
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
