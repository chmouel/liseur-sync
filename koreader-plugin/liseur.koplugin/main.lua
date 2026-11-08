--[[--
Liseur Sync client for KOReader: catalogue browsing and downloads, and
native reading-position sync against a Liseur Sync server.

@module koplugin.Liseur
--]]--

local ButtonDialog = require("ui/widget/buttondialog")
local ConfirmBox = require("ui/widget/confirmbox")
local DataStorage = require("datastorage")
local Dispatcher = require("dispatcher")
local Event = require("ui/event")
local InfoMessage = require("ui/widget/infomessage")
local InputDialog = require("ui/widget/inputdialog")
local LuaSettings = require("luasettings")
local NetworkMgr = require("ui/network/manager")
local TextViewer = require("ui/widget/textviewer")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local ffiUtil = require("ffi/util")
local lfs = require("libs/libkoreader-lfs")
local logger = require("logger")
local sha2 = require("ffi/sha2")
local util = require("util")
local _ = require("gettext")
local T = ffiUtil.template

local Network = require("liseur_network")
local Api = require("liseur_api")
local Downloads = require("liseur_downloads")
local Identity = require("liseur_identity")
local Positions = require("liseur_positions")
local Store = require("liseur_store")
local Transport = require("liseur_transport")
local LiseurUI = require("liseur_ui")

-- One value per KOReader process: a handover confirmed in this process
-- only takes effect after a restart, when the stock plugin has re-read
-- its own settings.
local PROCESS = package.loaded["liseur_process_token"] or { token = tostring(os.time()) .. "-" .. tostring(math.random(1, 2^30)) }
package.loaded["liseur_process_token"] = PROCESS

local SETTINGS_DIR = DataStorage:getSettingsDir()
local SETTINGS_FILE = SETTINGS_DIR .. "/liseur.lua"
local STATE_DIR = SETTINGS_DIR .. "/liseur"

local Liseur = WidgetContainer:extend{
    name = "liseur",
    is_doc_only = false,
}

