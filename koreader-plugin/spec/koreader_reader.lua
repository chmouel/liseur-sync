-- Drives the plugin through a real KOReader ReaderUI: download from the
-- catalogue, open the book, offer and apply a remote position, read,
-- upload, keep a local position over a remote one, and close.
-- Driven by spec/integration.sh after koreader_integration.lua.
--
-- Env: LISEUR_URL, LISEUR_TOKEN4 (this reader), LISEUR_TOKEN2 (another
-- device), PLUGIN_PARENT, WORK_DIR.

require("setupkoenv")
G_defaults = require("luadefaults"):open()
local DataStorage = require("datastorage")
G_reader_settings = require("luasettings"):open(DataStorage:getDataDir() .. "/settings.reader.lua")
G_reader_settings:saveSetting("extra_plugin_paths", { os.getenv("PLUGIN_PARENT") })
require("dbg"):turnOff()
require("logger"):setLevel(require("logger").levels.warn)
einkfb = require("ffi/framebuffer") -- luacheck: ignore
einkfb.dummy = true -- luacheck: ignore
local Device = require("device")
Device.screen:init()
require("document/canvascontext"):init(Device)
Device.input.dummy = true
package.path = os.getenv("PLUGIN_PARENT") .. "/liseur.koplugin/?.lua;" .. package.path

local Api = require("liseur_api")
local DocumentRegistry = require("document/documentregistry")
local Event = require("ui/event")
local LuaSettings = require("luasettings")
local ReaderUI = require("apps/reader/readerui")
local Transport = require("liseur_transport")
local UIManager = require("ui/uimanager")
local util = require("util")

local URL, WORK = os.getenv("LISEUR_URL"), os.getenv("WORK_DIR")
local Network = require("liseur_network")
local socket = require("socket")
local failures = 0
local function check(name, fn)
    local done, ok, err
    Network.run(function()
        ok, err = xpcall(fn, debug.traceback)
        done = true
    end)
    local deadline = socket.gettime() + 30
    while not done and socket.gettime() < deadline do
        UIManager:_checkTasks()
        socket.sleep(0.02)
    end
    if not done then ok, err = false, "test timed out" end
    print((ok and "ok   " or "FAIL ") .. name .. (ok and "" or (": " .. tostring(err))))
    if not ok then failures = failures + 1 end
    return ok
end
local function eq(want, got, what)
    if want ~= got then error((what or "value") .. ": want " .. tostring(want) .. ", got " .. tostring(got), 2) end
end
local readerui, plug
local function run_tasks()
    local deadline = socket.gettime() + 20
    repeat
        UIManager:_checkTasks()
        socket.sleep(0.02)
    until not (plug and plug.running) or socket.gettime() > deadline
    assert(not (plug and plug.running), "background job timed out")
end

local other = Api.new{ base = Api.parseBase(URL), token = os.getenv("LISEUR_TOKEN2"), transport = Transport.new{} }

local settings_file = DataStorage:getSettingsDir() .. "/liseur.lua"
local s = LuaSettings:open(settings_file)
s:saveSetting("server_url", URL)
s:saveSetting("token", os.getenv("LISEUR_TOKEN4"))
s:saveSetting("download_dir", WORK .. "/dl")
s:flush()
util.makePath(WORK .. "/dl")

local Liseur = dofile(os.getenv("PLUGIN_PARENT") .. "/liseur.koplugin/main.lua")
local shell = Liseur:new{ ui = { menu = { registerToMainMenu = function() end } } }

check("checking the connection records the account and device", function()
    local probe = assert(shell:checkToken())
    assert(shell:hasScope("sync") and shell:hasScope("library-read"))
    eq(probe.device_id, shell:probe().device_id)
end)

check("position sync waits for a restart after the handover", function()
    shell.info = function() end
    local orig = UIManager.show
    UIManager.show = function(_, w) if w.ok_callback then w.ok_callback() end end
    shell:enablePositions()
    UIManager.show = orig
    eq(true, shell:cfg("positions_enabled"))
    eq(false, shell:positionsActive(), "not in the process that confirmed it")
    -- Simulate the restart.
    shell:setCfg("handover_process", "an-earlier-process")
    eq(true, shell:positionsActive())
end)

