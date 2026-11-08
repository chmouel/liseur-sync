-- The book detail lines, and the LOOKOUT for the crash they caused:
-- `for _, s in ipairs(...)` makes `_` the loop index, so calling the
-- gettext function `_()` inside such a loop called a number. That was
-- `liseur_ui.lua:112` on a device, opening any book that has a series.

local BookInfo = require("liseur_bookinfo")

describe("liseur_bookinfo", function()
    it("names the series and its position", function()
        local lines = BookInfo.lines({
            title = "Les ombres du Temps",
            authors = { "Christopher Ruocchio" },
            series = { { name = "Sun Eater", position = 3 } },
        })
        assert.are.equal("Les ombres du Temps", lines[1])
        assert.are.equal("Christopher Ruocchio", lines[2])
        assert.are.equal("Sun Eater #3", lines[3])
        assert.are.equal("Not on this device", lines[4])
    end)

    it("copes with a series entry that has no position", function()
        local lines = BookInfo.lines({ title = "T", authors = {}, series = { { name = "Solo" } } })
        assert.are.equal("Solo", lines[2])
    end)

    it("tolerates a book with no authors and no series", function()
        local lines = BookInfo.lines({ title = "T" })
        assert.are.equal("T", lines[1])
        assert.are.equal("Not on this device", lines[2])
    end)

    it("marks a local copy and an unservable status", function()
        local lines = BookInfo.lines({ title = "T", status = "missing" }, "/books/T.epub")
        assert.are.equal("On this device", lines[2])
        assert.are.equal("The server cannot serve this file right now.", lines[3])
    end)

    it("strips markup from a description", function()
        local lines = BookInfo.lines({ title = "T", description = "<p>Hello <b>there</b></p>" })
        assert.are.equal("\nHello there", lines[#lines])
    end)
end)

-- A cheap guard against the whole class of bug. `_` is the gettext
-- function; anything else bound to that name (a loop variable, a function
-- parameter, a discarded return value) turns the next `_("...")` into a
-- call on a number or a nil. `local _ = require("gettext")` is the only
-- allowed binding.
local function shadow(line)
    if line:match([[^%s*local%s+_%s*=%s*require%(%s*["']gettext["']%s*%)%s*$]]) then return nil end
    -- Strings and trailing comments cannot bind anything, so drop them
    -- before looking for a binding.
    local code = line:gsub('"[^"]*"', '""'):gsub("'[^']*'", "''"):gsub("%-%-.*$", "")
    if code:match("for%s+_[%s,=]") then return "a loop variable" end
    local names = code:match("%f[%a]local%s+([^=]-)=") or code:match("%f[%a]local%s+([%w_%s,]+)$")
    if names then
        for name in names:gmatch("[^,%s]+") do
            if name == "_" then return "a discarded local" end
        end
    end
    local params = code:match("function%s*[%a_%.:]*%s*(%b())")
    if params then
        for name in params:sub(2, -2):gmatch("[^,%s]+") do
            if name == "_" then return "a function parameter" end
        end
    end
end

describe("the `_` shadow guard", function()
    -- Every binding position the guard claims to cover, and the one legal
    -- form. Each offender must be rejected; the legal form must pass.
    local offenders = {
        { "for _, s in ipairs(series) do", "a loop variable" },
        { "for _, v in pairs(t) do", "a loop variable" },
        { "for _ = 1, 3 do", "a loop variable" },
        { "local _ = discarded()", "a discarded local" },
        { "local _, rest = f()", "a discarded local" },
        { "local a, _ = f()", "a discarded local" },
        { "local a, _, b = f()", "a discarded local" },
        { "local f = function(_, x) return x end", "a function parameter" },
        { "local f = function(x, _) return x end", "a function parameter" },
        { "function pick(_) return 1 end", "a function parameter" },
        { "function M:pick(x, _) return x end", "a function parameter" },
        { 'local _ = require("gettext").n', "a discarded local" },
        { 'local _ = require("gettext") -- and then something else', "a discarded local" },
    }
    local legal = {
        'local _ = require("gettext")',
        '  local _ = require("gettext")  ',
        'local s = _("Series")',
        'text = T(_("More…"), n)',
        'local t = "for _, x in pairs(d) do" -- a string is not a binding',
        'for _i, s in ipairs(series) do',
        'local _i = 0',
        'function M.pick(x, y) return x end',
    }

    it("rejects every binding position it claims to cover", function()
        for _i, case in ipairs(offenders) do
            assert.are.equal(case[2], shadow(case[1]), case[1])
        end
    end)

    it("accepts gettext and lines that bind nothing", function()
        for _i, line in ipairs(legal) do
            assert.is_nil(shadow(line), line)
        end
    end)

    it("the plugin sources bind nothing but gettext", function()
        local dir = (debug.getinfo(1, "S").source:sub(2):match("(.*)/") or ".") .. "/../liseur.koplugin/"
        local p = io.popen("ls '" .. dir .. "'*.lua")
        local checked = 0
        for path in p:lines() do
            local f = assert(io.open(path, "rb"))
            local source = f:read("*a")
            f:close()
            checked = checked + 1
            local line = 0
            for text in source:gmatch("[^\n]*") do
                line = line + 1
                local reason = shadow(text)
                if reason then
                    error(string.format("%s:%d binds `_` to %s: %s", path, line, reason, (text:gsub("^%s+", ""))))
                end
            end
        end
        p:close()
        assert.is_true(checked > 8, "only " .. checked .. " modules were read")
    end)
end)
