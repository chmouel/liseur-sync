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

local A_MD5, A_SHA = util.partialMD5(path), book.sha256
local function edition(engine, sha, md5)
    return assert(engine:edition(sha or A_SHA, md5 or A_MD5))
end

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

-- The server answering `unknown_work` drops the book's mapping. A session
-- that stayed ready would never resolve it again: every later flush found
-- nothing writable and the book stayed stuck for as long as it was open.
check("a flush that meets an unknown work re-resolves before the next upload", function()
    local s = assert(plug.session, "no session")
    local engine, book = s.engine, s.book
    local work = book.work_id
    assert(s.ready, "the session is not ready")
    readerui:handleEvent(Event:new("GotoPage", readerui.document:getCurrentPage() + 1))
    plug:observeNow()
    -- Freeze the upload for a work the server has never heard of, the way
    -- a work deleted on the server leaves it.
    book.work_id = "00000000-0000-4000-8000-00000000dead"
    local p = assert(engine:prepare(book), "no upload was prepared")
    eq("00000000-0000-4000-8000-00000000dead", p.work_id)
    assert(engine:saveBook(book))
    local here = book["local"].xpointer
    plug:jobFlush()
    eq(false, s.ready, "the session stayed ready with no work")
    eq(nil, plug.session.book.pending, "the refused upload is still pending")
    assert(plug.topics.open, "the coordinator was not asked to resolve the book again")
    plug:schedule("open")
    run_tasks()
    eq("resolved", s.book.identity, s.book.last_error)
    eq(work, s.book.work_id, "the book did not resolve back to its work")
    assert(s.book["local"], "the local observation was dropped")
    eq(true, s.ready, "the session never became ready again")
    eq(nil, s.book.pending, "the local position was not uploaded again")
    local h = assert(other:get("/v1/works/" .. work .. "/positions", { limit = 1 }))
    eq(here, h.ops[1].foreign_pos, "the local position never reached the work")
end)

-- The same answer in the open job, for an upload settled there: the book
-- resolved and seeded again, and the position still went out.
-- The same answer for an upload the open job itself prepared: the book is
-- resolved and seeded again, and the position still went out. Nothing is
-- pending when it starts, so the upload that meets the refusal is the one
-- this job's push made.
check("the open job recovers when its own upload meets an unknown work", function()
    local s = assert(plug.session, "no session")
    local engine, book = s.engine, s.book
    local work = book.work_id
    assert(s.ready, "the session is not ready")
    readerui:handleEvent(Event:new("GotoPage", readerui.document:getCurrentPage() + 1))
    plug:observeNow()
    assert(require("liseur_positions").dirty(book), "no move to upload")
    eq(nil, book.pending, "the test starts with an upload in flight")
    -- A work this device followed until the server deleted it.
    book.work_id = "00000000-0000-4000-8000-00000000beef"
    local acc = assert(engine:account())
    acc.works[book.work_id] = { floor = 0, seeded = true }
    assert(engine:saveAccount())
    assert(engine:saveBook(book))
    local here = book["local"].xpointer
    plug:jobOpen(false, true)
    assert(plug.session, "the session was dropped")
    local why = "identity " .. tostring(s.book.identity) .. ", last error " .. tostring(s.book.last_error)
        .. ", pending " .. tostring(s.book.pending ~= nil)
    eq(false, s.ready, "the session stayed ready with no work: " .. why)
    eq(nil, s.book.pending, "the refused upload is still pending: " .. why)
    assert(plug.topics.open, "the coordinator was not asked to resolve the book again")
    plug:schedule("open")
    run_tasks()
    eq("resolved", s.book.identity, s.book.last_error)
    eq(work, s.book.work_id, "the book did not resolve back to its work")
    eq(true, s.ready, "the session never became ready again")
    eq(nil, s.book.pending)
    local h = assert(other:get("/v1/works/" .. work .. "/positions", { limit = 1 }))
    eq(here, h.ops[1].foreign_pos, "the local position never reached the work")
    -- The session that follows expects an unsent move, as it had one.
    readerui:handleEvent(Event:new("GotoPage", readerui.document:getCurrentPage() + 1))
    plug:observeNow()
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
    local saved = assert(session.engine:edition(session.sha, session.md5))
    eq(false, require("liseur_positions").dirty(saved))
    local history = assert(other:get("/v1/works/" .. saved.work_id .. "/positions", { limit = 1 }))
    eq(saved["local"].xpointer, history.ops[1].foreign_pos)
    session.book = saved
    plug.session = session
end)

check("closing saves the session and leaves nothing pending", function()
    local sha, md5 = plug.session.sha, plug.session.md5
    local engine = plug.session.engine
    readerui:onClose()
    run_tasks()
    eq(nil, plug.session)
    local b = assert(engine:edition(sha, md5))
    eq(nil, b.pending)
end)

-- The bug this pins: an upload that timed out records a `not_before` and
-- nothing ever looks at it again, so a book closed after a 429 or a
-- timeout stays pending until the reader moves in it once more. The reader
-- that owned the book is torn down by then, so the retry belongs to the
-- instance that outlives it, which is the one the file manager keeps.
check("a timed-out upload on a closed book is retried without new reading", function()
    local engine = assert(shell:engine())
    local b = assert(edition(engine))
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
    shell:unscheduleAll()
    -- The reader that owned the book is gone by the time the deadline
    -- expires, and KOReader builds a fresh plugin for the file manager
    -- rather than carrying the reader's over. Nothing below asks it to
    -- run: its init finding the deadline is what is being tested.
    local fm = Liseur:new{ ui = { menu = { registerToMainMenu = function() end } } }
    local saved_plug = plug
    plug = fm
    assert(fm.retry_task, "the file manager's plugin adopted no deadline")
    local deadline = socket.gettime() + 8
    while engine:edition(b.sha256, b.md5).pending and socket.gettime() < deadline do
        UIManager:_checkTasks()
        socket.sleep(0.02)
    end
    eq(nil, assert(engine:edition(b.sha256, b.md5)).pending, "the upload was never retried")
    plug = saved_plug
    fm:unscheduleAll()
    shell:unscheduleAll()
    shell:unscheduleAll()
    local h = assert(other:get("/v1/works/" .. b.work_id .. "/positions", { limit = 1 }))
    eq("/retry-marker", h.ops[1].foreign_pos)
end)

