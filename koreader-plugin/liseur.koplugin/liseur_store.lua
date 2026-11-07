-- Durable plugin state: small JSON documents written atomically
-- (temporary file, fsync, rename) under one directory per server and
-- account. Each document carries a schema version; a document written by
-- a newer plugin is never overwritten.

local json = require("json")

local Store = {}
Store.__index = Store

Store.SCHEMA = 1

local function valid_name(name)
    return type(name) == "string" and name:match("^[%w_%-]+$") ~= nil
end

-- fs: { mkdir(path), fsync(file)?, fsync_dir(dir)?, list(dir)? }. The
-- fsync functions return false, err on failure; a write that cannot be
-- made durable is reported as failed, so nothing is sent on its strength.
function Store.new(dir, fs)
    assert(dir and fs and fs.mkdir, "dir and fs.mkdir required")
    return setmetatable({ dir = dir, fs = fs, published = {} }, Store)
end

function Store:path(kind, name)
    assert(valid_name(kind) and valid_name(name), "invalid state name")
    return self.dir .. "/" .. kind .. "/" .. name .. ".json"
end

-- read returns the document, nil when absent, or nil, err when it
-- cannot be trusted (corrupt or written by a newer schema).
function Store:read(kind, name)
    local f, oerr, code = io.open(self:path(kind, name), "rb")
    if not f then
        -- Only a document that is not there is absent; anything else
        -- (permissions, I/O) must not be mistaken for a fresh start.
        if code == 2 or (oerr and oerr:find("No such file")) then return nil end
        return nil, "unreadable"
    end
    local raw = f:read("*a")
    f:close()
    local ok, doc = pcall(json.decode, raw)
    if not ok or type(doc) ~= "table" then
        return nil, "corrupt"
    end
    if type(doc.v) == "number" and doc.v > Store.SCHEMA then
        return nil, "newer"
    end
    return doc
end

function Store:write(kind, name, doc)
    local _, err = self:read(kind, name)
    if err == "newer" then
        return nil, "state was written by a newer plugin version; refusing to overwrite it"
    end
    doc.v = Store.SCHEMA
    local ok, encoded = pcall(json.encode, doc)
    if not ok then return nil, "cannot encode state: " .. tostring(encoded) end
    local dir = self.dir .. "/" .. kind
    self.fs.mkdir(dir)
    local path = self:path(kind, name)
    local tmp = path .. ".tmp"
    local f, oerr = io.open(tmp, "wb")
    if not f then return nil, "cannot write state: " .. tostring(oerr) end
    local wok, werr = f:write(encoded)
    if wok then wok, werr = f:flush() end
    if wok and self.fs.fsync then
        local sok, serr = self.fs.fsync(f)
        if sok == false then wok, werr = false, serr end
    end
    f:close()
    if not wok then
        os.remove(tmp)
        return nil, "cannot write state: " .. tostring(werr)
    end
    local rok, rerr = os.rename(tmp, path)
    if not rok then
        os.remove(tmp)
        return nil, "cannot replace state: " .. tostring(rerr)
    end
    if self.fs.fsync_dir then
        -- The rename must survive a power cut, and so must the
        -- directories holding it: those are synced until one write in
        -- this process has published them all, and again after any
        -- failure.
        local dirs = { dir }
        if not self.published[kind] then
            local d = dir
            for _ = 1, 3 do
                d = d:match("^(.*)/[^/]+$")
                if not d or d == "" then break end
                dirs[#dirs + 1] = d
            end
        end
        for _i, d in ipairs(dirs) do
            local dok, derr = self.fs.fsync_dir(d)
            if dok == false then
                self.published[kind] = nil
                return nil, "cannot make state durable: " .. tostring(derr)
            end
        end
        self.published[kind] = true
    end
    return true
end

function Store:remove(kind, name)
    os.remove(self:path(kind, name))
end

-- list returns the names of documents of one kind.
function Store:list(kind)
    local names = {}
    if self.fs.list then
        for _i, file in ipairs(self.fs.list(self.dir .. "/" .. kind) or {}) do
            local name = file:match("^([%w_%-]+)%.json$")
            if name then names[#names + 1] = name end
        end
        table.sort(names)
    end
    return names
end

return Store
