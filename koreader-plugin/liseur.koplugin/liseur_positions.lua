-- Native reading-position sync for one account: local observations,
-- exact-byte retries of POST /v1/ops, the /v1/changes feed with 410
-- recovery, and the per-book three-way decision.

local Api = require("liseur_api")
local Merge = require("liseur_merge")

local Positions = {}
Positions.__index = Positions

Positions.PAGE = 500
Positions.MAX_PAGES = 200

local present = Api.present

-- deps: api, store, now() -> unix seconds, md5(string) -> hex,
-- random() -> [0,1), device_id, account_id
function Positions.new(deps)
    assert(deps.api and deps.store and deps.now and deps.md5 and deps.device_id, "missing dependency")
    local self = setmetatable(deps, Positions)
    self.random = self.random or math.random
    return self
end

function Positions.timestamp(t)
    local secs = math.floor(t)
    local ms = math.floor((t - secs) * 1000 + 0.5)
    if ms >= 1000 then secs, ms = secs + 1, 0 end
    return os.date("!%Y-%m-%dT%H:%M:%S", secs) .. string.format(".%03dZ", ms)
end

local function uuid_from_hex(h)
    return h:sub(1, 8) .. "-" .. h:sub(9, 12) .. "-" .. h:sub(13, 16) .. "-" .. h:sub(17, 20) .. "-" .. h:sub(21, 32)
end

-- Account document --------------------------------------------------

function Positions:account()
    if not self._account then
        local doc, err = self.store:read("account", "state")
        if err then return nil, err end
        self._account = doc or { cursor = 0, works = {} }
        self._account.works = self._account.works or {}
        self._account.cursor = self._account.cursor or 0
    end
    return self._account
end

function Positions:saveAccount()
    local acc = assert(self._account)
    acc.account_id = self.account_id
    acc.device_id = self.device_id
    return self.store:write("account", "state", acc)
end

-- Book documents ----------------------------------------------------

-- A book's state is named by the full SHA-256 of the bytes it describes,
-- and that digest never changes: two files KOReader's partial MD5 cannot
-- tell apart keep separate positions, revisions and uploads. The partial
-- MD5 only names the fingerprint cache that leads to an edition.

Positions.EDITION = "editions"
Positions.FINGERPRINT = "fingerprints"
Positions.LEGACY = "books"

-- nameable reports whether sha may name a document: the store refuses
-- anything else, and a crash while naming state is not a failure mode a
-- reader should ever meet. Digests from the server are checked strictly
-- in Identity.verified before they reach here.
function Positions.nameable(sha)
    return type(sha) == "string" and sha:match("^[%w_%-]+$") ~= nil
end

-- digest reports whether sha is a full SHA-256. Only those describe bytes
-- here, so only those are adopted from a legacy document: anything else
-- could name any file the old partial MD5 happened to reach.
function Positions.digest(sha)
    return type(sha) == "string" and #sha == 64 and sha:match("^%x+$") ~= nil
end

function Positions:newEpoch()
    return string.format("%08x%08x", math.floor(self.random() * 0xffffffff), math.floor(self.now() * 1000) % 0xffffffff)
end

-- fingerprint reads the cache behind one partial MD5: which digests have
-- been seen under it, and which file last held which. It never decides
-- identity, only saves a hash.
function Positions:fingerprint(md5)
    local doc, err = self.store:read(self.FINGERPRINT, md5)
    if err then return nil, err end
    doc = doc or { md5 = md5 }
    doc.shas = doc.shas or {}
    doc.files = doc.files or {}
    return doc
end

function Positions:saveFingerprint(md5, fp)
    return self.store:write(self.FINGERPRINT, md5, fp)
end

local function sha_count(fp)
    local n = 0
    for _k in pairs(fp.shas or {}) do n = n + 1 end
    return n
end

