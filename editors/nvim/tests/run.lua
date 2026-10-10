-- Headless checks of the plugin's test keys: `make test-nvim`, which builds
-- the parser first and runs
--   nvim --headless -u NONE -i NONE -l editors/nvim/tests/run.lua
-- neotest, lazy.nvim and snacks.nvim are stubs; nothing here runs `nomi`.
local plugin = vim.fs.dirname(vim.fs.dirname(vim.fs.normalize(debug.getinfo(1, 'S').source:sub(2))))
vim.opt.rtp:prepend(plugin)
vim.g.mapleader = ' '
vim.treesitter.language.register('nomi', 'nomi')
if not pcall(vim.treesitter.language.add, 'nomi') then
  io.stderr:write('no nomi parser under ' .. plugin .. '/parser; run `make build-nvim-parser`\n')
  os.exit(1)
end

local failures, current = 0, ''

local function check(cond, msg)
  if not cond then
    failures = failures + 1
    io.stderr:write(('FAIL %s: %s\n'):format(current, msg))
  end
end

local function eq(got, want, what)
  check(vim.deep_equal(got, want), ('%s = %s, want %s'):format(what, vim.inspect(got), vim.inspect(want)))
end

-- Every command the runner started, through a Snacks stub.
local ran = {}
_G.Snacks = {
  terminal = {
    open = function(cmd)
      table.insert(ran, cmd)
      return {
        buf_valid = function()
          return false
        end,
      }
    end,
  },
}

local notes = {}
vim.notify = function(msg)
  table.insert(notes, msg)
end

local function reset_modules()
  for name in pairs(package.loaded) do
    if name == 'neotest' or name:match('^neotest%.') or name:match('^nomi') or name:match('^lazy') then
      package.loaded[name] = nil
    end
  end
  package.preload.neotest = nil
  package.preload['neotest.config'] = nil
end

local dir = vim.fn.tempname()
vim.fn.mkdir(dir, 'p')
dir = vim.uv.fs_realpath(dir)
vim.fn.writefile({}, dir .. '/nomi.toml')

local function write(name, lines)
  local path = dir .. '/' .. name
  vim.fn.writefile(lines, path)
  return path
end

