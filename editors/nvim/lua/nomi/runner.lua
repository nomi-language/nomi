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

-- The previous run: its command and directory, and for one test, which test
-- it was (see `tests_in`), so a rerun finds that test's current line.
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

local function field_text(node, field, source)
  local child = node:field(field)[1]
  return child and vim.treesitter.get_node_text(child, source) or nil
end

-- The owner an attached test is reported under: the declaration its prompt
-- decorates, prefixed by the types and impl blocks around it, as `nomi test`
-- names it (`impl / buffered`). An impl block is reported as `impl`; a type
-- by its name.
local owner_scopes = {
  impl_block = 'impl',
  struct_definition = true,
  enum_definition = true,
  type_definition = true,
  extern_type_definition = true,
}

function M.attached_owner(prompt, source)
  -- The first following sibling with a name: other prompts and doc
  -- comments can sit between a prompt and its declaration.
  local owner
  local sibling = prompt:next_named_sibling()
  while sibling and not owner do
    owner = field_text(sibling, 'name', source)
    sibling = sibling:next_named_sibling()
  end
  local parts = { owner or '?' }
  local node = prompt:parent()
  while node do
    local scope = owner_scopes[node:type()]
    if type(scope) == 'string' then
      table.insert(parts, 1, scope)
    elseif scope then
      table.insert(parts, 1, field_text(node, 'name', source) or node:type())
    end
    node = node:parent()
  end
  return table.concat(parts, ' / ')
end

-- The syntax tree of file: its loaded buffer's, else the file on disk's.
local function tree_of(file)
  for _, buf in ipairs(vim.api.nvim_list_bufs()) do
    if vim.api.nvim_buf_is_loaded(buf) and vim.api.nvim_buf_get_name(buf) == file then
      local ok, parser = pcall(vim.treesitter.get_parser, buf, 'nomi')
      if ok and parser then
        return parser:parse()[1]:root(), buf
      end
      return nil
    end
  end
  local fd = io.open(file, 'r')
  if not fd then
    return nil
  end
  local text = fd:read('*a')
  fd:close()
  local ok, parser = pcall(vim.treesitter.get_string_parser, text, 'nomi')
  if not ok then
    return nil
  end
  return parser:parse()[1]:root(), text
end

-- Every test in file, in order, as { key, label, line }. The key names the
-- test independently of where it sits: a `test` or `tests` group by its
-- group and own names, an attached test by its owner and its place among
-- that owner's prompts (its text is what an edit changes, so it is not part
-- of the key). A repeated key gets an ordinal.
local function tests_in(file)
  local tree, source = tree_of(file)
  if not tree then
    return nil
  end
  local out, seen = {}, {}
  local function add(node, kind, label)
    local key = kind .. '\n' .. label
    seen[key] = (seen[key] or 0) + 1
    table.insert(out, { key = key .. '\n' .. seen[key], label = label, line = node:start() + 1 })
  end
  local function walk(node, groups)
    local t = node:type()
    if t == 'test_declaration' or t == 'tests_declaration' then
      local names = vim.list_extend(vim.list_slice(groups), { field_text(node, 'name', source) or '?' })
      add(node, t, table.concat(names, ' / '))
      if t == 'tests_declaration' then
        groups = names
      end
    elseif t == 'attached_test_prompt' then
      add(node, t, M.attached_owner(node, source) .. ' //! test')
      return
    end
    for child in node:iter_children() do
      if child:named() then
        walk(child, groups)
      end
    end
  end
  walk(tree, {})
  return out
end

-- Which test in file starts on line (1-based), as `tests_in` names it.
local function test_at(file, line)
  for _, t in ipairs(tests_in(file) or {}) do
    if t.line == line then
      return { file = file, key = t.key, label = t.label }
    end
  end
  return nil
end

-- The current first line of a test `test_at` named, or nil when it is gone.
local function line_of(test)
  for _, t in ipairs(tests_in(test.file) or {}) do
    if t.key == test.key then
      return t.line
    end
  end
  return nil
end

local function exec(cmd, buf, test, cwd)
  last = { cmd = cmd, cwd = cwd or root(buf), test = test }

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

local function test_cmd(file, line)
  return { 'nomi', 'test', file, '--line', tostring(line) }
end

-- The first line (1-based) of the test enclosing the cursor, or nil.
function M.test_line(buf, row, col)
  buf = buf or 0
  if not row then
    local pos = vim.api.nvim_win_get_cursor(0)
    row, col = pos[1] - 1, pos[2]
  end
  -- get_node reads the last parse, which can predate an edit.
  local has_parser, parser = pcall(vim.treesitter.get_parser, buf, 'nomi')
  if has_parser and parser then
    parser:parse()
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
  local file = vim.api.nvim_buf_get_name(buf)
  exec(test_cmd(file, line), buf, test_at(file, line))
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
  exec(test_cmd(file, line), vim.uri_to_bufnr(uri), test_at(file, tonumber(line)))
end

function M.run_uri(uri)
  exec({ 'nomi', 'run', vim.uri_to_fname(uri) }, vim.uri_to_bufnr(uri))
end

-- Whether neotest is installed, found without loading it: lazy.nvim's
-- registry holds a plugin it has not loaded yet, and a plugin on
-- 'runtimepath' has its module there.
local function neotest_installed()
  if package.loaded.neotest then
    return true
  end
  local lazy = package.loaded['lazy.core.config']
  if lazy and type(lazy.plugins) == 'table' and lazy.plugins.neotest then
    return true
  end
  return #vim.api.nvim_get_runtime_file('lua/neotest/init.lua', false) > 0
end

-- neotest, when it is installed and the nomi adapter is among its adapters,
-- else nil. Requiring an installed neotest loads it if it is lazy, and runs
-- its setup, which is where a spec names the adapter.
function M.neotest()
  if not neotest_installed() then
    return nil
  end
  local ok, neotest = pcall(require, 'neotest')
  if not ok then
    return nil
  end
  local has_config, config = pcall(require, 'neotest.config')
  if has_config and type(config.adapters) == 'table' then
    for _, adapter in ipairs(config.adapters) do
      if type(adapter) == 'table' and adapter.name == 'nomi' then
        return neotest
      end
    end
  end
  -- A setup that named the adapter somewhere neotest.config does not list
  -- it, such as a project's own adapters.
  return package.loaded['nomi.neotest'] and neotest or nil
end

-- Rerun the previous command. A single test reruns at the line it starts on
-- now, since edits above it move it; one that no longer exists is reported
-- and not run.
function M.last()
  if not last then
    vim.notify('nomi: nothing has run yet', vim.log.levels.WARN)
    return
  end
  local cmd, test = last.cmd, last.test
  if test then
    local line = line_of(test)
    if not line then
      vim.notify(
        ('nomi: the last test run, %s, is no longer in %s'):format(test.label, vim.fn.fnamemodify(test.file, ':~:.')),
        vim.log.levels.WARN
      )
      return
    end
    cmd = test_cmd(test.file, line)
  end
  exec(cmd, 0, test, last.cwd)
end

return M