-- shared says the partial MD5 now names more than one file, so it can no
-- longer say which work a path belongs to: only the SHA-256 may. It is
-- recorded once and never taken back.
function Positions.shared(fp)
    return fp.shared == true or sha_count(fp) > 1
end

-- noteFile records that path holds sha. It only ever grows: a crash after
-- it leaves a digest that resolves to a document nobody wrote yet.
function Positions:noteFile(fp, path, sha, stat)
    if not Positions.nameable(sha) then return false end
    fp.shas[sha] = true
    if path and stat then fp.files[path] = { sha256 = sha, size = stat.size, mtime = stat.mtime } end
    if Positions.shared(fp) then fp.shared = true end
    return true
end

-- edition returns the document for these exact bytes, creating it when
-- the file has never been seen. A partial MD5 seen under two digests
-- makes every sibling untrusted for alias resolution.
function Positions:edition(sha, md5, fp)
    if not Positions.nameable(sha) then return nil, "this file has no digest to name its state by" end
    local doc, err = self.store:read(self.EDITION, sha)
    if err then return nil, err end
    if not doc then
        doc = { identity = "unresolved", revision = 0, acked = 0, epoch = self:newEpoch() }
    end
    doc.md5 = md5
    doc.sha256 = sha
    if fp and Positions.shared(fp) then doc.md5_untrusted = true end
    return doc
end

-- saveBook writes a document under its own digest. A legacy leftover
-- carries the key it was found under and goes back where it came from.
function Positions:saveBook(book)
    if book.legacy_key then
        local key = book.legacy_key
        book.legacy_key = nil
        local ok, err = self.store:write(self.LEGACY, key, book)
        book.legacy_key = key
        return ok, err
    end
    if not Positions.nameable(book.sha256) then
        return nil, "this book has no digest to name its state by"
    end
    return self.store:write(self.EDITION, book.sha256, book)
end

-- markUntrusted keeps a document's partial-MD5 alias honest. A partial
-- MD5 that now names more than one digest must never be sent as identity,
-- and a fingerprint record that cannot be read leaves the alias equally
-- unusable: the document resolves by its SHA-256 alone. The existing work
-- mapping is left untouched; only the alias is demoted.
function Positions:markUntrusted(doc)
    if not doc.md5 then return end
    local fp, err = self:fingerprint(doc.md5)
    if err or (fp and Positions.shared(fp)) then
        doc.md5_untrusted = true
    end
end