local function last_line()
  local cmd = ran[#ran]
  return cmd and tonumber(cmd[5])
end

local function test(name, fn)
  current = name
  ran, notes = {}, {}
  reset_modules()
  vim.cmd('silent! %bwipeout!')
  local ok, err = pcall(fn)
  check(ok, tostring(err))
end

local source = {
  'import std/io', --  1
  '', --  2
  '//! assert double(2) == 4', --  3
  'fn double(x: Int): Int {', --  4
  '    x * 2', --  5
  '}', --  6
  '', --  7
  'test "a" {', --  8
  '    assert True', --  9
  '}', -- 10
  '', -- 11
  'tests "g" {', -- 12
  '    test "b" {', -- 13
  '        assert True', -- 14
  '    }', -- 15
  '}', -- 16
}

local function open(name)
  local path = write(name, source)
  vim.cmd('edit ' .. vim.fn.fnameescape(path))
  vim.bo.filetype = 'nomi'
  return path, vim.api.nvim_get_current_buf()
end

local function insert_above(buf, n)
  local lines = {}
  for i = 1, n do
    lines[i] = '// added ' .. i
  end
  vim.api.nvim_buf_set_lines(buf, 1, 1, false, lines)
end

test('rerun last finds a moved test by name', function()
  local runner = require('nomi.runner')
  local path, buf = open('moved_test.nomi')
  vim.api.nvim_win_set_cursor(0, { 14, 8 })
  runner.nearest()
  eq(ran[1], { 'nomi', 'test', path, '--line', '13' }, 'nearest command')
  insert_above(buf, 3)
  runner.last()
  eq(last_line(), 16, 'rerun line after three lines were added above')
  runner.last()
  eq(last_line(), 16, 'second rerun line')
end)

test('rerun last finds an attached test after its prompt is edited', function()
  local runner = require('nomi.runner')
  local _, buf = open('attached_test.nomi')
  vim.api.nvim_win_set_cursor(0, { 3, 5 })
  runner.nearest()
  eq(last_line(), 3, 'nearest line')
  insert_above(buf, 2)
  vim.api.nvim_buf_set_lines(buf, 4, 5, false, { '//! assert double(2) == 5' })
  runner.last()
  eq(last_line(), 5, 'rerun line of the edited, moved prompt')
end)

test('rerun last of a group header reruns the group', function()
  local runner = require('nomi.runner')
  local _, buf = open('group_test.nomi')
  vim.api.nvim_win_set_cursor(0, { 12, 0 })
  runner.nearest()
  eq(last_line(), 12, 'nearest line')
  insert_above(buf, 1)
  runner.last()
  eq(last_line(), 13, 'rerun line')
end)

test('rerun last of a deleted test runs nothing', function()
  local runner = require('nomi.runner')
  local _, buf = open('deleted_test.nomi')
  vim.api.nvim_win_set_cursor(0, { 9, 4 })
  runner.nearest()
  vim.api.nvim_buf_set_lines(buf, 7, 11, false, {})
  runner.last()
  eq(#ran, 1, 'commands run')
  check(notes[#notes] and notes[#notes]:find('"a"', 1, true) and notes[#notes]:find('no longer in', 1, true),
    'notification ' .. vim.inspect(notes))
end)

test('rerun last reads a file with no loaded buffer from disk', function()
  local runner = require('nomi.runner')
  local path = write('disk_test.nomi', source)
  runner.test_uri_line(vim.uri_from_fname(path), 8)
  eq(ran[1], { 'nomi', 'test', path, '--line', '8' }, 'code lens command')
  local moved = vim.list_extend({ '// one', '// two' }, source)
  vim.fn.writefile(moved, path)
  runner.last()
  eq(last_line(), 10, 'rerun line from disk')
end)

test('rerun last of a file run repeats it', function()
  local runner = require('nomi.runner')
  local path = open('file_test.nomi')
  runner.file()
  runner.last()
  eq(ran[2], { 'nomi', 'test', path }, 'rerun command')
end)

-- neotest detection. The stub `neotest` module stands for a lazy plugin:
-- requiring it runs the spec's `opts`, which may name the adapter.
local function stub_neotest(names_adapter)
  local calls = {}
  package.preload.neotest = function()
    local adapters = {}
    if names_adapter then
      table.insert(adapters, require('nomi.neotest'))
    end
    package.loaded['neotest.config'] = { adapters = adapters }
    local function record(name)
      return function(...)
        table.insert(calls, { name, ... })
      end
    end
    return { run = { run = record('run'), run_last = record('run_last') } }
  end
  return calls
end

local function press(lhs)
  local map = vim.fn.maparg(lhs, 'n', false, true)
  check(map.buffer == 1 and map.callback, lhs .. ' is not a buffer mapping with a callback')
  if map.callback then
    map.callback()
  end
end

local function open_with_ftplugin(name)
  local path, buf = open(name)
  vim.b.did_ftplugin = nil
  vim.cmd('runtime! ftplugin/nomi.lua')
  return path, buf
end

test('a lazy neotest that names the adapter gets the keys', function()
  package.loaded['lazy.core.config'] = { plugins = { neotest = {} } }
  local calls = stub_neotest(true)
  open_with_ftplugin('lazy_test.nomi')
  check(package.loaded.neotest == nil, 'opening a file loaded neotest')
  press('<leader>tl')
  eq(calls[1], { 'run_last' }, 'neotest call')
  eq(#ran, 0, 'terminal commands')
end)

test('a lazy neotest without the adapter leaves the keys to the runner', function()
  package.loaded['lazy.core.config'] = { plugins = { neotest = {} } }
  local calls = stub_neotest(false)
  open_with_ftplugin('no_adapter_test.nomi')
  vim.api.nvim_win_set_cursor(0, { 9, 4 })
  press('<leader>tr')
  eq(#calls, 0, 'neotest calls')
  eq(last_line(), 8, 'runner line')
end)

test('without neotest installed it is never loaded', function()
  package.loaded['lazy.core.config'] = { plugins = {} }
  local required = false
  package.preload.neotest = function()
    required = true
    return {}
  end
  open_with_ftplugin('none_test.nomi')
  vim.api.nvim_win_set_cursor(0, { 9, 4 })
  press('<leader>tr')
  check(not required, 'neotest was required')
  eq(last_line(), 8, 'runner line')
end)

-- The neotest adapter's ids: an attached test's id must survive lines added
-- above it, which its displayed name (with line numbers) does not.
test('neotest ids of attached tests ignore their lines', function()
  package.loaded['nio'] = { scheduler = function() end }
  package.loaded['neotest.lib'] = {
    files = {
      read = function(p)
        return table.concat(vim.fn.readfile(p), '\n')
      end,
    },
    positions = {
      parse_tree = function(positions, opts)
        local ids = {}
        for _, pos in ipairs(positions) do
          if pos.type == 'test' then
            table.insert(ids, opts.position_id(pos, {}))
          end
        end
        return ids
      end,
    },
  }
  local adapter = require('nomi.neotest')
  local path = write('ids_test.nomi', source)
  local before = adapter.discover_positions(path)
  vim.fn.writefile(vim.list_extend({ '// one' }, source), path)
  local after = adapter.discover_positions(path)
  eq(after, before, 'ids after a line is added')
  eq(before[1], path .. '::double //! test #1', 'attached id')
  eq(before[2], path .. '::a', 'test id')
end)

-- A neotest tree of one file holding two tests, with just the methods the
-- adapter's `results` calls.
local function stub_tree(path)
  local function node(data, children)
    local n = { _data = data, _children = children or {} }
    function n:data()
      return self._data
    end
    function n:children()
      return self._children
    end
    function n:iter_nodes()
      local all = {}
      local function walk(x)
        table.insert(all, x)
        for _, c in ipairs(x._children) do
          walk(c)
        end
      end
      walk(self)
      local i = 0
      return function()
        i = i + 1
        if all[i] then
          return i, all[i]
        end
      end
    end
    return n
  end
  local ok = node({ type = 'test', id = path .. '::ok', path = path, range = { 0, 0, 2, 1 } })
  local bad = node({ type = 'test', id = path .. '::bad', path = path, range = { 4, 0, 7, 1 } })
  return node({ type = 'file', id = path, path = path, range = { 0, 0, 8, 0 } }, { ok, bad })
end

local function stub_neotest_lib()
  package.loaded['nio'] = {
    scheduler = function() end,
    fn = { tempname = vim.fn.tempname },
  }
  package.loaded['neotest.lib'] = {
    files = {
      read = function(p)
        return table.concat(vim.fn.readfile(p, 'b'), '\n')
      end,
    },
  }
end

local function run_results(path, lines)
  local out = vim.fn.tempname()
  vim.fn.writefile(lines, out, 'b')
  local adapter = require('nomi.neotest')
  return adapter.results({}, { output = out, code = 1 }, stub_tree(path)), out
end

local function read_output(result)
  return table.concat(vim.fn.readfile(result.output, 'b'), '\n')
end

local esc = '\27'

-- A test record as `nomi test --format json` prints it: `type` first, which
-- is what the adapter looks for on each line.
local function record(fields)
  return '{"type":"test",' .. vim.json.encode(fields):sub(2)
end

test('neotest runs nomi with colour forced', function()
  stub_neotest_lib()
  local path = write('spec_test.nomi', source)
  local tree = stub_tree(path)
  local spec = require('nomi.neotest').build_spec({ tree = tree })
  eq(spec.env, { NOMI_COLOR = 'always' }, 'env')
  eq(spec.command, { 'nomi', 'test', path, '--format', 'json' }, 'command')
end)

test('neotest output shows the coloured report, diagnostics the plain message', function()
  stub_neotest_lib()
  local path = write('report_test.nomi', source)
  local red_fail = esc .. '[1m' .. esc .. '[31mFAIL' .. esc .. '[0m a :: bad\n  ' .. esc .. '[2mline 7: assertion failed' .. esc .. '[0m\n'
  local records = {
    record({
      file = path, line = 1, end_line = 3, name = 'a :: ok', status = 'passed',
      message = '', report = esc .. '[32mok' .. esc .. '[0m a :: ok\n',
    }),
    record({
      file = path, line = 5, end_line = 8, name = 'a :: bad', status = 'failed',
      message = '  line 7: assertion failed', error_line = 7, report = red_fail,
    }),
    '{"type":"summary","passed":1,"failed":1,"blocked":0}',
  }
  local results = run_results(path, records)
  local ok, bad = results[path .. '::ok'], results[path .. '::bad']
  eq(ok.status, 'passed', 'ok status')
  eq(read_output(ok), esc .. '[32mok' .. esc .. '[0m a :: ok\n', 'ok output')
  eq(bad.status, 'failed', 'bad status')
  eq(read_output(bad), red_fail, 'bad output')
  eq(bad.short, 'line 7: assertion failed', 'short')
  eq(bad.errors, { { message = 'line 7: assertion failed', line = 6 } }, 'errors')
  eq(results[path].status, 'failed', 'file status')
end)

test('neotest output falls back to plain text without a report', function()
  stub_neotest_lib()
  local path = write('plain_test.nomi', source)
  local records = {
    record({ file = path, line = 1, name = 'a :: ok', status = 'passed', message = '' }),
    record({
      file = path, line = 5, name = 'a :: bad', status = 'failed',
      message = '  line 7: assertion failed', error_line = 7,
    }),
    '{"type":"summary","passed":1,"failed":1,"blocked":0}',
  }
  local results = run_results(path, records)
  eq(read_output(results[path .. '::ok']), 'ok a :: ok\n', 'ok output')
  eq(read_output(results[path .. '::bad']), 'FAIL a :: bad\nline 7: assertion failed\n', 'bad output')
end)

-- A test's own output (`dbg`, `io.print`) is in its record's `report`, under
-- the ok line, so a passing test's output file holds its `dbg` line.
test('neotest output of a passing test holds its dbg line', function()
  stub_neotest_lib()
  local path = write('dbg_test.nomi', source)
  local dbg = '  ' .. esc .. '[36mdbg' .. esc .. '[0m ' .. esc .. '[2mline 2:' .. esc .. '[0m '
    .. esc .. '[33m5' .. esc .. '[0m = ' .. esc .. '[33m5' .. esc .. '[0m\n'
  local report = esc .. '[32mok' .. esc .. '[0m a :: ok\n' .. dbg .. '  printed\n'
  local records = {
    record({
      file = path, line = 1, end_line = 3, name = 'a :: ok', status = 'passed',
      message = '', output = 'dbg line 2: 5 = 5\nprinted\n', report = report,
    }),
    '{"type":"summary","passed":1,"failed":0,"blocked":0}',
  }
  local results = run_results(path, records)
  local ok = results[path .. '::ok']
  eq(ok.status, 'passed', 'ok status')
  eq(read_output(ok), report, 'ok output')
end)

test('neotest with no JSON results reports plain text', function()
  stub_neotest_lib()
  local path = write('nojson_test.nomi', source)
  local results = run_results(path, { esc .. '[31mpanic: boom' .. esc .. '[0m' })
  local r = results[path .. '::bad']
  eq(r.status, 'failed', 'status')
  check(r.short:find('Output began: panic: boom', 1, true) ~= nil, 'short = ' .. r.short)
  check(not r.short:find(esc, 1, true), 'short carries escapes: ' .. vim.inspect(r.short))
end)

vim.fn.delete(dir, 'rf')
if failures > 0 then
  io.stderr:write(failures .. ' failed\n')
  os.exit(1)
end
io.stdout:write('nvim plugin checks passed\n')
os.exit(0)
