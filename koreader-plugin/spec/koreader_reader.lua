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
    local ConfirmBox = require("ui/widget/confirmbox")
    local orig = UIManager.show
    UIManager.show = function(_, w) if w.ok_callback then w.ok_callback() end end
    shell:enablePositions()
    UIManager.show = orig
    eq(true, shell:cfg("positions_enabled"))
    eq(false, shell:positionsActive(), "not in the process that confirmed it")
    local _ = ConfirmBox
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

print(failures == 0 and "all reader checks passed" or (failures .. " reader check(s) failed"))
os.exit(failures == 0 and 0 or 1)
