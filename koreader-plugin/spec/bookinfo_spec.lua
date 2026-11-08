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
-- function; anything else bound to that name (a loop variable, a discarded
-- first return value) turns the next `_("...")` into a call on a number or
-- a nil. `local _ = require("gettext")` is the only allowed binding.
describe("plugin sources", function()
    local dir = (debug.getinfo(1, "S").source:sub(2):match("(.*)/") or ".") .. "/../liseur.koplugin/"

    it("never bind `_` to anything but gettext", function()
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
                local where = string.format("%s:%d", path, line)
                local trimmed = text:gsub("^%s+", "")
                if text:find("for%s+_%s*[,=]") then
                    error(where .. " shadows gettext with a loop variable: " .. trimmed)
                end
                if text:find("local%s+_%s*,") then
                    error(where .. " discards into `_` and shadows gettext: " .. trimmed)
                end
                if text:find("local%s+_%s*=") and not text:find('require%("gettext"%)') then
                    error(where .. " binds `_` to something other than gettext: " .. trimmed)
                end
            end
        end
        p:close()
        assert.is_true(checked > 8, "only " .. checked .. " modules were read")
    end)
end)
