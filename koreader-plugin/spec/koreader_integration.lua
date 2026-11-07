-- End-to-end check of the plugin's modules against a running Liseur
-- Sync server, inside a KOReader tree (real LuaSocket, sha2, util).
-- Driven by spec/integration.sh, which starts the throwaway server.
--
-- Env: LISEUR_URL, LISEUR_TOKEN, LISEUR_TOKEN2 (a second device of the
-- same account), PLUGIN_DIR, WORK_DIR.

require("setupkoenv")
G_defaults = require("luadefaults"):open()
local DataStorage = require("datastorage")
G_reader_settings = require("luasettings"):open(DataStorage:getDataDir() .. "/settings.reader.lua")
package.path = os.getenv("PLUGIN_DIR") .. "/?.lua;" .. package.path

local Api = require("liseur_api")
local Catalog = require("liseur_catalog")
local Downloads = require("liseur_downloads")
local Identity = require("liseur_identity")
local Positions = require("liseur_positions")
local Store = require("liseur_store")
local Transport = require("liseur_transport")
local json = require("json")
local lfs = require("libs/libkoreader-lfs")
local sha2 = require("ffi/sha2")
local util = require("util")

local URL, WORK = os.getenv("LISEUR_URL"), os.getenv("WORK_DIR")
local failures = 0
local function check(name, fn)
    local ok, err = pcall(fn)
    print((ok and "ok   " or "FAIL ") .. name .. (ok and "" or (": " .. tostring(err))))
    if not ok then failures = failures + 1 end
end
local function eq(want, got, what)
    if want ~= got then error((what or "value") .. ": want " .. tostring(want) .. ", got " .. tostring(got), 2) end
end

local function sha256_file(path)
    local f = assert(io.open(path, "rb"))
    local u = sha2.sha256()
    while true do
        local c = f:read(65536)
        if not c then break end
        u(c)
    end
    f:close()
    return u()
end

