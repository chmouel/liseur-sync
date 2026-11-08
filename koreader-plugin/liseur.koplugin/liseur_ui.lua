-- KOReader screens for the Liseur plugin: the main-menu entries, the
-- library browser, sync status and position history. All network work
-- goes through the plugin instance (`plugin`).

local ButtonDialog = require("ui/widget/buttondialog")
local ConfirmBox = require("ui/widget/confirmbox")
local InputDialog = require("ui/widget/inputdialog")
local Menu = require("ui/widget/menu")
local NetworkMgr = require("ui/network/manager")
local TextViewer = require("ui/widget/textviewer")
local UIManager = require("ui/uimanager")
local ffiUtil = require("ffi/util")
local lfs = require("libs/libkoreader-lfs")
local _ = require("gettext")
local T = ffiUtil.template

local Network = require("liseur_network")
local Api = require("liseur_api")
local BookInfo = require("liseur_bookinfo")
local Catalog = require("liseur_catalog")
local Positions = require("liseur_positions")

local UI = {}

local function percent(p)
    if not p then return "?" end
    return string.format("%.1f%%", p * 100)
end

local function basename(path)
    return (path or ""):match("([^/]+)$") or path
end

local function errtext(err)
    if type(err) == "table" then
        if err.kind == "forbidden" then
            return _("this token does not have the permission for that; create a token with the needed permission")
        end
        if err.kind == "auth" then return _("the token was refused") end
        return err.message or err.kind or "?"
    end
    return tostring(err)
end

-- Browser -------------------------------------------------------------

local Browser = Menu:extend{
    is_borderless = true,
    is_popout = false,
    covers_fullscreen = true,
    title_bar_fm_style = true,
}

function Browser:init()
    self.paths = {}
    Menu.init(self)
end

-- Menu:switchItemTable does not assign self.title, so every move sets it
-- here; the title bar, Back and the next page all read it.
function Browser:push(title, items)
    table.insert(self.paths, { title = self.title, items = self.item_table })
    self.title = title
    self:switchItemTable(title, items)
end

function Browser:onReturn()
    local prev = table.remove(self.paths)
    if prev then
        self.title = prev.title
        self:switchItemTable(prev.title, prev.items)
    end
    return true
end

function Browser:onClose()
    if #self.paths > 0 then return self:onReturn() end
    UIManager:close(self)
    return true
end

