-- Scripted HTTP transport for specs: each request takes the next
-- response from the queue and is recorded for assertions.

local json = require("json")

local Fake = {}
Fake.__index = Fake

function Fake.new()
    local self = setmetatable({ queue = {}, requests = {} }, Fake)
    self.transport = function(req) return self:handle(req) end
    return self
end

-- respond(status, body_table_or_string, headers) or respond(nil, "error")
function Fake:respond(status, body, headers)
    table.insert(self.queue, { status = status, body = body, headers = headers })
    return self
end

function Fake:handle(req)
    table.insert(self.requests, req)
    local r = table.remove(self.queue, 1)
    if not r then error("unexpected request " .. req.method .. " " .. req.url) end
    if r.status == nil then return nil, r.body end
    local body = r.body
    if type(body) == "table" then body = json.encode(body) end
    if req.sink then
        if body and body ~= "" then
            local ok, err = req.sink(body)
            if not ok then return nil, err end
        end
        req.sink(nil)
        return { status = r.status, headers = r.headers or {}, body = "" }
    end
    return { status = r.status, headers = r.headers or {}, body = body or "" }
end

function Fake:last()
    return self.requests[#self.requests]
end

return Fake