check("automatic Progress sync blocks position writes", function()
    local k = LuaSettings:open(DataStorage:getSettingsDir() .. "/kosync.lua")
    k:saveSetting("settings", { auto_sync = true })
    k:flush()
    eq(false, shell:positionsActive())
    k:saveSetting("settings", { auto_sync = false })
    k:flush()
    eq(true, shell:positionsActive())
end)

local path, book
check("a slow network worker yields to UI tasks", function()
    local ticked = false
    UIManager:scheduleIn(0.05, function() ticked = true end)
    local result = Network.worker(function() socket.sleep(0.2); return "response" end)
    eq("response", result)
    assert(ticked, "UI tasks were blocked by the worker")
end)

check("cancelling a network worker returns an unknown outcome", function()
    UIManager:scheduleIn(0.05, function()
        local top = UIManager._window_stack[#UIManager._window_stack]
        assert(top and top.widget.dismiss_callback, "no cancellable network widget")
        top.widget.dismiss_callback()
    end)
    local result, err = Network.worker(function() socket.sleep(1); return "response" end)
    eq(nil, result)
    eq("network operation cancelled", err)
end)

check("a catalogue book downloads through the plugin", function()
    local Catalog = require("liseur_catalog")
    local api = assert(shell:api())
    local folders = assert(Catalog.folders(api))
    book = assert(Catalog.folderBooks(api, folders[1].folder_id))[1]
    shell:download(book, function(p) path = p end)
    run_tasks()
    assert(path, "no path")
    eq(path, shell:localPathFor(book.book_id))
end)

local offers, infos = {}, {}
check("opening the book starts a session without writing", function()
    readerui = ReaderUI:new{ dimen = Device.screen:getSize(), document = DocumentRegistry:openDocument(path) }
    plug = readerui.liseur
    assert(plug, "plugin not loaded by ReaderUI")
    plug.offer = function(_, _, _, d) offers[#offers + 1] = d end
    plug.info = function(_, text) infos[#infos + 1] = text end
    assert(plug.session, "no session")
    eq(false, require("liseur_positions").dirty(plug.session.book))
end)

check("the open job resolves the book and offers the remote position", function()
    run_tasks()
    local b = plug.session.book
    eq("resolved", b.identity, b.last_error)
    eq(book.book_id, b.book_id)
    eq(1, #offers, "one offer")
    eq("pull", offers[1].kind)
end)

check("going to the offered position lands on it and owes nothing", function()
    local d = offers[1]
    plug:goTo(plug.session.engine, plug.session.book, d.op)
    run_tasks()
    local here = readerui.rolling:getLastProgress()
    local want = readerui.document:getPageFromXPointer(d.op.foreign_pos)
    eq(want, readerui.document:getPageFromXPointer(here), "page")
    eq(false, require("liseur_positions").dirty(plug.session.book))
    eq(false, plug.applying)
end)

local sent_xp
check("reading on is observed and uploaded with the KOReader position", function()
    local page = readerui.document:getCurrentPage()
    readerui:handleEvent(Event:new("GotoPage", page + 3))
    plug:observeNow()
    assert(require("liseur_positions").dirty(plug.session.book), "not dirty")
    sent_xp = readerui.rolling:getLastProgress()
    plug:jobFlush()
    eq(nil, plug.session.book.pending, "pending")
    local h = assert(other:get("/v1/works/" .. plug.session.book.work_id .. "/positions", { limit = 1 }))
    eq(sent_xp, h.ops[1].foreign_pos)
    eq(plug.session.book.sha256, h.ops[1].edition_sha)
end)

check("a remote move is offered on sync, and keeping this place sends it", function()
    local xp = "/body/DocFragment[4]/body/p[20]/text().0"
    assert(readerui.document:isXPointerInDocument(xp))
    local data = assert(other:post("/v1/ops", { ops = { {
        op_id = "other-device-op-1", work_id = plug.session.book.work_id, progression = 0.9,
        client_ts = os.date("!%Y-%m-%dT%H:%M:%SZ"), foreign_pos = xp, edition_sha = plug.session.book.sha256,
    } } }))
    eq("applied", data.results[1].status)
    offers = {}
    plug:jobOpen(true)
    eq(1, #offers)
    local d = offers[1]
    eq("other-device-op-1", d.op.op_id)
    assert(plug.session.engine:keep(plug.session.book, d.op))
    plug:jobFlush()
    eq(nil, plug.session.book.pending)
    local h = assert(other:get("/v1/works/" .. plug.session.book.work_id .. "/positions", { limit = 1 }))
    eq(sent_xp, h.ops[1].foreign_pos, "this device's place is newest again")
end)

check("page turns do not postpone an already scheduled flush", function()
    plug:unscheduleAll()
    local schedule = UIManager.scheduleIn
    local flushes = 0
    UIManager.scheduleIn = function(self, delay, task)
        if task == plug.flush_task then flushes = flushes + 1 end
        return schedule(self, delay, task)
    end
    for _ = 1, 3 do
        readerui:handleEvent(Event:new("GotoPage", readerui.document:getCurrentPage() + 1))
        plug:observeNow()
    end
    UIManager.scheduleIn = schedule
    eq(1, flushes)
    plug:unscheduleAll()
end)

check("a closed offline book is reconciled and uploaded after reconnect", function()
    plug:observeNow(true)
    plug:saveSession()
    local session = plug.session
    assert(require("liseur_positions").dirty(session.book))
    eq(nil, session.book.pending)
    plug:unscheduleAll()
    plug.session = nil
    plug:onNetworkConnected()
    run_tasks()
    local saved = assert(session.engine:book(session.md5))
    eq(false, require("liseur_positions").dirty(saved))
    local history = assert(other:get("/v1/works/" .. saved.work_id .. "/positions", { limit = 1 }))
    eq(saved["local"].xpointer, history.ops[1].foreign_pos)
    session.book = saved
    plug.session = session
end)

check("closing saves the session and leaves nothing pending", function()
    local md5 = plug.session.md5
    local engine = plug.session.engine
    readerui:onClose()
    run_tasks()
    eq(nil, plug.session)
    local b = assert(engine:book(md5))
    eq(nil, b.pending)
end)

-- The bug this pins: an upload that timed out records a `not_before` and
-- nothing ever looks at it again, so a book closed after a 429 or a
-- timeout stays pending until the reader moves in it once more.
check("a timed-out upload on a closed book is retried without new reading", function()
    local engine = assert(shell:engine())
    local b = assert(engine:book(util.partialMD5(path)))
    assert(b.work_id, "the book has no work to upload to")
    engine:observe(b, { progression = 0.4242, xpointer = "/retry-marker" })
    assert(engine:prepare(b))
    assert(engine:saveBook(b))
    local real = engine.api.postRaw
    engine.api.postRaw = function() return nil, { kind = "transport", message = "timed out" } end
    eq("retry", engine:send(b))
    engine.api.postRaw = real
    assert(b.pending and b.pending.not_before and b.pending.not_before > engine.now(), "no backoff recorded")
    b.pending.not_before = engine.now() + 1
    assert(engine:saveBook(b))
    plug:unscheduleAll()
    plug:schedule("flush")
    run_tasks()
    assert(plug.retry_task, "the coordinator scheduled no retry")
    local deadline = socket.gettime() + 8
    while engine:book(b.md5).pending and socket.gettime() < deadline do
        UIManager:_checkTasks()
        socket.sleep(0.02)
    end
    eq(nil, assert(engine:book(b.md5)).pending, "the upload was never retried")
    local h = assert(other:get("/v1/works/" .. b.work_id .. "/positions", { limit = 1 }))
    eq("/retry-marker", h.ops[1].foreign_pos)
end)

check("a token that was never checked is learned on first use", function()
    shell:setCfg("probe", nil)
    shell.engine_cache = nil
    eq(true, shell:positionsActive(), "a missing probe must not block position work")
    local engine, err = shell:engine()
    assert(engine, err)
    local probe = shell:probe()
    assert(probe and probe.device_id, "probe not remembered")
    eq(shell:cfg("server_url"), probe.server)
    eq(shell:probe().device_id, probe.device_id, "a second call does not re-ask")
end)

check("a refused token is reported, not a dead end", function()
    local good = shell:cfg("token")
    shell:setCfg("token", "not-a-real-token")
    shell:setCfg("probe", nil)
    shell.engine_cache = nil
    local engine, err = shell:engine()
    assert(not engine)
    assert(type(err) == "string" and #err > 10, tostring(err))
    assert(err:find("token"), err)
    shell:setCfg("token", good)
    shell:setCfg("probe", nil)
    shell.engine_cache = nil
end)

check("a stock Progress sync switched to automatic in memory is seen at once", function()
    local fake_ui = { kosync = { settings = { auto_sync = true } }, menu = { registerToMainMenu = function() end } }
    local p = Liseur:new{ ui = fake_ui }
    eq(false, p:positionsActive())
end)

-- A book with a series is what crashed on a Kindle: a book-detail line
-- shadowed gettext with a loop variable and the dialog died.
check("a book detail dialog for a book with a series builds", function()
    local LiseurUI = require("liseur_ui")
    local shown
    local orig = UIManager.show
    UIManager.show = function(_, widget) shown = widget return true end
    local ok, err = pcall(LiseurUI.bookDetails, shell, {
        book_id = "no-such-book", title = "Les ombres du Temps", authors = { "Christopher Ruohio" },
        series = { { name = "Sun Eater", position = 3 } }, status = "active",
    })
    UIManager.show = orig
    assert(ok, err)
    assert(shown and type(shown.title) == "string", "no dialog")
    assert(shown.title:find("Sun Eater #3", 1, true), "no series line: " .. tostring(shown.title))
end)

-- Neither the Kindle crash nor the `_` shadow behind it was reachable from
-- the module specs, so walk the whole menu: build every entry and run every
-- callback body once. Screens are captured instead of painted, and the
-- network wrapper runs its work inline so no request is left in flight.
-- A settings entry is walked to its Save button, because a save that does
-- not refresh the labels it was opened from is the bug this class hides.
check("every main-menu entry builds and runs", function()
    local LiseurUI = require("liseur_ui")
    local NetworkMgr = require("ui/network/manager")
    local filemanagerutil = require("apps/filemanager/filemanagerutil")
    local shown, dialogs = {}, {}
    local updates = 0
    local menu = { updateItems = function() updates = updates + 1 end }
    local orig_show, orig_run = UIManager.show, Network.run
    local orig_connected, orig_online = NetworkMgr.runWhenConnected, NetworkMgr.runWhenOnline
    local orig_choose = filemanagerutil.showChooseDialog
    UIManager.show = function(_, widget)
        shown[#shown + 1] = widget
        if widget.getInputText then dialogs[#dialogs + 1] = widget end
        return true
    end
    Network.run = function(fn) return fn() end
    NetworkMgr.runWhenConnected = function(_, fn) return fn() end
    NetworkMgr.runWhenOnline = function(_, fn) return fn() end
    filemanagerutil.showChooseDialog = function(title)
        shown[#shown + 1] = { text = title }
    end
    local ok, err = pcall(function()
        local touched = 0
        local function walk(list)
            for _i, item in ipairs(list) do
                if item.text_func then
                    assert(type(item.text_func()) == "string", "a menu label is not text")
                    touched = touched + 1
                end
                if item.checked_func then item.checked_func() end
                if item.enabled_func then item.enabled_func() end
                if item.callback then
                    item.callback(menu)
                    touched = touched + 1
                end
                if item.sub_item_table then walk(item.sub_item_table) end
            end
        end
        walk(LiseurUI.mainMenu(plug))
        assert(touched >= 12, "only " .. touched .. " entries were exercised")

        local ca
        for _i, d in ipairs(dialogs) do
            if d.title == "CA certificate bundle (leave empty to search the usual places)" then ca = d end
        end
        assert(ca, "the certificate setting opened no dialog")
        ca.getInputText = function() return "/tmp/liseur-test-ca.pem" end
        local before = updates
        ca.buttons[1][2].callback()
        eq(before + 1, updates, "a settings save did not refresh the menu")
        eq("/tmp/liseur-test-ca.pem", plug:cfg("ca_file"), "the saved value was not stored")
    end)
    UIManager.show = orig_show
    Network.run = orig_run
    NetworkMgr.runWhenConnected, NetworkMgr.runWhenOnline = orig_connected, orig_online
    filemanagerutil.showChooseDialog = orig_choose
    plug:setCfg("ca_file", nil)
    assert(ok, err)
    assert(#shown > 0, "no screen or dialog was produced")
    UIManager:close(plug.browser)
    plug.browser = nil
    plug.engine_cache = nil
end)

-- The browser pushes a title and an item table per level and Back gives the
-- previous pair back. A walk that only asks whether something happened
-- cannot see a wrong title or a stale table, so check both, with the
-- catalogue answered from tables instead of the server.
check("the library browser pushes and returns the expected screens", function()
    local LiseurUI = require("liseur_ui")
    local Catalog = require("liseur_catalog")
    local NetworkMgr = require("ui/network/manager")
    local shown = {}
    local orig_show, orig_run = UIManager.show, Network.run
    local orig_connected = NetworkMgr.runWhenConnected
    local real = {}
    UIManager.show = function(_, widget) shown[#shown + 1] = widget return true end
    Network.run = function(fn) return fn() end
    NetworkMgr.runWhenConnected = function(_, fn) return fn() end
    for _i, name in ipairs({ "folders", "folderBooks", "search", "entities", "seriesBooks", "entityBooks" }) do
        real[name] = Catalog[name]
    end
    local folder = { folder_id = "f1", name = "Shelves" }
    local book = {
        book_id = "b1", title = "A book", authors = { "An author" }, status = "active",
        series = { { id = "s1", name = "Sun Eater", position = 3 } },
    }
    Catalog.folders = function() return { folder }, nil end
    Catalog.folderBooks = function() return { book }, nil end
    Catalog.entities = function() return { { id = "s1", name = "Sun Eater", book_count = 1 } }, nil end
    Catalog.seriesBooks = function() return { book }, nil end
    Catalog.entityBooks = function() return { book }, nil end
    local ok, err = pcall(function()
        local function texts(items)
            local out = {}
            for _i, item in ipairs(items) do out[#out + 1] = item.text end
            return table.concat(out, "|")
        end
        local function tap(browser, label)
            for _i, item in ipairs(browser.item_table) do
                if item.text == label then
                    assert(item.callback, "no callback on " .. label)
                    return item.callback()
                end
            end
            error("no browser entry " .. label, 2)
        end
        LiseurUI.showLibrary(plug)
        local browser = assert(plug.browser, "no library browser")
        eq("Liseur library", browser.title)
        local root = browser.item_table
        eq("Continue reading", root[1].text)
        tap(browser, "Folders")
        eq("Folders", browser.title)
        eq("Shelves", texts(browser.item_table))
        tap(browser, "Shelves")
        eq("Shelves", browser.title)
        eq("A book — An author", texts(browser.item_table))
        local before = #shown
        tap(browser, "A book — An author")
        assert(#shown == before + 1, "a book entry opened nothing")
        assert(shown[#shown].title:find("Sun Eater #3", 1, true), "the book dialog lost its series")
        assert(browser:onReturn())
        eq("Folders", browser.title)
        eq("Shelves", texts(browser.item_table))
        assert(browser:onReturn())
        eq("Liseur library", browser.title)
        assert(browser.item_table == root, "Back did not restore the root table")
        tap(browser, "Series")
        eq("Series", browser.title)
        eq("Sun Eater", texts(browser.item_table))
        tap(browser, "Sun Eater")
        eq("Sun Eater", browser.title)
        eq("3. A book — An author", texts(browser.item_table))
        assert(browser:onReturn())
        assert(browser:onReturn())
        eq("Liseur library", browser.title)
        assert(browser.item_table == root, "Back did not restore the root table")
    end)
    for _i, name in ipairs({ "folders", "folderBooks", "search", "entities", "seriesBooks", "entityBooks" }) do
        Catalog[name] = real[name]
    end
    UIManager.show = orig_show
    Network.run = orig_run
    NetworkMgr.runWhenConnected = orig_connected
    assert(ok, err)
    UIManager:close(plug.browser)
    plug.browser = nil
end)

-- Continue reading, Downloaded and Sync status read local state only, so
-- they must open with Wi-Fi off: runWhenConnected asks to connect and drops
-- the work when the reader declines, which left a purely local list
-- impossible to open. Neither variant may ask for a connection or send a
-- request (the engine and its first-use /v1/token are what would).
check("the local screens open with the network down", function()
    local LiseurUI = require("liseur_ui")
    local NetworkMgr = require("ui/network/manager")
    local shown = {}
    local asked, ran, probes = 0, 0, 0
    local orig_show, orig_run = UIManager.show, Network.run
    local orig_connected = NetworkMgr.runWhenConnected
    local orig_check = plug.checkToken
    UIManager.show = function(_, widget) shown[#shown + 1] = widget return true end
    Network.run = function(fn) ran = ran + 1 return fn() end
    NetworkMgr.runWhenConnected = function(_, _fn) asked = asked + 1 end
    plug.checkToken = function(...) probes = probes + 1 return orig_check(...) end
    local engine, no_engine = plug:engine()
    assert(engine, no_engine)
    local saved_probe = plug:probe()
    local ok, err = pcall(function()
        local function tap(browser, label)
            for _i, item in ipairs(browser.item_table) do
                if item.text == label then return item.callback() end
            end
            error("no browser entry " .. label, 2)
        end
        -- Once with the probe known, once with the probe gone and only the
        -- engine cache left.
        for _i, probe in ipairs({ "known", "absent" }) do
            if probe == "absent" then plug:setCfg("probe", nil) else plug:setCfg("probe", saved_probe) end
            asked, ran, probes = 0, 0, 0
            LiseurUI.showLibrary(plug)
            local browser = assert(plug.browser, "no library browser")
            tap(browser, "Continue reading")
            eq("Continue reading", browser.title, "Continue reading, probe " .. probe)
            assert(#browser.item_table > 0, "Continue reading built no list with the probe " .. probe)
            assert(browser:onReturn())
            tap(browser, "Downloaded")
            eq("Downloaded", browser.title, "Downloaded, probe " .. probe)
            assert(#browser.item_table > 0, "Downloaded built no list with the probe " .. probe)
            assert(browser:onReturn())
            LiseurUI.showStatus(plug)
            local status = shown[#shown]
            assert(status and status.title == "Liseur sync status", "no status screen with the probe " .. probe)
            eq(0, asked, "a local screen asked for a connection (probe " .. probe .. ")")
            eq(0, probes, "a local screen sent a request (probe " .. probe .. ")")
            assert(ran >= 3, "the local screen did not run inline (probe " .. probe .. ")")
        end
    end)
    plug.checkToken = orig_check
    plug:setCfg("probe", saved_probe)
    UIManager.show = orig_show
    Network.run = orig_run
    NetworkMgr.runWhenConnected = orig_connected
    assert(ok, err)
    UIManager:close(plug.browser)
    plug.browser = nil
end)

-- A corrupt account document used to crash the whole reader path, because
-- engine() shadowed gettext with the value it discarded.
check("engine() refuses a corrupt account document instead of crashing", function()
    local probe = assert(shell:probe())
    local dir = DataStorage:getSettingsDir() .. "/liseur/" .. Api.namespace(Api.parseBase(URL), probe.account_id) .. "/account"
    util.makePath(dir)
    local f = assert(io.open(dir .. "/state.json", "wb"))
    f:write("{this is not json")
    f:close()
    shell.engine_cache = nil
    local ok, engine, err = pcall(function() return shell:engine() end)
    os.remove(dir .. "/state.json")
    shell.engine_cache = nil
    assert(ok, engine)
    assert(engine == nil, "a corrupt account document was accepted")
    assert(type(err) == "string" and err:find("unreadable"), tostring(err))
end)

print(failures == 0 and "all reader checks passed" or (failures .. " reader check(s) failed"))
os.exit(failures == 0 and 0 or 1)
