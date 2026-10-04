-- A neotest adapter for Nomi.
--
-- Load it from your neotest spec: `adapters = { require('nomi.neotest') }`.
-- Nothing else in the plugin requires this module or neotest, so a setup
-- without neotest pays nothing for it.
--
-- Tests run through `nomi test --format json`, which prints one JSON record
-- per test. Records are matched to discovered positions by FILE and FIRST
-- LINE, never by name: a test's printed name is display text (an attached
-- test is `impl / buffered //! test lines 58-60`), while its first line is
-- the one `nomi test --line N` selects.
--
-- neotest and nio are required when neotest calls in, not when this module
-- loads, so naming the adapter in a lazy neotest spec does not load neotest.
local function lib()
  return require('neotest.lib')
end

local function nio()
  return require('nio')
end

local adapter = { name = 'nomi' }

-- The nearest nomi.toml, else the repository root, as the terminal runner
-- (nomi.runner) chooses.
local root_patterns = { 'nomi.toml', '.git' }

---@async
function adapter.root(dir)
  return lib().files.match_root_pattern(unpack(root_patterns))(dir)
end

local skip_dirs = {
  ['.git'] = true,
  node_modules = true,
  dist = true,
  build = true,
  target = true,
  parser = true,
  ['.astro'] = true,
  ['.cache'] = true,
  ['.zig-cache'] = true,
}

function adapter.filter_dir(name)
  return not skip_dirs[name]
end

-- Any .nomi file: attached `//!` tests live beside the code they test, so a
-- file without tests just yields no positions.
function adapter.is_test_file(file_path)
  return file_path ~= nil and vim.endswith(file_path, '.nomi')
end

local function unquote(text)
  return (text:gsub('^"', ''):gsub('"$', ''))
end

local function field_text(node, field, source)
  local child = node:field(field)[1]
  return child and vim.treesitter.get_node_text(child, source) or nil
end

-- The owner an attached test is reported under: the declaration its prompt
-- decorates, prefixed by the types and impl blocks around it, as `nomi test`
-- names it (`impl / buffered`).
-- An impl block is reported as `impl`; a type by its name.
local owner_scopes = {
  impl_block = 'impl',
  struct_definition = true,
  enum_definition = true,
  type_definition = true,
  extern_type_definition = true,
}

local function attached_owner(prompt, source)
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

-- The rows (0-based) of a prompt's own `//!` lines. Its tree-sitter node can
-- run on to the start of the declaration below it.
local function prompt_rows(prompt, lines)
  local first, _, last = prompt:range()
  local last_prompt = first
  for row = first, last do
    local text = lines[row + 1]
    if text and text:match('^%s*//!') then
      last_prompt = row
    end
  end
  return first, last_prompt
end

