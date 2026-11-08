local Api = require("liseur_api")
local Downloads = require("liseur_downloads")
local Fake = require("fake_http")

local function tmpdir()
    local p = os.tmpname()
    os.remove(p)
    os.execute("mkdir -p '" .. p .. "'")
    return p
end

local function exists(path)
    local f = io.open(path, "rb")
    if f then f:close() return true end
    return false
end

local function read(path)
    local f = io.open(path, "rb")
    local s = f:read("*a")
    f:close()
    return s
end

local function files(dir)
    local out = {}
    local p = io.popen("ls -A '" .. dir .. "'")
    for line in p:lines() do out[#out + 1] = line end
    p:close()
    return out
end

-- The "hash" is the content itself, which is enough to check that the
-- verified bytes are exactly what was streamed.
local deps = {
    exists = exists,
    sha256 = function()
        local parts = {}
        return function(chunk)
            if chunk then parts[#parts + 1] = chunk return end
            return table.concat(parts)
        end
    end,
}

local function api(fake)
    return Api.new{ base = Api.parseBase("https://h.example"), token = "t", transport = fake.transport }
end

describe("liseur_downloads names", function()
    it("never yields a path separator or a hidden file", function()
        local name = Downloads.filename({ title = "../../etc/passwd", authors = { "A/B" }, media_type = "application/epub+zip" })
        assert.is_nil(name:find("/"))
        assert.are.equal("A_B - .._.._etc_passwd.epub", name)
        assert.are.equal("book.epub", Downloads.filename({ title = "...", authors = {} }))
        assert.are.equal("T.pdf", Downloads.filename({ title = "T", authors = {}, filename = "x.PDF" }))
    end)

    it("keeps long names whole UTF-8 characters", function()
        local name = Downloads.filename({ title = string.rep("é", 100), authors = {} })
        assert.truthy(#name <= 125)
        assert.is_nil(name:find("[\192-\255]%.epub$"))
    end)

    it("finds a free name instead of overwriting", function()
        local taken = { ["/d/T.epub"] = true, ["/d/T (2).epub"] = true }
        assert.are.equal("/d/T (3).epub", Downloads.freePath("/d", "T.epub", function(p) return taken[p] end))
    end)
end)

describe("liseur_downloads cached copies", function()
    local dir, path
    local function content(p)
        local f = io.open(p, "rb")
        if not f then return nil end
        local s = f:read("*a")
        f:close()
        return s
    end
    local function write(s)
        local f = assert(io.open(path, "wb"))
        f:write(s)
        f:close()
    end
    -- The content stands in for the digest, as in the download tests.
    local cached = {
        size = function(p) local s = content(p) return s and #s or nil end,
        sha256 = content,
    }
    before_each(function()
        dir = tmpdir()
        path = dir .. "/A - T.epub"
    end)

    it("reuses a copy whose bytes are still the catalogue book", function()
        write("hello world")
        assert.is_true(Downloads.intact({ path = path, sha256 = "hello world", size = 11 }, cached))
    end)

    it("refuses a copy replaced by different bytes of the same size", function()
        write("world hello")
        assert.is_false(Downloads.intact({ path = path, sha256 = "hello world", size = 11 }, cached))
    end)

    it("refuses a copy that is gone, changed size, or has no recorded digest", function()
        assert.is_false(Downloads.intact({ path = path, sha256 = "hello world", size = 11 }, cached))
        write("hello")
        assert.is_false(Downloads.intact({ path = path, sha256 = "hello world", size = 11 }, cached))
        write("hello world")
        assert.is_false(Downloads.intact({ path = path, size = 11 }, cached))
        assert.is_false(Downloads.intact(nil, cached))
    end)
end)

describe("liseur_downloads", function()
    local dir, fake
    local book
    before_each(function()
        dir = tmpdir()
        fake = Fake.new()
        book = { book_id = "b1", title = "T", authors = { "A" }, status = "active", sha256 = "hello world", size_bytes = 11 }
    end)

    it("verifies and moves a complete download into place", function()
        fake:respond(200, "hello world")
        local path = Downloads.download(api(fake), book, dir, deps)
        assert.are.equal(dir .. "/A - T.epub", path)
        assert.are.equal("hello world", read(path))
        assert.are.same({ "A - T.epub" }, files(dir))
        assert.matches("/v1/books/b1/download$", fake:last().url)
    end)

    it("does not keep a download that could not be made durable", function()
        fake:respond(200, "hello world")
        local failing = setmetatable({ fsync = function() return false, "EIO" end }, { __index = deps })
        local path, err = Downloads.download(api(fake), book, dir, failing)
        assert.is_nil(path)
        assert.are.equal("storage", err.kind)
        assert.are.same({}, files(dir))
    end)

    it("does not keep a truncated download", function()
        fake:respond(200, "hello")
        local path, err = Downloads.download(api(fake), book, dir, deps)
        assert.is_nil(path)
        assert.are.equal("corrupt", err.kind)
        assert.are.same({}, files(dir))
    end)

    it("does not keep the wrong content", function()
        fake:respond(200, "HELLO WORLD")
        local path, err = Downloads.download(api(fake), book, dir, deps)
        assert.is_nil(path)
        assert.are.equal("corrupt", err.kind)
        assert.are.same({}, files(dir))
    end)

    it("stops a body larger than the catalogue described", function()
        fake:respond(200, "hello world and more")
        local path = Downloads.download(api(fake), book, dir, deps)
        assert.is_nil(path)
        assert.are.same({}, files(dir))
    end)

    it("an error answer leaves nothing behind", function()
        fake:respond(409, '{"error":"the file behind this book changed on disk"}')
        local path, err = Downloads.download(api(fake), book, dir, deps)
        assert.is_nil(path)
        assert.are.equal("conflict", err.kind)
        assert.are.same({}, files(dir))
    end)

    it("never replaces an existing book", function()
        local f = io.open(dir .. "/A - T.epub", "wb")
        f:write("mine")
        f:close()
        fake:respond(200, "hello world")
        local path = Downloads.download(api(fake), book, dir, deps)
        assert.are.equal(dir .. "/A - T (2).epub", path)
        assert.are.equal("mine", read(dir .. "/A - T.epub"))
    end)

    it("leaves an existing file or link alone and writes nothing through it", function()
        local victim = dir .. "/victim"
        local f = io.open(victim, "wb") f:write("keep") f:close()
        local d = { exists = deps.exists, sha256 = deps.sha256, random = function() return 0 end }
        os.execute("ln -s '" .. victim .. "' '" .. dir .. "/.liseur-b1-00000000.part'")
        fake:respond(200, "hello world")
        local path, err = Downloads.download(api(fake), book, dir, d)
        assert.is_nil(path)
        assert.are.equal("storage", err.kind)
        assert.are.equal("keep", read(victim))
        assert.are.equal(0, #fake.requests)
    end)

    it("refuses a book that is not active", function()
        book.status = "missing"
        local path, err = Downloads.download(api(fake), book, dir, deps)
        assert.is_nil(path)
        assert.are.equal("unavailable", err.kind)
        assert.are.equal(0, #fake.requests)
    end)
end)
