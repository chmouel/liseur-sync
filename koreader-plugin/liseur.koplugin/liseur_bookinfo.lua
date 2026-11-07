-- The lines a book's detail dialog shows. Kept apart from the widgets so
-- it can be checked without a screen, and so that formatting a series
-- title cannot accidentally shadow the gettext function `_`.

local _ = require("gettext")
local T = require("ffi/util").template

local BookInfo = {}

function BookInfo.lines(book, local_path)
    local lines = { book.title }
    if #(book.authors or {}) > 0 then lines[#lines + 1] = table.concat(book.authors, ", ") end
    for _i, s in ipairs(book.series or {}) do
        lines[#lines + 1] = s.position and T(_("%1 #%2"), s.name or "?", s.position) or (s.name or "?")
    end
    if book.publisher then lines[#lines + 1] = book.publisher end
    if book.size_bytes then lines[#lines + 1] = string.format("%.1f MB", book.size_bytes / 1048576) end
    lines[#lines + 1] = local_path and _("On this device") or _("Not on this device")
    if book.status and book.status ~= "active" then lines[#lines + 1] = _("The server cannot serve this file right now.") end
    if book.description then lines[#lines + 1] = "\n" .. book.description:gsub("<[^>]+>", "") end
    return lines
end

return BookInfo
