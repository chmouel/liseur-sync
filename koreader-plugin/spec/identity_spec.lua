local Api = require("liseur_api")
local Fake = require("fake_http")
local Identity = require("liseur_identity")
local json = require("json")

local function api(fake)
    return Api.new{ base = Api.parseBase("https://h.example"), token = "t", transport = fake.transport }
end

describe("liseur_identity", function()
    it("changed bytes never keep the previous work", function()
        local book = { md5 = "m", sha256 = "old", size = 10, mtime = 1, work_id = "w", book_id = "b", identity = "resolved" }
        assert.is_false(Identity.refresh(book, "/f.epub", { size = 10, mtime = 1 }, function() error("not rehashed") end))
        Identity.refresh(book, "/f.epub", { size = 11, mtime = 2 }, function() return "new" end)
        assert.are.equal("new", book.sha256)
        assert.is_nil(book.work_id)
        assert.is_nil(book.book_id)
        assert.is_false(Identity.writable(book))
    end)

    it("changed bytes under the same partial MD5 resolve by SHA-256 alone", function()
        local fake = Fake.new()
        local book = { md5 = "m", sha256 = "old", size = 10, mtime = 1, work_id = "w", identity = "resolved" }
        Identity.refresh(book, "/f.epub", { size = 12, mtime = 2 }, function() return "new" end)
        fake:respond(201, { work_id = "w2", confidence = "high", created = true })
        assert.is_true(Identity.resolve(api(fake), book))
        assert.are.same({ { kind = "sha256", value = "new" } }, json.decode(fake:last().body).identifiers)
    end)

    it("new bytes forget every agreement about the old ones", function()
        local book = {
            md5 = "m", sha256 = "old", size = 10, mtime = 1, work_id = "w", identity = "resolved",
            ["local"] = { progression = 0.9 }, baseline = { progression = 0.9, op_id = "o" },
            revision = 4, acked = 3, pending = { revision = 4, raw = "{}" },
        }
        Identity.refresh(book, "/f.epub", { size = 12, mtime = 2 }, function() return "new" end)
        assert.is_nil(book["local"])
        assert.is_nil(book.baseline)
        assert.is_nil(book.pending)
        assert.are.equal(1, #book.retired)
        assert.are.equal(book.revision, book.acked)
    end)

    it("a touched file with the same bytes keeps its work", function()
        local book = { md5 = "m", sha256 = "same", size = 10, mtime = 1, work_id = "w", identity = "resolved" }
        Identity.refresh(book, "/f.epub", { size = 10, mtime = 5 }, function() return "same" end)
        assert.are.equal("w", book.work_id)
    end)

    it("resolves a sideloaded file by its fingerprints", function()
        local fake = Fake.new()
        fake:respond(201, { work_id = "w1", confidence = "high", created = true })
        local book = { md5 = "abc", sha256 = "def" }
        assert.is_true(Identity.resolve(api(fake), book))
        assert.are.equal("w1", book.work_id)
        local sent = json.decode(fake:last().body)
        assert.are.same({ { kind = "sha256", value = "def" }, { kind = "partial-md5", value = "abc" } }, sent.identifiers)
        assert.is_nil(sent.title)
    end)

    it("an ambiguous file is never attached to a work", function()
        local fake = Fake.new()
        fake:respond(409, { error = "identifiers resolve to multiple works", works = { "w1", "w2" } })
        local book = { md5 = "abc" }
        local ok, err = Identity.resolve(api(fake), book)
        assert.is_nil(ok)
        assert.are.equal("ambiguous", err.kind)
        assert.are.equal("ambiguous", book.identity)
        assert.are.same({ "w1", "w2" }, book.works)
        assert.is_false(Identity.writable(book))
    end)

    it("a title-only match is not accepted", function()
        local fake = Fake.new()
        fake:respond(200, { work_id = "w1", confidence = "low", created = false })
        local book = { md5 = "abc" }
        assert.is_nil((Identity.resolve(api(fake), book)))
        assert.is_false(Identity.writable(book))
    end)

    it("a downloaded book resolves through the catalogue and checks the fingerprint", function()
        local fake = Fake.new()
        fake:respond(200, { book_id = "b1", work_id = "w1", identifiers = { { kind = "partial-md5", value = "abc" } } })
        local book = { md5 = "abc", book_id = "b1" }
        assert.is_true(Identity.resolve(api(fake), book))
        assert.matches("/v1/books/b1/resolve$", fake:last().url)
        assert.are.equal("w1", book.work_id)

        fake:respond(200, { book_id = "b1", work_id = "w1", identifiers = { { kind = "partial-md5", value = "zzz" } } })
        local other = { md5 = "abc", book_id = "b1" }
        local ok, err = Identity.resolve(api(fake), other)
        assert.is_nil(ok)
        assert.are.equal("mismatch", err.kind)
        assert.is_false(Identity.writable(other))
    end)

    it("a catalogue match by title and author alone is not accepted", function()
        local fake = Fake.new()
        fake:respond(200, { book_id = "b1", work_id = "w1", confidence = "low", identifiers = {} })
        local book = { md5 = "abc", book_id = "b1" }
        local ok, err = Identity.resolve(api(fake), book)
        assert.is_nil(ok)
        assert.are.equal("ambiguous", err.kind)
        assert.is_false(Identity.writable(book))
    end)

    it("a catalogue copy with other bytes is a mismatch", function()
        local fake = Fake.new()
        fake:respond(200, { book_id = "b1", work_id = "w1", identifiers = { { kind = "sha256", value = "theirs" } } })
        local book = { md5 = "abc", sha256 = "mine", book_id = "b1" }
        assert.is_nil((Identity.resolve(api(fake), book)))
        assert.are.equal("mismatch", book.identity)
    end)

    it("falls back to fingerprints when the catalogue book is gone", function()
        local fake = Fake.new()
        fake:respond(404, { error = "book not found" })
        fake:respond(200, { work_id = "w2", confidence = "high" })
        local book = { md5 = "abc", book_id = "b1" }
        assert.is_true(Identity.resolve(api(fake), book))
        assert.are.equal("w2", book.work_id)
        assert.is_nil(book.book_id)
    end)
end)