local fs = {
    mkdir = function(path) util.makePath(path) end,
    fsync = function(f)
        local ok, res, err = pcall(ffiUtil.fsyncOpenedFile, f)
        if not ok then return false, res end
        return res, err
    end,
    fsync_dir = function(path)
        local ok, res, err = pcall(ffiUtil.fsyncDirectory, path)
        if not ok then return false, res end
        return res, err
    end,
    list = function(dir)
        local out = {}
        if lfs.attributes(dir, "mode") ~= "directory" then return out end
        for name in lfs.dir(dir) do out[#out + 1] = name end
        return out
    end,
}

local function exists(path)
    return lfs.symlinkattributes(path, "mode") ~= nil
end

-- now is the wall clock the state is written with.
local function now()
    return ffiUtil.getTimestamp and ffiUtil.getTimestamp() or os.time()
end

-- sha256_file returns the digest of the whole file, or nil when the file
-- can be opened but not read to the end. A failed read is not EOF: a digest
-- of the bytes that happened to arrive would name a file that was never
-- seen, so it is refused like an unreadable file.
local function sha256_file(path)
    local f = io.open(path, "rb")
    if not f then return nil end
    local update = sha2.sha256()
    while true do
        local chunk, err = f:read(64 * 1024)
        if not chunk then
            if err then f:close() return nil end
            break
        end
        update(chunk)
    end
    f:close()
    return update()
end

-- hashFile is the one place the whole-file digest is read, so a test can
-- make it fail the way a file being replaced under the reader does.
Liseur.hash_file = sha256_file

function Liseur:hashFile(path)
    return (self.hash_file or Liseur.hash_file)(path)
end

function Liseur:init()
    self.settings = LuaSettings:open(SETTINGS_FILE)
    self.topics = {}
    self.observe_task = function() self:observeNow() end
    self.save_task = function() self:saveSession() end
    self.flush_task = function()
        self.flush_scheduled = nil
        if NetworkMgr:isConnected() then self:schedule("flush") end
    end
    self:onDispatcherRegisterActions()
    self.ui.menu:registerToMainMenu(self)
    self:loadRefusal()
    self:adoptBacklog()
end

function Liseur:onDispatcherRegisterActions()
    Dispatcher:registerAction("liseur_library", { category = "none", event = "LiseurLibrary", title = _("Liseur library"), general = true })
    Dispatcher:registerAction("liseur_sync_now", { category = "none", event = "LiseurSyncNow", title = _("Liseur: sync this book now"), reader = true })
    Dispatcher:registerAction("liseur_status", { category = "none", event = "LiseurStatus", title = _("Liseur sync status"), general = true })
end

function Liseur:whenConnected(fn)
    NetworkMgr:runWhenConnected(function() Network.run(fn) end)
end

-- Settings ------------------------------------------------------------

function Liseur:cfg(key, default)
    return self.settings:readSetting(key, default)
end

function Liseur:setCfg(key, value)
    if value == nil then
        self.settings:delSetting(key)
    else
        self.settings:saveSetting(key, value)
    end
    self.settings:flush()
    -- The file holds a bearer secret; keep it private where the
    -- filesystem allows it.
    pcall(os.execute, "chmod 600 '" .. SETTINGS_FILE:gsub("'", "'\\''") .. "' 2>/dev/null")
end

function Liseur:base()
    return Api.parseBase(self:cfg("server_url"))
end

function Liseur:api(short)
    local base, err = self:base()
    if not base then return nil, err end
    local token = self:cfg("token")
    if not token or token == "" then return nil, _("No device token is set.") end
    local transport = Transport.new{
        background = true,
        ca_file = self:cfg("ca_file"),
        block = short and 4 or 10,
        total = short and 8 or 60,
    }
    return Api.new{ base = base, token = token, transport = transport }
end

function Liseur:probe()
    return self:cfg("probe")
end

function Liseur:hasScope(scope)
    local p = self:probe()
    if not p then return false end
    for _i, s in ipairs(p.scopes or {}) do
        if s == scope then return true end
    end
    return false
end

-- checkToken asks the server who this token is and remembers it. short
-- uses the brief timeouts for a probe on the way to something else. A
-- different account or device opens a different state namespace, so only
-- then is the open session detached; checking the same identity keeps the
-- session, and the queued work in it, alive.
function Liseur:checkToken(short)
    local api, err = self:api(short)
    if not api then return nil, { message = err } end
    local data, rerr = api:get("/v1/token")
    if not data then return nil, rerr end
    local scopes = {}
    for _i, s in ipairs(type(data.scopes) == "table" and data.scopes or {}) do
        if type(s) == "string" then scopes[#scopes + 1] = s end
    end
    local probe = {
        account_id = Api.present(data.account_id),
        device_id = Api.present(data.device_id),
        name = Api.present(data.name),
        scopes = scopes,
        server = api.base.url,
        checked_at = os.time(),
    }
    if type(probe.account_id) ~= "string" or type(probe.device_id) ~= "string" then
        return nil, { message = _("The server did not identify the account and device for this token.") }
    end
    local previous = self:probe()
    self:setCfg("probe", probe)
    if not previous or previous.server ~= probe.server or previous.account_id ~= probe.account_id
        or previous.device_id ~= probe.device_id then
        self:detachSession()
    end
    self.auth_failed = nil
    if not short then
        -- A permission the server took away is only known to be back once
        -- the token has been checked again, and only an explicit check is
        -- that: the pause is written to disk for the next instance, and a
        -- probe on the way to something else must not lift it.
        self.denied = nil
        self:setCfg("refusal", nil)
    end
    return probe
end

-- detachSession saves the open book under the engine it was loaded
-- with and forgets it, with every queued job. Used whenever the server,
-- token, account or device may have changed, so that nothing from one
-- identity is ever written or sent under another.
function Liseur:detachSession()
    self:unscheduleAll()
    self.topics = {}
    -- A refusal belongs to the identity that was refused, not to the one
    -- that replaces it.
    self.auth_failed = nil
    self.denied = nil
    self:setCfg("refusal", nil)
    local s = self.session
    if s then
        s.engine:saveBook(s.book)
        self.session = nil
    end
    self.engine_cache = nil
    self.download_index = nil
end

-- engine returns the position engine for the current server, account
-- and device, or nil and a reason.
local function probeFailure(err)
    if err and err.kind == "transport" then return _("the device could not reach the server") end
    if err and err.kind == "auth" then return _("the server refused this token") end
    return (err and err.message) or _("no answer from the server")
end

-- ensureProbe returns who this token is, asking the server once if it has
-- never been asked. The reader should not have to press Check connection
-- before anything works.
function Liseur:ensureProbe()
    local base, berr = self:base()
    if not base then return nil, berr end
    local probe = self:probe()
    if probe and probe.server == base.url then return probe end
    local fresh, err = self:checkToken(true)
    if not fresh then
        local why = T(_("Could not use this device token: %1. Liseur Sync → Settings → Check connection tries again."), probeFailure(err))
        self.probe_error = why
        return nil, why
    end
    self.probe_error = nil
    return fresh
end

-- engineFor builds the engine for a probe already known. It reads local
-- state and sends nothing, so it is safe with the network down.
local function engineFor(plugin, probe)
    local base = plugin:base()
    local ns = Api.namespace(base, probe.account_id)
    local cache = plugin.engine_cache
    if cache and cache.ns == ns and cache.device_id == probe.device_id then
        return cache.engine
    end
    local api, err = plugin:api()
    if not api then return nil, err end
    local engine = Positions.new{
        api = api,
        store = Store.new(STATE_DIR .. "/" .. ns, fs),
        now = now,
        md5 = sha2.md5,
        device_id = probe.device_id,
        account_id = probe.account_id,
    }
    local acc, aerr = engine:account()
    if not acc then
        -- A newer or unreadable account state pauses this namespace.
        return nil, aerr == "newer" and _("Liseur state on this device was written by a newer plugin version. Update the plugin.")
            or _("Liseur state on this device is unreadable.")
    end
    -- Reading local state only: a document left under the old partial-MD5
    -- key is moved to the edition its digest names, once. A migration that
    -- could not finish leaves the engine unavailable: no write may run on
    -- state whose destination was never proven.
    local migrated, merr = engine:migrateLegacy()
    if not migrated then
        return nil, merr == "newer" and _("Liseur state on this device was written by a newer plugin version. Update the plugin.")
            or _("Liseur state on this device is unreadable.")
    end
    plugin.engine_cache = { ns = ns, device_id = probe.device_id, server = base.url, engine = engine }
    return engine
end

function Liseur:engine()
    local probe, perr = self:ensureProbe()
    if not probe then return nil, perr end
    return engineFor(self, probe)
end

-- offlineEngine returns the engine when having it needs no request: a probe
-- already known for this server, or an engine already built. The cache is
-- dropped whenever the server, token or account may change, so it still
-- names this reader. The local screens read it with the network down.
function Liseur:offlineEngine()
    local base = self:base()
    if not base then return nil end
    local probe = self:probe()
    if probe then
        if probe.server ~= base.url then return nil end
        return engineFor(self, probe)
    end
    local cache = self.engine_cache
    if cache and cache.server == base.url then return cache.engine end
end

-- downloadIndex reads the download index for engine. offline keeps the
-- lookup to state already on this device: building an engine can ask
-- /v1/token, and a menu callback runs outside any coroutine, so it must
-- never wait on a request.
function Liseur:downloadIndex(engine, offline)
    engine = engine or self:offlineEngine()
    if not engine and not offline then engine = self:engine() end
    if not engine then return { books = {} } end
    if not self.download_index or self.download_index.engine ~= engine then
        self.download_index = { engine = engine, doc = engine.store:read("downloads", "index") or { books = {} } }
    end
    return self.download_index.doc
end

function Liseur:saveDownloadIndex(index)
    local engine = self:engine()
    if not engine then return end
    self.download_index = { engine = engine, doc = index }
    return engine.store:write("downloads", "index", index)
end

-- localPathFor returns the downloaded copy of a catalogue book that is
-- still on this device with the same bytes, if any. It reads local state
-- only, so a book detail dialog can be built without a request. A file
-- whose bytes changed is not that book any more, whatever its name, so
-- the recorded digest is read, not only the size. A digest read in this
-- run is reused while the file keeps its size and modification time.
-- quick compares the size alone, for a list badge that must not read
-- every book on the page.
function Liseur:localPathFor(book_id, quick)
    local index = self:downloadIndex(nil, true)
    local entry = index.books and index.books[book_id]
    local intact = Downloads.intact(entry, {
        size = function(path) return lfs.attributes(path, "size") end,
        sha256 = not quick and function(path)
            local attr = lfs.attributes(path)
            if not attr then return nil end
            self.file_digests = self.file_digests or {}
            local seen = self.file_digests[path]
            if seen and seen.size == attr.size and seen.mtime == attr.modification then return seen.sha256 end
            local sha = self:hashFile(path)
            self.file_digests[path] = sha and { size = attr.size, mtime = attr.modification, sha256 = sha } or nil
            return sha
        end or nil,
    })
    if intact then return entry.path end
end

-- Handover ------------------------------------------------------------

function Liseur:stockSyncAutomatic()
    -- The loaded plugin changes its setting in memory and writes it
    -- later, so ask it first.
    local live = self.ui and self.ui.kosync
    if type(live) == "table" and type(live.settings) == "table" and live.settings.auto_sync == true then
        return true
    end
    local ok, kosync = pcall(LuaSettings.open, LuaSettings, SETTINGS_DIR .. "/kosync.lua")
    if not ok or not kosync then return false end
    local s = kosync:readSetting("settings")
    return type(s) == "table" and s.auto_sync == true
end

-- rememberRefusal writes a pause to disk. KOReader builds a fresh plugin
-- for the file manager, so an in-memory flag would end the pause at the
-- reader's door, and the documented promise is that it lasts until Check
-- connection. The record names the identity that was refused: a token,
-- account or device it was not about never inherits it.
function Liseur:rememberRefusal(kind, message)
    local probe = self:probe()
    if not probe then return end
    self:setCfg("refusal", {
        kind = kind,
        message = message,
        server = probe.server,
        account_id = probe.account_id,
        device_id = probe.device_id,
    })
end

-- loadRefusal restores that pause, if what was written down is still
-- about the identity this instance works as.
function Liseur:loadRefusal()
    local stored = self:cfg("refusal")
    if type(stored) ~= "table" then return end
    local base, probe = self:base(), self:probe()
    local current = base and probe and base.url == probe.server
        and stored.server == base.url and stored.account_id == probe.account_id
        and stored.device_id == probe.device_id
    if not current then
        self:setCfg("refusal", nil)
        return
    end
    if stored.kind == "auth" then
        self.auth_failed = true
    else
        self.denied = stored.message or _("the server refused this permission")
    end
end

-- positionsActive answers without asking the server, so it can be called
-- from event handlers and menu items. An unasked token does not block: the
-- first job probes and reports what is wrong. A token known to lack sync
-- does block, and so does another plugin that already owns positions.
function Liseur:positionsActive()
    if self:cfg("positions_enabled") ~= true then return false end
    if self:cfg("handover_process") == PROCESS.token then return false end
    if self:stockSyncAutomatic() then return false end
    if not self:base() or not self:cfg("token") then return false end
    local probe = self:probe()
    if probe and not self:hasScope("sync") then return false end
    return true
end

function Liseur:positionsState()
    if self:cfg("positions_enabled") ~= true then return _("off") end
    if self:cfg("handover_process") == PROCESS.token then return _("on after KOReader restarts") end
    if self:stockSyncAutomatic() then return _("paused: Progress sync is set to sync automatically") end
    if self.denied then return _("paused: the server refused a permission; run Check connection") end
    if self.auth_failed then return _("paused: the server refused the token; run Check connection") end
    if not self:probe() then return _("on, not yet checked with the server") end
    if not self:hasScope("sync") then return _("paused: the token lacks the sync permission") end
    return _("on")
end

-- enablePositions asks the server what this token may do before it
-- offers to turn position sync on: a token pasted a moment ago has never
-- been checked, and the documented order (paste, then enable) must not
-- report a permission the token may well have. The probe runs inside the
-- network wrapper so the first /v1/token never blocks the reader.
function Liseur:enablePositions(menu)
    if self:stockSyncAutomatic() then
        self:info(_("Progress sync is set to sync automatically. Turn that off first (Progress sync → Automatically keep documents in sync), restart KOReader, then come back here.\n\nIts server settings stay as they are, so you can switch back later."))
        return
    end
    self:whenConnected(function()
        local probe, why = self:ensureProbe()
        if not probe then
            self:info(why)
            return
        end
        if not self:hasScope("sync") then
            self:info(_("This device token does not have the sync permission. Create a token with sync, then check the connection again."))
            return
        end
        UIManager:show(ConfirmBox:new{
            text = _("Liseur will write this device's reading positions to the server and offer positions from your other devices.\n\nKeep Progress sync's automatic sync off while this is on, so only one of them writes. Position sync starts after KOReader restarts."),
            ok_text = _("Turn on"),
            ok_callback = function()
                self:setCfg("positions_enabled", true)
                self:setCfg("handover_process", PROCESS.token)
                if menu then menu:updateItems() end
                self:info(_("Restart KOReader to start position sync."))
            end,
        })
    end)
end

function Liseur:disablePositions()
    self:setCfg("positions_enabled", false)
    self:setCfg("handover_process", nil)
    self:detachSession()
    self:info(_("Position sync is off. Unsent positions are kept and will be offered again if you turn it back on. To return to Progress sync, turn its automatic sync back on."))
end

-- Reading session -----------------------------------------------------

function Liseur:currentPosition()
    local doc = self.ui.document
    if not doc then return nil end
    if doc.info.has_pages then
        local page = self.ui.paging:getLastProgress()
        return { progression = self.ui.paging:getLastPercent(), xpointer = page and tostring(page) }
    end
    return { progression = self.ui.rolling:getLastPercent(), xpointer = self.ui.rolling:getLastProgress() }
end

function Liseur:failOpen(reason)
    self.session_error = reason
    self:info(T(_("This book cannot be synced: %1"), reason))
end

function Liseur:warnDisabled(s)
    if not s or s.warned_disabled then return end
    local b = s.book
    if s.stale or b.identity == "ambiguous" or b.identity == "mismatch" then
        s.warned_disabled = true
        self:info(T(_("This book cannot be synced: %1"), self:identityText(b, s.stale)))
    end
end

function Liseur:onReaderReady()
    self.session = nil
    if not self.ui.document or not self:positionsActive() then return end
    -- KOReader keeps the first fingerprint it computed for a file in the
    -- sidecar, even after the file is replaced, so the bytes are read again
    -- here. The sidecar is left alone.
    local file = self.ui.document.file
    local md5 = util.partialMD5(file)
    if not md5 then
        self:failOpen(_("this file has no KOReader fingerprint yet; reopen it"))
        return
    end
    local engine, eerr = self:engine()
    if not engine then
        logger.warn("Liseur: no session for this book:", eerr)
        self:failOpen(eerr or _("could not prepare position sync"))
        return
    end
    local fp, fperr = engine:fingerprint(md5)
    if not fp then
        logger.warn("Liseur: fingerprint state unreadable:", fperr)
        self:failOpen(_("Liseur state on this device is unreadable"))
        return
    end
    local attr = lfs.attributes(file)
    if not attr then
        self:failOpen(_("the bytes of this file could not be read"))
        return
    end
    local stat = { size = attr.size, mtime = attr.modification }
    -- No verified digest means no session and nothing written: attaching
    -- this file to whatever document the partial MD5 used to name would
    -- hand its reading to another file's work. The bytes are read here,
    -- never taken from the cache: a replacement with the same size and
    -- mtime is still different bytes.
    local sha = Identity.locate(file, function(p) return self:hashFile(p) end)
    if not sha then
        logger.warn("Liseur: this file's bytes could not be read; not syncing it")
        self:failOpen(_("the bytes of this file could not be read"))
        return
    end
    self.session_error = nil
    -- The cache is written before the document it leads to: it only grows.
    engine:noteFile(fp, file, sha, stat)
    local saved, serr = engine:saveFingerprint(md5, fp)
    if not saved then
        logger.warn("Liseur: fingerprint state could not be saved:", serr)
        self:failOpen(_("fingerprint state could not be saved"))
        return
    end
    local book, err = engine:edition(sha, md5, fp)
    if not book then
        logger.warn("Liseur: book state unreadable:", err)
        self:failOpen(_("Liseur state on this device is unreadable"))
        return
    end
    book.path, book.size, book.mtime = file, stat.size, stat.mtime
    local pos = self:currentPosition()
    if pos then engine:observe(book, pos, true) end
    engine:saveBook(book)
    self.session = { md5 = md5, sha = sha, file = file, book = book, engine = engine }
    if book.identity == "ambiguous" or book.identity == "mismatch" then
        self:warnDisabled(self.session)
        return
    end
    if NetworkMgr:isConnected() then self:schedule("open") end
end

function Liseur:noteMove()
    if not self.session or self.applying then return end
    UIManager:unschedule(self.observe_task)
    UIManager:scheduleIn(1, self.observe_task)
end

function Liseur:onPageUpdate() self:noteMove() end
function Liseur:onPosUpdate() self:noteMove() end

function Liseur:observeNow(no_schedule)
    local s = self.session
    if not s or self.applying then return end
    local pos = self:currentPosition()
    if pos and s.engine:observe(s.book, pos) and not no_schedule then
        UIManager:unschedule(self.save_task)
        UIManager:scheduleIn(5, self.save_task)
        if not self.flush_scheduled then
            self.flush_scheduled = true
            UIManager:scheduleIn(30, self.flush_task)
        end
    end
end

function Liseur:saveSession()
    local s = self.session
    if not s then return end
    local ok, err = s.engine:saveBook(s.book)
    if not ok then
        logger.warn("Liseur: cannot save reading state:", err)
        s.storage_error = err
    end
end

function Liseur:unscheduleAll()
    self.flush_scheduled = nil
    UIManager:unschedule(self.observe_task)
    UIManager:unschedule(self.save_task)
    UIManager:unschedule(self.flush_task)
    self:cancelRetry()
end

-- adoptBacklog hands this instance the work left behind on disk. KOReader
-- builds a fresh plugin for the file manager rather than carrying the
-- reader's over, so a flush queued by a closing reader dies with it, and so
-- does a deadline it armed; init is the last moment anything can notice
-- either. The deadline itself is honoured, so nothing is asked early, and a
-- device that is offline wakes nothing: the connection event drains it.
function Liseur:adoptBacklog()
    if self.closed or not self:positionsActive() or not NetworkMgr:isConnected() then return end
    local engine = self:offlineEngine()
    if not engine or self:suspended() then return end
    self:scheduleRetry(engine)
    if self:backlogDue(engine) then self:schedule("flush") end
end

-- backlogDue reports whether this instance owes work that no timer would
-- ever fire for: an upload frozen without a deadline, or reading this device
-- recorded and never prepared. It is asked once, when an instance starts, so
-- a book that can never be resolved costs one job and not a loop.
function Liseur:backlogDue(engine)
    if not engine then return false end
    local function due(p)
        return type(p) == "table" and p.device_id == engine.device_id
            and (type(p.not_before) ~= "number" or p.not_before <= now())
    end
    for _i, book in ipairs(engine:documents()) do
        if due(book.pending) then return true end
        for _j, p in ipairs(book.retired or {}) do
            if due(p) then return true end
        end
        -- Reading this device never uploaded is the other thing no deadline
        -- describes. A book whose identity is unclear has nothing to send it
        -- to, so it is not a reason to wake up.
        if not book.legacy_key and book.identity ~= "ambiguous" and book.identity ~= "mismatch"
            and Positions.dirty(book) then
            return true
        end
    end
    return false
end

-- suspended reports whether this instance must write nothing more: the
-- reader that owned it is gone, or the server refused its token or a
-- permission. It is the cheap half of the coordinator's gate, so it can be
-- asked between two jobs.
function Liseur:suspended()
    return self.closed == true or self.auth_failed ~= nil or self.denied ~= nil
end

-- cancelRetry drops the timer set for a frozen upload's backoff.
function Liseur:cancelRetry()
    if self.retry_task then
        UIManager:unschedule(self.retry_task)
        self.retry_task = nil
    end
    self.retry_at = nil
end

-- nextRetryAt returns the earliest moment an upload frozen on this device
-- may be tried again. An upload paused under another device identity is
-- not ours to retry, and a refused token or a refused permission
-- suspends all background work.
function Liseur:nextRetryAt(engine)
    if not engine or self.auth_failed or self.denied then return nil end
    local at
    local function consider(p)
        if type(p) ~= "table" or p.device_id ~= engine.device_id then return end
        local nb = p.not_before
        if type(nb) ~= "number" then return end
        if not at or nb < at then at = nb end
    end
    for _i, book in ipairs(engine:documents()) do
        consider(book.pending)
        for _j, p in ipairs(book.retired or {}) do consider(p) end
    end
    return at
end

-- scheduleRetry arranges the one retry a timed-out or rate-limited upload
-- needs. Without it a book closed after such an answer stays pending until
-- the reader moves again. The coordinator arms it once per backoff and the
-- retry re-arms it only if the next attempt fails too. A reader that has
-- been torn down arms nothing: its timer would outlive it. engine defaults
-- to the one this instance already built; a plugin that has not served a
-- request yet passes the one it can read from local state.
function Liseur:scheduleRetry(engine)
    if self.closed or not self:positionsActive() then return end
    engine = engine or (self.engine_cache and self.engine_cache.engine)
    local at = self:nextRetryAt(engine)
    if not at then
        -- Nothing may be asked again while a refusal is in force.
        self:cancelRetry()
        return
    end
    -- An armed timer is not the same deadline: an upload frozen a moment
    -- later with a shorter backoff must not wait behind the one already
    -- waiting, so an earlier deadline replaces the timer.
    if self.retry_task and self.retry_at and self.retry_at <= at then return end
    self:cancelRetry()
    local delay = math.max(1, at - now())
    self.retry_at = at
    self.retry_task = function()
        self.retry_task, self.retry_at = nil, nil
        if NetworkMgr:isConnected() then self:schedule("flush") end
    end
    UIManager:scheduleIn(delay, self.retry_task)
end

function Liseur:onCloseDocument()
    if not self.session then return end
    self:unscheduleAll()
    self:observeNow(true)
    self:saveSession()
    self.topics = {}
    self.session = nil
    if NetworkMgr:isConnected() then self:schedule("flush") end
end

function Liseur:onSuspend()
    if not self.session then return end
    self:unscheduleAll()
    self:observeNow(true)
    self:saveSession()
    self.topics = {}
end

function Liseur:onResume()
    if self.session and NetworkMgr:isConnected() then
        UIManager:scheduleIn(2, function() self:schedule("open") end)
    end
end

function Liseur:onNetworkConnected()
    self:schedule("flush")
end

function Liseur:onCloseWidget()
    -- The reader that owned this instance is gone. Its already queued job
    -- still runs once, but nothing new may be queued for it and no timer
    -- may be left behind, or a dead instance would keep talking to the
    -- server. Unsent work stays on disk for the file manager's instance.
    self.closed = true
    self:unscheduleAll()
end

-- Coordinator: one network job at a time, urgent topics first ----------

local ORDER = { "open", "flush" }

function Liseur:schedule(topic)
    if self.closed then return end
    self.topics[topic] = true
    if self.running then return end
    UIManager:nextTick(function() Network.run(function() self:runTopics() end) end)
end

function Liseur:runTopics()
    if self.running then return end
    if self:suspended() or not self:positionsActive() then
        -- A torn-down reader writes nothing, and a refused token or a
        -- refused permission pauses background work until the connection
        -- is checked again; position work runs only while this plugin owns
        -- positions.
        self.topics = {}
        return
    end
    self.running = true
    local guard = 0
    while guard < 10 do
        guard = guard + 1
        local topic
        for _i, t in ipairs(ORDER) do
            if self.topics[t] then topic = t break end
        end
        if not topic then break end
        self.topics[topic] = nil
        if self:suspended() then
            -- Asking again inside the same drain is exactly what the pause
            -- promises not to do: the job that just ran was refused, and
            -- the next one would be refused too.
            self.topics = {}
            break
        end
        local ok, err = pcall(topic == "open" and self.jobOpen or self.jobFlush, self)
        if not ok then logger.warn("Liseur: sync job failed:", err) end
    end
    self.running = false
    self:scheduleRetry()
end

function Liseur:noteError(book, err)
    if not err then return end
    book.last_error = err.message
    if err.kind == "auth" then
        self.auth_failed = true
        self:rememberRefusal("auth", err.message)
    elseif err.kind == "forbidden" then
        -- A permission the server takes away does not come back by asking
        -- again, and the retry deadline stored for an earlier timeout has
        -- already passed: retrying would only ask, and be refused, once a
        -- second. Wait for an explicit check of the connection instead.
        self.denied = err.message or _("the server refused this permission")
        self:rememberRefusal("forbidden", self.denied)
    end
end

-- verifySession re-reads the bytes of the open book. A digest that cannot
-- be read, or that is no longer this edition's, leaves the session unable to
-- write: the old work and its position must never be offered to other bytes.
function Liseur:verifySession(s)
    local file = s.file
    local attr = file and lfs.attributes(file)
    if not attr then return false end
    local stat = { size = attr.size, mtime = attr.modification }
    local fp = s.engine:fingerprint(s.book.md5)
    -- Re-read the bytes: a file replaced under the same size and mtime is
    -- new content, and its reading must not reach the old work.
    local sha = Identity.locate(file, function(p) return self:hashFile(p) end)
    if not sha or sha ~= s.sha or s.book.sha256 ~= sha then
        s.stale = true
        s.ready = false
        return false
    end
    local cached = fp and fp.files and fp.files[file]
    if fp and (not cached or cached.size ~= stat.size or cached.mtime ~= stat.mtime) then
        s.engine:noteFile(fp, file, sha, stat)
        s.engine:saveFingerprint(s.book.md5, fp)
    end
    return true
end

-- ensureIdentity resolves the open book to a work, once, and starts
-- following that work's positions.
function Liseur:ensureIdentity(s)
    local book, engine = s.book, s.engine
    if not book.book_id then
        local index = self:downloadIndex()
        for book_id, entry in pairs(index.books or {}) do
            if entry.sha256 == book.sha256 then book.book_id = book_id end
        end
    end
    if not Identity.writable(book) then
        if book.identity == "ambiguous" or book.identity == "mismatch" then return false end
        engine:markUntrusted(book)
        local ok, err = Identity.resolve(engine.api, book)
        engine:saveBook(book)
        if not ok then
            self:noteError(book, err)
            return false
        end
    end
    local acc = engine:account()
    if not acc then return false end
    if engine:track(book.work_id) or not acc.works[book.work_id].seeded then
        local ok, err = engine:seed(book.work_id)
        if not ok then
            self:noteError(book, err)
            return false
        end
    end
    return true
end

-- settleBook delivers what is frozen for a book: retired uploads for
-- bytes it no longer has, then its own pending upload. Nothing is sent
-- unless the document holding it was just written durably.
function Liseur:settleBook(engine, book)
    if not book.pending and not book.retired then return true end
    local saved, serr = engine:saveBook(book)
    if not saved then
        book.last_error = serr
        return false
    end
    if book.retired then
        local ok, err = engine:sendRetired(book)
        engine:saveBook(book)
        if not ok then
            self:noteError(book, err)
            return false
        end
    end
    if not book.pending then return true end
    local status, err = engine:send(book)
    engine:saveBook(book)
    if not status then
        self:noteError(book, err)
        return false
    end
    return status == "acked" or status == "renamed" or status == "refused" or status == "unknown_work"
end

-- afterSettle keeps a session honest after an upload was settled. The
-- server answering that it does not know the work drops the mapping, and
-- a session left ready would never resolve that book again: nothing could
-- be sent, and every later flush would run into the same wall. Readiness
-- goes, the local observation stays, and the open job resolves and seeds
-- the book before anything else is uploaded.
function Liseur:afterSettle(s)
    if not s or Identity.writable(s.book) then return end
    s.ready = false
    self.topics["open"] = true
end

-- push sends the current book's local position when the decision says
-- this device owes it. The op is persisted before it is sent.
function Liseur:push(engine, book)
    if not engine:prepare(book) then return end
    local ok, err = engine:saveBook(book)
    if not ok then
        Positions.restorePeak(book, book.pending)
        book.pending = nil
        book.last_error = err
        return
    end
    self:settleBook(engine, book)
end

function Liseur:exactFor()
    local doc = self.ui.document
    if not doc then return nil end
    return function(remote, here)
        if doc.info.has_pages then
            local a, b = tonumber(remote), tonumber(here)
            if a and b then return a == b end
            return nil
        end
        if not doc:isXPointerInDocument(remote) then return nil end
        local ok1, p1 = pcall(doc.getPageFromXPointer, doc, remote)
        local ok2, p2 = pcall(doc.getPageFromXPointer, doc, here)
        if ok1 and ok2 and p1 and p2 then return p1 == p2 end
        return nil
    end
end

-- jobOpen resolves the open book, settles any upload in flight, follows
-- the feed and reconciles. quiet records an offer instead of prompting.
function Liseur:jobOpen(interactive, quiet)
    local s = self.session
    if not s or not self:positionsActive() or s.engine ~= self:engine() then return end
    -- The request boundary, not only the coordinator: an explicit sync asks
    -- this job directly, and a refusal recorded while it waited for the
    -- network is still in force here.
    if self:suspended() then
        if interactive then self:info(T(_("Position sync is %1."), self:positionsState())) end
        return
    end
    local book, engine = s.book, s.engine
    if book.retired and not self:settleBook(engine, book) then
        -- The frozen bytes could not be delivered; whatever the reason,
        -- the book's new state must not be sent ahead of them.
        if interactive then self:info(T(_("A previous upload is still waiting: %1"), book.last_error or _("network"))) end
        return
    end
    if not self:verifySession(s) then
        if interactive then
            self:info(T(_("This book cannot be synced yet: %1"), self:identityText(book, s.stale)))
        else
            self:warnDisabled(s)
        end
        return
    end
    if not self:ensureIdentity(s) then
        if interactive then
            self:info(T(_("This book cannot be synced yet: %1"), self:identityText(book)))
        else
            self:warnDisabled(s)
        end
        return
    end
    if book.pending and not self:settleBook(engine, book) then
        if interactive then self:info(T(_("A previous upload is still waiting: %1"), book.last_error or _("network"))) end
        return
    end
    if not Identity.writable(book) and not self:ensureIdentity(s) then
        -- Settling the upload can retire the mapping (the server answered
        -- that it does not know that work). Resolve and seed again, or the
        -- session would stay ready but unable to write anything.
        if interactive then
            self:info(T(_("This book cannot be synced yet: %1"), self:identityText(book)))
        else
            self:warnDisabled(s)
        end
        return
    end
    local ok, err = engine:pull()
    if not ok then
        self:noteError(book, err)
        if interactive then self:info(T(_("Could not read positions from the server: %1"), err.message or "")) end
        return
    end
    if err == "partial" or self.session ~= s or not self:positionsActive() then return end
    s.ready = true
    self:act(engine, book, engine:decide(book, self:exactFor()), not quiet, interactive)
    if not Identity.writable(book) then
        self:afterSettle(s)
    end
    engine:saveBook(book)
end

-- jobFlush settles uploads and sends the open book's position when it
-- is owed. closing keeps it to the open book, one feed page and short
-- timeouts, so closing a book never waits on a backlog.
function Liseur:jobFlush(closing)
    if not self:positionsActive() then return end
    local engine = self:engine()
    if not engine then return end
    local s = self.session
    if s and s.engine ~= engine then
        self.session = nil
        return
    end
    local saved_api = engine.api
    if closing then engine.api = self:api(true) or saved_api end
    local ok, err = pcall(self.flushWith, self, engine, closing)
    engine.api = saved_api
    if not ok then error(err) end
end

function Liseur:flushWith(engine, closing)
    local s = self.session
    if not closing then
        for _i, book in ipairs(engine:documents()) do
            -- The backlog is a second way to write a lot at once, so a
            -- refusal stops it as promptly as it stops the topic loop.
            if self:suspended() then return end
            -- A disk copy of the open book must never be loaded and saved
            -- over the copy in memory, so the edition it is open as is
            -- skipped by its digest.
            if not s or book.sha256 ~= s.sha then
                local settled = self:settleBook(engine, book)
                if settled and Positions.dirty(book) then
                    -- An edition's identity is its digest. The file it last
                    -- named may be gone, and that is not this document's
                    -- business; a legacy leftover has no digest to resolve.
                    local closed = { book = book, engine = engine, file = book.path, sha = book.sha256 }
                    if not book.legacy_key and self:ensureIdentity(closed) then
                        local ok, err = engine:pull()
                        if ok and err ~= "partial" then
                            self:act(engine, book, engine:decide(book), false, false)
                            engine:saveBook(book)
                        elseif not ok then
                            self:noteError(book, err)
                        end
                    end
                end
            end
        end
    end
    if not s then return end
    -- The backlog stopped as soon as a refusal was recorded, and the open
    -- book's own upload is the very next request: it is gated the same way.
    if self:suspended() then return end
    if s.book.retired and not self:settleBook(engine, s.book) then return end
    if not s.ready then
        -- Never upload a book's first position before reading the
        -- server's.
        if closing then return end
        return self:jobOpen(false, true)
    end
    if not self:verifySession(s) then
        self:warnDisabled(s)
        return
    end
    if Identity.writable(s.book) and Positions.dirty(s.book) then
        if s.book.pending and not self:settleBook(engine, s.book) then return end
        if not Identity.writable(s.book) then return self:afterSettle(s) end
        local ok, err = engine:pull(closing and 1 or nil)
        if not ok then
            self:noteError(s.book, err)
            return
        end
        -- Deciding against a feed that is not caught up could send a
        -- position over one this device has not seen yet.
        if err == "partial" or self.session ~= s or not self:positionsActive() then return end
        self:act(engine, s.book, engine:decide(s.book, self:exactFor()), false, false)
        engine:saveBook(s.book)
        if not Identity.writable(s.book) then self:afterSettle(s) end
    end
end

-- act carries out a merge decision. A remote position is only ever
-- offered, never applied silently, and only when prompting is allowed
-- (opening, resuming, or an explicit sync).
function Liseur:act(engine, book, d, may_prompt, interactive)
    if d.kind == "push" then
        self:push(engine, book)
        if interactive then self:info(book.pending and _("Position upload is waiting for the network.") or _("Position sent.")) end
    elseif d.kind == "pull" or d.kind == "conflict" then
        book.offer = d.op and d.op.op_id
        if may_prompt and d.op then self:offer(engine, book, d) end
    elseif d.kind == "in_sync" then
        if d.op and engine:accept(book, d.op) then book.acked = book.revision end
        book.offer = nil
        if interactive then self:info(_("This book is in sync.")) end
    end
end

local function percent(p)
    if not p then return "?" end
    return string.format("%.1f%%", p * 100)
end

local function when(ts)
    if type(ts) ~= "string" then return "" end
    return ts:sub(1, 16):gsub("T", " ") .. " UTC"
end

function Liseur:offer(engine, book, d)
    local op = d.op
    local exact = Positions.remoteLocation(book, op) ~= nil
    local here = book["local"] and book["local"].progression
    local text
    if d.kind == "conflict" then
        text = T(_("This book was read here and on another device since they last agreed.\n\nOther device: %1 (%2)\nThis device: %3"), percent(op.progression), when(op.client_ts), percent(here))
    else
        text = T(_("Another device is at a different place in this book.\n\nOther device: %1 (%2)\nThis device: %3"), percent(op.progression), when(op.client_ts), percent(here))
    end
    if not exact then
        text = text .. "\n\n" .. _("The other position can only be reached approximately, by percentage.")
    end
    local dialog
    dialog = ButtonDialog:new{
        title = text,
        buttons = {
            {
                {
                    text = T(_("Go to %1"), percent(op.progression)),
                    callback = function()
                        UIManager:close(dialog)
                        self:goTo(engine, book, op)
                    end,
                },
            },
            {
                {
                    text = _("Keep this position"),
                    callback = function()
                        UIManager:close(dialog)
                        if engine:keep(book, op) then
                            book.offer = nil
                            engine:saveBook(book)
                            self:schedule("flush")
                        end
                    end,
                },
            },
        },
    }
    UIManager:show(dialog)
end

function Liseur:jump(location, progression)
    local doc = self.ui.document
    if location and doc.info.has_pages and tonumber(location) then
        self.ui:handleEvent(Event:new("GotoPage", tonumber(location)))
    elseif location and not doc.info.has_pages and doc:isXPointerInDocument(location) then
        self.ui:handleEvent(Event:new("GotoXPointer", location))
    else
        self.ui:handleEvent(Event:new("GotoPercent", progression * 100))
    end
end

function Liseur:goTo(engine, book, op)
    if not self.session or self.session.book ~= book then return end
    if not engine:accept(book, op) then
        self:info(_("A newer position arrived meanwhile. Sync again to see it."))
        return
    end
    self:unscheduleAll()
    self.applying = true
    self:jump(Positions.remoteLocation(book, op), op.progression)
    UIManager:nextTick(function()
        local pos = self:currentPosition()
        if pos and self.session and self.session.book == book then
            engine:settle(book, pos)
            book.offer = nil
            engine:saveBook(book)
        end
        self.applying = false
    end)
end

-- User-chosen position from the history list: a deliberate move, so it
-- becomes this device's position and is sent.
function Liseur:goToChosen(op, book)
    self:unscheduleAll()
    self:jump(Positions.remoteLocation(book, op), op.progression)
    UIManager:nextTick(function() self:observeNow() self:saveSession() self:schedule("flush") end)
end

-- Commands ------------------------------------------------------------

function Liseur:info(text)
    UIManager:show(InfoMessage:new{ text = text })
end

function Liseur:identityText(book, stale)
    if stale then return _("this file changed on disk; reopen it to sync it") end
    if book.identity == "resolved" then return _("matched to a work on the server") end
    if book.identity == "ambiguous" then return _("this file matches more than one work; it is not synced") end
    if book.identity == "mismatch" then return _("this file differs from the catalogue copy; it is not synced") end
    return book.last_error or _("not matched yet")
end

function Liseur:onLiseurSyncNow()
    if not self.ui.document then return end
    if not self:positionsActive() then
        self:info(T(_("Position sync is %1."), self:positionsState()))
        return
    end
    -- An explicit sync is not a way around a pause: the pause is exactly
    -- what the reader must resolve with Check connection first.
    if self:suspended() then
        self:info(T(_("Position sync is %1."), self:positionsState()))
        return
    end
    if not self.session then
        self:info(self.session_error and T(_("This book cannot be synced: %1"), self.session_error)
            or _("This book has no KOReader fingerprint yet; reopen it."))
        return
    end
    self:whenConnected(function()
        if self:suspended() then
            self:info(T(_("Position sync is %1."), self:positionsState()))
            return
        end
        self:jobOpen(true)
    end)
end

function Liseur:onLiseurLibrary()
    LiseurUI.showLibrary(self)
end

function Liseur:onLiseurStatus()
    LiseurUI.showStatus(self)
end

function Liseur:showBookPanel()
    local s = self.session
    local lines = {}
    if not s then
        lines[#lines + 1] = T(_("Position sync: %1"), self:positionsState())
    else
        local b = s.book
        local remote = s.engine:remote(b)
        lines[#lines + 1] = T(_("Identity: %1"), self:identityText(b))
        lines[#lines + 1] = T(_("This device: %1"), percent(b["local"] and b["local"].progression))
        if remote then
            lines[#lines + 1] = T(_("Newest on server: %1 (%2)%3"), percent(remote.progression), when(remote.client_ts),
                remote.device_id == s.engine.device_id and (" " .. _("from this device")) or "")
        end
        lines[#lines + 1] = T(_("Unsent change: %1"), Positions.dirty(b) and _("yes") or _("no"))
        if b.pending then
            lines[#lines + 1] = b.pending.device_id ~= s.engine.device_id
                and _("An upload made under another device identity is paused.")
                or _("An upload is waiting to be confirmed.")
        end
        if b.last_sync then lines[#lines + 1] = T(_("Last confirmed: %1"), os.date("%Y-%m-%d %H:%M", math.floor(b.last_sync))) end
        if b.last_error then lines[#lines + 1] = T(_("Last problem: %1"), b.last_error) end
    end
    UIManager:show(TextViewer:new{ title = _("Liseur: this book"), text = table.concat(lines, "\n") })
end

function Liseur:showHistory()
    local s = self.session
    if not s or not Identity.writable(s.book) then
        self:info(_("This book is not matched to a work on the server yet."))
        return
    end
    self:whenConnected(function()
        local h, err = s.engine:history(s.book.work_id)
        if not h then
            self:info(T(_("Could not read the position history: %1"), err.message or ""))
            return
        end
        LiseurUI.showHistory(self, s, h)
    end)
end

function Liseur:forgetFurthest(work_id, upto_seq)
    local engine = self:engine()
    if not engine then return end
    local floor, err = engine:forgetFurthest(work_id, upto_seq)
    if not floor then
        self:info(T(_("Could not forget the furthest position: %1"), err.message or ""))
    else
        self:info(_("That furthest position will no longer be suggested. Your reading history is unchanged."))
    end
end

function Liseur:discardPaused()
    local s = self.session
    if s and s.engine:discardPaused(s.book) then
        s.engine:saveBook(s.book)
        self:schedule("flush")
        self:info(_("The paused upload was dropped; this device's position will be sent again under the current device."))
    end
end

-- Downloads -------------------------------------------------------------

function Liseur:downloadDir()
    local dir = self:cfg("download_dir") or G_reader_settings:readSetting("download_dir") or G_reader_settings:readSetting("home_dir")
    if dir and lfs.attributes(dir, "mode") == "directory" then return dir end
end

function Liseur:download(book, callback)
    local dir = self:downloadDir()
    if not dir then
        self:info(_("Choose a download folder first (Liseur Sync → Settings → Download folder)."))
        return
    end
    local existing = self:localPathFor(book.book_id)
    if existing then
        callback(existing)
        return
    end
    self:whenConnected(function()
        local api, aerr = self:api()
        if not api then
            self:info(aerr)
            return
        end
        local msg = InfoMessage:new{ text = T(_("Downloading %1…"), book.title) }
        UIManager:show(msg)
        UIManager:forceRePaint()
        local path, err = Downloads.download(api, book, dir, {
            exists = exists,
            sha256 = sha2.sha256,
            fsync = fs.fsync,
            worker = Network.worker,
        })
        UIManager:close(msg)
        if not path then
            self:info(T(_("Download failed: %1"), err.message or ""))
            return
        end
        local index = self:downloadIndex()
        index.books = index.books or {}
        index.books[book.book_id] = { path = path, sha256 = book.sha256, size = book.size_bytes, title = book.title, at = os.time() }
        local indexed, ierr = self:saveDownloadIndex(index)
        if not indexed then logger.warn("Liseur: download index not saved:", ierr) end
        local engine = self:engine()
        local md5 = util.partialMD5(path)
        if engine and md5 then
            -- The bytes were just verified against the catalogue, so that
            -- digest is recorded as the file's own: it leads to this file's
            -- edition and never to another file's, whatever the partial MD5
            -- they share.
            local sha = Identity.verified(book.sha256)
            local attr = lfs.attributes(path)
            local fp = engine:fingerprint(md5)
            if not fp then sha = nil end
            if sha then
                if attr then engine:noteFile(fp, path, sha, { size = attr.size, mtime = attr.modification }) end
                engine:saveFingerprint(md5, fp)
                local s = self.session
                if s and s.engine == engine and s.sha == sha then
                    s.book.path = path
                    if attr then s.book.size, s.book.mtime = attr.size, attr.modification end
                    s.book.book_id = book.book_id
                    engine:saveBook(s.book)
                else
                    local doc = engine:edition(sha, md5, fp)
                    if doc then
                        doc.path = path
                        doc.book_id = book.book_id
                        engine:saveBook(doc)
                    end
                end
            end
        end
        callback(path)
    end)
end

function Liseur:openFile(path)
    if self.browser then
        UIManager:close(self.browser)
        self.browser = nil
    end
    local filemanagerutil = require("apps/filemanager/filemanagerutil")
    filemanagerutil.openFile(self.ui, path, nil, true)
end

-- Menu ----------------------------------------------------------------

function Liseur:addToMainMenu(menu_items)
    menu_items.liseur = {
        text = _("Liseur Sync"),
        sorting_hint = "tools",
        sub_item_table_func = function() return LiseurUI.mainMenu(self) end,
    }
end

function Liseur:editSetting(title, key, hint, on_save, menu, opts)
    opts = opts or {}
    local dialog
    local previous = self:cfg(key)
    dialog = InputDialog:new{
        title = title,
        input = previous or "",
        input_hint = hint,
        text_type = opts.text_type,
        buttons = {
            {
                { text = _("Cancel"), id = "close", callback = function() UIManager:close(dialog) end },
                {
                    text = _("Save"),
                    is_enter_default = true,
                    callback = function()
                        local v = dialog:getInputText():gsub("^%s+", ""):gsub("%s+$", "")
                        UIManager:close(dialog)
                        if on_save then
                            local ok, msg = on_save(v)
                            if not ok then
                                self:info(msg)
                                return
                            end
                        end
                        self:setCfg(key, v ~= "" and v or nil)
                        if key == "server_url" or key == "token" then
                            -- A new server or token may be another account
                            -- or device: nothing continues until it is
                            -- checked. An unchanged value changes nothing,
                            -- so the open session and its queued work stay.
                            if (previous or "") ~= v then
                                self:setCfg("probe", nil)
                                self:detachSession()
                            end
                        elseif key == "ca_file" then
                            -- A bundle change is not an identity change, so
                            -- the session keeps running; only the next
                            -- request has to use the new bundle.
                            local engine = self.engine_cache and self.engine_cache.engine
                            local api = engine and self:api()
                            if api then engine.api = api end
                        end
                        if menu then menu:updateItems() end
                    end,
                },
            },
        },
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function Liseur:checkConnection(menu)
    self:whenConnected(function()
        local previous = self:probe()
        local probe, err = self:checkToken()
        if not probe then
            local hint = ""
            if err and err.kind == "auth" then hint = "\n\n" .. _("The token was refused. Create a new device token on the server.") end
            self:info(T(_("Connection failed: %1"), (err and err.message) or "") .. hint)
            return
        end
        -- The check is also the recovery action the pause message names,
        -- and KOReader's runWhenConnected sends no event on a device that
        -- is already online: the backlog has to be woken here or not at all.
        if self:positionsActive() and NetworkMgr:isConnected() then self:schedule("flush") end
        local text = T(_("Connected to %1\nDevice: %2 (%3)\nPermissions: %4"), probe.server, probe.name or "?", probe.device_id, table.concat(probe.scopes, ", "))
        if previous and previous.account_id ~= probe.account_id then
            text = text .. "\n\n" .. _("This is a different account from before. Its reading state is kept separately; nothing from the previous account will be sent to it.")
        elseif previous and previous.device_id ~= probe.device_id then
            text = text .. "\n\n" .. _("This token belongs to a different device than before. Uploads prepared under the old device are paused until you resend them from this book's Liseur menu.")
        end
        if menu then menu:updateItems() end
        self:info(text)
    end)
end

return Liseur