-- documents returns every book document on disk, editions followed by
-- legacy leftovers (marked legacy_key). The backlog, the retry timer and
-- the status screen all work from this.
function Positions:documents()
    local out = {}
    for _i, sha in ipairs(self.store:list(self.EDITION)) do
        local doc = self.store:read(self.EDITION, sha)
        if doc then
            doc.sha256 = doc.sha256 or sha
            -- State saved before a collision was known is read back through
            -- the authoritative fingerprint record, so a shared partial MD5
            -- never names a work from the backlog.
            self:markUntrusted(doc)
            out[#out + 1] = doc
        end
    end
    for _i, md5 in ipairs(self.store:list(self.LEGACY)) do
        local doc = self.store:read(self.LEGACY, md5)
        if doc then
            doc.legacy_key = md5
            out[#out + 1] = doc
        end
    end
    return out
end

-- migrateLegacy moves an old partial-MD5-keyed document to the edition
-- its recorded digest names. It is idempotent, and a document it cannot
-- trust is left where it is: it is still read back as a legacy leftover,
-- whose frozen uploads keep their exact bytes and are still delivered.
-- It returns true, or nil and err when state cannot be read: the caller
-- must not write on the strength of a migration that did not finish.
function Positions:migrateLegacy()
    for _i, md5 in ipairs(self.store:list(self.LEGACY)) do
        local doc, derr = self.store:read(self.LEGACY, md5)
        if derr then return nil, derr end
        local sha = doc and doc.sha256
        if doc and Positions.digest(sha) then
            local existing, rerr = self.store:read(self.EDITION, sha)
            if rerr then
                -- An unreadable destination is not an absent one. Keep the
                -- legacy copy and report, so nothing overwrites a document
                -- that may be there and no write proceeds on a guess.
                return nil, rerr
            elseif not existing then
                local fp, ferr = self:fingerprint(md5)
                if ferr then return nil, ferr end
                if fp then
                    fp.shas[sha] = true
                    if doc.md5_untrusted then fp.shared = true end
                    -- No file entry: the first open after migrating rehashes
                    -- once, rather than trusting a stat nobody checked.
                    local saved, serr = self:saveFingerprint(md5, fp)
                    if not saved then return nil, serr end
                    doc.legacy_key = nil
                    saved, serr = self:saveBook(doc)
                    if not saved then return nil, serr end
                    self.store:remove(self.LEGACY, md5)
                end
            elseif existing.epoch == doc.epoch then
                -- An earlier run already copied it, and the edition may have
                -- moved on since: never overwrite it.
                local saved, serr = self:saveBook(existing)
                if not saved then return nil, serr end
                self.store:remove(self.LEGACY, md5)
            end
        end
    end
    return true
end

function Positions.dirty(book)
    local rev = book.revision or 0
    return rev > (book.acked or 0) and rev ~= book.refused_revision
end

local function same_position(a, b)
    if a == nil or b == nil then return false end
    if a.xpointer and b.xpointer then return a.xpointer == b.xpointer end
    return a.progression == b.progression
end

-- observe records the reader's current position. initial marks the
-- position found at open: it is remembered without becoming something
-- this device owes the server, unless it differs from the last position
-- this plugin saw (the book was read elsewhere on this device).
function Positions:observe(book, pos, initial)
    if pos.progression == nil or pos.progression ~= pos.progression then return false end
    local p = math.max(0, math.min(1, pos.progression))
    local new = { progression = p, xpointer = pos.xpointer, at = self.now() }
    if same_position(book["local"], new) then return false end
    local had = book["local"] ~= nil
    book["local"] = new
    if not initial or had then
        book.revision = (book.revision or 0) + 1
    end
    return true
end

-- settle records a position reached by applying an agreed remote
-- position: it becomes the local position without being owed.
function Positions:settle(book, pos)
    book["local"] = { progression = pos.progression, xpointer = pos.xpointer, at = self.now() }
    book.acked = book.revision
end

-- Remote state ------------------------------------------------------

local function trim(op)
    return {
        op_id = present(op.op_id),
        seq = tonumber(present(op.seq)) or 0,
        device_id = present(op.device_id),
        progression = tonumber(present(op.progression)),
        foreign_pos = present(op.foreign_pos),
        edition_sha = present(op.edition_sha),
        origin = present(op.origin),
        origin_alias = present(op.origin_alias),
        client_ts = present(op.client_ts),
    }
end
Positions.trim = trim

function Positions:remote(book)
    local acc = self:account()
    local w = acc and book.work_id and acc.works[book.work_id]
    return w and w.remote
end

function Positions:floor(work_id)
    local acc = self:account()
    local w = acc and acc.works[work_id]
    return w and w.floor or 0
end

local function take(work, op)
    local t = trim(op)
    if t.op_id == nil or t.progression == nil then return end
    if work.remote == nil or t.seq > (work.remote.seq or 0) then
        work.remote = t
    end
end

-- track registers a work so the changes feed keeps its newest remote
-- position. The caller seeds it with seed() once.
function Positions:track(work_id)
    local acc = assert(self:account())
    if not acc.works[work_id] then
        acc.works[work_id] = { floor = 0 }
        return true
    end
    return false
end

-- seed reads the newest retained position for one work.
function Positions:seed(work_id)
    local data, err = self.api:get("/v1/works/" .. Api.escape(work_id) .. "/positions", { limit = 1 })
    if not data then return nil, err end
    local acc = assert(self:account())
    local work = acc.works[work_id] or { floor = 0 }
    acc.works[work_id] = work
    for _i, op in ipairs(type(data.ops) == "table" and data.ops or {}) do take(work, op) end
    work.floor = math.max(work.floor or 0, tonumber(present(data.furthest_floor)) or 0)
    work.seeded = true
    local ok, werr = self:saveAccount()
    if not ok then return nil, { kind = "storage", message = werr } end
    return true
end

local function learn_floors(acc, floors)
    if type(floors) ~= "table" then return end
    for work_id, floor in pairs(floors) do
        local w = acc.works[work_id]
        local f = tonumber(present(floor))
        if w and f and f > (w.floor or 0) then w.floor = f end
    end
end

-- resync rebuilds remote state from /v1/heads after the feed cursor
-- fell behind the server's compaction horizon. Local observations and
-- pending uploads are untouched.
function Positions:resync()
    local data, err = self.api:get("/v1/heads")
    if not data then return nil, err end
    local acc = assert(self:account())
    local newest = {}
    for _i, op in ipairs(type(data.ops) == "table" and data.ops or {}) do
        local w = present(op.work_id)
        if w and acc.works[w] then
            newest[w] = newest[w] or { floor = acc.works[w].floor }
            take(newest[w], op)
        end
    end
    for w, work in pairs(acc.works) do
        work.remote = newest[w] and newest[w].remote or nil
    end
    learn_floors(acc, data.furthest_floors)
    acc.cursor = tonumber(present(data.snapshot_seq)) or acc.cursor
    local ok, werr = self:saveAccount()
    if not ok then return nil, { kind = "storage", message = werr } end
    return true
end

-- pull follows /v1/changes from the stored cursor. Each page's remote
-- positions and its cursor are committed in one atomic write. With
-- max_pages it may stop early and returns true, "partial".
function Positions:pull(max_pages)
    local acc, aerr = self:account()
    if not acc then return nil, { kind = "storage", message = aerr } end
    for _i = 1, max_pages or Positions.MAX_PAGES do
        local data, err = self.api:get("/v1/changes", { since = acc.cursor, limit = Positions.PAGE })
        if not data then
            if err.kind == "gone" then return self:resync() end
            return nil, err
        end
        local next_cursor = acc.cursor
        local ops = type(data.ops) == "table" and data.ops or {}
        for _i, op in ipairs(ops) do
            local seq = tonumber(present(op.seq)) or 0
            if seq > next_cursor then next_cursor = seq end
            local w = present(op.work_id)
            if w and acc.works[w] then take(acc.works[w], op) end
        end
        if #ops == 0 then
            local hw = tonumber(present(data.high_water))
            if hw and hw > next_cursor then next_cursor = hw end
        end
        learn_floors(acc, data.furthest_floors)
        acc.cursor = next_cursor
        local ok, werr = self:saveAccount()
        if not ok then return nil, { kind = "storage", message = werr } end
        if data.has_more ~= true then return true end
    end
    return true, "partial"
end

-- Decisions ---------------------------------------------------------

-- remoteLocation returns the remote op's KOReader position when it is
-- bound to these exact bytes, else nil (percentage only). An op that names
-- an edition is bound to that edition alone: a different digest is a
-- different file, never a pass to the alias branch. A partial MD5 this
-- device no longer trusts (the bytes behind it changed) may not bind an op
-- either.
function Positions.remoteLocation(book, op)
    if not op or type(op.foreign_pos) ~= "string" or op.foreign_pos == "" then return nil end
    if op.edition_sha then
        if book.sha256 and op.edition_sha == book.sha256 then return op.foreign_pos end
        return nil
    end
    if op.origin_alias and not book.md5_untrusted
        and op.origin_alias == "partial-md5:" .. tostring(book.md5) then
        return op.foreign_pos
    end
    return nil
end

-- decide returns the merge decision for a book plus the remote op it
-- was made against. exact(remote_location, local_location) may report
-- whether both land on the same rendered page.
function Positions:decide(book, exact)
    local remote_op = self:remote(book)
    local l = book["local"] and Merge.state(book["local"].progression) or nil
    local base = book.baseline and Merge.state(book.baseline.progression) or nil
    local r
    -- The op the baseline was agreed on (including this device's own
    -- echo) is nothing new: only a different op is a remote change.
    if remote_op and not (book.baseline and remote_op.op_id == book.baseline.op_id) then
        r = Merge.state(remote_op.progression)
    end
    local agreement
    local where = r and Positions.remoteLocation(book, remote_op)
    if where and exact and book["local"] and book["local"].xpointer then
        agreement = exact(where, book["local"].xpointer)
    end
    local d = Merge.reconcile(l, r, base, Positions.dirty(book), agreement)
    d.op = remote_op
    return d
end

-- accept makes a shown remote candidate the agreed baseline. It refuses
-- when a newer remote position arrived after the prompt was shown.
function Positions:accept(book, op)
    local current = self:remote(book)
    if not current or current.op_id ~= op.op_id then return false end
    book.baseline = { progression = op.progression, xpointer = Positions.remoteLocation(book, op), op_id = op.op_id, seq = op.seq }
    return true
end

-- keep makes the local position win over a shown remote candidate.
function Positions:keep(book, op)
    local current = self:remote(book)
    if not current or current.op_id ~= op.op_id then return false end
    book.baseline = { progression = op.progression, op_id = op.op_id, seq = op.seq }
    if not Positions.dirty(book) then book.revision = (book.revision or 0) + 1 end
    return true
end

-- Upload ------------------------------------------------------------

-- prepare freezes the op for the current local revision and must be
-- persisted before anything is sent.
function Positions:prepare(book)
    if book.pending then return book.pending end
    local l = book["local"]
    if not l or not book.work_id then return nil end
    local op_id = uuid_from_hex(self.md5(table.concat({ self.device_id, book.work_id, book.epoch or "", tostring(book.revision) }, "|")))
    local op = {
        op_id = op_id,
        work_id = book.work_id,
        client_ts = Positions.timestamp(l.at or self.now()),
        progression = l.progression,
        foreign_pos = l.xpointer,
        edition_sha = book.sha256,
    }
    local json = require("json")
    book.pending = {
        revision = book.revision,
        op_id = op_id,
        raw = json.encode(op),
        device_id = self.device_id,
        work_id = book.work_id,
        progression = l.progression,
        xpointer = l.xpointer,
        attempts = 0,
    }
    return book.pending
end

-- send delivers the pending op with its exact stored bytes. Returns a
-- status string: "acked", "renamed", "refused", "unknown_work",
-- "retry", "paused_device", or nil, err for auth/permission problems.
function Positions:send(book)
    local p = book.pending
    if not p then return "nothing" end
    local acc, aerr = self:account()
    if not acc then return nil, { kind = "storage", message = "account state: " .. tostring(aerr) } end
    if p.device_id ~= self.device_id then return "paused_device" end
    if p.not_before and self.now() < p.not_before then return "retry" end
    local data, err = self.api:postRaw("/v1/ops", '{"ops":[' .. p.raw .. ']}')
    if not data then
        if err.kind == "refused" and err.code == "unknown_work" then
            book.pending = nil
            book.work_id = nil
            book.identity = "unresolved"
            return "unknown_work"
        end
        if err.kind == "refused" and err.code then
            book.pending = nil
            book.refused_revision = p.revision
            book.last_error = err.message
            return "refused"
        end
        if Api.retryable(err) then
            p.attempts = (p.attempts or 0) + 1
            p.not_before = self.now() + (err.retry_after or Api.backoff(p.attempts, self.random))
            return "retry"
        end
        return nil, err
    end
    local result = type(data.results) == "table" and data.results[1] or {}
    local status = present(result.status)
    if status == "applied" or status == "duplicate" then
        local seq = tonumber(present(result.seq)) or 0
        book.acked = math.max(book.acked or 0, p.revision)
        book.baseline = { progression = p.progression, xpointer = p.xpointer, op_id = p.op_id, seq = seq }
        book.pending = nil
        book.last_sync = self.now()
        book.last_error = nil
        local work = acc.works[p.work_id]
        if work and (work.remote == nil or seq > (work.remote.seq or 0)) then
            work.remote = {
                op_id = p.op_id, seq = seq, device_id = self.device_id,
                progression = p.progression, foreign_pos = p.xpointer, edition_sha = book.sha256,
            }
            self:saveAccount()
        end
        return "acked"
    end
    if status == "conflict" then
        book.pending = nil
        book.revision = (book.revision or 0) + 1
        return "renamed"
    end
    p.attempts = (p.attempts or 0) + 1
    p.not_before = self.now() + Api.backoff(p.attempts, self.random)
    return "retry"
end

-- sendRetired delivers uploads frozen for bytes this file no longer
-- has. Their outcome changes nothing about the book's current state.
function Positions:sendRetired(book)
    local retired = book.retired or {}
    local kept = {}
    for i, p in ipairs(retired) do
        local waiting = p.device_id ~= self.device_id or (p.not_before and self.now() < p.not_before)
        if waiting then
            kept[#kept + 1] = p
        else
            local data, err = self.api:postRaw("/v1/ops", '{"ops":[' .. p.raw .. ']}')
            local status = data and type(data.results) == "table" and data.results[1] and present(data.results[1].status)
            if not data and (err.kind == "auth" or err.kind == "forbidden") then
                for j = i, #retired do kept[#kept + 1] = retired[j] end
                book.retired = kept
                return nil, err
            end
            -- Delivered, or refused for good (a reused id, or a 400 the
            -- same bytes would get again): done. Anything else is an
            -- unknown outcome and the frozen bytes stay.
            local settled = status == "applied" or status == "duplicate" or status == "conflict"
                or (not data and err.kind == "refused" and err.code ~= nil)
            if not settled then
                p.attempts = (p.attempts or 0) + 1
                p.not_before = self.now() + ((err and err.retry_after) or Api.backoff(p.attempts, self.random))
                kept[#kept + 1] = p
            end
        end
    end
    book.retired = #kept > 0 and kept or nil
    return true
end

-- discardPaused drops an upload frozen under another device identity;
-- the local position is re-sent from this device as a new revision.
function Positions:discardPaused(book)
    if book.pending and book.pending.device_id ~= self.device_id then
        book.pending = nil
        book.revision = (book.revision or 0) + 1
        return true
    end
    return false
end

-- History -----------------------------------------------------------

function Positions:history(work_id, limit)
    local data, err = self.api:get("/v1/works/" .. Api.escape(work_id) .. "/positions", { limit = limit or 50 })
    if not data then return nil, err end
    local out = { ops = {}, furthest = {}, floor = tonumber(present(data.furthest_floor)) or 0 }
    for _i, op in ipairs(type(data.ops) == "table" and data.ops or {}) do out.ops[#out.ops + 1] = trim(op) end
    for _i, op in ipairs(type(data.furthest) == "table" and data.furthest or {}) do
        local t = trim(op)
        if t.seq > out.floor then out.furthest[#out.furthest + 1] = t end
    end
    return out
end

function Positions:forgetFurthest(work_id, upto_seq)
    local data, err = self.api:post("/v1/works/" .. Api.escape(work_id) .. "/furthest/forget", { upto_seq = upto_seq })
    if not data then return nil, err end
    local floor = tonumber(present(data.furthest_floor)) or upto_seq
    local acc = self:account()
    if acc and acc.works[work_id] then
        acc.works[work_id].floor = math.max(acc.works[work_id].floor or 0, floor)
        self:saveAccount()
    end
    return floor
end

return Positions