-- A permission the server takes away used to be retried for ever: a
-- timeout had stored a deadline that was already past, so every second
-- the coordinator asked again and was refused again. The pending bytes
-- stay exactly as they are, and only a check of the connection lifts it.
check("a denied permission pauses uploads until the connection is checked", function()
    local engine = assert(shell:engine())
    local b = assert(edition(engine))
    assert(b.work_id, "the book has no work to upload to")
    engine:observe(b, { progression = 0.5151, xpointer = "/denied-marker" })
    assert(engine:prepare(b))
    b.pending.not_before = engine.now() - 1 -- an earlier timeout, already expired
    assert(engine:saveBook(b))
    local saved_token = engine.api.token
    local transport = engine.api.transport
    local requests = 0
    engine.api.transport = function(...)
        requests = requests + 1
        return transport(...)
    end
    -- The token the probe saw loses its sync permission on the server.
    engine.api.token = os.getenv("LISEUR_TOKEN5")
    shell:unscheduleAll()
    shell:schedule("flush")
    local saved_plug = plug
    plug = shell
    run_tasks()
    plug = saved_plug
    assert(shell.denied, "the refusal was not recorded")
    eq(nil, shell.retry_task, "a retry was armed for a refused permission")
    assert(shell:positionsState():find("Check connection", 1, true), shell:positionsState())
    local after = assert(engine:edition(b.sha256, b.md5))
    assert(after.pending, "the pending upload was dropped")
    eq("/denied-marker", after.pending.xpointer, "the pending bytes changed")
    local before = requests
    for _ = 1, 3 do
        shell.topics.flush = true
        shell:runTopics()
        UIManager:_checkTasks()
    end
    eq(before, requests, "a refused permission was asked again")
    -- An explicit check of the connection is what lifts the pause, and it
    -- also asks for what was waiting: the reader is told to run it, and
    -- KOReader sends no reconnect event on a device that never went away.
    engine.api.token = saved_token
    engine.api.transport = transport
    shell:checkConnection()
    eq(nil, shell.denied, "the pause survived a successful check")
    local deadline = socket.gettime() + 20
    while (shell.running or engine:edition(b.sha256, b.md5).pending) and socket.gettime() < deadline do
        UIManager:_checkTasks()
        socket.sleep(0.02)
    end
    eq(nil, assert(engine:edition(b.sha256, b.md5)).pending, "the upload was never sent after revalidation")
    shell:unscheduleAll()
end)

-- The pause has to hold inside one drain as well: a queued open and a
-- queued flush both start by sending the frozen upload, so without a check
-- between them the same coordinator invocation would ask, be refused, and
-- ask again.
check("a refused permission stops the drain it was discovered in", function()
    local engine = assert(shell:engine())
    local b = assert(edition(engine))
    engine:observe(b, { progression = 0.6161, xpointer = "/drain-marker" })
    assert(engine:prepare(b))
    b.pending.not_before = engine.now() - 1
    assert(engine:saveBook(b))
    shell.denied = nil
    shell.session = { md5 = b.md5, sha = b.sha256, file = path, book = b, engine = engine, ready = true }
    local saved_token = engine.api.token
    local transport = engine.api.transport
    local requests = 0
    engine.api.transport = function(...)
        requests = requests + 1
        return transport(...)
    end
    engine.api.token = os.getenv("LISEUR_TOKEN5")
    shell:unscheduleAll()
    shell.topics = { open = true, flush = true }
    shell:runTopics()
    engine.api.token = saved_token
    engine.api.transport = transport
    assert(shell.denied, "the refusal was not recorded")
    eq(1, requests, "the drain that met the refusal asked again")
    shell.session = nil
    shell.denied = nil
    shell:setCfg("refusal", nil)
    shell:unscheduleAll()
end)

