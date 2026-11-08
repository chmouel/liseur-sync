-- Catalogue reads: folders, folder books, search, entities and books.
-- Every list is one page plus the opaque cursor for the next one; the
-- caller decides whether to ask for more. Nothing here writes.

local Api = require("liseur_api")

local present = Api.present
local Catalog = {}

Catalog.PAGE = 50
Catalog.KINDS = { "series", "contributors", "tags" }

local function list(v)
    return type(v) == "table" and v or {}
end

local function str(v)
    v = present(v)
    return type(v) == "string" and v or nil
end

-- book normalises one catalogue book object to the fields the UI uses.
function Catalog.book(b)
    local authors, series = {}, {}
    for _i, c in ipairs(list(b.contributors)) do
        local role = str(c.role)
        if role == nil or role == "" or role == "author" or role == "aut" then
            authors[#authors + 1] = str(c.name)
        end
    end
    for _i, s in ipairs(list(b.series)) do
        series[#series + 1] = { id = str(s.id), name = str(s.name), position = tonumber(present(s.position)) }
    end
    return {
        book_id = str(b.book_id),
        folder_id = str(b.folder_id),
        title = str(b.title) or str(b.filename) or "?",
        status = str(b.status),
        sha256 = str(b.sha256),
        size_bytes = tonumber(present(b.size_bytes)),
        media_type = str(b.media_type),
        filename = str(b.filename),
        description = str(b.description),
        publisher = str(b.publisher),
        published_date = str(b.published_date),
        authors = authors,
        series = series,
        languages = list(b.languages),
        subjects = list(b.subjects),
    }
end

local function books(raw)
    local out = {}
    for _i, b in ipairs(list(raw)) do out[#out + 1] = Catalog.book(b) end
    return out
end

function Catalog.folders(api, after)
    local data, err = api:get("/v1/folders", { limit = Catalog.PAGE, after = after })
    if not data then return nil, err end
    local out = {}
    for _i, f in ipairs(list(data.folders)) do
        out[#out + 1] = { folder_id = str(f.folder_id), name = str(f.name) or "?", kind = str(f.kind), accepts_uploads = f.accepts_uploads == true }
    end
    return out, str(data.next_after)
end

function Catalog.folderBooks(api, folder_id, cursor, order)
    local data, err = api:get("/v1/folders/" .. Api.escape(folder_id) .. "/books", { limit = Catalog.PAGE, cursor = cursor, order = order })
    if not data then return nil, err end
    return books(data.books), str(data.next_cursor)
end

-- search is unpaged on the server; truncated says the answer was cut.
function Catalog.search(api, folder_id, q)
    local data, err = api:get("/v1/folders/" .. Api.escape(folder_id) .. "/search", { q = q })
    if not data then return nil, err end
    return books(data.books), data.truncated == true
end

function Catalog.entities(api, kind, after)
    local data, err = api:get("/v1/entities/" .. Api.escape(kind), { limit = Catalog.PAGE, after = after })
    if not data then return nil, err end
    local out = {}
    for _i, e in ipairs(list(data.entities)) do
        out[#out + 1] = { id = str(e.id), name = str(e.name) or "?", book_count = tonumber(present(e.book_count)) or 0 }
    end
    return out, str(data.next_after)
end

-- sortSeries orders books by their position in one series. The server
-- pages entity books in catalogue order, so a series is read whole
-- (it is small) and ordered here.
function Catalog.sortSeries(list_, entity_id)
    local function pos(b)
        for _i, s in ipairs(b.series) do
            if s.id == entity_id then return s.position end
        end
    end
    table.sort(list_, function(a, b)
        local pa, pb = pos(a), pos(b)
        if pa and pb and pa ~= pb then return pa < pb end
        if pa and not pb then return true end
        if pb and not pa then return false end
        return a.title < b.title
    end)
    return list_
end

function Catalog.entityBooks(api, kind, entity_id, cursor)
    local data, err = api:get("/v1/entities/" .. Api.escape(kind) .. "/" .. Api.escape(entity_id) .. "/books", { limit = Catalog.PAGE, cursor = cursor })
    if not data then return nil, err end
    return books(data.books), str(data.next_cursor)
end

-- seriesBooks reads up to max_pages of a series and orders it.
function Catalog.seriesBooks(api, entity_id, max_pages)
    local all, cursor = {}, nil
    for _i = 1, max_pages or 10 do
        local page, next_or_err = Catalog.entityBooks(api, "series", entity_id, cursor)
        if not page then return nil, next_or_err end
        for _i, b in ipairs(page) do all[#all + 1] = b end
        cursor = next_or_err
        if not cursor then return Catalog.sortSeries(all, entity_id), false end
    end
    return Catalog.sortSeries(all, entity_id), true
end

function Catalog.getBook(api, book_id)
    local data, err = api:get("/v1/books/" .. Api.escape(book_id))
    if not data then return nil, err end
    return Catalog.book(data)
end

return Catalog
