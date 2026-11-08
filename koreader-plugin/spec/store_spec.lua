local Store = require("liseur_store")

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

describe("liseur_store", function()
    local dir, store
    before_each(function()
        dir = tmpdir()
        store = Store.new(dir, fs)
    end)

    it("round-trips a document and stamps the schema", function()
        assert.is_true(store:write("books", "abc", { revision = 2 }))
        local doc = store:read("books", "abc")
        assert.are.equal(2, doc.revision)
        assert.are.equal(Store.SCHEMA, doc.v)
        assert.are.same({ "abc" }, store:list("books"))
    end)

    it("ignores an interrupted save", function()
        store:write("books", "abc", { revision = 1 })
        local f = io.open(dir .. "/books/abc.json.tmp", "wb")
        f:write('{"revision":')
        f:close()
        assert.are.equal(1, store:read("books", "abc").revision)
        assert.are.same({ "abc" }, store:list("books"))
    end)

    it("reports a corrupt document instead of starting over", function()
        fs.mkdir(dir .. "/books")
        local f = io.open(dir .. "/books/bad.json", "wb")
        f:write("{nope")
        f:close()
        local doc, err = store:read("books", "bad")
        assert.is_nil(doc)
        assert.are.equal("corrupt", err)
        assert.is_nil((store:write("books", "bad", { revision = 1 })))
        assert.are.equal("{nope", io.open(dir .. "/books/bad.json", "rb"):read("*a"))
    end)

    it("never overwrites state from a newer plugin", function()
        fs.mkdir(dir .. "/account")
        local f = io.open(dir .. "/account/state.json", "wb")
        f:write('{"v":99,"cursor":5}')
        f:close()
        local doc, err = store:read("account", "state")
        assert.is_nil(doc)
        assert.are.equal("newer", err)
        assert.is_nil((store:write("account", "state", { cursor = 1 })))
        local raw = io.open(dir .. "/account/state.json", "rb"):read("*a")
        assert.are.equal('{"v":99,"cursor":5}', raw)
    end)

    it("a write that cannot be made durable fails and keeps the old document", function()
        store:write("books", "abc", { revision = 1 })
        local failing = Store.new(dir, {
            mkdir = fs.mkdir,
            fsync = function() return false, "EIO" end,
        })
        local ok, err = failing:write("books", "abc", { revision = 2 })
        assert.is_nil(ok)
        assert.matches("EIO", err)
        assert.are.equal(1, store:read("books", "abc").revision)
        local nodir = Store.new(dir, { mkdir = fs.mkdir, fsync_dir = function() return false, "EROFS" end })
        assert.is_nil((nodir:write("books", "abc", { revision = 3 })))
    end)

    it("an unreadable document is an error, not a fresh start", function()
        store:write("account", "state", { cursor = 9 })
        os.execute("chmod 000 '" .. dir .. "/account/state.json'")
        local doc, err = store:read("account", "state")
        os.execute("chmod 600 '" .. dir .. "/account/state.json'")
        assert.is_nil(doc)
        assert.are.equal("unreadable", err)
    end)

    it("syncs the directories holding state until they are published", function()
        local synced, fail = {}, false
        local s2 = Store.new(dir .. "/ns", {
            mkdir = fs.mkdir,
            fsync_dir = function(p)
                synced[#synced + 1] = p
                if fail and p == dir then return false, "EIO" end
                return true
            end,
        })
        fail = true
        assert.is_nil((s2:write("books", "a", {})))
        fail = false
        synced = {}
        assert.is_true(s2:write("books", "a", {}))
        assert.are.same({ dir .. "/ns/books", dir .. "/ns", dir }, { synced[1], synced[2], synced[3] },
            "a retry after a failed sync publishes every directory again")
        synced = {}
        assert.is_true(s2:write("books", "b", {}))
        assert.are.same({ dir .. "/ns/books" }, synced)
    end)

    it("refuses names that could escape its directory", function()
        assert.has_error(function() store:write("books", "../x", {}) end)
    end)
end)
