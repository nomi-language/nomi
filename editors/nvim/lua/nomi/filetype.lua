-- The `nomi` filetype: a `.nomi` file, or an extensionless script whose
-- first line is a `#!` naming nomi (`#!/usr/bin/env nomi`), which the
-- compiler runs as a program.
local M = {}

-- is_nomi_shebang reports whether line is a `#!` line that names nomi as a
-- word.
function M.is_nomi_shebang(line)
  return line ~= nil and line:find('^#!') ~= nil and line:find('%f[%w_]nomi%f[^%w_]') ~= nil
end

function M.register()
  vim.filetype.add({
    extension = { nomi = 'nomi' },
    pattern = {
      ['.*'] = {
        function(_, bufnr)
          local first = vim.api.nvim_buf_get_lines(bufnr, 0, 1, false)[1]
          if M.is_nomi_shebang(first) then
            return 'nomi'
          end
        end,
        { priority = -math.huge },
      },
    },
  })
end

return M