-- The hop from the last backlog book to the open one is the same drain:
-- the backlog loop stops on a refusal, but the open book's own upload is
-- the very next request, so it has to be gated too.
check("a refusal on the backlog's last book stops the open book's upload", function()
    local engine = assert(shell:engine())
    local b = assert(edition(engine))
    assert(b.work_id, "the book has no work to upload to")
    local saved_doc = assert(engine.store:read("editions", b.sha256))
    engine:observe(b, { progression = 0.7171, xpointer = "/session-marker" })
    assert(engine:prepare(b))
    b.pending.not_before = engine.now() - 1
    assert(engine:saveBook(b))
    -- A second book with an expired upload, named so that the sorted
    -- backlog reaches it after the open book and nothing else.
    local ghost = {
        md5 = string.rep("f", 32), work_id = b.work_id, identity = "resolved", revision = 1, acked = 0,
        sha256 = string.rep("f", 64), ["local"] = { progression = 0.31, xpointer = "/ghost-marker" },
    }
    assert(engine:prepare(ghost), "no upload was prepared for the backlog book")
    ghost.pending.not_before = engine.now() - 1
    assert(engine:saveBook(ghost))
    local names = engine.store:list("editions")
    eq(ghost.sha256, names[#names], "the backlog book is not the last one drained")
    shell.denied, shell.auth_failed = nil, nil
    shell.session = { md5 = b.md5, sha = b.sha256, file = path, book = b, engine = engine, ready = true }
    local saved_token = engine.api.token
    local transport = engine.api.transport
    local requests = 0
    engine.api.transport = function(...)
        requests = requests + 1
        return transport(...)
    end
    engine.api.token = os.getenv("LISEUR_TOKEN5")
    shell:unscheduleAll()
    shell.topics = { flush = true }
    shell:runTopics()
    engine.api.token = saved_token
    engine.api.transport = transport
    assert(shell.denied, "the refusal was not recorded")
    eq(1, requests, "the drain went on to the open book after a refusal")
    shell.session = nil
    shell.denied = nil
    shell:setCfg("refusal", nil)
    engine.store:remove("editions", ghost.sha256)
    -- The upload these checks sent used an op id derived from the
    -- revision they left behind; put the document back where they found
    -- it, with a revision no later run can reuse.
    saved_doc.revision = (saved_doc.revision or 0) + 1000
    saved_doc.acked = saved_doc.revision
    assert(engine.store:write("editions", b.sha256, saved_doc))
    shell:unscheduleAll()
end)

-- An armed timer is not a deadline. A refusal lasts across the readers
-- that answer for it, so the instance the file manager builds must not
-- restart what the server just refused.
check("a refusal is not carried out of the reader by closing it", function()
    local engine = assert(shell:engine())
    local b = assert(edition(engine))
    local saved_doc = assert(engine.store:read("editions", b.sha256))
    engine:observe(b, { progression = 0.9191, xpointer = "/persisted-marker" })
    assert(engine:prepare(b))
    b.pending.not_before = engine.now() - 1
    assert(engine:saveBook(b))
    shell.denied, shell.auth_failed = nil, nil
    shell.session = { md5 = b.md5, sha = b.sha256, file = path, book = b, engine = engine, ready = true }
    local saved_token = engine.api.token
    engine.api.token = os.getenv("LISEUR_TOKEN5")
    shell:unscheduleAll()
    shell.topics = { flush = true }
    shell:runTopics()
    assert(shell.denied, "the refusal was not recorded")
    shell.session = nil
    -- The reader is closed and the file manager builds its own plugin; it
    -- reads the same settings file, and nothing tells it what happened.
    local fm = Liseur:new{ ui = { menu = { registerToMainMenu = function() end } } }
    local saved_plug = plug
    plug = fm
    assert(fm.denied, "the refusal was forgotten by the instance that outlived the reader")
    assert(not fm.retry_task, "the new instance armed a retry under a refusal")
    local fem = assert(fm:engine())
    local transport = fem.api.transport
    local requests = 0
    fem.api.transport = function(...)
        requests = requests + 1
        return transport(...)
    end
    fm.topics = { open = true, flush = true }
    fm:runTopics()
    eq(0, requests, "the instance the file manager built restarted a refused upload")
    -- The check the pause names is the one thing that lifts it, and it
    -- also sends what was waiting.
    fm:checkConnection()
    eq(nil, fm.denied, "the pause survived an explicit check of the connection")
    eq(nil, fm:cfg("refusal"), "the pause outlived the connection it was waiting for")
    local deadline = socket.gettime() + 20
    while (fm.running or engine:edition(b.sha256, b.md5).pending) and socket.gettime() < deadline do
        UIManager:_checkTasks()
        socket.sleep(0.02)
    end
    assert(requests > 0, "the upload was never sent after revalidation")
    eq(nil, assert(engine:edition(b.sha256, b.md5)).pending, "the upload was never sent after revalidation")
    plug = saved_plug
    fm:unscheduleAll()
    engine.api.token = saved_token
    shell.denied = nil
    shell:setCfg("refusal", nil)
    -- The upload these checks sent used an op id derived from the
    -- revision they left behind; put the document back where they found
    -- it, with a revision no later run can reuse.
    saved_doc.revision = (saved_doc.revision or 0) + 1000
    saved_doc.acked = saved_doc.revision
    assert(engine.store:write("editions", b.sha256, saved_doc))
    shell:unscheduleAll()
end)

-- An armed timer is not the same deadline: a book frozen later with a
-- shorter backoff must not wait behind the one already waiting.
check("an earlier retry deadline replaces the armed timer", function()
    local engine = assert(shell:engine())
    local b = assert(edition(engine))
    assert(b.work_id, "the book has no work to upload to")
    local saved_doc = assert(engine.store:read("editions", b.sha256))
    engine:observe(b, { progression = 0.8181, xpointer = "/later-marker" })
    assert(engine:prepare(b))
    b.pending.not_before = engine.now() + 600
    assert(engine:saveBook(b))
    local armed = {}
    local scheduleIn, unschedule = UIManager.scheduleIn, UIManager.unschedule
    UIManager.scheduleIn = function(_, delay, fn) armed[#armed + 1] = { delay = delay, fn = fn } end
    UIManager.unschedule = function() end
    shell:unscheduleAll()
    shell:scheduleRetry(engine)
    eq(1, #armed, "no timer was armed for the frozen upload")
    assert(armed[1].delay > 500, "the timer was armed for the wrong deadline")
    local ghost = {
        md5 = string.rep("e", 32), work_id = b.work_id, identity = "resolved", revision = 1, acked = 0,
        sha256 = string.rep("e", 64), ["local"] = { progression = 0.12, xpointer = "/earlier-marker" },
    }
    assert(engine:prepare(ghost))
    ghost.pending.not_before = engine.now() + 60
    assert(engine:saveBook(ghost))
    shell:scheduleRetry(engine)
    eq(2, #armed, "the earlier deadline did not replace the timer")
    assert(armed[2].delay < 120, "the timer was rearmed for the wrong deadline")
    shell:scheduleRetry(engine)
    eq(2, #armed, "a second call armed a duplicate timer")
    UIManager.scheduleIn, UIManager.unschedule = scheduleIn, unschedule
    shell:unscheduleAll()
    engine.store:remove("editions", ghost.sha256)
    -- The upload these checks sent used an op id derived from the
    -- revision they left behind; put the document back where they found
    -- it, with a revision no later run can reuse.
    saved_doc.revision = (saved_doc.revision or 0) + 1000
    saved_doc.acked = saved_doc.revision
    assert(engine.store:write("editions", b.sha256, saved_doc))
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
    local probe = shell:probe()
    if not probe then probe = assert(shell:ensureProbe()) end
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

-- F7: a migration that could not read its destination must not hand back
-- an engine, because every use of one is a chance to write over state that
-- was never proven.
check("engine() refuses to build after a failed migration", function()
    local Positions = require("liseur_positions")
    local real = Positions.migrateLegacy
    Positions.migrateLegacy = function() return nil, "unreadable" end
    local saved = shell.engine_cache
    shell.engine_cache = nil
    local ok, engine, err = pcall(function() return shell:engine() end)
    Positions.migrateLegacy = real
    shell.engine_cache = saved
    assert(ok, engine)
    assert(engine == nil, "an engine was built despite a failed migration")
    assert(type(err) == "string" and err:find("unreadable"), tostring(err))
end)

-- Closing the reader tears its plugin down, but its queued job and its
-- retry timer used to outlive it: with position sync off from the file
-- manager's own plugin afterwards, the dead instance still asked the
-- server, and armed itself again every second for as long as the process
-- lived. The reader's queued job is left in place here: what must stop it
-- is the instance knowing it is gone, not the test tidying up first.
check("a closed reader neither writes nor re-arms itself", function()
    local fresh = ReaderUI:new{ dimen = Device.screen:getSize(), document = DocumentRegistry:openDocument(path) }
    local p = assert(fresh.liseur, "plugin not loaded by ReaderUI")
    local saved_plug = plug
    plug = p
    run_tasks()
    local s = assert(p.session, "the new reader opened no session")
    assert(s.ready, "the session never became ready: " .. tostring(s.book.last_error))
    local engine, book = s.engine, s.book
    p:observeNow(true)
    assert(engine:prepare(book), "no upload was prepared")
    book.pending.not_before = engine.now() - 1 -- a retry deadline already past
    assert(engine:saveBook(book))
    local frozen = book.pending.raw
    local requests = 0
    local transport = engine.api.transport
    engine.api.transport = function(...)
        requests = requests + 1
        return transport(...)
    end
    fresh:onClose()
    eq(true, p.topics.flush, "the closing reader queued nothing for the dead instance to reject")
    eq(nil, p.retry_task, "a closed reader kept its timer")
    assert(p.closed, "the plugin did not see the reader go")
    p:schedule("open")
    eq(nil, p.topics.open, "a closed reader queued new work")
    -- Position sync is turned off from the plugin the file manager builds
    -- for itself. The reader's own settings were read once and still say
    -- it is on, which is what let the dead instance keep talking.
    local fm = Liseur:new{ ui = { menu = { registerToMainMenu = function() end } } }
    fm:setCfg("positions_enabled", false)
    eq(false, fm:positionsActive(), "the live plugin did not see the change")
    eq(true, p:positionsActive(), "the closed reader re-read the settings it had already loaded")
    requests = 0
    local deadline = socket.gettime() + 3
    while socket.gettime() < deadline do
        UIManager:_checkTasks()
        socket.sleep(0.02)
    end
    eq(nil, p.retry_task, "a closed reader armed a retry after the reader was gone")
    eq(nil, p.topics.flush, "the dead instance kept its queued job")
    eq(0, requests, "a closed reader sent a request")
    engine.api.transport = transport
    local left = assert(engine:edition(book.sha256, book.md5))
    assert(left.pending, "the unsent position was lost")
    eq(frozen, left.pending.raw, "the frozen upload changed")
    fm:setCfg("positions_enabled", true)
    plug = saved_plug
    UIManager:close(fresh)
end)

-- A book detail dialog is built from a menu callback, outside any
-- coroutine. A download index that asked the server for its engine froze
-- the reader until the request answered or timed out.
check("a book detail dialog never probes the server for the local path", function()
    local LiseurUI = require("liseur_ui")
    local saved_probe, saved_cache = shell:probe(), shell.engine_cache
    shell:setCfg("probe", nil)
    shell.engine_cache = nil
    local probes = 0
    local orig_check = shell.checkToken
    shell.checkToken = function(...)
        probes = probes + 1
        return orig_check(...)
    end
    local shown
    local orig_show = UIManager.show
    UIManager.show = function(_, widget) shown = widget return true end
    local ok, err = pcall(LiseurUI.bookDetails, shell, book)
    UIManager.show = orig_show
    shell.checkToken = orig_check
    shell:setCfg("probe", saved_probe)
    shell.engine_cache = saved_cache
    assert(ok, err)
    assert(shown and type(shown.title) == "string", "the dialog was not built")
    eq(0, probes, "a book detail dialog asked the server who the token is")
    assert(shown.title:find("Not on this device", 1, true),
        "the dialog guessed instead of saying where the copy is: " .. shown.title)
end)

-- Turning a page and closing before the 30-second flush is the ordinary
-- way a book is put down. KOReader closes the reader synchronously, so the
-- flush it queued dies with it, and the position has no retry deadline for
-- the next instance to find: it is still owed because it is unsent.
check("a dirty close is drained by the instance the file manager keeps", function()
    local Positions = require("liseur_positions")
    local reader = ReaderUI:new{ dimen = Device.screen:getSize(), document = DocumentRegistry:openDocument(path) }
    local p = assert(reader.liseur, "plugin not loaded by ReaderUI")
    p.offer = function() end
    p.info = function() end
    local saved_plug = plug
    plug = p
    run_tasks()
    local s = assert(p.session, "the reader opened no session")
    assert(s.ready, "the session never became ready: " .. tostring(s.book.last_error))
    local engine = s.engine
    assert(engine:pull())
    local held = engine:remote(s.book)
    if held then assert(engine:accept(s.book, held), "the book did not settle on what the server holds") end
    assert(engine:saveBook(s.book))
    reader:handleEvent(Event:new("GotoPage", reader.document:getCurrentPage() + 2))
    p:observeNow(true)
    assert(Positions.dirty(s.book), "no move to drain")
    local here, work = s.book["local"].xpointer, s.book.work_id
    local before = assert(other:get("/v1/works/" .. work .. "/positions", { limit = 1 })).ops[1]
    -- The reader is gone before its own flush could run, and it must not
    -- have sent anything on its way out.
    reader:onClose()
    run_tasks()
    assert(Positions.dirty(assert(engine:edition(s.book.sha256, s.book.md5))), "the closed reader sent its position")
    eq(before.op_id, assert(other:get("/v1/works/" .. work .. "/positions", { limit = 1 })).ops[1].op_id)
    -- The file manager builds its own plugin, and nothing tells it what was
    -- left behind: finding it on disk is the whole mechanism.
    local fm = Liseur:new{ ui = { menu = { registerToMainMenu = function() end } } }
    plug = fm
    assert(fm.topics.flush, "the file manager's instance found nothing to do")
    run_tasks()
    local after = assert(engine:edition(s.book.sha256, s.book.md5))
    eq(false, Positions.dirty(after), "the unsent position was never sent")
    eq(nil, fm.topics.flush, "the instance kept the job it had already run")
    eq(nil, fm.retry_task, "the instance armed a retry nobody asked for")
    local newest = assert(other:get("/v1/works/" .. work .. "/positions", { limit = 1 })).ops[1]
    eq(here, newest.foreign_pos, "the last position never reached the server")
    eq(after.sha256, newest.edition_sha)
    plug = saved_plug
    fm:unscheduleAll()
end)

-- An explicit sync is not a way around the pause a refusal wrote, and the
-- pause lasts until Check connection because that is what the message says.
check("Sync now is refused while a refusal is in force", function()
    local reader = ReaderUI:new{ dimen = Device.screen:getSize(), document = DocumentRegistry:openDocument(path) }
    local p = assert(reader.liseur, "plugin not loaded by ReaderUI")
    local infos = {}
    p.offer = function() end
    p.info = function(_, text) infos[#infos + 1] = text end
    local saved_plug = plug
    plug = p
    run_tasks()
    local s = assert(p.session, "the reader opened no session")
    local engine, book = s.engine, s.book
    reader:handleEvent(Event:new("GotoPage", reader.document:getCurrentPage() + 1))
    p:observeNow(true)
    assert(engine:prepare(book), "no upload to refuse")
    book.pending.not_before = engine.now() - 1 -- an expired deadline, as a 403 leaves it
    assert(engine:saveBook(book))
    local saved_token = engine.api.token
    local transport = engine.api.transport
    local requests = 0
    engine.api.transport = function(...)
        requests = requests + 1
        return transport(...)
    end
    engine.api.token = os.getenv("LISEUR_TOKEN5")
    p.denied = "the server refused this permission"
    infos = {}
    p:onLiseurSyncNow()
    run_tasks()
    eq(0, requests, "Sync now asked a refused permission again")
    local said = table.concat(infos, " | ")
    assert(said:find("Check connection", 1, true), "the pause was not explained: " .. said)
    -- An explicit check is what lifts it, and it also sends what waited.
    engine.api.token = saved_token
    engine.api.transport = transport
    p:checkConnection()
    eq(nil, p.denied, "the pause survived a successful check")
    local deadline = socket.gettime() + 20
    while (p.running or engine:edition(book.sha256, book.md5).pending) and socket.gettime() < deadline do
        UIManager:_checkTasks()
        socket.sleep(0.02)
    end
    eq(nil, assert(engine:edition(book.sha256, book.md5)).pending, "the upload was never resumed by the check")
    local newest = assert(other:get("/v1/works/" .. book.work_id .. "/positions", { limit = 1 })).ops[1]
    eq(book["local"].xpointer, newest.foreign_pos, "the waiting position never reached the server")
    plug = saved_plug
    p:unscheduleAll()
    reader:onClose()
end)

-- A download verifies the bytes against the catalogue before it names
-- them, so the digest it just checked is the one that names the state. A
-- file replaced at the same path is a new book: the old bytes' edition
-- keeps its work, its position and its frozen upload, and the new one
-- starts with nothing of it.
check("a re-download whose bytes changed under an unchanged stat never inherits the old edition", function()
    local lfs = require("libs/libkoreader-lfs")
    local json = require("json")
    local engine = assert(shell:engine())
    local md5 = util.partialMD5(path)
    local attr = assert(lfs.attributes(path), "the downloaded copy is gone")
    local op_id = "8f1d6c54-0000-4000-8000-000000000001"
    local old_sha = string.rep("1", 64)
    -- The state the path is known by belongs to the bytes that are about to
    -- be replaced: nothing of it may be inherited.
    engine.store:remove("editions", book.sha256)
    local old = assert(engine:edition(old_sha, md5))
    old.path, old.size, old.mtime = path, attr.size, attr.modification
    old.work_id = "00000000-0000-4000-8000-00000000aaaa"
    old.book_id = "the-old-catalogue-book"
    old.identity = "resolved"
    old["local"] = { progression = 0.25, xpointer = "/old-place" }
    old.baseline = { progression = 0.25, op_id = "the-old-op" }
    old.revision = 9
    old.acked = 8
    old.pending = {
        revision = 9, op_id = op_id, work_id = old.work_id, device_id = engine.device_id,
        progression = 0.25, xpointer = "/old-place", attempts = 0,
        raw = json.encode({
            op_id = op_id, work_id = old.work_id, client_ts = "2026-01-01T00:00:00.000Z",
            progression = 0.25, foreign_pos = "/old-place", edition_sha = old_sha,
        }),
    }
    assert(engine:saveBook(old))
    local fp = assert(engine:fingerprint(md5))
    assert(engine:noteFile(fp, path, old_sha, { size = attr.size, mtime = attr.modification }))
    assert(engine:saveFingerprint(md5, fp))
    os.remove(path)
    -- The new copy lands with new bytes and a new mtime; make the stat it is
    -- seen with match the recorded one, which is what hid the change.
    local real_attributes = lfs.attributes
    lfs.attributes = function(p, what)
        local v = real_attributes(p, what)
        if p == path and not what and v then
            return { size = attr.size, modification = attr.modification, mode = v.mode }
        end
        return v
    end
    local downloaded
    shell:download(book, function(p) downloaded = p end)
    run_tasks()
    lfs.attributes = real_attributes
    eq(path, downloaded, "the download did not reuse the free name")
    local after = assert(engine:edition(book.sha256, md5))
    eq(book.sha256, after.sha256, "the verified digest was not recorded")
    assert(after.work_id ~= old.work_id, "the new bytes inherited the old bytes' work")
    eq(nil, after["local"], "the new bytes inherited the old position")
    eq(nil, after.pending, "the new bytes inherited the old upload")
    eq(book.book_id, after.book_id, "the catalogue book was not linked to the new bytes")
    eq(false, require("liseur_positions").dirty(after), "the replacement is owed an upload")
    -- The bytes that were replaced keep everything they were owed.
    local kept = assert(engine:edition(old_sha, md5))
    eq(9, kept.revision)
    eq(8, kept.acked)
    eq(op_id, kept.pending.op_id)
    eq("/old-place", kept.pending.xpointer)
    engine.store:remove("editions", old_sha)
    engine.store:remove("editions", book.sha256)
    fp = assert(engine:fingerprint(md5))
    fp.shas[old_sha] = nil
    fp.shas[book.sha256] = nil
    fp.files[path] = nil
    assert(engine:saveFingerprint(md5, fp))
end)

-- F5: a warm cache entry says what a path held, not what it holds now.
-- The bytes are read at open and again before any upload, so a file
-- replaced under the same sampled MD5 and the same size and mtime is a
-- new edition and never reaches the old work.
check("a warm cache never lets replaced bytes keep the old edition", function()
    local lfs = require("libs/libkoreader-lfs")
    local sha2 = require("ffi/sha2")
    local engine = assert(shell:engine())
    local function hash(p)
        local f = assert(io.open(p, "rb"))
        local update = sha2.sha256()
        while true do
            local chunk = f:read(64 * 1024)
            if not chunk then break end
            update(chunk)
        end
        f:close()
        return update()
    end
    local copy = WORK .. "/replaced.epub"
    local src = assert(io.open(path, "rb"))
    local bytes = assert(src:read("*a"))
    src:close()
    local out = assert(io.open(copy, "wb"))
    out:write("X" .. bytes:sub(2)) -- same length, different bytes
    out:close()
    local md5 = assert(util.partialMD5(copy))
    local attr = assert(lfs.attributes(copy))
    local old_sha = string.rep("2", 64)
    -- The cache claims these bytes at exactly this path and stat, which is
    -- what a same-partial-MD5 replacement leaves behind.
    local fp = assert(engine:fingerprint(md5))
    assert(engine:noteFile(fp, copy, old_sha, { size = attr.size, mtime = attr.modification }))
    assert(engine:saveFingerprint(md5, fp))
    local old = assert(engine:edition(old_sha, md5))
    old.identity, old.work_id = "resolved", "warm-cache-work"
    assert(engine:saveBook(old))
    local blind = Liseur:new{ ui = {
        menu = { registerToMainMenu = function() end },
        document = { file = copy, info = { has_pages = true } },
        paging = { getLastProgress = function() return 1 end, getLastPercent = function() return 0.1 end },
    } }
    blind:onReaderReady()
    local s = assert(blind.session, "the replaced file opened no session")
    eq(hash(copy), s.sha, "the warm cache digest was taken for the current bytes")
    assert(s.book.work_id ~= "warm-cache-work", "the current bytes inherited the old work")
    -- The same read gates every upload: replace the bytes again under the
    -- stat the cache still reports and verify the session refuses them.
    out = assert(io.open(copy, "wb"))
    out:write("Y" .. bytes:sub(2))
    out:close()
    local real_attributes = lfs.attributes
    lfs.attributes = function(p, what)
        local v = real_attributes(p, what)
        if p == copy and not what and v then
            return { size = attr.size, modification = attr.modification, mode = v.mode, type = v.type }
        end
        return v
    end
    local ok = blind:verifySession(s)
    lfs.attributes = real_attributes
    eq(false, ok, "an upload was allowed against replaced bytes")
    assert(s.stale, "the session was not marked stale")
    blind:unscheduleAll()
    os.remove(copy)
    engine.store:remove("editions", old_sha)
    engine.store:remove("editions", s.sha)
    fp = assert(engine:fingerprint(md5))
    fp.shas[old_sha], fp.shas[s.sha], fp.files[copy] = nil, nil, nil
    assert(engine:saveFingerprint(md5, fp))
end)

-- Two files can carry the same KOReader partial MD5 and the same stat, so
-- the partial MD5 may lead to a cache but never to a position: each file's
-- reading belongs to its own SHA-256. A digest that cannot be read opens
-- nothing at all, and the next open reads the bytes again.
check("colliding files keep separate editions and a failed digest reads nothing", function()
    local Catalog = require("liseur_catalog")
    local engine = assert(shell:engine())
    local api = assert(shell:api())
    local md5 = util.partialMD5(path)
    local folders = assert(Catalog.folders(api))
    local revised
    for _i, b in ipairs(assert(Catalog.folderBooks(api, folders[1].folder_id))) do
        if b.book_id ~= book.book_id then revised = b end
    end
    assert(revised, "the fixture's second book is not in the catalogue")
    local owner = assert(other:post("/v1/works/resolve", { identifiers = { { kind = "partial-md5", value = md5 } } }))
    local work_a = assert(owner.work_id, "no work owns the file's fingerprint")
    local head = assert(other:get("/v1/works/" .. work_a .. "/positions", { limit = 1 })).ops[1]
    assert(head, "the old work has no position to protect")
    local revised_path
    shell:download(revised, function(p) revised_path = p end)
    run_tasks()
    assert(revised_path and revised_path ~= path, "the second book landed on the first one's path")
    eq(md5, util.partialMD5(revised_path), "the two catalogue books do not share a partial MD5")
    local revised_doc = assert(engine:edition(revised.sha256, md5))
    eq(revised.book_id, revised_doc.book_id, "the new bytes were not linked to their catalogue book")
    eq(nil, revised_doc.work_id, "the old work survived the new bytes")

    -- The bytes cannot be read: no session, and nothing is written for a
    -- file whose digest is unknown. The cache is cold for this copy, as it
    -- is for a file that changed since it was last looked at, so the digest
    -- has to be read and the read is what fails.
    local fp = assert(engine:fingerprint(md5))
    fp.files[revised_path] = nil
    assert(engine:saveFingerprint(md5, fp))
    local editions = #engine.store:list("editions")
    local blind = Liseur:new{ ui = {
        menu = { registerToMainMenu = function() end },
        document = { file = revised_path, info = { has_pages = true } },
        paging = { getLastProgress = function() return 1 end, getLastPercent = function() return 0.1 end },
    } }
    local blind_infos = {}
    blind.info = function(_, text) blind_infos[#blind_infos + 1] = text end
    blind.hash_file = function() return nil end
    blind:onReaderReady()
    eq(nil, blind.session, "a file whose bytes could not be read opened a session")
    blind:observeNow()
    eq(nil, blind.session, "a failed digest read left work to observe")
    assert(blind.session_error, "the reader was not told why the book is not synced")
    eq(1, #blind_infos, "opening an unreadable file did not notify the reader once")
    assert(blind_infos[1]:find("bytes", 1, true), "the warning did not give the reason: " .. tostring(blind_infos[1]))
    eq(editions, #engine.store:list("editions"), "a failed digest read wrote an edition")
    eq(nil, assert(engine:fingerprint(md5)).files[revised_path], "a failed digest read seeded the cache")
    blind:unscheduleAll()

    local reader = ReaderUI:new{ dimen = Device.screen:getSize(), document = DocumentRegistry:openDocument(revised_path) }
    local p = assert(reader.liseur, "plugin not loaded by ReaderUI")
    p.offer = function() end
    p.info = function() end
    local saved_plug = plug
    plug = p
    run_tasks()
    local s = assert(p.session, "the changed book opened no session")
    eq(md5, s.md5, "the session is not for the shared fingerprint")
    eq(revised.sha256, s.sha, "the session is not keyed by the file's own digest")
    eq("resolved", s.book.identity, s.book.last_error)
    assert(s.book.work_id, "the changed book has no work")
    assert(s.book.work_id ~= work_a,
        "the changed bytes were handed to the work that owns the old fingerprint")
    local work_b = s.book.work_id
    -- Its reading lands on its own work, with its own bytes named.
    reader:handleEvent(Event:new("GotoPage", reader.document:getCurrentPage() + 1))
    p:observeNow()
    assert(require("liseur_positions").dirty(s.book), "the changed book owes nothing")
    p:jobFlush()
    local head_b = assert(other:get("/v1/works/" .. work_b .. "/positions", { limit = 1 })).ops[1]
    -- What it still owes is one unsent move, and one upload frozen for it,
    -- not due yet: neither can be decided by anything below.
    engine:observe(s.book, { progression = 0.7777, xpointer = "/second-copy-unsent" })
    assert(engine:prepare(s.book), "no upload was prepared for the changed book")
    s.book.pending.not_before = engine.now() + 600
    assert(engine:saveBook(s.book))
    local frozen = s.book.pending.raw
    local common = { size = s.book.size, modification = s.book.mtime }
    assert(common.size and common.modification, "the changed copy recorded no stat")
    UIManager:close(reader)
    run_tasks()

    -- Reopening the original copy: same partial MD5, same length and the
    -- same mtime as the copy seen last, which is the shape that used to be
    -- taken for "nothing moved", leaving it with B's digest and B's work.
    local real_attributes = require("libs/libkoreader-lfs").attributes
    require("libs/libkoreader-lfs").attributes = function(p, what)
        local v = real_attributes(p, what)
        if not what and v and (p == path or p == revised_path) then
            return { size = common.size, modification = common.modification, mode = v.mode, type = v.type }
        end
        return v
    end
    local back = ReaderUI:new{ dimen = Device.screen:getSize(), document = DocumentRegistry:openDocument(path) }
    local p2 = assert(back.liseur, "plugin not loaded by ReaderUI")
    p2.offer = function() end
    p2.info = function() end
    plug = p2
    run_tasks()
    require("libs/libkoreader-lfs").attributes = real_attributes
    local s2 = assert(p2.session, "the original copy opened no session")
    eq(A_SHA, s2.sha, "the original copy is not its own edition")
    eq(md5, s2.md5)
    eq("resolved", s2.book.identity, s2.book.last_error)
    eq(work_a, s2.book.work_id, "the original copy was handed to the other copy's work")
    -- The changed copy's documents are exactly as they were left.
    local kept_b = assert(engine:edition(revised.sha256, md5))
    eq(frozen, kept_b.pending.raw, "the other copy's frozen upload was rewritten")
    eq("/second-copy-unsent", kept_b["local"].xpointer, "the other copy's unsent move was lost")
    -- Reading the original copy moves its own work only.
    local e2 = assert(p2:engine())
    assert(e2:pull(), "the original copy could not read the server's positions")
    local held = assert(e2:remote(s2.book), "the original copy's work holds no position")
    assert(e2:accept(s2.book, held), "the original copy did not settle on what the server holds")
    back:handleEvent(Event:new("GotoPage", back.document:getCurrentPage() + 3))
    p2:observeNow()
    p2:jobFlush()
    local a_now = assert(other:get("/v1/works/" .. work_a .. "/positions", { limit = 1 })).ops[1]
    assert(a_now.op_id ~= head.op_id, "reading the original copy reached no work")
    eq(A_SHA, a_now.edition_sha, "the original copy's reading carried the other copy's bytes")
    local b_now = assert(other:get("/v1/works/" .. work_b .. "/positions", { limit = 1 })).ops[1]
    eq(head_b.op_id, b_now.op_id, "reading the original copy wrote to the other copy's work")
    plug = saved_plug
    UIManager:close(back)
    engine.store:remove("editions", revised.sha256)
end)

check("a read that fails after the open yields no digest", function()
    -- Opening a directory succeeds on Linux and reading it fails, which is
    -- the shape of a broken read: a digest of the bytes that did arrive
    -- would name a file nobody read.
    eq(nil, Liseur.hash_file("/"), "a failed read was digested as a short file")
    local read_back = Liseur.hash_file(WORK .. "/books/small.epub")
    assert(read_back and #read_back == 64, "a readable file has no digest")
end)

check("book details keep descriptions in a scrollable viewer and mask the token in settings", function()
    local LiseurUI = require("liseur_ui")
    local shown = {}
    local orig_show = UIManager.show
    UIManager.show = function(_, widget) shown[#shown + 1] = widget return true end
    local blurb = "<p>First paragraph &amp; more.</p><p>" .. string.rep("Long synopsis text. ", 80) .. "</p>"
    local ok, err = pcall(function()
        LiseurUI.bookDetails(shell, {
            book_id = "no-such-book",
            title = "A Long Book",
            authors = { "Ann Author" },
            description = blurb,
            status = "active",
        })
        local dialog = assert(shown[#shown], "no detail dialog shown")
        assert(not dialog.title:find("First paragraph", 1, true),
            "the description was crammed into the ButtonDialog header")
        local desc_btn
        for _i, row in ipairs(dialog.buttons or {}) do
            for _j, b in ipairs(row) do
                if b.text == "Description" then desc_btn = b end
            end
        end
        assert(desc_btn and desc_btn.callback, "no Description button on a book with a description")
        desc_btn.callback()
        local viewer = assert(shown[#shown], "Description button opened no viewer")
        eq("A Long Book", viewer.title)
        eq("book_info", viewer.text_type)
        assert(viewer.text:find("First paragraph & more.", 1, true), "HTML entities were not decoded in Description viewer")

        -- Token editing masks the existing token with text_type = "password".
        local menu = LiseurUI.mainMenu(shell)
        local settings = menu[#menu].sub_item_table
        local token_item = settings[2]
        token_item.callback()
        local input = assert(shown[#shown], "token setting opened no InputDialog")
        eq("password", input.text_type, "the existing token was not masked in Settings")
        UIManager:close(input)
        UIManager:close(viewer)
        UIManager:close(dialog)
    end)
    UIManager.show = orig_show
    assert(ok, err)
end)

check("an ambiguous or mismatched book notifies the reader once and does not nag", function()
    local engine = assert(shell:engine())
    local md5 = util.partialMD5(path)
    local doc = assert(engine:edition(A_SHA, md5))
    local saved_identity, saved_work = doc.identity, doc.work_id
    doc.identity = "ambiguous"
    doc.work_id = nil
    assert(engine:saveBook(doc))

    local infos = {}
    local r = Liseur:new{ ui = {
        menu = { registerToMainMenu = function() end },
        document = { file = path, info = { has_pages = true } },
        paging = { getLastProgress = function() return 1 end, getLastPercent = function() return 0.1 end },
    } }
    r.info = function(_, text) infos[#infos + 1] = text end
    r:onReaderReady()
    eq(1, #infos, "the reader was not told on open that syncing is disabled for this book")
    assert(infos[1]:find("more than one work", 1, true), "the warning gave no reason: " .. tostring(infos[1]))
    -- Background jobs and page turns must not repeat the same modal warning.
    r:jobOpen(false, false)
    r:jobFlush()
    eq(1, #infos, "the warning was repeated during background sync")
    r:unscheduleAll()

    -- When ambiguity is discovered asynchronously during jobOpen, it also
    -- notifies once and no more.
    doc.identity = "unresolved"
    doc.work_id = nil
    assert(engine:saveBook(doc))
    infos = {}
    local r2 = Liseur:new{ ui = {
        menu = { registerToMainMenu = function() end },
        document = { file = path, info = { has_pages = true } },
        paging = { getLastProgress = function() return 1 end, getLastPercent = function() return 0.1 end },
    } }
    r2.info = function(_, text) infos[#infos + 1] = text end
    r2:onReaderReady()
    r2:unscheduleAll()
    eq(0, #infos, "an unresolved book warned before asking the server")
    local Identity = require("liseur_identity")
    local orig_resolve = Identity.resolve
    Identity.resolve = function(_api, b)
        b.identity = "mismatch"
        b.work_id = nil
        return nil, { kind = "mismatch", message = "this file's bytes differ from the catalogue copy" }
    end
    r2:jobOpen(false, false)
    r2:jobOpen(false, false)
    Identity.resolve = orig_resolve
    eq(1, #infos, "asynchronous mismatch did not warn exactly once")
    assert(infos[1]:find("differs from the catalogue copy", 1, true), "wrong reason: " .. tostring(infos[1]))
    r2:unscheduleAll()

    doc.identity = saved_identity
    doc.work_id = saved_work
    assert(engine:saveBook(doc))
end)

check("opening another book from inside the reader switches documents and saves the current book", function()
    local Catalog = require("liseur_catalog")
    local Positions = require("liseur_positions")
    local api = assert(shell:api())
    local folders = assert(Catalog.folders(api))
    local second
    for _i, b in ipairs(assert(Catalog.folderBooks(api, folders[1].folder_id))) do
        if b.book_id ~= book.book_id then second = b end
    end
    assert(second, "no second book in catalogue")
    local second_path
    shell:download(second, function(p) second_path = p end)
    run_tasks()
    assert(second_path and second_path ~= path, "second book path missing")

    local reader = ReaderUI:new{ dimen = Device.screen:getSize(), document = DocumentRegistry:openDocument(path) }
    local p = assert(reader.liseur, "plugin not loaded by ReaderUI")
    p.offer = function() end
    p.info = function() end
    local saved_plug = plug
    plug = p
    run_tasks()
    local s = assert(p.session, "the reader opened no session")
    local engine = s.engine
    reader:handleEvent(Event:new("GotoPage", 3))
    local switched_to
    local orig_switch = reader.switchDocument
    reader.switchDocument = function(self_ui, new_file)
        switched_to = new_file
        self_ui:onClose(false)
    end
    p:openFile(second_path)
    reader.switchDocument = orig_switch
    eq(second_path, switched_to, "openFile from ReaderUI did not switchDocument")
    eq(nil, p.session, "switching documents did not close the current session")
    local saved = assert(engine:edition(A_SHA, s.md5))
    assert(Positions.dirty(saved), "switching documents lost the unsent page turn")
    plug = saved_plug
    p:unscheduleAll()
    UIManager:close(reader)
end)

print(failures == 0 and "all reader checks passed" or (failures .. " reader check(s) failed"))
os.exit(failures == 0 and 0 or 1)
