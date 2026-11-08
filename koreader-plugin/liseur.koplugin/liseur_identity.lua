-- Book identity: which server work a local file's reading belongs to.
-- A file is known by its KOReader partial MD5 plus its full SHA-256;
-- a path or a title is never identity, and changed bytes never inherit
-- the previous mapping.

local Api = require("liseur_api")

local Identity = {}

local function digest(v)
    return type(v) == "string" and v:match("^%x+$") ~= nil
end

-- state_name mirrors what the store accepts as a document name.
local function state_name(v)
    return type(v) == "string" and v:match("^[%w_%-]+$") ~= nil
end

-- verified normalises a digest the caller checked against the catalogue.
-- A value that is not a full SHA-256 is not a verified digest.
function Identity.verified(sha)
    if not digest(sha) then return nil end
    sha = sha:lower()
    if #sha == 64 then return sha end
end

-- locate returns the SHA-256 of the bytes at path, or nil when they
-- cannot be read. It reads the whole file at every identity boundary:
-- two files with the same partial MD5, size and mtime can hold different
-- bytes, so a cached stat is never proof of content, even for a path
-- this device hashed before. verified_sha is a digest the caller checked
-- against the catalogue for these exact bytes (a download just verified
-- on the wire) and is taken as it is. A failed read returns nothing, so
-- the caller opens no session and the next attempt hashes again instead
-- of trusting a look that never succeeded.
function Identity.locate(path, sha256_file, verified_sha)
    local sha = Identity.verified(verified_sha)
    if sha then return sha end
    local hashed = sha256_file and sha256_file(path)
    if not state_name(hashed) then return nil end
    return hashed:lower()
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
    -- The catalogue joins on every digest the server holds for the book,
    -- including the partial MD5, so it is only asked while that MD5 still
    -- names these bytes. Otherwise unchanged sample windows would hand
    -- the file to whatever work owns the book it used to be, and its
    -- reading would follow the old positions.
    if book.book_id and not book.md5_untrusted then
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
