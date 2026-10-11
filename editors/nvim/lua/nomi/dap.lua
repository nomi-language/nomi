-- Debugging Nomi with nvim-dap: the `nomi` adapter, which runs `nomi dap`,
-- and its configurations.
--
-- Nothing requires nvim-dap at load time. plugin/nomi.lua registers the
-- adapter when nvim-dap is already loaded, or when lazy.nvim loads it later
-- (its `User LazyLoad` event), and the debug commands require it at the
-- keypress, so a setup without nvim-dap pays nothing.
local M = {}

-- The launch configuration that debugs `main` of file.
function M.main_config(file, cwd)
  return {
    type = 'nomi',
    request = 'launch',
    name = 'Debug ' .. vim.fs.basename(file),
    mode = 'run',
    program = file,
    cwd = cwd,
  }
end

-- The launch configuration that debugs the tests of file: the one test (or
-- `tests` group, or `//!` attached test) starting on line, else them all.
function M.test_config(file, line, cwd)
  return {
    type = 'nomi',
    request = 'launch',
    name = line and ('Debug test at %s:%d'):format(vim.fs.basename(file), line)
      or ('Debug tests in ' .. vim.fs.basename(file)),
    mode = 'test',
    program = file,
    line = line,
    cwd = cwd,
  }
end

-- The configurations `:DapNew` and `dap.continue()` offer in a Nomi buffer.
-- Function values are evaluated when the session starts.
function M.configurations()
  local function cwd()
    return require('nomi.runner').root(0)
  end
  return {
    { type = 'nomi', request = 'launch', name = 'Nomi: debug file', mode = 'run', program = '${file}', cwd = cwd },
    {
      type = 'nomi',
      request = 'launch',
      name = 'Nomi: debug test under cursor',
      mode = 'test',
      program = '${file}',
      line = function()
        return require('nomi.runner').test_line(0)
      end,
      cwd = cwd,
    },
    { type = 'nomi', request = 'launch', name = 'Nomi: debug tests in file', mode = 'test', program = '${file}', cwd = cwd },
  }
end

-- Registers the adapter and configurations with dap, leaving any the user
-- defined alone, and sends the function breakpoints to each Nomi session as
-- it starts, before nvim-dap sends the line breakpoints and finishes
-- configuring it.
function M.register(dap)
  if not dap.adapters.nomi then
    dap.adapters.nomi = { type = 'executable', command = 'nomi', args = { 'dap' } }
  end
  if not dap.configurations.nomi then
    dap.configurations.nomi = M.configurations()
  end
  if dap.listeners and dap.listeners.before then
    dap.listeners.before.event_initialized = dap.listeners.before.event_initialized or {}
    dap.listeners.before.event_initialized['nomi.function_breakpoints'] = function(session)
      if session and session.config and session.config.type == 'nomi' then
        M.send_function_breakpoints(session)
      end
    end
  end
end

-- nvim-dap, loaded and registered, or nil (with a notification) when it is
-- not installed.
function M.dap()
  local ok, dap = pcall(require, 'dap')
  if not ok then
    vim.notify('nomi: debugging needs nvim-dap (https://github.com/mfussenegger/nvim-dap)', vim.log.levels.ERROR)
    return nil
  end
  M.register(dap)
  return dap
end

local function run(config)
  local dap = M.dap()
  if dap then
    dap.run(config)
  end
end

-- Debug the test under the cursor, or with none there, the file's `main`.
function M.nearest()
  local runner = require('nomi.runner')
  local buf = vim.api.nvim_get_current_buf()
  local file = vim.api.nvim_buf_get_name(buf)
  local line = runner.test_line(buf)
  if line then
    run(M.test_config(file, line, runner.root(buf)))
  else
    run(M.main_config(file, runner.root(buf)))
  end
end

function M.file()
  local runner = require('nomi.runner')
  local buf = vim.api.nvim_get_current_buf()
  run(M.test_config(vim.api.nvim_buf_get_name(buf), nil, runner.root(buf)))
end

function M.run()
  local runner = require('nomi.runner')
  local buf = vim.api.nvim_get_current_buf()
  run(M.main_config(vim.api.nvim_buf_get_name(buf), runner.root(buf)))
end

-- Step back to the previous statement, or reverse continue to the latest
-- earlier breakpoint stop (or the program's start), in the session that is
-- paused: `nomi dap` replays the program from its start with the effects it
-- recorded. A program that starts a task cannot go back, and the session
-- says why.
function M.step_back()
  local dap = M.dap()
  if dap then
    dap.step_back()
  end
end

function M.reverse_continue()
  local dap = M.dap()
  if dap then
    dap.reverse_continue()
  end
end

-- Step into one of the calls the current statement makes: nvim-dap asks
-- which (`nomi dap` lists them, lambdas passed to Iter adapters included).
function M.step_into_target()
  local dap = M.dap()
  if dap then
    dap.step_into({ askForTargets = true })
  end
end

-- Function breakpoints. nvim-dap keeps line breakpoints only, so the plugin
-- keeps the function names and sends them to every Nomi session, the one
-- running now included. A name is spelled as the debugger's call stack
-- names functions: `transact`, `Account.deposit`, `Card.to_string`,
-- `Display.to_string` (every impl), `tiny_bank.transact`.
M.function_breakpoints = {}

-- Toggles the function breakpoint on name; with no name, lists them.
function M.toggle_function_breakpoint(name)
  name = vim.trim(name or '')
  if name == '' then
    if #M.function_breakpoints == 0 then
      vim.notify('nomi: no function breakpoints; add one with :NomiDebug break <function>')
    else
      vim.notify('nomi: function breakpoints: ' .. table.concat(M.function_breakpoints, ', '))
    end
    return
  end
  local removed = false
  for i, n in ipairs(M.function_breakpoints) do
    if n == name then
      table.remove(M.function_breakpoints, i)
      removed = true
      break
    end
  end
  if not removed then
    table.insert(M.function_breakpoints, name)
  end
  vim.notify(('nomi: %s the function breakpoint on %s'):format(removed and 'removed' or 'added', name))
  local ok, dap = pcall(require, 'dap')
  if ok and dap.session then
    local session = dap.session()
    if session and session.config and session.config.type == 'nomi' then
      M.send_function_breakpoints(session)
    end
  end
end

-- Sends the function breakpoints to session, saying which name nothing in
-- the program matches.
function M.send_function_breakpoints(session)
  if not (session.capabilities and session.capabilities.supportsFunctionBreakpoints) then
    return
  end
  local bps = {}
  for _, name in ipairs(M.function_breakpoints) do
    table.insert(bps, { name = name })
  end
  session:request('setFunctionBreakpoints', { breakpoints = bps }, function(err, response)
    if err then
      vim.notify('nomi: setting function breakpoints failed: ' .. tostring(err.message or err), vim.log.levels.ERROR)
      return
    end
    for i, b in ipairs(response and response.breakpoints or {}) do
      if not b.verified then
        vim.notify(('nomi: function breakpoint %s: %s'):format(bps[i] and bps[i].name or '?', b.message or 'not set'),
          vim.log.levels.WARN)
      end
    end
  end)
end

-- The commands of nomi-lsp's Debug code lenses (internal/lsp/code_lens.go).
function M.test_uri_line(uri, line)
  local file = vim.uri_to_fname(uri)
  run(M.test_config(file, tonumber(line), require('nomi.runner').root(vim.uri_to_bufnr(uri))))
end

function M.main_uri(uri)
  local file = vim.uri_to_fname(uri)
  run(M.main_config(file, require('nomi.runner').root(vim.uri_to_bufnr(uri))))
end

return M
