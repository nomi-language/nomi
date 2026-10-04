-- Draw `//!` attached tests dimmed, like comments.
--
-- A decoration provider covers each attached-test line, after its `//!`
-- marker, with the `NomiAttachedTest` group, for the rows a window draws.
-- Its priority sits above tree-sitter (100) and semantic tokens (125-127) and
-- below diagnostics (150), so a diagnostic's underline still shows at full
-- strength. The marker keeps its @comment.documentation capture.
--
-- With `vim.g.nomi_reveal_attached_test` (default on), the attached test under
-- a window's cursor is not dimmed. The tests come from the parsed tree, only
-- for the rows being drawn; a cursor move redraws only when it enters or
-- leaves a test.
--
-- Settings, read at draw time:
--   vim.g.nomi_dim_attached_tests = false     no dimming at all
--   vim.g.nomi_reveal_attached_test = false   dim the test under the cursor too
local M = {}

local ns = vim.api.nvim_create_namespace('nomi.attached_tests')
local group = 'NomiAttachedTest'
local priority = vim.hl.priorities.semantic_tokens + 5
local prompt_query
-- Per window: the key of the attached test the cursor was in at the last
-- check, or false.
local cursor_block = {}

local function enabled()
  return vim.g.nomi_dim_attached_tests ~= false
end

local function reveal()
  return vim.g.nomi_reveal_attached_test ~= false
end

-- The group looks exactly like Comment: `nocombine` replaces the attributes
-- drawn below it (a keyword's bold, say) instead of adding to them. `default`
-- leaves a user's own definition alone.
local function define_highlight()
  local comment = vim.api.nvim_get_hl(0, { name = 'Comment', link = false })
  comment.nocombine = true
  comment.default = true
  vim.api.nvim_set_hl(0, group, comment)
end

local function query()
  if prompt_query == nil then
    local ok, q = pcall(vim.treesitter.query.parse, 'nomi', '(attached_test_prompt) @test')
    prompt_query = ok and q or false
  end
  return prompt_query
end

-- The rows an attached_test_prompt node covers. The node ends at column 0
-- of the line after its last prompt.
local function rows(node)
  local srow, _, erow, ecol = node:range()
  if ecol == 0 and erow > srow then
    erow = erow - 1
  end
  return srow, erow
end

-- The attached test containing (row, col), as "srow:erow", or false.
local function block_at(buf, row, col)
  local ok, node = pcall(vim.treesitter.get_node, { bufnr = buf, pos = { row, col }, ignore_injections = true })
  if not ok then
    return false
  end
  while node do
    if node:type() == 'attached_test_prompt' then
      local srow, erow = rows(node)
      return srow .. ':' .. erow
    end
    node = node:parent()
  end
  return false
end

local function cursor_key(win, buf)
  local cursor = vim.api.nvim_win_get_cursor(win)
  local row = cursor[1] - 1
  local line = vim.api.nvim_buf_get_lines(buf, row, row + 1, false)[1] or ''
  -- The first non-blank column: on a `//!` line that is the marker, which is
  -- a child of the test, wherever the cursor is on the line.
  local col = (line:find('%S') or (cursor[2] + 1)) - 1
  return block_at(buf, row, col)
end

local function on_win(_, win, buf, toprow, botrow)
  if not enabled() or not vim.b[buf].nomi_attached_tests then
    return false
  end
  local q = query()
  if not q then
    return false
  end
  local parser = vim.treesitter.get_parser(buf, 'nomi', { error = false })
  if not parser then
    return false
  end
  local trees = parser:parse({ toprow, botrow + 1 })
  local tree = trees and trees[1]
  if not tree then
    return false
  end
  local skip = false
  if reveal() then
    skip = cursor_key(win, buf)
    cursor_block[win] = skip
  end
  for _, node in q:iter_captures(tree:root(), buf, toprow, botrow + 1) do
    local srow, erow = rows(node)
    if srow .. ':' .. erow ~= skip then
      local first = math.max(srow, toprow)
      local last = math.min(erow, botrow)
      local lines = vim.api.nvim_buf_get_lines(buf, first, last + 1, false)
      for i, line in ipairs(lines) do
        local marker = line:find('//!', 1, true)
        local col = marker and marker + 2 or 0
        if col < #line then
          vim.api.nvim_buf_set_extmark(buf, ns, first + i - 1, col, {
            end_row = first + i - 1,
            end_col = #line,
            hl_group = group,
            priority = priority,
            ephemeral = true,
          })
        end
      end
    end
  end
  return false
end

local function on_cursor_moved(args)
  if not enabled() or not reveal() then
    return
  end
  local win = vim.api.nvim_get_current_win()
  if vim.api.nvim_win_get_buf(win) ~= args.buf then
    return
  end
  local key = cursor_key(win, args.buf)
  local previous = cursor_block[win]
  if key == previous then
    return
  end
  cursor_block[win] = key
  -- Redraw the rows of the test left and the test entered.
  for _, k in ipairs({ previous or '', key or '' }) do
    local srow, erow = k:match('^(%d+):(%d+)$')
    if srow then
      vim.api.nvim__redraw({ win = win, range = { tonumber(srow), tonumber(erow) + 1 }, valid = false })
    end
  end
end

local initialized = false

local function init()
  if initialized then
    return
  end
  initialized = true
  define_highlight()
  local augroup = vim.api.nvim_create_augroup('nomi.attached_tests', { clear = true })
  vim.api.nvim_create_autocmd('ColorScheme', { group = augroup, callback = define_highlight })
  vim.api.nvim_create_autocmd('WinClosed', {
    group = augroup,
    callback = function(args)
      cursor_block[tonumber(args.match)] = nil
    end,
  })
  vim.api.nvim_set_decoration_provider(ns, { on_win = on_win })
end

--- Dim attached tests in `buf`. Returns the command that undoes it, for
--- b:undo_ftplugin.
function M.attach(buf)
  init()
  vim.b[buf].nomi_attached_tests = true
  local augroup = vim.api.nvim_create_augroup('nomi.attached_tests.' .. buf, { clear = true })
  vim.api.nvim_create_autocmd({ 'CursorMoved', 'CursorMovedI' }, {
    group = augroup,
    buffer = buf,
    callback = on_cursor_moved,
  })
  return string.format("call v:lua.require'nomi.attached_tests'.detach(%d)", buf)
end

function M.detach(buf)
  if vim.api.nvim_buf_is_valid(buf) then
    vim.b[buf].nomi_attached_tests = nil
  end
  pcall(vim.api.nvim_del_augroup_by_name, 'nomi.attached_tests.' .. buf)
end

return M
