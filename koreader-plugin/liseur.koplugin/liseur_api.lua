-- HTTP client for the Liseur Sync native API: base-URL rules, request
-- building and typed error classification. The byte transport is
-- injected so this module stays testable outside KOReader.

local json = require("json")

local Api = {}
Api.__index = Api

local LOOPBACK = { ["localhost"] = true, ["127.0.0.1"] = true, ["[::1]"] = true }
local DEFAULT_PORTS = { http = "80", https = "443" }

-- Values decoded from JSON null are functions under KOReader's luajson.
local function present(v)
    if v == nil or type(v) == "function" or type(v) == "userdata" then return nil end
    return v
end
Api.present = present

-- parseBase validates a user-supplied server URL. It returns a table
-- {scheme, host, port, prefix, origin, url} or nil and a message.
function Api.parseBase(input)
    if type(input) ~= "string" then return nil, "server URL required" end
    local s = input:gsub("^%s+", ""):gsub("%s+$", "")
    local scheme, rest = s:match("^(%a[%w+.-]*)://(.*)$")
    if not scheme then return nil, "server URL must start with https://" end
    scheme = scheme:lower()
    if scheme ~= "https" and scheme ~= "http" then
        return nil, "server URL must use https"
    end
    if rest:find("[?#]") then return nil, "server URL must not contain a query or fragment" end
    local authority, path = rest:match("^([^/]*)(.*)$")
    if authority == "" then return nil, "server URL has no host" end
    if authority:find("@") then return nil, "server URL must not contain credentials" end
    local host, port
    if authority:sub(1, 1) == "[" then
        host, port = authority:match("^(%[[%x:]+%])(.*)$")
        if not host then return nil, "invalid server host" end
    else
        host, port = authority:match("^([^:]+)(.*)$")
    end
    if not host or not host:match("^[%w%.%-%[%]:]+$") then return nil, "invalid server host" end
    host = host:lower()
    if port ~= "" then
        port = port:match("^:(%d+)$")
        if not port or tonumber(port) < 1 or tonumber(port) > 65535 then
            return nil, "invalid server port"
        end
        if port == DEFAULT_PORTS[scheme] then port = nil end
    else
        port = nil
    end
    if scheme == "http" and not LOOPBACK[host] then
        return nil, "plain http is only allowed for this device (localhost); use https"
    end
    path = path:gsub("/+$", "")
    if path:find("%.%.") or path:find("//") then return nil, "invalid server path" end
    local origin = scheme .. "://" .. host .. (port and (":" .. port) or "")
    return {
        scheme = scheme,
        host = host,
        port = port,
        prefix = path,
        origin = origin,
        url = origin .. path,
    }
end

-- namespace names the durable-state directory for one server and
-- account. Only lowercase letters and digits pass through and every
-- other byte, capitals included, becomes _xx: the encoding is
-- reversible even on a case-insensitive filesystem, so different
-- servers, schemes, prefixes or accounts never share a directory.
function Api.namespace(base, account_id)
    local function enc(s)
        return (tostring(s):gsub("[^%l%d]", function(c) return string.format("_%02x", c:byte()) end))
    end
    return enc(base.url) .. "--" .. enc(account_id)
end

function Api.escape(s)
    return (tostring(s):gsub("[^%w%-%._~]", function(c)
        return string.format("%%%02X", c:byte())
    end))
end

