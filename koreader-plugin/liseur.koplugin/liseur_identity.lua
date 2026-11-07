-- Book identity: which server work a local file's reading belongs to.
-- A file is known by its KOReader partial MD5 plus its full SHA-256;
-- a path or a title is never identity, and changed bytes never inherit
-- the previous mapping.

local Api = require("liseur_api")

local Identity = {}

-- refresh updates the file facts on a book document. stat = {size,
-- mtime}; sha256_file(path) hashes the whole file. When the bytes
-- changed since the last look, the work mapping and every agreement
-- made about the old bytes are dropped; an upload already frozen for
-- the old work is kept aside and still delivered, but can no longer
-- touch the new bytes' state.
function Identity.refresh(book, path, stat, sha256_file)
    local changed = book.size ~= stat.size or book.mtime ~= stat.mtime
    book.path = path
    if not changed and book.sha256 then return false end
    local sha = sha256_file(path)
    if not sha then return false end
    if book.sha256 and book.sha256 ~= sha then
        -- Same partial MD5, different bytes: the MD5 no longer says
        -- which work this is, so only the new SHA-256 may.
        book.work_id = nil
        book.book_id = nil
        book.identity = "unresolved"
        book.works = nil
        book.md5_untrusted = true
        if book.pending then
            book.retired = book.retired or {}
            table.insert(book.retired, book.pending)
            book.pending = nil
        end
        book["local"] = nil
        book.baseline = nil
        book.refused_revision = nil
        book.acked = book.revision
        book.offer = nil
    end
    book.sha256 = sha
    book.size = stat.size
    book.mtime = stat.mtime
    return true
end

-- writable reports whether reading state may be attached to this book.
function Identity.writable(book)
    return book.identity == "resolved" and book.work_id ~= nil
end

local function identifier(identifiers, kind)
    for _i, id in ipairs(type(identifiers) == "table" and identifiers or {}) do
        if Api.present(id.kind) == kind then return Api.present(id.value) end
    end
end

-- resolve asks the server which work the book is. It prefers the
-- catalogue book the file was downloaded from, and checks that the
-- server's fingerprint for it matches the bytes on this device.
-- Returns true, or nil and err; on ambiguity it records the candidates
-- and leaves the book unwritable.
local function ambiguous(book, err)
    book.identity = "ambiguous"
    book.work_id = nil
    local works = err.data and Api.present(err.data.works)
    book.works = type(works) == "table" and works or nil
    return nil, { kind = "ambiguous", message = "this file matches more than one work on the server" }
end

function Identity.resolve(api, book)
    local data, err
    if book.book_id then
        data, err = api:postRaw("/v1/books/" .. Api.escape(book.book_id) .. "/resolve", "{}")
        if data then
            local remote_md5 = identifier(data.identifiers, "partial-md5")
            local remote_sha = identifier(data.identifiers, "sha256")
            if (remote_md5 and remote_md5 ~= book.md5) or (remote_sha and book.sha256 and remote_sha ~= book.sha256) then
                book.identity = "mismatch"
                book.work_id = nil
                return nil, { kind = "mismatch", message = "this file's bytes differ from the catalogue copy" }
            end
        elseif err.kind == "conflict" then
            return ambiguous(book, err)
        elseif err.kind ~= "not_found" then
            return nil, err
        else
            book.book_id = nil
        end
    end
    if not data then
        local ids = {}
        if book.sha256 then ids[#ids + 1] = { kind = "sha256", value = book.sha256 } end
        if not book.md5_untrusted then ids[#ids + 1] = { kind = "partial-md5", value = book.md5 } end
        if #ids == 0 then return nil, { kind = "unresolved", message = "this file has no usable fingerprint yet" } end
        data, err = api:post("/v1/works/resolve", { identifiers = ids })
        if not data then
            if err.kind == "conflict" then return ambiguous(book, err) end
            return nil, err
        end
    end
    if Api.present(data.confidence) == "low" then
        book.identity = "ambiguous"
        book.work_id = nil
        return nil, { kind = "ambiguous", message = "the server only matched this file by title and author" }
    end
    local work_id = Api.present(data.work_id)
    if type(work_id) ~= "string" or work_id == "" then
        return nil, { kind = "malformed", message = "the server did not name a work" }
    end
    book.work_id = work_id
    book.identity = "resolved"
    book.works = nil
    return true
end

return Identity
