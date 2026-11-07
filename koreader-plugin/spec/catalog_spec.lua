local Api = require("liseur_api")
local Catalog = require("liseur_catalog")
local Fake = require("fake_http")
local json = require("json")

local function api(fake)
    return Api.new{ base = Api.parseBase("https://h.example/p"), token = "t", transport = fake.transport }
end

describe("liseur_catalog", function()
    it("normalises a book and tolerates nulls", function()
        local b = Catalog.book({
            book_id = "b", title = "T", status = "active", sha256 = "s", size_bytes = 3,
            contributors = { { id = "c", name = "Ann", role = "author" }, { id = "d", name = "Ed", role = "editor" } },
            series = { { id = "s1", name = "S", position = 2 } },
            description = json.null,
        })
        assert.are.same({ "Ann" }, b.authors)
        assert.are.equal(2, b.series[1].position)
        assert.is_nil(b.description)
    end)

    it("pages folders with the opaque after cursor", function()
        local fake = Fake.new()
        fake:respond(200, { folders = { { folder_id = "f", name = "F", kind = "plain" } }, next_after = "c1" })
        local folders, next_after = Catalog.folders(api(fake), nil)
        assert.are.equal("F", folders[1].name)
        assert.are.equal("c1", next_after)
        fake:respond(200, { folders = {} })
        local _, last = Catalog.folders(api(fake), "c1")
        assert.is_nil(last)
        assert.matches("after=c1", fake:last().url)
    end)

    it("pages folder books with the cursor parameter", function()
        local fake = Fake.new()
        fake:respond(200, { books = { { book_id = "b", title = "T" } }, next_cursor = "n" })
        local books, next_cursor = Catalog.folderBooks(api(fake), "f/1", nil, "recent")
        assert.are.equal("n", next_cursor)
        assert.are.equal("b", books[1].book_id)
        assert.matches("/p/v1/folders/f%%2F1/books%?limit=50&order=recent$", fake:last().url)
    end)

    it("reads a whole series and orders it by position", function()
        local fake = Fake.new()
        fake:respond(200, { books = {
            { book_id = "3", title = "C", series = { { id = "s", position = 3 } } },
            { book_id = "x", title = "Loose", series = { { id = "s" } } },
        }, next_cursor = "k" })
        fake:respond(200, { books = {
            { book_id = "1", title = "A", series = { { id = "s", position = 1 } } },
        } })
        local books, more = Catalog.seriesBooks(api(fake), "s", 10)
        assert.is_false(more)
        assert.are.same({ "1", "3", "x" }, { books[1].book_id, books[2].book_id, books[3].book_id })
    end)

    it("reports a truncated search", function()
        local fake = Fake.new()
        fake:respond(200, { books = {}, facets = {}, truncated = true })
        local _, truncated = Catalog.search(api(fake), "f", "dune")
        assert.is_true(truncated)
        assert.matches("q=dune", fake:last().url)
    end)
end)
