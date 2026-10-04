if vim.g.loaded_nomi then
  return
end
vim.g.loaded_nomi = true

-- Also in ftdetect/, which a lazy-loading plugin manager may skip.
vim.filetype.add({ extension = { nomi = 'nomi' } })
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
