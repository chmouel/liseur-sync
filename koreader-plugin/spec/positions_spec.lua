local Api = require("liseur_api")
local Fake = require("fake_http")
local Identity = require("liseur_identity")
local Positions = require("liseur_positions")
local Store = require("liseur_store")
local json = require("json")

local function fakemd5(s)
    local h = 2166136261
    local out = {}
    for round = 1, 4 do
        for i = 1, #s do h = (h * 16777619 + s:byte(i) + round) % 4294967296 end
        out[#out + 1] = string.format("%08x", h)
    end
    return table.concat(out)
end

local function tmpdir()
    local p = os.tmpname()
    os.remove(p)
    os.execute("mkdir -p '" .. p .. "'")
    return p
end

local fs = {
    mkdir = function(path) os.execute("mkdir -p '" .. path .. "'") end,
    list = function(dir)
        local out = {}
        local p = io.popen("ls -a '" .. dir .. "' 2>/dev/null")
        for line in p:lines() do out[#out + 1] = line end
        p:close()
        return out
    end,
}

local function setup(device)
    local fake = Fake.new()
    local clock = { t = 1700000000 }
    local dir = tmpdir()
    local function engine(dev)
        return Positions.new{
            api = Api.new{ base = Api.parseBase("https://h.example"), token = "t", transport = fake.transport },
            store = Store.new(dir, fs),
            now = function() return clock.t end,
            md5 = fakemd5,
            random = function() return 0.5 end,
            device_id = dev or device or "dev-1",
            account_id = "acc",
        }
    end
    return fake, engine(), clock, engine
end

local function resolved(engine, md5, sha)
    sha = sha or "sha-a"
    local b = assert(engine:edition(sha, md5 or "m1"))
    b.identity = "resolved"
    b.work_id = "w1"
    engine:account()
    engine:track("w1")
    assert(engine:saveBook(b))
    return b
end

describe("liseur_positions observations", function()
    it("the position found at open is not owed, a later move is", function()
        local _, e = setup()
        local b = resolved(e)
        assert.is_true(e:observe(b, { progression = 0, xpointer = "/body/p[1]" }, true))
        assert.is_false(Positions.dirty(b))
        assert.is_false(e:observe(b, { progression = 0, xpointer = "/body/p[1]" }))
        assert.is_true(e:observe(b, { progression = 0.1, xpointer = "/body/p[9]" }))
        assert.is_true(Positions.dirty(b))
    end)

    it("an open position that differs from the last one seen is owed", function()
        local _, e = setup()
        local b = resolved(e)
        e:observe(b, { progression = 0.2, xpointer = "/a" }, true)
        e:observe(b, { progression = 0.5, xpointer = "/b" }, true)
        assert.is_true(Positions.dirty(b))
    end)

    it("position zero is a real position", function()
        local _, e = setup()
        local b = resolved(e)
        e:observe(b, { progression = 0.3, xpointer = "/a" }, true)
        e:observe(b, { progression = 0, xpointer = "/start" })
        e:prepare(b)
        assert.are.equal(0, json.decode(b.pending.raw).progression)
    end)
end)

describe("liseur_positions uploads", function()
    local fake, e, clock, engine
    before_each(function()
        fake, e, clock, engine = setup()
    end)

    local function moved(book, p)
        e:observe(book, { progression = p, xpointer = "/x" .. p })
    end

    it("sends the frozen bytes and records the agreement", function()
        local b = resolved(e)
        moved(b, 0.4)
        local p = e:prepare(b)
        local sent = json.decode(p.raw)
        assert.are.equal("w1", sent.work_id)
        assert.are.equal(0.4, sent.progression)
        assert.are.equal("/x0.4", sent.foreign_pos)
        assert.are.equal("sha-a", sent.edition_sha)
        assert.are.equal("2023-11-14T22:13:20.000Z", sent.client_ts)
        assert.is_nil(sent.locator)
        fake:respond(200, { results = { { op_id = p.op_id, status = "applied", seq = 12 } } })
        assert.are.equal("acked", e:send(b))
        assert.are.equal('{"ops":[' .. p.raw .. ']}', fake:last().body)
        assert.is_false(Positions.dirty(b))
        assert.is_nil(b.pending)
        assert.are.equal(12, b.baseline.seq)
        assert.are.equal(12, e:remote(b).seq)
    end)

    it("retries an unknown outcome with the same bytes, even after a restart", function()
        local b = resolved(e)
        moved(b, 0.4)
        local p = e:prepare(b)
        assert.is_true(e:saveBook(b))
        fake:respond(nil, "timeout")
        assert.are.equal("retry", e:send(b))
        e:saveBook(b)
        local e2 = engine()
        local again = e2:edition("sha-a","m1")
        e2:account()
        clock.t = clock.t + 3600
        moved(again, 0.6) -- later reading does not change what is in flight
        assert.are.equal(p.raw, again.pending.raw)
        fake:respond(200, { results = { { op_id = p.op_id, status = "duplicate", seq = 5 } } })
        assert.are.equal("acked", e2:send(again))
        assert.are.equal('{"ops":[' .. p.raw .. ']}', fake:last().body)
        assert.is_true(Positions.dirty(again), "the newer move is still owed")
    end)

    it("an unreadable acknowledgement is retried with the same bytes", function()
        local b = resolved(e)
        moved(b, 0.4)
        local p = e:prepare(b)
        fake:respond(200, "<html>")
        assert.are.equal("retry", e:send(b))
        assert.are.equal(p, b.pending)
        assert.is_true(type(p.not_before) == "number")
    end)

    it("a peak the reader went back from is sent before the current position", function()
        local b = resolved(e)
        moved(b, 0.9)
        moved(b, 0.3)
        local p = e:prepare(b)
        local sent = json.decode('{"ops":[' .. p.raw .. ']}').ops
        assert.are.equal(2, #sent)
        assert.are.equal(0.9, sent[1].progression)
        assert.are.equal(0.3, sent[2].progression)
        assert.are.equal(p.op_id, sent[2].op_id)
        assert.is_true(sent[1].op_id ~= sent[2].op_id)
        fake:respond(200, { results = { { status = "applied", seq = 7 }, { status = "applied", seq = 8 } } })
        assert.are.equal("acked", e:send(b))
        assert.are.equal(8, b.baseline.seq)
        assert.are.equal(0.3, b.baseline.progression)
        moved(b, 0.4)
        assert.are.equal(1, #json.decode('{"ops":[' .. e:prepare(b).raw .. ']}').ops)
    end)

    it("a peak survives an upload that has to be renamed", function()
        local b = resolved(e)
        moved(b, 0.9)
        moved(b, 0.3)
        e:prepare(b)
        fake:respond(200, { results = { { status = "applied", seq = 7 }, { status = "conflict" } } })
        assert.are.equal("renamed", e:send(b))
        local sent = json.decode('{"ops":[' .. e:prepare(b).raw .. ']}').ops
        assert.are.equal(2, #sent)
        assert.are.equal(0.9, sent[1].progression)
    end)

    it("honours Retry-After before sending again", function()
        local b = resolved(e)
        moved(b, 0.4)
        e:prepare(b)
        fake:respond(429, { error = "slow down" }, { ["retry-after"] = "120" })
        assert.are.equal("retry", e:send(b))
        assert.are.equal(clock.t + 120, b.pending.not_before)
        assert.are.equal("retry", e:send(b))
        assert.are.equal(1, #fake.requests)
    end)

    it("an op id reused with another payload gets a fresh id next time", function()
        local b = resolved(e)
        moved(b, 0.4)
        local first = e:prepare(b).op_id
        fake:respond(200, { results = { { op_id = first, status = "conflict", reason = "op_id reused" } } })
        assert.are.equal("renamed", e:send(b))
        assert.is_true(Positions.dirty(b))
        assert.truthy(e:prepare(b).op_id ~= first)
    end)

    it("a refused permission keeps the exact bytes and adds no deadline", function()
        local b = resolved(e)
        moved(b, 0.4)
        local frozen_raw = e:prepare(b).raw
        fake:respond(429, { error = "slow down" }, { ["retry-after"] = "60" })
        assert.are.equal("retry", e:send(b))
        local waited = b.pending.not_before
        assert.are.equal(clock.t + 60, waited)
        -- The deadline passes, and the scope is gone by the time it does.
        clock.t = clock.t + 60
        fake:respond(403, { error = "token lacks required scope: sync" })
        local status, err = e:send(b)
        assert.is_nil(status)
        assert.are.equal("forbidden", err.kind)
        -- frozen_raw is a string, so this compares against the payload as it
        -- was sent rather than against whatever table b.pending is now.
        assert.are.equal(frozen_raw, b.pending.raw, "the pending bytes changed")
        assert.are.equal(waited, b.pending.not_before, "the refusal wrote a new deadline")
        assert.is_true(Positions.dirty(b))
    end)

    it("an unknown work drops the mapping and keeps the reading", function()
        local b = resolved(e)
        moved(b, 0.4)
        e:prepare(b)
        fake:respond(400, { error = "op x: unknown work", code = "unknown_work" })
        assert.are.equal("unknown_work", e:send(b))
        assert.are.equal("unresolved", b.identity)
        assert.is_nil(b.work_id)
        assert.is_true(Positions.dirty(b))
    end)

    it("a refused op is not resent until the reader moves again", function()
        local b = resolved(e)
        moved(b, 0.4)
        e:prepare(b)
        fake:respond(400, { error = "op x: time in future", code = "time_in_future" })
        assert.are.equal("refused", e:send(b))
        assert.is_false(Positions.dirty(b))
        moved(b, 0.5)
        assert.is_true(Positions.dirty(b))
    end)

    it("nothing is sent while the account state cannot be read", function()
        local b = resolved(e)
        e:saveAccount()
        moved(b, 0.4)
        e:prepare(b)
        e:saveBook(b)
        local file = e.store:path("account", "state")
        os.execute("chmod 000 '" .. file .. "'")
        local fresh = engine()
        local again = fresh:edition("sha-a","m1")
        local status, err = fresh:send(again)
        os.execute("chmod 600 '" .. file .. "'")
        assert.is_nil(status)
        assert.are.equal("storage", err.kind)
        assert.are.equal(0, #fake.requests)
    end)

    it("an upload frozen under another device is paused, not replayed", function()
        local b = resolved(e)
        moved(b, 0.4)
        e:prepare(b)
        e:saveBook(b)
        local other = engine("dev-2")
        local again = other:edition("sha-a","m1")
        assert.are.equal("paused_device", other:send(again))
        assert.are.equal(0, #fake.requests)
        assert.is_true(other:discardPaused(again))
        local p = other:prepare(again)
        assert.are.equal("dev-2", p.device_id)
    end)
end)

describe("liseur_positions retired uploads", function()
    it("are delivered without touching the new bytes' agreement", function()
        local fake, e = setup()
        local b = resolved(e)
        b.retired = {
            { raw = '{"op_id":"old"}', device_id = "dev-1" },
            { raw = '{"op_id":"other"}', device_id = "dev-9" },
            { raw = '{"op_id":"later"}', device_id = "dev-1" },
        }
        fake:respond(200, { results = { { op_id = "old", status = "applied", seq = 3 } } })
        fake:respond(nil, "timeout")
        assert.is_true(e:sendRetired(b))
        assert.are.equal('{"ops":[{"op_id":"old"}]}', fake.requests[1].body)
        assert.are.equal(2, #b.retired, "the other device's and the unanswered one stay")
        assert.is_not_nil(b.retired[2].not_before)
        assert.is_nil(b.baseline)
        assert.are.equal(0, b.acked)
    end)

    it("are kept on any answer that does not settle them", function()
        local fake, e, clock = setup()
        local b = resolved(e)
        b.retired = { { raw = '{"op_id":"a"}', device_id = "dev-1" } }
        fake:respond(200, {})
        e:sendRetired(b)
        assert.are.equal(1, #b.retired)
        clock.t = clock.t + 100000
        fake:respond(200, "not json")
        e:sendRetired(b)
        assert.are.equal(1, #b.retired)
        clock.t = clock.t + 100000
        fake:respond(400, { error = "op a: time in future", code = "time_in_future" })
        e:sendRetired(b)
        assert.is_nil(b.retired)
    end)
end)

describe("liseur_positions feed", function()
    local fake, e
    before_each(function()
        fake, e = setup()
    end)

    it("follows pages, keeps only tracked works and commits the cursor", function()
        resolved(e)
        fake:respond(200, { ops = {
            { op_id = "a", work_id = "w1", seq = 3, progression = 0.2, device_id = "d2" },
            { op_id = "b", work_id = "w9", seq = 4, progression = 0.9, device_id = "d2" },
        }, high_water = 9, has_more = true, furthest_floors = { w1 = 2 } })
        fake:respond(200, { ops = {
            { op_id = "c", work_id = "w1", seq = 7, progression = 0.3, device_id = "d2" },
        }, high_water = 9, has_more = false, furthest_floors = {} })
        assert.is_true(e:pull())
        local acc = e:account()
        assert.are.equal(7, acc.cursor)
        assert.are.equal("c", acc.works.w1.remote.op_id)
        assert.are.equal(2, acc.works.w1.floor)
        assert.is_nil(acc.works.w9)
        assert.matches("since=4", fake.requests[2].url)
        assert.are.equal(7, e.store:read("account", "state").cursor)
    end)

    it("an empty page advances to the high-water mark", function()
        e:account()
        e.store:write("account", "state", { cursor = 4, works = {} })
        e._account = nil
        fake:respond(200, { ops = {}, high_water = 11, has_more = false, furthest_floors = {} })
        assert.is_true(e:pull())
        assert.are.equal(11, e:account().cursor)
    end)

    it("an expired cursor reseeds from heads without touching local reading", function()
        local b = resolved(e)
        e:observe(b, { progression = 0.4, xpointer = "/p" })
        e:prepare(b)
        e:saveBook(b)
        fake:respond(410, { error = "resync_required", high_water = 50 })
        fake:respond(200, {
            ops = {
                { op_id = "x", work_id = "w1", seq = 40, progression = 0.5, device_id = "d2" },
                { op_id = "y", work_id = "w1", seq = 44, progression = 0.6, device_id = "d3" },
            },
            furthest = {}, furthest_floors = { w1 = 10 }, snapshot_seq = 50,
        })
        assert.is_true(e:pull())
        local acc = e:account()
        assert.are.equal(50, acc.cursor)
        assert.are.equal("y", acc.works.w1.remote.op_id)
        assert.are.equal(10, acc.works.w1.floor)
        local again = e:edition("sha-a","m1")
        assert.is_not_nil(again.pending)
        assert.matches("/v1/heads", fake:last().url)
    end)

    it("drops ops it cannot read but still moves past them", function()
        resolved(e)
        fake:respond(200, { ops = {
            { op_id = "a", work_id = "w1", seq = 3, progression = json.null },
        }, high_water = 3, has_more = false })
        assert.is_true(e:pull())
        assert.are.equal(3, e:account().cursor)
        assert.is_nil(e:account().works.w1.remote)
    end)
end)

describe("liseur_positions decisions", function()
    local fake, e
    before_each(function()
        fake, e = setup()
    end)

    local function remote(op)
        e:account().works.w1.remote = Positions.trim(op)
    end

    it("opening at the cover does not overwrite a remote chapter", function()
        local b = resolved(e)
        e:observe(b, { progression = 0, xpointer = "/cover" }, true)
        remote({ op_id = "r", seq = 5, progression = 0.4, device_id = "d2" })
        local d = e:decide(b)
        assert.are.equal("pull", d.kind)
        assert.are.equal("r", d.op.op_id)
    end)

    it("reading backwards is sent", function()
        local b = resolved(e)
        b.baseline = { progression = 0.9, op_id = "r", seq = 5 }
        remote({ op_id = "r", seq = 5, progression = 0.9, device_id = "d2" })
        e:observe(b, { progression = 0.9, xpointer = "/end" }, true)
        e:observe(b, { progression = 0.02, xpointer = "/start" })
        assert.are.equal("push", e:decide(b).kind)
    end)

    it("this device's own echo is the agreement", function()
        local b = resolved(e)
        e:observe(b, { progression = 0.3, xpointer = "/a" }, true)
        b.baseline = { progression = 0.3, op_id = "mine", seq = 8 }
        remote({ op_id = "mine", seq = 8, progression = 0.3, device_id = "dev-1" })
        assert.are.equal("in_sync", e:decide(b).kind)
    end)

    it("exact positions on the same rendered page settle a near difference", function()
        local b = resolved(e)
        e:observe(b, { progression = 0.071, xpointer = "/here" }, true)
        remote({ op_id = "r", seq = 5, progression = 0.078, device_id = "d2", foreign_pos = "/there", edition_sha = "sha-a" })
        local d = e:decide(b, function(there, here)
            assert.are.equal("/there", there)
            assert.are.equal("/here", here)
            return true
        end)
        assert.are.equal("in_sync", d.kind)
    end)

    it("only trusts a remote KOReader position bound to these bytes", function()
        local b = resolved(e)
        b.md5 = "m1"
        assert.is_nil(Positions.remoteLocation(b, { foreign_pos = "/x", edition_sha = "other" }))
        assert.is_nil(Positions.remoteLocation(b, { foreign_pos = "/x" }))
        assert.are.equal("/x", Positions.remoteLocation(b, { foreign_pos = "/x", edition_sha = "sha-a" }))
        assert.are.equal("/x", Positions.remoteLocation(b, { foreign_pos = "/x", origin_alias = "partial-md5:m1" }))
    end)

    it("an op naming another edition is never rescued by the alias branch", function()
        local b = resolved(e)
        b.md5 = "m1"
        -- An explicit edition that is not this file's bytes is a different
        -- edition, whatever alias the op also carries.
        assert.is_nil(Positions.remoteLocation(b,
            { foreign_pos = "/x", edition_sha = "sha-b", origin_alias = "partial-md5:m1" }))
        -- An edition the server named, and we cannot compare: still nil.
        assert.is_nil(Positions.remoteLocation({ md5 = "m1", md5_untrusted = true },
            { foreign_pos = "/x", edition_sha = "sha-b", origin_alias = "partial-md5:m1" }))
    end)

    it("an untrusted partial MD5 vetoes the alias branch", function()
        local b = resolved(e)
        b.md5 = "m1"
        b.md5_untrusted = true
        assert.is_nil(Positions.remoteLocation(b, { foreign_pos = "/x", origin_alias = "partial-md5:m1" }))
        -- The SHA-256 still speaks for the bytes.
        assert.are.equal("/x", Positions.remoteLocation(b, { foreign_pos = "/x", edition_sha = "sha-a" }))
        -- No foreign position at all is never a location either.
        assert.is_nil(Positions.remoteLocation(b, { foreign_pos = "", origin_alias = "partial-md5:m1" }))
    end)

    it("a choice made against a stale prompt is refused", function()
        local b = resolved(e)
        remote({ op_id = "r1", seq = 5, progression = 0.4, device_id = "d2" })
        local shown = e:remote(b)
        remote({ op_id = "r2", seq = 6, progression = 0.7, device_id = "d3" })
        assert.is_false(e:accept(b, shown))
        assert.is_false(e:keep(b, shown))
        assert.is_true(e:accept(b, e:remote(b)))
    end)

    it("reading on from an agreed exact position is sent, not a conflict", function()
        local b = resolved(e)
        remote({ op_id = "r", seq = 5, progression = 0.3, device_id = "d2", foreign_pos = "/r", edition_sha = "sha-a" })
        e:observe(b, { progression = 0, xpointer = "/cover" }, true)
        assert.is_true(e:accept(b, e:remote(b)))
        e:settle(b, { progression = 0.3, xpointer = "/r" })
        e:observe(b, { progression = 0.32, xpointer = "/later" })
        assert.are.equal("push", e:decide(b, function() return false end).kind)
    end)

    it("keeping this position makes it the one to send", function()
        local b = resolved(e)
        e:observe(b, { progression = 0.2, xpointer = "/a" }, true)
        remote({ op_id = "r", seq = 5, progression = 0.6, device_id = "d2" })
        assert.are.equal("pull", e:decide(b).kind)
        assert.is_true(e:keep(b, e:remote(b)))
        assert.are.equal("push", e:decide(b).kind)
    end)

    it("agreeing on the current place still sends a peak not sent yet", function()
        local b = resolved(e)
        e:observe(b, { progression = 0.3, xpointer = "/a" }, true)
        e:observe(b, { progression = 0.9, xpointer = "/far" })
        e:observe(b, { progression = 0.3, xpointer = "/a" })
        remote({ op_id = "r", seq = 5, progression = 0.3, device_id = "d2" })
        assert.are.equal("push", e:decide(b).kind)
    end)

    it("going to another device's place keeps a peak not sent yet", function()
        local b = resolved(e)
        e:observe(b, { progression = 0.2, xpointer = "/a" }, true)
        e:observe(b, { progression = 0.9, xpointer = "/far" })
        remote({ op_id = "r", seq = 5, progression = 0.3, device_id = "d2" })
        assert.is_true(e:accept(b, e:remote(b)))
        e:settle(b, { progression = 0.3, xpointer = "/c" })
        assert.is_true(Positions.owed(b))
        assert.are.equal("push", e:decide(b).kind)
        local acked = b.acked
        local sent = json.decode('{"ops":[' .. e:prepare(b).raw .. ']}').ops
        assert.is_true(b.revision > acked, "the op reused a spent revision")
        assert.are.equal(0.9, sent[1].progression)
        assert.are.equal(0.3, sent[2].progression)
    end)

    it("accepting then settling leaves nothing owed", function()
        local b = resolved(e)
        e:observe(b, { progression = 0.2, xpointer = "/a" }, true)
        e:observe(b, { progression = 0.25, xpointer = "/b" })
        remote({ op_id = "r", seq = 5, progression = 0.6, device_id = "d2" })
        assert.is_true(e:accept(b, e:remote(b)))
        e:settle(b, { progression = 0.6, xpointer = "/c" })
        assert.is_false(Positions.dirty(b))
        assert.are.equal("in_sync", e:decide(b).kind)
    end)
end)

describe("liseur_positions history", function()
    it("hides furthest candidates at or below the forgotten floor", function()
        local fake, e = setup()
        fake:respond(200, {
            ops = { { op_id = "a", seq = 9, progression = 0.3, device_id = "d" } },
            furthest = {
                { op_id = "f1", seq = 4, progression = 0.99, device_id = "d" },
                { op_id = "f2", seq = 9, progression = 0.3, device_id = "d" },
            },
            furthest_floor = 4,
        })
        local h = e:history("w1")
        assert.are.equal(1, #h.furthest)
        assert.are.equal("f2", h.furthest[1].op_id)
        resolved(e)
        fake:respond(200, { furthest_floor = 9 })
        assert.are.equal(9, e:forgetFurthest("w1", 9))
        assert.are.same({ upto_seq = 9 }, json.decode(fake:last().body))
        assert.are.equal(9, e:floor("w1"))
    end)
end)

-- Two files can carry the same KOReader partial MD5, so a document named
-- by it cannot be either of them: each file's state belongs to its full
-- SHA-256, and one file's unsent reading is never the other's to clear.
describe("liseur_positions editions", function()
    local fake, e, clock, engine
    before_each(function()
        fake, e, clock, engine = setup()
    end)

    local function note(fp, path, sha)
        assert.is_true(e:noteFile(fp, path, sha, { size = 10, mtime = 1 }))
    end

    it("two digests under one partial MD5 keep separate revisions and positions", function()
        local fp = assert(e:fingerprint("m1"))
        note(fp, "/a.epub", "sha-a")
        local a = resolved(e, "m1", "sha-a")
        e:observe(a, { progression = 0.2, xpointer = "/a" }, true)
        e:observe(a, { progression = 0.4, xpointer = "/a2" })
        a.path = "/a.epub"
        assert.is_true(e:saveBook(a))
        -- The other file, same partial MD5 and the same stat.
        note(fp, "/b.epub", "sha-b")
        assert.is_true(e:saveFingerprint("m1", fp))
        local b = assert(e:edition("sha-b", "m1", fp))
        assert.are.equal("unresolved", b.identity)
        assert.are.equal(0, b.revision)
        b.path = "/b.epub"
        e:observe(b, { progression = 0.9, xpointer = "/b" }, true)
        e:observe(b, { progression = 0.95, xpointer = "/b2" })
        assert.is_true(e:saveBook(b))
        -- A is exactly as it was left: its unsent move is still unsent.
        local again = assert(e:edition("sha-a", "m1", fp))
        assert.are.equal("resolved", again.identity)
        assert.are.equal("w1", again.work_id)
        assert.are.equal(a.revision, again.revision)
        assert.are.equal(0.4, again["local"].progression)
        assert.is_true(Positions.dirty(again))
        assert.is_nil(again.pending, "the unsent move was turned into an upload")
        -- And B owes what B read, not what A read.
        assert.are.equal(0.95, b["local"].progression)
        assert.is_true(Positions.dirty(b))
    end)

    it("a partial MD5 seen under two digests stops naming either file", function()
        local fp = assert(e:fingerprint("m1"))
        note(fp, "/a.epub", "sha-a")
        assert.is_false(Positions.shared(fp))
        note(fp, "/b.epub", "sha-b")
        assert.is_true(Positions.shared(fp))
        assert.is_true(fp.shared, "the collision was not recorded")
        local a = assert(e:edition("sha-a", "m1", fp))
        local b = assert(e:edition("sha-b", "m1", fp))
        assert.is_true(a.md5_untrusted)
        assert.is_true(b.md5_untrusted)
        assert.is_nil(Positions.remoteLocation(a, { foreign_pos = "/x", origin_alias = "partial-md5:m1" }))
        assert.are.equal("/x", Positions.remoteLocation(a, { foreign_pos = "/x", edition_sha = "sha-a" }))
    end)

    -- F6: the backlog reads documents() straight off disk, where a
    -- document saved before a collision does not yet say its partial MD5
    -- is shared. The authoritative fingerprint record has the last word.
    it("a document saved before a collision resolves by SHA alone from the backlog", function()
        local SHA_A, SHA_B = string.rep("a", 64), string.rep("b", 64)
        local a = assert(e:edition(SHA_A, "m1"))
        a.identity = "unresolved"
        assert(e:observe(a, { progression = 0.3, xpointer = "/a" }))
        assert(e:saveBook(a))
        local fp = assert(e:fingerprint("m1"))
        note(fp, "/a.epub", SHA_A)
        note(fp, "/b.epub", SHA_B)
        assert(e:saveFingerprint("m1", fp))
        local doc
        for _i, b in ipairs(e:documents()) do
            if b.sha256 == SHA_A then doc = b end
        end
        doc = assert(doc, "the backlog lost the earlier document")
        assert.is_true(doc.md5_untrusted, "the shared partial MD5 was still trusted")
        fake:respond(201, { work_id = "work-a", confidence = "high", created = true })
        assert.is_true(Identity.resolve(e.api, doc))
        assert.are.same({ { kind = "sha256", value = SHA_A } }, json.decode(fake:last().body).identifiers)
        assert.are.equal("work-a", doc.work_id)
    end)

    it("an unreadable fingerprint record fails closed and sends no alias", function()
        local SHA_A = string.rep("a", 64)
        local a = assert(e:edition(SHA_A, "m1"))
        a.identity = "unresolved"
        assert(e:saveBook(a))
        local real = e.store.read
        e.store.read = function(store, kind, name)
            if kind == "fingerprints" and name == "m1" then return nil, "unreadable" end
            return real(store, kind, name)
        end
        local doc
        for _i, b in ipairs(e:documents()) do
            if b.sha256 == SHA_A then doc = b end
        end
        e.store.read = real
        doc = assert(doc)
        assert.is_true(doc.md5_untrusted, "an unreadable fingerprint record was trusted")
        fake:respond(201, { work_id = "work-a", confidence = "high", created = true })
        assert.is_true(Identity.resolve(e.api, doc))
        assert.are.same({ { kind = "sha256", value = SHA_A } }, json.decode(fake:last().body).identifiers)
    end)

    it("a resolved work under its own SHA keeps it when the alias goes stale", function()
        local SHA_A = string.rep("a", 64)
        local a = resolved(e, "m1", SHA_A)
        local fp = assert(e:fingerprint("m1"))
        note(fp, "/a.epub", SHA_A)
        note(fp, "/b.epub", string.rep("b", 64))
        assert(e:saveFingerprint("m1", fp))
        local doc
        for _i, b in ipairs(e:documents()) do
            if b.sha256 == SHA_A then doc = b end
        end
        doc = assert(doc)
        assert.is_true(doc.md5_untrusted)
        assert.are.equal("w1", doc.work_id, "the existing work mapping was dropped")
        assert.is_true(Identity.writable(doc))
    end)

    it("the cache is written before the document it leads to", function()
        local fp = assert(e:fingerprint("m1"))
        note(fp, "/a.epub", "sha-a")
        assert.is_true(e:saveFingerprint("m1", fp))
        local stored = assert(e.store:read("fingerprints", "m1"))
        assert.is_true(stored.shas["sha-a"])
        assert.are.equal("sha-a", stored.files["/a.epub"].sha256)
        -- A crash here leaves a cache entry and no edition: the next open
        -- rehashes the bytes (a stat is not proof) and creates the document.
        assert.is_nil(e.store:read("editions", "sha-a"))
        assert.are.equal("sha-a", Identity.locate("/a.epub", function() return "sha-a" end))
    end)
end)

describe("liseur_positions migration", function()
    local SHA_A = string.rep("a", 64)
    local fake, e, clock, engine
    before_each(function()
        fake, e, clock, engine = setup()
    end)

    local function legacy(md5, doc)
        doc.md5 = doc.md5 or md5
        assert.is_true(e.store:write("books", md5, doc))
        return doc
    end

    it("retains the legacy source until a visible destination is durable", function()
        legacy("m1", { sha256 = SHA_A, epoch = "epoch-1", revision = 7,
            pending = { raw = '{"op_id":"pending"}' } })
        local failing_fs = {}
        for k, v in pairs(fs) do failing_fs[k] = v end
        failing_fs.fsync_dir = function() return false, "sync failed" end
        e.store.fs = failing_fs
        e:migrateLegacy()
        assert.is_not_nil(e.store:read("books", "m1"))
        e.store.fs = fs
        local source = e.store:read("books", "m1")
        assert.is_true(e:saveBook(source))
        e.store.fs = failing_fs
        local ok = e:migrateLegacy()
        assert.is_nil(ok)
        assert.is_not_nil(e.store:read("books", "m1"))
        e.store.fs = fs
        assert.is_true(e:migrateLegacy())
        assert.is_nil(e.store:read("books", "m1"))
        assert.are.equal(source.pending.raw, e.store:read("editions", SHA_A).pending.raw)
    end)

    it("moves a document to the edition its digest names, bytes and all", function()
        legacy("m1", {
            sha256 = SHA_A, identity = "resolved", work_id = "w1", epoch = "epoch-1",
            revision = 7, acked = 4, md5_untrusted = true,
            ["local"] = { progression = 0.5, xpointer = "/here" },
            pending = { revision = 7, raw = '{"op_id":"p"}', device_id = "dev-1" },
            retired = { { raw = '{"op_id":"r"}', device_id = "dev-1" } },
            last_error = "timed out",
        })
        e:migrateLegacy()
        assert.is_nil(e.store:read("books", "m1"), "the old document was not removed")
        local moved = assert(e.store:read("editions", SHA_A))
        assert.are.equal("epoch-1", moved.epoch)
        assert.are.equal(7, moved.revision)
        assert.are.equal(4, moved.acked)
        assert.are.equal(0.5, moved["local"].progression)
        assert.are.equal('{"op_id":"p"}', moved.pending.raw)
        assert.are.equal('{"op_id":"r"}', moved.retired[1].raw)
        assert.are.equal("timed out", moved.last_error)
        assert.is_nil(moved.legacy_key)
        local fp = assert(e.store:read("fingerprints", "m1"))
        assert.is_true(fp.shas[SHA_A])
        assert.is_true(fp.shared, "the collision mark was lost")
        assert.is_nil(next(fp.files), "migration seeded a stat nobody checked")
    end)

    it("stops migration when fingerprint or edition writes fail", function()
        for _, kind in ipairs({ "fingerprints", "editions" }) do
            local _, test_engine = setup()
            assert.is_true(test_engine.store:write("books", "m1", { md5 = "m1", sha256 = SHA_A, epoch = "one" }))
            local write = test_engine.store.write
            test_engine.store.write = function(store, target, name, doc)
                if target == kind then return nil, "write failed" end
                return write(store, target, name, doc)
            end
            local ok, err = test_engine:migrateLegacy()
            assert.is_nil(ok)
            assert.are.equal("write failed", err)
            assert.is_not_nil(test_engine.store:read("books", "m1"))
        end
    end)

    it("a rerun after a crash leaves the edition alone and drops the old copy", function()
        legacy("m1", { sha256 = SHA_A, identity = "resolved", work_id = "w1", epoch = "epoch-1", revision = 1 })
        -- The copy was written before the crash; the edition has moved on.
        assert.is_true(e.store:write("editions", SHA_A,
            { sha256 = SHA_A, epoch = "epoch-1", revision = 99, ["local"] = { progression = 0.9 } }))
        e:migrateLegacy()
        assert.is_nil(e.store:read("books", "m1"))
        assert.are.equal(99, assert(e.store:read("editions", SHA_A)).revision, "the edition was overwritten")
    end)

    it("an edition from another epoch is kept beside the old document", function()
        legacy("m1", { sha256 = SHA_A, identity = "resolved", work_id = "w1", epoch = "epoch-1", revision = 1 })
        assert.is_true(e.store:write("editions", SHA_A, { sha256 = SHA_A, epoch = "epoch-2", revision = 99 }))
        e:migrateLegacy()
        assert.is_not_nil(e.store:read("books", "m1"), "an ambiguous leftover was deleted")
        assert.are.equal(99, assert(e.store:read("editions", SHA_A)).revision)
    end)

    -- F7: a destination that cannot be read is not an absent one. Treating
    -- the error as absence used to overwrite a newer edition and drop the
    -- legacy source; it must instead report and leave both exactly as they
    -- were.
    it("an unreadable destination is reported, never overwritten", function()
        legacy("m1", {
            sha256 = SHA_A, identity = "resolved", work_id = "w1", epoch = "epoch-1",
            revision = 3, acked = 1, pending = { revision = 3, raw = '{"op_id":"p"}' },
        })
        assert.is_true(e.store:write("editions", SHA_A, { sha256 = SHA_A, epoch = "epoch-2", revision = 99 }))
        local real = e.store.read
        e.store.read = function(store, kind, name)
            if kind == "editions" and name == SHA_A then return nil, "unreadable" end
            return real(store, kind, name)
        end
        local ok, err = e:migrateLegacy()
        e.store.read = real
        assert.is_nil(ok, "a failed migration reported success")
        assert.is_not_nil(err)
        assert.are.equal(99, assert(e.store:read("editions", SHA_A)).revision, "the destination was overwritten")
        local kept = assert(e.store:read("books", "m1"), "the legacy source was removed")
        assert.are.equal('{"op_id":"p"}', kept.pending.raw, "the frozen bytes were lost")
        assert.are.equal(3, kept.revision)
    end)

    it("a destination read error never creates a second copy", function()
        legacy("m1", { sha256 = SHA_A, identity = "unresolved", epoch = "epoch-1", revision = 2 })
        local real = e.store.read
        e.store.read = function(store, kind, name)
            if kind == "editions" and name == SHA_A then return nil, "unreadable" end
            return real(store, kind, name)
        end
        local ok, err = e:migrateLegacy()
        e.store.read = real
        assert.is_nil(ok)
        assert.is_not_nil(err)
        assert.is_nil(e.store:read("editions", SHA_A), "a copy was written on an unread destination")
        assert.is_not_nil(e.store:read("books", "m1"))
    end)

    it("a document with no trustworthy digest stays put, and its frozen bytes still go out", function()
        legacy("m9", {
            revision = 3, acked = 0, identity = "unresolved",
            ["local"] = { progression = 0.7, xpointer = "/unsent" },
            pending = { revision = 3, raw = '{"op_id":"left"}', device_id = "dev-1", work_id = "w1" },
        })
        e:migrateLegacy()
        local found
        for _i, b in ipairs(e:documents()) do
            if b.md5 == "m9" then found = b end
        end
        assert(found, "the leftover is not read back")
        assert.are.equal("m9", found.legacy_key)
        assert.is_not_nil(e.store:read("books", "m9"), "a document it could not trust was moved or deleted")
        -- The exact bytes are delivered, and the position that was never
        -- frozen is not turned into one.
        e:account()
        fake:respond(200, { results = { { op_id = "left", status = "applied", seq = 1 } } })
        assert.are.equal("acked", e:send(found))
        assert.are.equal('{"ops":[{"op_id":"left"}]}', fake:last().body)
        assert.is_true(e:saveBook(found))
        assert.is_nil(assert(e.store:read("books", "m9")).pending)
        assert.are.equal(0.7, found["local"].progression)
    end)

    it("a shaky digest is not trusted to name a document", function()
        legacy("m8", { sha256 = "sha-a", identity = "resolved", work_id = "w1", epoch = "epoch-1", revision = 2 })
        e:migrateLegacy()
        assert.is_not_nil(e.store:read("books", "m8"), "a name that is not a digest was adopted")
        assert.is_nil(e.store:read("editions", "sha-a"))
    end)

    it("documents reads editions and leftovers in one list", function()
        local a = resolved(e, "m1", SHA_A)
        assert.is_true(e:saveBook(a))
        legacy("m9", { revision = 0, acked = 0 })
        local seen = {}
        for _i, b in ipairs(e:documents()) do seen[b.legacy_key or b.sha256] = true end
        assert.is_true(seen[SHA_A])
        assert.is_true(seen["m9"])
    end)
end)
