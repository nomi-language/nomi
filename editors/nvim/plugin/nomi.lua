if vim.g.loaded_nomi then
  return
end
vim.g.loaded_nomi = true

-- Also in ftdetect/, which a lazy-loading plugin manager may skip.
require('nomi.filetype').register()
vim.treesitter.language.register('nomi', 'nomi')

-- Opt out with `vim.g.nomi_lsp = false` before this plugin loads.
if vim.g.nomi_lsp ~= false then
  vim.lsp.enable('nomi')
end

-- The commands nomi-lsp's code lenses carry, which `vim.lsp.codelens.run()`
-- executes: Run test / Run tests / Run attached test, and Run on `fn main`.
vim.lsp.commands['nomi.runTest'] = function(command)
  local args = command.arguments or {}
  require('nomi.runner').test_uri_line(args[1], args[2])
end
vim.lsp.commands['nomi.runMain'] = function(command)
  require('nomi.runner').run_uri((command.arguments or {})[1])
end
-- The Debug lenses beside them start nvim-dap (nomi.dap), required at the
-- click.
vim.lsp.commands['nomi.debugTest'] = function(command)
  local args = command.arguments or {}
  require('nomi.dap').test_uri_line(args[1], args[2])
end
vim.lsp.commands['nomi.debugMain'] = function(command)
  require('nomi.dap').main_uri((command.arguments or {})[1])
end

-- The `nomi` debug adapter for nvim-dap, registered without loading
-- nvim-dap: now if it is loaded, else when lazy.nvim loads it. A setup that
-- loads it some other way gets the adapter from ftplugin/nomi.lua, or from
-- the first debug command.
if package.loaded.dap then
  require('nomi.dap').register(package.loaded.dap)
else
  vim.api.nvim_create_autocmd('User', {
    pattern = 'LazyLoad',
    callback = function(ev)
      if ev.data == 'nvim-dap' and package.loaded.dap then
        require('nomi.dap').register(package.loaded.dap)
        return true
      end
    end,
  })
end

-- :NomiDebug [nearest|file|run|back|reverse|target|break <function>]: debug
-- the test under the cursor (or the file's main with none there), every
-- test in the file, or main; in a paused session, step back, reverse
-- continue, or step into a target the current statement's calls offer; or
-- toggle a function breakpoint (`break` alone lists them).
local debug_verbs = {
  nearest = 'nearest',
  file = 'file',
  run = 'run',
  back = 'step_back',
  reverse = 'reverse_continue',
  target = 'step_into_target',
}
vim.api.nvim_create_user_command('NomiDebug', function(opts)
  local what, rest = opts.args:match('^%s*(%S*)%s*(.-)%s*$')
  if what == '' then
    what = 'nearest'
  end
  if what == 'break' then
    require('nomi.dap').toggle_function_breakpoint(rest)
    return
  end
  local fn = debug_verbs[what] and require('nomi.dap')[debug_verbs[what]]
  if not fn or rest ~= '' then
    vim.notify('nomi: :NomiDebug takes nearest, file, run, back, reverse, target or break <function>', vim.log.levels.ERROR)
    return
  end
  fn()
end, {
  nargs = '*',
  complete = function(_, line)
    if line:match('^%s*%S+%s+%S+%s') then
      return {}
    end
    return { 'nearest', 'file', 'run', 'back', 'reverse', 'target', 'break' }
  end,
})

-- :NomiTest [nearest|file|all|last] and :NomiRun, run in a terminal.
vim.api.nvim_create_user_command('NomiTest', function(opts)
  local runner = require('nomi.runner')
  local fn = runner[opts.args ~= '' and opts.args or 'nearest']
  if not fn or opts.args == 'run' or opts.args == 'test_line' then
    vim.notify('nomi: :NomiTest takes nearest, file, all or last', vim.log.levels.ERROR)
    return
  end
  fn()
end, {
  nargs = '?',
  complete = function()
    return { 'nearest', 'file', 'all', 'last' }
  end,
})
vim.api.nvim_create_user_command('NomiRun', function()
  require('nomi.runner').run()
end, {})
