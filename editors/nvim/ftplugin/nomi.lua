if vim.b.did_ftplugin then
  return
end
vim.b.did_ftplugin = 1

local bo = vim.bo
bo.commentstring = '// %s'
bo.comments = ':///,://'
bo.expandtab = true
bo.shiftwidth = 4
bo.tabstop = 4
bo.softtabstop = 4

local undo = { 'setlocal commentstring< comments< expandtab< shiftwidth< tabstop< softtabstop<' }

-- Highlighting. Needs the `nomi` parser (parser/nomi.so on 'runtimepath');
-- `make install-nvim` builds it.
local ok, err = pcall(vim.treesitter.start, 0, 'nomi')
if ok then
  local wo = vim.wo[0][0]
  wo.foldmethod = 'expr'
  wo.foldexpr = 'v:lua.vim.treesitter.foldexpr()'
  wo.foldlevel = 99
  table.insert(undo, 'setlocal foldmethod< foldexpr< foldlevel<')
  -- `call`, not `lua`: `:lua` takes the rest of the line, `|` included, so
  -- every undo command joined after it would be read as Lua.
  table.insert(undo, 'call v:lua.vim.treesitter.stop()')

  -- Draw `//!` attached tests dimmed, except the one under the cursor.
  -- `vim.g.nomi_dim_attached_tests = false` and
  -- `vim.g.nomi_reveal_attached_test = false` turn the two parts off.
  table.insert(undo, require('nomi.attached_tests').attach(vim.api.nvim_get_current_buf()))

  -- Tree-sitter indentation comes from nvim-treesitter, not Neovim itself.
  -- Use it when that plugin is present; otherwise keep 'autoindent'.
  local has_nts, nts = pcall(require, 'nvim-treesitter')
  if has_nts and type(nts.indentexpr) == 'function' then
    bo.indentexpr = "v:lua.require'nvim-treesitter'.indentexpr()"
    table.insert(undo, 'setlocal indentexpr<')
  elseif vim.fn.exists('*nvim_treesitter#indent') == 1 then
    bo.indentexpr = 'nvim_treesitter#indent()'
    table.insert(undo, 'setlocal indentexpr<')
  end
elseif not vim.g.nomi_parser_warned then
  vim.g.nomi_parser_warned = true
  vim.notify(
    'nomi: no tree-sitter parser for Nomi, so no highlighting (run `make install-nvim`): ' .. tostring(err),
    vim.log.levels.WARN
  )
end

-- Test and run keys, matching LazyVim's test-extra layout. Set
-- `vim.g.nomi_keymaps = false` to skip them; the :Nomi* commands stay.
--
-- The test keys go to neotest when the nomi adapter is configured, and to the
-- terminal runner otherwise. The check runs at keypress: the adapter module
-- being loaded means a neotest spec named it, and requiring neotest then
-- loads it if it is lazy, so nothing here loads neotest for a setup that
-- does not use it.
if vim.g.nomi_keymaps ~= false then
  local runner = require('nomi.runner')
  local function neotest()
    if not package.loaded['nomi.neotest'] then
      return nil
    end
    local ok, mod = pcall(require, 'neotest')
    return ok and mod or nil
  end
  local function test_key(with_neotest, fallback)
    return function()
      local nt = neotest()
      if nt then
        with_neotest(nt)
      else
        fallback()
      end
    end
  end
  local function map(lhs, fn, desc)
    vim.keymap.set('n', lhs, fn, { buffer = 0, desc = desc })
    table.insert(undo, 'silent! nunmap <buffer> ' .. lhs)
  end
  map('<leader>tr', test_key(function(nt)
    nt.run.run()
  end, runner.nearest), 'Nomi: run test under cursor')
  map('<leader>tt', test_key(function(nt)
    nt.run.run(vim.fn.expand('%:p'))
  end, runner.file), 'Nomi: run tests in file')
  map('<leader>tT', test_key(function(nt)
    nt.run.run(runner.root(0))
  end, runner.all), 'Nomi: run all tests')
  map('<leader>tl', test_key(function(nt)
    nt.run.run_last()
  end, runner.last), 'Nomi: rerun last')
  map('<leader>cx', runner.run, 'Nomi: run file')
end

vim.b.undo_ftplugin = table.concat(undo, ' | ')