local function query_string(query)
    if not query then return "" end
    local keys = {}
    for k in pairs(query) do keys[#keys + 1] = k end
    table.sort(keys)
    local parts = {}
    for _i, k in ipairs(keys) do
        local v = query[k]
        if type(v) == "table" then
            for _i, item in ipairs(v) do parts[#parts + 1] = Api.escape(k) .. "=" .. Api.escape(item) end
        elseif v ~= nil and v ~= "" then
            parts[#parts + 1] = Api.escape(k) .. "=" .. Api.escape(v)
        end
    end
    if #parts == 0 then return "" end
    return "?" .. table.concat(parts, "&")
end

-- Api.new{base=parseBase(...), token=..., transport=fn, max_body=n}
function Api.new(o)
    assert(o.base and o.transport, "base and transport required")
    return setmetatable({
        base = o.base,
        token = o.token,
        transport = o.transport,
        max_body = o.max_body or 4 * 1024 * 1024,
    }, Api)
end

-- url joins an API path onto the base, keeping any path prefix. Path
-- segments that carry ids must already be escaped with Api.escape.
function Api:url(path, query)
    assert(path:sub(1, 1) == "/", "path must be absolute")
    return self.base.url .. path .. query_string(query)
end

-- sameOrigin reports whether an absolute URL belongs to the configured
-- server, so a credential is never sent anywhere else.
function Api:sameOrigin(url)
    local other = Api.parseBase((url:match("^([^?#]*)")))
    return other ~= nil and other.origin == self.base.origin
end

local function classify(status, data, headers)
    local err = { status = status }
    if type(data) == "table" then
        err.message = present(data.error)
        err.code = present(data.code)
        err.data = data
    end
    if status >= 300 and status < 400 then
        err.kind = "redirect"
        err.message = "the server answered with a redirect; refusing to follow it"
    elseif status == 401 then
        err.kind = "auth"
    elseif status == 403 then
        err.kind = "forbidden"
    elseif status == 404 then
        err.kind = "not_found"
    elseif status == 409 then
        err.kind = "conflict"
    elseif status == 410 then
        err.kind = "gone"
    elseif status == 413 then
        err.kind = "too_large"
    elseif status == 429 then
        err.kind = "rate_limited"
        local ra = headers and (headers["retry-after"] or headers["Retry-After"])
        err.retry_after = tonumber(ra)
    elseif status >= 500 then
        err.kind = "server"
    else
        err.kind = "refused"
    end
    err.message = err.message or ("HTTP " .. status)
    return err
end
Api.classify = classify

-- retryable reports whether the same request may be sent again later.
-- A transport failure leaves the server outcome unknown: the caller must
-- resend the same bytes rather than assume a rejection.
function Api.retryable(err)
    return err.kind == "transport" or err.kind == "server" or err.kind == "rate_limited"
end

-- backoff returns seconds to wait before attempt n (1-based), with jitter.
function Api.backoff(n, random)
    random = random or math.random
    local cap = math.min(15 * 2 ^ math.max(0, n - 1), 30 * 60)
    return cap / 2 + random() * cap / 2
end

-- request sends one call. opts: query, body (table, encoded here) or raw
-- (pre-encoded string, sent verbatim), sink (streams the body instead of
-- buffering it), headers. Returns data, response on success; nil, err.
function Api:request(method, path, opts)
    opts = opts or {}
    local headers = { ["Accept"] = "application/json" }
    if self.token then headers["Authorization"] = "Bearer " .. self.token end
    local body = opts.raw
    if body == nil and opts.body ~= nil then body = json.encode(opts.body) end
    if body ~= nil then headers["Content-Type"] = "application/json" end
    for k, v in pairs(opts.headers or {}) do headers[k] = v end
    local url = self:url(path, opts.query)
    local resp, terr = self.transport({
        method = method,
        url = url,
        headers = headers,
        body = body,
        sink = opts.sink,
        max_body = opts.max_body or self.max_body,
    })
    if not resp then
        return nil, { kind = "transport", message = terr or "network failure", unknown_outcome = true }
    end
    local data
    if not opts.sink and resp.body and resp.body ~= "" then
        local ok, decoded = pcall(json.decode, resp.body)
        if ok then data = decoded end
    end
    if resp.status >= 200 and resp.status < 300 then
        if not opts.sink and opts.expect_json ~= false and type(data) ~= "table" and resp.status ~= 204 then
            return nil, { kind = "malformed", status = resp.status, message = "the server answered with something that is not JSON" }
        end
        return data or {}, resp
    end
    return nil, classify(resp.status, data, resp.headers)
end

function Api:get(path, query, opts)
    opts = opts or {}
    opts.query = query
    return self:request("GET", path, opts)
end

function Api:post(path, body, opts)
    opts = opts or {}
    opts.body = body
    return self:request("POST", path, opts)
end

function Api:postRaw(path, raw)
    return self:request("POST", path, { raw = raw })
end

return Api
