-- Message catalogs: the TOML files a module's `message` translations live
-- in, under the directory `[i18n].catalogs` names in its nomi.toml
-- (`locales` unless it says otherwise). nomi-lsp attaches to them, and to
-- no other TOML file (lsp/nomi.lua's root_dir).
local M = {}

-- dir is the catalog directory a manifest's text names, relative to its
-- module root, or nil when its `[i18n]` table lists no locales, so it has
-- no catalogs. Only `locales` and `catalogs` in `[i18n]` are read.
function M.dir(manifest)
  local in_i18n, listed, dir = false, false, 'locales'
  for raw in (manifest .. '\n'):gmatch('([^\n]*)\n') do
    local line = vim.trim((raw:gsub('#.*$', '')))
    local table_name = line:match('^%[%s*([^%]]-)%s*%]$')
    if table_name then
      in_i18n = table_name == 'i18n'
    elseif in_i18n then
      if line:match('^locales%s*=') then
        listed = true
      end
      local value = line:match('^catalogs%s*=%s*["\']([^"\']*)["\']')
      if value then
        dir = value
      end
    end
  end
  if not listed then
    return nil
  end
  return (dir:gsub('^%./', ''):gsub('/+$', ''))
end

-- root is the module root of the catalog at path, an absolute path, or nil
-- when path is no catalog: not under the catalog directory of the nearest
-- module, or in a module with no locales.
function M.root(path)
  if not path or path == '' or not path:match('%.toml$') then
    return nil
  end
  local root = vim.fs.root(path, 'nomi.toml')
  if not root then
    return nil
  end
  local f = io.open(vim.fs.joinpath(root, 'nomi.toml'), 'r')
  if not f then
    return nil
  end
  local dir = M.dir(f:read('*a'))
  f:close()
  if not dir then
    return nil
  end
  local prefix = vim.fs.joinpath(root, dir) .. '/'
  if path:sub(1, #prefix) ~= prefix then
    return nil
  end
  return root
end

return M
