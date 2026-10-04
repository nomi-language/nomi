-- Run Nomi tests and programs from Neovim, the way Zed's runnables do
-- (editors/zed/languages/nomi/tasks.json): `nomi test <file> --line <row>` for the
-- test under the cursor, `nomi test <file>`, `nomi test`, and `nomi run <file>`.
local M = {}

-- Nodes `nomi test --line` selects by their first line, as Zed's runnables.scm
-- tags them.
local test_nodes = {
  test_declaration = true,
  tests_declaration = true,
  attached_test_prompt = true,
}

local last

-- The output window from the previous run. Snacks keys terminals by command
-- line, so every run would otherwise open its own window; closing the old one
-- first keeps all runs in a single split and kills a test still in flight.
local term

local function root(buf)
  return vim.fs.root(buf, { 'nomi.toml', '.git' }) or vim.fn.getcwd()
end

-- The directory tests run from: the nearest nomi.toml, else the repository.
M.root = root

local function exec(cmd, buf)
  last = { cmd = cmd, cwd = root(buf) }

  -- Plain split when snacks.nvim is absent, so the plugin works on its own.
  if not _G.Snacks or not Snacks.terminal then
    vim.cmd('botright 15new')
    vim.fn.jobstart(cmd, { cwd = last.cwd, term = true })
    return
  end

  if term and term:buf_valid() then
    term:close()
  end
  -- interactive = false keeps the buffer around after the run exits and leaves
  -- the window in normal mode, so the output stays readable and scrollable.
  term = Snacks.terminal.open(cmd, {
    cwd = last.cwd,
    interactive = false,
    win = { position = 'bottom', height = 15 },
  })
end

-- The first line (1-based) of the test enclosing the cursor, or nil.
function M.test_line(buf, row, col)
  buf = buf or 0
  if not row then
    local pos = vim.api.nvim_win_get_cursor(0)
    row, col = pos[1] - 1, pos[2]
  end
  local ok, node = pcall(vim.treesitter.get_node, { bufnr = buf, pos = { row, col } })
  if not ok then
    return nil
  end
  while node do
    if test_nodes[node:type()] then
      return node:start() + 1
    end
    node = node:parent()
  end
  return nil
end

function M.nearest()
  local buf = vim.api.nvim_get_current_buf()
  local line = M.test_line(buf)
  if not line then
    vim.notify('nomi: the cursor is not inside a test', vim.log.levels.WARN)
    return
  end
  exec({ 'nomi', 'test', vim.api.nvim_buf_get_name(buf), '--line', tostring(line) }, buf)
end

function M.file()
  local buf = vim.api.nvim_get_current_buf()
  exec({ 'nomi', 'test', vim.api.nvim_buf_get_name(buf) }, buf)
end

function M.all()
  exec({ 'nomi', 'test' }, vim.api.nvim_get_current_buf())
end

function M.run()
  local buf = vim.api.nvim_get_current_buf()
  exec({ 'nomi', 'run', vim.api.nvim_buf_get_name(buf) }, buf)
end

-- The commands of nomi-lsp's code lenses (internal/lsp/code_lens.go), which
-- name a file by URI: run the test whose first line is `line`, or the file.
function M.test_uri_line(uri, line)
  local file = vim.uri_to_fname(uri)
  exec({ 'nomi', 'test', file, '--line', tostring(line) }, vim.uri_to_bufnr(uri))
end

function M.run_uri(uri)
  exec({ 'nomi', 'run', vim.uri_to_fname(uri) }, vim.uri_to_bufnr(uri))
end

function M.last()
  if not last then
    vim.notify('nomi: nothing has run yet', vim.log.levels.WARN)
    return
  end
  exec(last.cmd, 0)
end

return M