local function positions_in(source, file_path)
  local ok, parser = pcall(vim.treesitter.get_string_parser, source, 'nomi')
  if not ok then
    error('nomi: no tree-sitter parser for Nomi (run `make install-nvim`): ' .. tostring(parser))
  end
  local tree_root = parser:parse()[1]:root()
  local lines = vim.split(source, '\n', { plain = true })
  local positions = {
    {
      type = 'file',
      path = file_path,
      name = vim.fs.basename(file_path),
      range = { 0, 0, #lines, 0 },
    },
  }

  local function walk(node)
    local t = node:type()
    if t == 'test_declaration' or t == 'tests_declaration' then
      table.insert(positions, {
        type = t == 'tests_declaration' and 'namespace' or 'test',
        path = file_path,
        name = unquote(field_text(node, 'name', source) or '?'),
        range = { node:range() },
      })
    elseif t == 'attached_test_prompt' then
      local first, last = prompt_rows(node, lines)
      local label = first == last and ('line %d'):format(first + 1)
        or ('lines %d-%d'):format(first + 1, last + 1)
      local _, first_col = node:range()
      table.insert(positions, {
        type = 'test',
        path = file_path,
        name = ('%s //! test %s'):format(attached_owner(node, source), label),
        range = { first, first_col, last, #(lines[last + 1] or '') },
      })
      return
    end
    for child in node:iter_children() do
      if child:named() then
        walk(child)
      end
    end
  end
  walk(tree_root)
  return positions
end

---@async
function adapter.discover_positions(file_path)
  local source = lib().files.read(file_path)
  -- Tree-sitter may not run in the fast context the read resumes in.
  nio().scheduler()
  local positions = positions_in(source, file_path)
  return lib().positions.parse_tree(positions, {
    nested_tests = false,
    require_namespaces = false,
  })
end

local function project_root(path)
  return vim.fs.root(path, root_patterns) or vim.fs.dirname(path)
end

---@param args neotest.RunArgs
function adapter.build_spec(args)
  local pos = args.tree:data()
  local cmd = { 'nomi', 'test', pos.path }
  -- A `tests` header's first line selects its whole group, as a test's first
  -- line selects the test.
  if pos.type == 'test' or pos.type == 'namespace' then
    vim.list_extend(cmd, { '--line', tostring(pos.range[1] + 1) })
  end
  vim.list_extend(cmd, { '--format', 'json' })
  vim.list_extend(cmd, args.extra_args or {})
  return {
    command = cmd,
    cwd = project_root(pos.path),
    context = { position_id = pos.id },
  }
end

local function realpath(path)
  return (vim.uv or vim.loop).fs_realpath(path) or path
end

-- The records in a run's output. The integrated strategy runs the command in
-- a pty, so stdout and stderr arrive merged with `\r\n` endings; a record is
-- any line holding a JSON object that starts with `{"type":`.
local function parse_records(output)
  local records = {}
  for line in vim.gsplit(output, '\n', { plain = true }) do
    line = line:gsub('\r$', '')
    local at = line:find('{"type":', 1, true)
    if at then
      local ok, rec = pcall(vim.json.decode, line:sub(at))
      if ok and type(rec) == 'table' and rec.type then
        table.insert(records, rec)
      end
    end
  end
  return records
end

local function dedent(text)
  local lines = vim.split(text, '\n', { plain = true })
  local indent
  for _, l in ipairs(lines) do
    if l:match('%S') then
      local lead = #l:match('^%s*')
      indent = indent and math.min(indent, lead) or lead
    end
  end
  if not indent or indent == 0 then
    return text
  end
  for i, l in ipairs(lines) do
    lines[i] = l:sub(indent + 1)
  end
  return table.concat(lines, '\n')
end

local function write_output(text)
  local path = nio().fn.tempname()
  local f = io.open(path, 'w')
  if f then
    f:write(text)
    f:close()
  end
  return path
end

local function record_result(rec)
  local message = rec.message and rec.message ~= '' and dedent(rec.message) or nil
  if rec.status == 'passed' then
    return { status = 'passed', output = write_output('ok ' .. (rec.name or '') .. '\n') }
  end
  if rec.status == 'blocked' then
    return {
      status = 'skipped',
      short = message and ('blocked: ' .. vim.split(message, '\n')[1]) or 'blocked',
      output = write_output('BLOCKED ' .. (rec.name or '') .. '\n' .. (message or '') .. '\n'),
    }
  end
  local err = { message = message or 'failed' }
  if type(rec.error_line) == 'number' and rec.error_line > 0 then
    err.line = rec.error_line - 1
  end
  return {
    status = 'failed',
    short = message and vim.split(message, '\n')[1] or 'failed',
    errors = { err },
    output = write_output('FAIL ' .. (rec.name or '') .. '\n' .. (message or '') .. '\n'),
  }
end

local rank = { passed = 1, skipped = 0, failed = 2 }

---@async
---@param spec neotest.RunSpec
---@param result neotest.StrategyResult
---@param tree neotest.Tree
function adapter.results(spec, result, tree)
  local ok, output = pcall(lib().files.read, result.output)
  output = ok and output or ''
  local records = parse_records(output)

  local results = {}

  local has_summary = false
  for _, rec in ipairs(records) do
    if rec.type == 'summary' then
      has_summary = true
    end
  end
  if not has_summary then
    -- A `nomi` that predates `--format json` prints its usage instead, and a
    -- crash prints whatever it printed. Either way there are no records.
    local first = vim.trim((output:gsub('\r', '')))
    first = first ~= '' and vim.split(first, '\n', { plain = true })[1] or '(no output)'
    local message = ('nomi test printed no JSON results (exit %d); this nomi may predate '
      .. '`nomi test --format json`, so update it. Output began: %s'):format(result.code or -1, first)
    for _, node in tree:iter_nodes() do
      local pos = node:data()
      if pos.type ~= 'dir' then
        results[pos.id] = {
          status = 'failed',
          short = message,
          errors = { { message = message } },
          output = result.output,
        }
      end
    end
    return results
  end

  -- (file, first line) -> test position id, for every test in the run.
  local by_line = {}
  for _, node in tree:iter_nodes() do
    local pos = node:data()
    if pos.type == 'test' then
      by_line[realpath(pos.path) .. ':' .. (pos.range[1] + 1)] = pos.id
    end
  end

  local file_failures = {}
  for _, rec in ipairs(records) do
    if rec.type == 'test' and rec.file and rec.line then
      local id = by_line[realpath(rec.file) .. ':' .. rec.line]
      if id then
        results[id] = record_result(rec)
      end
    elseif rec.type == 'file' and rec.file then
      file_failures[realpath(rec.file)] = rec
    end
  end

  -- A file that failed to load never ran its tests; each of them fails with
  -- the load error. Anything else without a record was never reported.
  for _, node in tree:iter_nodes() do
    local pos = node:data()
    if pos.type == 'test' and not results[pos.id] then
      local failure = file_failures[realpath(pos.path)]
      if failure then
        results[pos.id] = record_result(failure)
      else
        results[pos.id] = {
          status = 'failed',
          short = 'no result reported',
          errors = { { message = 'nomi test reported no result for this test' } },
          output = result.output,
        }
      end
    end
  end

  -- Namespaces, files and directories take their worst child's status.
  local function aggregate(node)
    local pos = node:data()
    if pos.type == 'test' then
      return results[pos.id] and results[pos.id].status
    end
    local status
    for _, child in ipairs(node:children()) do
      local s = aggregate(child)
      if s and (not status or rank[s] > rank[status]) then
        status = s
      end
    end
    local failure = pos.type == 'file' and file_failures[realpath(pos.path)]
    if failure then
      results[pos.id] = record_result(failure)
      return 'failed'
    end
    if status then
      results[pos.id] = { status = status, output = result.output }
    end
    return status
  end
  aggregate(tree)
  return results
end

return adapter