-- paged builds a list whose last entry loads the next page in place.
local function paged(browser, title, fetch, to_item)
    local items = {}
    local function load(cursor)
        local page, next_or_err = fetch(cursor)
        if not page then
            UIManager:show(require("ui/widget/infomessage"):new{ text = T(_("Could not load: %1"), errtext(next_or_err)) })
            return false
        end
        if items[#items] and items[#items].more then table.remove(items) end
        for _i, v in ipairs(page) do items[#items + 1] = to_item(v) end
        if next_or_err then
            items[#items + 1] = {
                text = _("More…"),
                more = true,
                callback = function()
                    local at = #items
                    Network.run(function()
                        if load(next_or_err) then browser:switchItemTable(browser.title, items, at) end
                    end)
                end,
            }
        end
        if #items == 0 then items[1] = { text = _("Nothing here."), select_enabled = false } end
        return true
    end
    if load(nil) then browser:push(title, items) end
end

-- connected, not online: runWhenOnline means "can resolve
-- dns.msftncsi.com", which a server on the local network never needs.
local function connected(fn)
    NetworkMgr:runWhenConnected(function() Network.run(fn) end)
end

-- with_engine runs fn(engine) inline when the engine is already local, and
-- behind the connectivity wrapper only when building one has to ask
-- /v1/token. A screen that reads local state must not need Wi-Fi: with it
-- off, runWhenConnected asks to connect and drops the work if the reader
-- declines.
local function with_engine(plugin, fn)
    local engine = plugin:offlineEngine()
    if engine then return Network.run(function() fn(engine) end) end
    connected(function()
        local online, err = plugin:engine()
        if not online then
            plugin:info(err or _("Check the connection first."))
            return
        end
        fn(online)
    end)
end

function UI.bookDetails(plugin, book)
    local local_path = book.book_id and plugin:localPathFor(book.book_id)
    local lines = BookInfo.lines(book, local_path)
    local dialog
    local open = function(path)
        if plugin.browser then UIManager:close(plugin.browser) plugin.browser = nil end
        plugin:openFile(path)
    end
    dialog = ButtonDialog:new{
        title = table.concat(lines, "\n"),
        title_align = "left",
        buttons = {
            {
                {
                    text = local_path and _("Open") or _("Download and open"),
                    enabled = local_path ~= nil or book.status == nil or book.status == "active",
                    callback = function()
                        UIManager:close(dialog)
                        if local_path then
                            open(local_path)
                        else
                            plugin:download(book, open)
                        end
                    end,
                },
                {
                    text = _("Download"),
                    enabled = local_path == nil and (book.status == nil or book.status == "active"),
                    callback = function()
                        UIManager:close(dialog)
                        plugin:download(book, function(path)
                            plugin:info(T(_("Saved to %1"), path))
                        end)
                    end,
                },
            },
            { { text = _("Close"), callback = function() UIManager:close(dialog) end } },
        },
    }
    UIManager:show(dialog)
end

local function book_item(plugin, book, prefix)
    local text = (prefix or "") .. book.title
    if #book.authors > 0 then text = text .. " — " .. book.authors[1] end
    return {
        text = text,
        mandatory = book.book_id and plugin:localPathFor(book.book_id) and _("on device") or nil,
        callback = function() UI.bookDetails(plugin, book) end,
    }
end

local function series_prefix(book, series_id)
    for _i, s in ipairs(book.series) do
        if s.id == series_id and s.position then return tostring(s.position) .. ". " end
    end
    return ""
end

-- The engine, and a first-use /v1/token behind it, are asked for inside
-- the network wrapper: asking before it blocks the UI on a request.
function UI.continueReading(plugin, browser)
    with_engine(plugin, function(engine)
        local rows = {}
        for _i, md5 in ipairs(engine.store:list("books")) do
            local b = engine:book(md5)
            if b and b.path and b["local"] and lfs.attributes(b.path, "mode") == "file" then rows[#rows + 1] = b end
        end
        table.sort(rows, function(a, b) return (a["local"].at or 0) > (b["local"].at or 0) end)
        local items = {}
        for i = 1, math.min(#rows, 40) do
            local b = rows[i]
            local remote = engine:remote(b)
            local text = basename(b.path) .. " — " .. percent(b["local"].progression)
            if remote and remote.device_id ~= engine.device_id and math.abs((remote.progression or 0) - (b["local"].progression or 0)) >= 0.005 then
                text = text .. T(_(" (elsewhere %1)"), percent(remote.progression))
            end
            items[#items + 1] = {
                text = text,
                callback = function()
                    UIManager:close(browser)
                    plugin.browser = nil
                    plugin:openFile(b.path)
                end,
            }
        end
        if #items == 0 then items[1] = { text = _("No synced books on this device yet."), select_enabled = false } end
        browser:push(_("Continue reading"), items)
    end)
end

function UI.downloaded(plugin, browser)
    with_engine(plugin, function(engine)
        local index = plugin:downloadIndex(engine)
        local rows = {}
        for book_id, e in pairs(index.books or {}) do
            if e.path and lfs.attributes(e.path, "mode") == "file" then
                rows[#rows + 1] = { book_id = book_id, e = e }
            end
        end
        table.sort(rows, function(a, b) return (a.e.at or 0) > (b.e.at or 0) end)
        local items = {}
        for _i, r in ipairs(rows) do
            items[#items + 1] = {
                text = r.e.title or basename(r.e.path),
                callback = function()
                    UIManager:close(browser)
                    plugin.browser = nil
                    plugin:openFile(r.e.path)
                end,
            }
        end
        if #items == 0 then items[1] = { text = _("Nothing downloaded with Liseur yet."), select_enabled = false } end
        browser:push(_("Downloaded"), items)
    end)
end

local function api_or_info(plugin)
    local api, err = plugin:api()
    if not api then plugin:info(err) end
    return api
end

function UI.folders(plugin, browser, on_folder)
    local api = api_or_info(plugin)
    if not api then return end
    connected(function()
        paged(browser, _("Folders"), function(after) return Catalog.folders(api, after) end, function(f)
            return {
                text = f.name,
                callback = function() on_folder(f) end,
            }
        end)
    end)
end

function UI.folderBooks(plugin, browser, folder)
    local api = api_or_info(plugin)
    if not api then return end
    connected(function()
        paged(browser, folder.name, function(cursor) return Catalog.folderBooks(api, folder.folder_id, cursor, "recent") end,
            function(b) return book_item(plugin, b) end)
    end)
end

function UI.search(plugin, browser, folder)
    local api = api_or_info(plugin)
    if not api then return end
    local dialog
    dialog = InputDialog:new{
        title = T(_("Search in %1"), folder.name),
        buttons = {
            {
                { text = _("Cancel"), id = "close", callback = function() UIManager:close(dialog) end },
                {
                    text = _("Search"),
                    is_enter_default = true,
                    callback = function()
                        local q = dialog:getInputText()
                        UIManager:close(dialog)
                        if q == "" then return end
                        connected(function()
                            local books, truncated = Catalog.search(api, folder.folder_id, q)
                            if not books then
                                plugin:info(T(_("Search failed: %1"), errtext(truncated)))
                                return
                            end
                            local items = {}
                            for _i, b in ipairs(books) do items[#items + 1] = book_item(plugin, b) end
                            if truncated then items[#items + 1] = { text = _("More results exist; refine the search."), select_enabled = false } end
                            if #items == 0 then items[1] = { text = _("No books found."), select_enabled = false } end
                            browser:push(T(_("Search: %1"), q), items)
                        end)
                    end,
                },
            },
        },
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function UI.entities(plugin, browser, kind, title)
    local api = api_or_info(plugin)
    if not api then return end
    connected(function()
        paged(browser, title, function(after) return Catalog.entities(api, kind, after) end, function(e)
            return {
                text = e.name,
                mandatory = tostring(e.book_count),
                callback = function()
                    connected(function()
                        if kind == "series" then
                            local books, more = Catalog.seriesBooks(api, e.id, 10)
                            if not books then
                                plugin:info(T(_("Could not load: %1"), errtext(more)))
                                return
                            end
                            local items = {}
                            for _i, b in ipairs(books) do items[#items + 1] = book_item(plugin, b, series_prefix(b, e.id)) end
                            if more then items[#items + 1] = { text = _("This series is too long to show whole."), select_enabled = false } end
                            browser:push(e.name, items)
                        else
                            paged(browser, e.name, function(cursor) return Catalog.entityBooks(api, kind, e.id, cursor) end,
                                function(b) return book_item(plugin, b) end)
                        end
                    end)
                end,
            }
        end)
    end)
end

function UI.summary(plugin)
    local api = api_or_info(plugin)
    if not api then return end
    connected(function()
        local probe, perr = plugin:ensureProbe()
        if not probe then
            plugin:info(perr)
            return
        end
        if not plugin:hasScope("read-insights") then
            plugin:info(_("Reading statistics need a token with the read-insights permission."))
            return
        end
        local data, err = api:get("/v1/insights/summary")
        if not data then
            plugin:info(T(_("Could not load reading statistics: %1"), errtext(err)))
            return
        end
        local p = Api.present
        local lines = {
            T(_("Period: %1 days"), tostring(p(data.range_days) or "?")),
            T(_("Reading time: %1 h %2 min"), math.floor((tonumber(p(data.total_active_minutes)) or 0) / 60), math.floor((tonumber(p(data.total_active_minutes)) or 0) % 60)),
            T(_("Sessions: %1"), tostring(p(data.sessions) or 0)),
            T(_("Pages read (reported or from edition page counts): %1"), tostring(p(data.total_pages) or 0)),
            T(_("Current streak: %1 days"), tostring(p(data.streak_days) or 0)),
        }
        if p(data.first_activity_day) then lines[#lines + 1] = T(_("Reading since: %1"), p(data.first_activity_day)) end
        if p(data.timezone) then lines[#lines + 1] = T(_("Account time zone: %1"), p(data.timezone)) end
        UIManager:show(TextViewer:new{ title = _("Reading summary"), text = table.concat(lines, "\n") })
    end)
end

function UI.showLibrary(plugin)
    if not plugin:base() or not plugin:cfg("token") then
        plugin:info(_("Set the server URL and a device token first (Liseur Sync → Settings)."))
        return
    end
    local browser
    local function folder_then(fn)
        return function() UI.folders(plugin, browser, function(f) fn(f) end) end
    end
    local root = {
        { text = _("Continue reading"), callback = function() UI.continueReading(plugin, browser) end },
        { text = _("Downloaded"), callback = function() UI.downloaded(plugin, browser) end },
        { text = _("Folders"), callback = folder_then(function(f) UI.folderBooks(plugin, browser, f) end) },
        { text = _("Series"), callback = function() UI.entities(plugin, browser, "series", _("Series")) end },
        { text = _("Contributors"), callback = function() UI.entities(plugin, browser, "contributors", _("Contributors")) end },
        { text = _("Tags"), callback = function() UI.entities(plugin, browser, "tags", _("Tags")) end },
        { text = _("Search…"), callback = folder_then(function(f) UI.search(plugin, browser, f) end) },
        { text = _("Reading summary"), callback = function() UI.summary(plugin) end },
    }
    browser = Browser:new{ title = _("Liseur library"), item_table = root }
    browser.close_callback = nil
    plugin.browser = browser
    UIManager:show(browser)
end

function UI.showStatus(plugin)
    local function show(engine)
        local lines = {}
        local probe = plugin:probe()
        local base = plugin:base()
        lines[#lines + 1] = T(_("Server: %1"), base and base.url or _("not set"))
        lines[#lines + 1] = T(_("Device token: %1"), plugin:cfg("token") and _("set") or _("not set"))
        if plugin.probe_error then lines[#lines + 1] = plugin.probe_error end
        if probe then
            lines[#lines + 1] = T(_("Account: %1"), probe.account_id)
            lines[#lines + 1] = T(_("Device: %1 (%2)"), probe.name or "?", probe.device_id)
            lines[#lines + 1] = T(_("Permissions: %1"), table.concat(probe.scopes or {}, ", "))
            lines[#lines + 1] = T(_("Last checked: %1"), os.date("%Y-%m-%d %H:%M", probe.checked_at or 0))
        end
        lines[#lines + 1] = T(_("Position sync: %1"), plugin:positionsState())
        lines[#lines + 1] = T(_("Download folder: %1"), plugin:downloadDir() or _("not set"))
        if engine then
            local acc = engine:account()
            if acc then lines[#lines + 1] = T(_("Changes cursor: %1"), tostring(acc.cursor)) end
            local waiting, paused, blocked, unsent = {}, {}, {}, {}
            for _i, md5 in ipairs(engine.store:list("books")) do
                local b = engine:book(md5)
                if b then
                    local name = basename(b.path) or md5
                    if b.pending and b.pending.device_id ~= engine.device_id then paused[#paused + 1] = name
                    elseif b.pending then waiting[#waiting + 1] = name
                    elseif b.identity == "ambiguous" or b.identity == "mismatch" then blocked[#blocked + 1] = name
                    elseif Positions.dirty(b) then unsent[#unsent + 1] = name end
                end
            end
            local function section(title, list)
                if #list == 0 then return end
                lines[#lines + 1] = ""
                lines[#lines + 1] = title
                for _i, n in ipairs(list) do lines[#lines + 1] = "  • " .. n end
            end
            section(_("Uploads waiting for confirmation:"), waiting)
            section(_("Uploads paused (made under another device):"), paused)
            section(_("Not synced (identity unclear):"), blocked)
            section(_("Read here, not sent yet:"), unsent)
        end
        UIManager:show(TextViewer:new{ title = _("Liseur sync status"), text = table.concat(lines, "\n") })
    end
    -- Status is worth showing with the network down, so a missing engine is
    -- a line left out, not a reason to refuse the screen.
    local engine = plugin:offlineEngine()
    if engine then return Network.run(function() show(engine) end) end
    connected(function() show(plugin:engine()) end)
end

function UI.showHistory(plugin, session, history)
    local items = {}
    local engine = session.engine
    for _i, op in ipairs(history.ops) do
        local who = op.device_id == engine.device_id and _("this device") or (op.origin == "kosync" and _("Progress sync device") or _("another device"))
        items[#items + 1] = {
            text = T("%1 — %2", percent(op.progression), ((op.client_ts or ""):sub(1, 16):gsub("T", " "))),
            mandatory = who,
            callback = function()
                UIManager:show(ConfirmBox:new{
                    text = T(_("Go to %1 from %2? This becomes this device's position."), percent(op.progression), who),
                    ok_callback = function()
                        UIManager:close(plugin.history_menu)
                        plugin:goToChosen(op, session.book)
                    end,
                })
            end,
        }
    end
    local furthest
    for _i, op in ipairs(history.furthest) do
        if not furthest or op.progression > furthest.progression then furthest = op end
    end
    if furthest then
        table.insert(items, 1, {
            text = T(_("Furthest reached: %1"), percent(furthest.progression)),
            callback = function()
                local dialog
                dialog = ButtonDialog:new{
                    title = _("The furthest position is a way back if a position was lost. If it is wrong (for example an accidental jump to the end), forget it: it will no longer be suggested. No reading history is deleted."),
                    buttons = {
                        { { text = T(_("Go to %1"), percent(furthest.progression)), callback = function()
                            UIManager:close(dialog)
                            UIManager:close(plugin.history_menu)
                            plugin:goToChosen(furthest, session.book)
                        end } },
                        { { text = _("Forget this furthest position"), callback = function()
                            UIManager:close(dialog)
                            local upto = 0
                            for _i, op in ipairs(history.furthest) do upto = math.max(upto, op.seq or 0) end
                            connected(function() plugin:forgetFurthest(session.book.work_id, upto) end)
                        end } },
                        { { text = _("Close"), callback = function() UIManager:close(dialog) end } },
                    },
                }
                UIManager:show(dialog)
            end,
        })
    end
    if #items == 0 then items[1] = { text = _("No positions on the server yet."), select_enabled = false } end
    plugin.history_menu = Menu:new{
        title = _("Position history"),
        item_table = items,
        is_borderless = true,
        is_popout = false,
        covers_fullscreen = true,
    }
    UIManager:show(plugin.history_menu)
end

function UI.mainMenu(plugin)
    local in_reader = plugin.ui.document ~= nil
    return {
        {
            text = _("Library"),
            callback = function() UI.showLibrary(plugin) end,
        },
        {
            text = _("This book"),
            enabled_func = function() return in_reader end,
            sub_item_table = {
                { text = _("Status"), callback = function() plugin:showBookPanel() end },
                { text = _("Sync now"), callback = function() plugin:onLiseurSyncNow() end },
                { text = _("Position history"), callback = function() plugin:showHistory() end },
                {
                    text = _("Resend paused upload from this device"),
                    enabled_func = function()
                        local s = plugin.session
                        return s ~= nil and s.book.pending ~= nil and s.book.pending.device_id ~= s.engine.device_id
                    end,
                    callback = function() plugin:discardPaused() end,
                },
            },
        },
        {
            text = _("Reading summary"),
            callback = function() UI.summary(plugin) end,
        },
        {
            text = _("Sync status"),
            callback = function() UI.showStatus(plugin) end,
            separator = true,
        },
        {
            text = _("Settings"),
            sub_item_table = {
                {
                    text_func = function() return T(_("Server: %1"), plugin:cfg("server_url") or _("not set")) end,
                    keep_menu_open = true,
                    callback = function(touchmenu_instance)
                        plugin:editSetting(_("Liseur Sync server URL"), "server_url", "https://books.example.org", function(v)
                            local base, err = Api.parseBase(v)
                            if not base then return false, err end
                            return true
                        end, touchmenu_instance)
                    end,
                },
                {
                    text_func = function() return T(_("Device token: %1"), plugin:cfg("token") and _("set") or _("not set")) end,
                    keep_menu_open = true,
                    callback = function(touchmenu_instance)
                        plugin:editSetting(_("Device token (from the server's Devices page)"), "token", "", function(v)
                            if v:find("%s") then return false, _("A token has no spaces.") end
                            return true
                        end, touchmenu_instance)
                    end,
                },
                {
                    text = _("Check connection"),
                    callback = function(touchmenu_instance) plugin:checkConnection(touchmenu_instance) end,
                },
                {
                    text_func = function() return T(_("Download folder: %1"), plugin:downloadDir() or _("not set")) end,
                    keep_menu_open = true,
                    callback = function(touchmenu_instance)
                        local filemanagerutil = require("apps/filemanager/filemanagerutil")
                        filemanagerutil.showChooseDialog(_("Download folder:"), function(path)
                            plugin:setCfg("download_dir", path)
                            if touchmenu_instance then touchmenu_instance:updateItems() end
                        end, plugin:downloadDir())
                    end,
                },
                {
                    text_func = function() return T(_("Certificate bundle: %1"), plugin:cfg("ca_file") or _("automatic")) end,
                    keep_menu_open = true,
                    callback = function(touchmenu_instance)
                        plugin:editSetting(_("CA certificate bundle (leave empty to search the usual places)"), "ca_file", "/etc/ssl/certs/ca-certificates.crt", nil, touchmenu_instance)
                    end,
                },
                {
                    text_func = function() return T(_("Position sync: %1"), plugin:positionsState()) end,
                    checked_func = function() return plugin:cfg("positions_enabled") == true end,
                    callback = function(touchmenu_instance)
                        if plugin:cfg("positions_enabled") == true then
                            plugin:disablePositions()
                            if touchmenu_instance then touchmenu_instance:updateItems() end
                        else
                            plugin:enablePositions(touchmenu_instance)
                        end
                    end,
                },
            },
        },
    }
end

return UI
