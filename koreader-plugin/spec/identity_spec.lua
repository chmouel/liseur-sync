local Api = require("liseur_api")
local Fake = require("fake_http")
local Identity = require("liseur_identity")
local json = require("json")

local function api(fake)
    return Api.new{ base = Api.parseBase("https://h.example"), token = "t", transport = fake.transport }
end

describe("liseur_identity", function()
    local SHA_A = string.rep("a", 64)
    local SHA_B = string.rep("b", 64)

    describe("locate", function()
        it("hashes the file when nothing cached it", function()
            local asked = 0
            local sha = Identity.locate("/a.epub", function(path)
                asked = asked + 1
                assert.are.equal("/a.epub", path)
                return SHA_A
            end)
            assert.are.equal(SHA_A, sha)
            assert.are.equal(1, asked)
        end)

        -- F5: a warm cache entry is not proof of content. A file replaced
        -- under an unchanged size and mtime is different bytes, so an
        -- identity boundary always reads the file.
        it("a warm cache entry never replaces a read, even on an unchanged stat", function()
            local asked = 0
            local sha = Identity.locate("/a.epub", function()
                asked = asked + 1
                return SHA_B
            end)
            assert.are.equal(SHA_B, sha, "the cached digest was trusted over the bytes")
            assert.are.equal(1, asked)
        end)

        -- F1: a hash that could not be read leaves no trace, so the next
        -- look at the same path and the same stat is a real read again.
        it("a failed read returns nothing and is retried, not cached", function()
            local calls = 0
            local function failing()
                calls = calls + 1
                return nil
            end
            assert.is_nil(Identity.locate("/a.epub", failing))
            assert.are.equal(1, calls)
            assert.is_nil(Identity.locate("/a.epub", failing))
            assert.are.equal(2, calls, "the failed read was taken as an answer")
            assert.are.equal(SHA_A, Identity.locate("/a.epub", function() return SHA_A end))
        end)

        it("a verified digest needs no read", function()
            local sha = Identity.locate("/a.epub",
                function() error("a verified digest must not be recomputed") end, SHA_B:upper())
            assert.are.equal(SHA_B, sha, "the verified digest was not normalised")
        end)

        it("a malformed verified digest is not a verified digest", function()
            local asked = 0
            local sha = Identity.locate("/a.epub", function()
                asked = asked + 1
                return SHA_A
            end, "not-a-digest")
            assert.are.equal(SHA_A, sha)
            assert.are.equal(1, asked, "a malformed digest was taken as proof")
            assert.is_nil(Identity.verified("sha-a"))
            assert.is_nil(Identity.verified(string.rep("a", 63)))
            assert.are.equal(SHA_A, Identity.verified(string.rep("A", 64)))
        end)
    end)

    it("changed bytes under the same partial MD5 resolve by SHA-256 alone", function()
        local fake = Fake.new()
        local book = { md5 = "m", sha256 = "new", md5_untrusted = true }
        fake:respond(201, { work_id = "w2", confidence = "high", created = true })
        assert.is_true(Identity.resolve(api(fake), book))
        assert.are.same({ { kind = "sha256", value = "new" } }, json.decode(fake:last().body).identifiers)
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

    it("changed bytes never resolve through the catalogue book they came from", function()
        local fake = Fake.new()
        fake:respond(201, { work_id = "w2", confidence = "high", created = true })
        local book = { md5 = "m", sha256 = "new", book_id = "b1", md5_untrusted = true }
        assert.is_true(Identity.resolve(api(fake), book))
        assert.matches("/v1/works/resolve$", fake:last().url)
        assert.are.same({ { kind = "sha256", value = "new" } }, json.decode(fake:last().body).identifiers)
        assert.are.equal("w2", book.work_id)
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