local fs = {
    mkdir = function(p) util.makePath(p) end,
    list = function(dir)
        local out = {}
        if lfs.attributes(dir, "mode") ~= "directory" then return out end
        for n in lfs.dir(dir) do out[#out + 1] = n end
        return out
    end,
}

local base = assert(Api.parseBase(URL))
local function api_for(token)
    return Api.new{ base = base, token = token, transport = Transport.new{ block = 5, total = 20 } }
end
local api = api_for(os.getenv("LISEUR_TOKEN"))
local api2 = api_for(os.getenv("LISEUR_TOKEN2"))

local probe, probe2
check("GET /v1/token names account, device and scopes", function()
    probe = assert(api:get("/v1/token"))
    probe2 = assert(api2:get("/v1/token"))
    assert(type(probe.account_id) == "string" and type(probe.device_id) == "string")
    eq(probe.account_id, probe2.account_id, "same account")
    assert(probe.device_id ~= probe2.device_id, "two devices")
    local has = {}
    for _, s in ipairs(probe.scopes) do has[s] = true end
    assert(has.sync and has["library-read"] and has["read-insights"], "scopes")
end)

local book
check("catalogue lists the folder and its book", function()
    local folders = assert(Catalog.folders(api))
    eq(1, #folders, "folders")
    local books = assert(Catalog.folderBooks(api, folders[1].folder_id))
    eq(1, #books, "books")
    book = books[1]
    eq("A Small Test Book", book.title)
    eq("Ann Author", book.authors[1])
    local again = assert(Catalog.getBook(api, book.book_id))
    eq(book.sha256, again.sha256)
    local results = assert(Catalog.search(api, folders[1].folder_id, "small"))
    eq(1, #results, "search results")
end)

local path
check("download is verified and lands under a safe name", function()
    local dir = WORK .. "/downloads"
    util.makePath(dir)
    path = assert(Downloads.download(api, book, dir, { exists = function(p) return lfs.attributes(p, "mode") ~= nil end, sha256 = sha2.sha256 }))
    eq(dir .. "/Ann Author - A Small Test Book.epub", path)
    eq(book.sha256, sha256_file(path), "sha256")
    local second = assert(Downloads.download(api, book, dir, { exists = function(p) return lfs.attributes(p, "mode") ~= nil end, sha256 = sha2.sha256 }))
    eq(dir .. "/Ann Author - A Small Test Book (2).epub", second)
end)

local function engine(a, p, sub)
    return Positions.new{
        api = a,
        store = Store.new(WORK .. "/state-" .. sub, fs),
        now = require("ffi/util").getTimestamp,
        md5 = sha2.md5,
        device_id = p.device_id,
        account_id = p.account_id,
    }
end

local e1, b1
check("a downloaded book resolves to a work and its fingerprint agrees", function()
    e1 = engine(api, probe, "1")
    b1 = assert(e1:book(util.partialMD5(path)))
    b1.book_id = book.book_id
    local attr = lfs.attributes(path)
    Identity.refresh(b1, path, { size = attr.size, mtime = attr.modification }, sha256_file)
    local raw = assert(api:postRaw("/v1/books/" .. book.book_id .. "/resolve", "{}"))
    local server_md5
    for _, id in ipairs(raw.identifiers) do
        if id.kind == "partial-md5" then server_md5 = id.value end
    end
    eq(b1.md5, server_md5, "KOReader partial MD5 matches the server's")
    local ok, err = Identity.resolve(api, b1)
    assert(ok, err and err.message)
    assert(Identity.writable(b1))
    e1:account()
    e1:track(b1.work_id)
    assert(e1:seed(b1.work_id))
end)

check("a sideloaded copy is recognised by its fingerprints", function()
    local side = { md5 = util.partialMD5(path), sha256 = sha256_file(path) }
    assert(Identity.resolve(api, side))
    eq(b1.work_id, side.work_id, "same work")
end)

local first_op
check("a local move is sent once and acknowledged", function()
    e1:observe(b1, { progression = 0, xpointer = "/body/DocFragment[1]/body/p[1]" }, true)
    e1:observe(b1, { progression = 0.25, xpointer = "/body/DocFragment[2]/body/p[3]/text().0" })
    eq("push", e1:decide(b1).kind)
    local p = e1:prepare(b1)
    first_op = p
    assert(e1:saveBook(b1))
    eq("acked", e1:send(b1))
    assert(e1:saveBook(b1))
    assert(b1.baseline.seq > 0)
end)

check("resending the same bytes is a duplicate, not a second position", function()
    local data = assert(api:postRaw("/v1/ops", '{"ops":[' .. first_op.raw .. ']}'))
    eq("duplicate", data.results[1].status)
end)

check("the feed brings back this device's own echo as agreement", function()
    assert(e1:pull())
    eq(first_op.op_id, e1:remote(b1).op_id)
    eq("in_sync", e1:decide(b1).kind)
end)

local e2, b2
check("another device sees the position bound to the same bytes", function()
    e2 = engine(api2, probe2, "2")
    b2 = assert(e2:book(util.partialMD5(path)))
    b2.sha256 = sha256_file(path)
    assert(Identity.resolve(api2, b2))
    eq(b1.work_id, b2.work_id)
    e2:account()
    e2:track(b2.work_id)
    assert(e2:seed(b2.work_id))
    e2:observe(b2, { progression = 0, xpointer = "/cover" }, true)
    local d = e2:decide(b2)
    eq("pull", d.kind, "cover-open pulls")
    eq("/body/DocFragment[2]/body/p[3]/text().0", Positions.remoteLocation(b2, d.op))
end)

check("both devices moving is a conflict, and keeping wins after the choice", function()
    assert(e2:accept(b2, e2:remote(b2)))
    e2:settle(b2, { progression = 0.25, xpointer = "/body/DocFragment[2]/body/p[3]/text().0" })
    e2:observe(b2, { progression = 0.6, xpointer = "/body/DocFragment[4]/body/p[1]" })
    e2:prepare(b2)
    eq("acked", e2:send(b2))
    e1:observe(b1, { progression = 0.3, xpointer = "/body/DocFragment[2]/body/p[9]" })
    assert(e1:pull())
    local d = e1:decide(b1)
    eq("conflict", d.kind)
    assert(e1:keep(b1, d.op))
    eq("push", e1:decide(b1).kind)
    e1:prepare(b1)
    eq("acked", e1:send(b1))
end)

check("an upload frozen under another device is paused", function()
    e1:observe(b1, { progression = 0.31, xpointer = "/x" })
    e1:prepare(b1)
    assert(e1:saveBook(b1))
    local other = engine(api2, probe2, "1")
    local again = assert(other:book(b1.md5))
    eq("paused_device", other:send(again))
end)

check("history lists positions and forgetting the furthest raises the floor", function()
    local h = assert(e1:history(b1.work_id))
    assert(#h.ops >= 3, "ops")
    assert(#h.furthest >= 1, "furthest")
    local upto = 0
    for _, op in ipairs(h.furthest) do upto = math.max(upto, op.seq) end
    local floor = assert(e1:forgetFurthest(b1.work_id, upto))
    assert(floor >= upto)
    local after = assert(e1:history(b1.work_id))
    for _, op in ipairs(after.furthest) do assert(op.seq > floor) end
end)

check("the feed is caught up after pulling", function()
    local acc = e1:account()
    local data = assert(api:get("/v1/changes", { since = acc.cursor, limit = 500 }))
    eq(false, data.has_more)
end)

check("a stock Progress sync device reads the plugin's KOReader position", function()
    local transport = Transport.new{ block = 5, total = 20 }
    local key = sha2.md5(os.getenv("KOSYNC_CODE"))
    local headers = { ["Accept"] = "application/vnd.koreader.v1+json", ["Content-Type"] = "application/json" }
    local created = assert(transport({ method = "POST", url = URL .. "/adapter/kosync/users/create", headers = headers,
        body = json.encode({ username = "stock-kindle", password = key }) }))
    eq(201, created.status, created.body)
    headers["x-auth-user"] = "stock-kindle"
    headers["x-auth-key"] = key
    local got = assert(transport({ method = "GET", url = URL .. "/adapter/kosync/syncs/progress/" .. b1.md5, headers = headers }))
    eq(200, got.status, got.body)
    local newest = assert(e1:history(b1.work_id, 1)).ops[1]
    eq(newest.foreign_pos, json.decode(got.body).progress)
end)

check("reading summary needs only read-insights", function()
    local data = assert(api:get("/v1/insights/summary"))
    assert(type(data.stats_revision) == "string", "stats_revision is a string")
end)

check("a refused scope is reported as forbidden", function()
    local p3 = os.getenv("LISEUR_TOKEN3")
    local data, err = api_for(p3):get("/v1/folders")
    assert(not data)
    eq("forbidden", err.kind)
end)

print(failures == 0 and "all integration checks passed" or (failures .. " integration check(s) failed"))
os.exit(failures == 0 and 0 or 1)
