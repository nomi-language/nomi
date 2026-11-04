-- The injection queries a Nomi buffer's parser gives the languages it injects.
--
-- A Nomi `Html"""..."""` literal is one injected HTML document with its
-- `${...}` slots left out (queries/nomi/injections.scm). An HTML node that
-- spans a slot, such as the value of `href="/posts/${post.id}/edit"`, still
-- has the slot's characters inside its range, and Neovim reads a node's text
-- for predicates straight from the buffer over that range. nvim-treesitter's
-- html injections have a lit-html rule that injects JavaScript into any
-- attribute value whose text matches `%${`. So every attribute value with a
-- slot became JavaScript, starting two characters in and with the slot cut
-- out: `/admin/posts/${post.id}/edit` drew `dmin/posts/` as JavaScript names
-- and `/edit` as a `//` comment.
--
-- In a Nomi literal `${` is always a Nomi slot, never lit-html, so the
-- parser of a Nomi buffer gets the html injection query without the patterns
-- that test for `${`. Every other html injection (`<style>`, `<script>`,
-- `style=`, `on...=`) is kept. HTML files keep their own query.
local M = {}

-- The query's top-level patterns: each starts at a `(` or `[` outside any
-- bracket, string or comment and runs to the next one.
local function patterns(text)
  local out, depth, start, i, n = {}, 0, 1, 1, #text
  while i <= n do
    local c = text:sub(i, i)
    if c == ';' then
      i = (text:find('\n', i, true) or n) + 1
    elseif c == '"' then
      i = i + 1
      while i <= n do
        local s = text:sub(i, i)
        if s == '\\' then
          i = i + 2
        elseif s == '"' then
          i = i + 1
          break
        else
          i = i + 1
        end
      end
    else
      if c == '(' or c == '[' then
        if depth == 0 and i > start then
          table.insert(out, text:sub(start, i - 1))
          start = i
        end
        depth = depth + 1
      elseif c == ')' or c == ']' then
        depth = depth - 1
      end
      i = i + 1
    end
  end
  table.insert(out, text:sub(start))
  return out
end

-- The query text with every pattern that matches on `${` left out, or nil
-- when no pattern does.
function M.without_slot_patterns(text)
  local kept, dropped = {}, false
  for _, pattern in ipairs(patterns(text)) do
    -- Comments ride with the pattern before them, and the one above the
    -- lit-html rule shows `${`, so only the pattern's code is searched.
    if pattern:gsub(';[^\n]*', ''):find('%${', 1, true) then
      dropped = true
    else
      table.insert(kept, pattern)
    end
  end
  if not dropped then
    return nil
  end
  return table.concat(kept)
end

-- The html injection query, as Neovim would load it, without its `${`
-- patterns; nil when it has none (or html has no injection query).
function M.html()
  local parts = {}
  for _, file in ipairs(vim.treesitter.query.get_files('html', 'injections')) do
    local f = io.open(file, 'r')
    if f then
      table.insert(parts, f:read('*a'))
      f:close()
    end
  end
  return M.without_slot_patterns(table.concat(parts, '\n'))
end

-- Options for vim.treesitter.get_parser on a Nomi buffer. They take effect
-- only when the call creates the buffer's parser, so the ftplugin makes it
-- before vim.treesitter.start does.
function M.parser_opts()
  local html = M.html()
  if not html then
    return nil
  end
  return { injections = { html = html } }
end

return M
