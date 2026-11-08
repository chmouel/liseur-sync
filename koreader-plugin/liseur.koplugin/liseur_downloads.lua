-- Catalogue downloads: stream into a temporary file beside the target,
-- verify length and SHA-256 against the catalogue, then rename into
-- place. A partial download is never left under a book's name, and a name
-- already taken in the folder is passed over for a free one. The check
-- for that free name and the rename are two steps, so another process
-- writing the same name in between is the one acknowledged race (see the
-- plugin README): Lua has no no-replace rename.

local Api = require("liseur_api")

local Downloads = {}

local EXTENSIONS = {
    ["application/epub+zip"] = "epub",
    ["application/pdf"] = "pdf",
    ["application/x-mobipocket-ebook"] = "mobi",
    ["application/x-cbz"] = "cbz",
    ["text/plain"] = "txt",
}

local function extension(book)
    local ext = book.filename and book.filename:match("%.([%w]+)$")
    if ext and #ext <= 5 then return ext:lower() end
    return EXTENSIONS[book.media_type or ""] or "epub"
end

-- filename builds "Author - Title.ext" with only characters every
-- KOReader filesystem accepts; it never contains a path separator.
function Downloads.filename(book)
    local author = book.authors and book.authors[1]
    local base = author and (author .. " - " .. book.title) or book.title or book.book_id or "book"
    base = base:gsub("[%z\1-\31\127/\\:%*%?\"<>|]", "_")
    base = base:gsub("^[%s%.]+", ""):gsub("[%s%.]+$", "")
    if base == "" then base = "book" end
    if #base > 120 then
        base = base:sub(1, 120)
        -- do not cut a UTF-8 sequence in half
        while #base > 0 and base:byte(#base) >= 0x80 and base:byte(#base) < 0xC0 do base = base:sub(1, -2) end
        if #base > 0 and base:byte(#base) >= 0xC0 then base = base:sub(1, -2) end
    end
    return base .. "." .. extension(book)
end

-- freePath returns dir/name, or dir/name (2), (3)... when taken.
function Downloads.freePath(dir, name, exists)
    local stem, ext = name:match("^(.*)(%.[^.]+)$")
    if not stem then stem, ext = name, "" end
    local candidate = dir .. "/" .. name
    local n = 2
    while exists(candidate) do
        if n > 999 then return nil end
        candidate = string.format("%s/%s (%d)%s", dir, stem, n, ext)
        n = n + 1
    end
    return candidate
end

-- intact reports whether the file at an index entry's path is still the
-- catalogue book downloaded there. Size is the cheap refusal; the digest
-- is what decides, because a replaced file can keep both the name and the
-- size. An entry with no recorded digest, or a file that cannot be read,
-- is not intact: downloading again is the safe answer.
-- deps: size(path) -> bytes|nil, sha256(path) -> hex|nil
-- Without deps.sha256 only the size is compared: cheap enough for a list
-- badge, never enough to open or reuse the file.
function Downloads.intact(entry, deps)
    if type(entry) ~= "table" or not entry.path or not entry.sha256 then return false end
    if deps.size(entry.path) ~= entry.size then return false end
    if not deps.sha256 then return true end
    local sha = deps.sha256(entry.path)
    return type(sha) == "string" and sha:lower() == tostring(entry.sha256):lower()
end

-- download fetches one catalogue book into dir.
-- deps: exists(path) (must not follow symlinks), sha256() ->
--       updater(chunk)/updater() -> hex, fsync(file)?, fsync_dir(dir)?,
--       random()?
-- Returns path or nil, err.
function Downloads.download(api, book, dir, deps)
    if book.status and book.status ~= "active" then
        return nil, { kind = "unavailable", message = "this book has no downloadable file right now" }
    end
    if not book.sha256 or not book.size_bytes then
        return nil, { kind = "malformed", message = "the catalogue did not describe this file" }
    end
    -- A fresh, unpredictable name that nothing (not even a symlink)
    -- occupies, since Lua cannot open a file exclusively.
    local tmp = string.format("%s/.liseur-%s-%08x.part", dir, (book.book_id or "book"):gsub("[^%w%-]", "_"),
        math.floor((deps.random or math.random)() * 0xffffffff))
    if deps.exists(tmp) then
        return nil, { kind = "storage", message = "a temporary download file is in the way; try again" }
    end
    local function transfer()
        local f, oerr = io.open(tmp, "wb")
        if not f then return nil, { kind = "storage", message = "cannot write to the download folder: " .. tostring(oerr) } end
        local hash = deps.sha256()
        local written, failure = 0, nil
        local limit = book.size_bytes
        local sink = function(chunk, err)
            if chunk == nil then return 1 end
            written = written + #chunk
            if written > limit then
                -- Keep reading a little so an error answer still reaches
                -- the caller with its status; stop a runaway body.
                failure = "the server sent more data than the catalogue described"
                if written > limit + 65536 then return nil, failure end
                return 1
            end
            local ok, werr = f:write(chunk)
            if not ok then
                failure = "cannot write the download: " .. tostring(werr)
                return nil, failure
            end
            hash(chunk)
            return 1
        end
        local ok, err = api:request("GET", "/v1/books/" .. Api.escape(book.book_id) .. "/download", {
            sink = sink,
            headers = { ["Accept"] = "*/*", ["Accept-Encoding"] = "identity" },
            expect_json = false,
        })
        local fok = f:flush()
        if fok and deps.fsync and deps.fsync(f) == false then fok = nil end
        f:close()
        if not ok or failure or not fok then
            os.remove(tmp)
            if not ok then return nil, err end
            if failure then return nil, { kind = "storage", message = failure } end
            return nil, { kind = "storage", message = "cannot finish writing the download" }
        end
        if written ~= book.size_bytes or hash() ~= book.sha256:lower() then
            os.remove(tmp)
            return nil, { kind = "corrupt", message = "the downloaded file does not match the catalogue; nothing was saved" }
        end
        return true
    end
    local ok, err
    if deps.worker then ok, err = deps.worker(transfer) else ok, err = transfer() end
    if not ok then
        os.remove(tmp)
        return nil, type(err) == "table" and err or { kind = "transport", message = err or "download interrupted" }
    end
    -- Checked immediately before the rename; on filesystems without a
    -- no-replace rename (FAT on e-readers) this is as close as Lua gets.
    local path = Downloads.freePath(dir, Downloads.filename(book), deps.exists)
    if not path then
        os.remove(tmp)
        return nil, { kind = "storage", message = "too many files with this name already" }
    end
    local ok, rerr = os.rename(tmp, path)
    if not ok then
        os.remove(tmp)
        return nil, { kind = "storage", message = "cannot move the download into place: " .. tostring(rerr) }
    end
    -- The new name must survive a power cut too, or a download reported
    -- as finished could vanish.
    if deps.fsync_dir then
        local sok, serr = deps.fsync_dir(dir)
        if sok == false then
            os.remove(path)
            return nil, { kind = "storage", message = "cannot finish writing the download: " .. tostring(serr) }
        end
    end
    return path
end

return Downloads
