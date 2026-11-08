-- KOReader byte transport for liseur_api: LuaSocket over LuaSec with
-- certificate and host-name verification, no redirects, bounded
-- response bodies and timeouts. It also refuses to run behind an HTTP
-- proxy: LuaSocket rewrites the host and port of every request to the
-- proxy but keeps our `create`, so the certificate checked would be the
-- proxy's while the bearer token travelled on to it.
--
-- KOReader 2026.07.2 ships stock LuaSec 1.3.2, whose https defaults to
-- verify = "none" and never checks the host name. This module therefore
-- builds its own TLS connection and refuses to talk HTTPS without a CA
-- bundle rather than falling back to an unverified one.

local Transport = {}

Transport.CA_PATHS = {
    "data/ca-bundle.crt",
    "/etc/ssl/certs/ca-certificates.crt",
    "/etc/pki/tls/certs/ca-bundle.crt",
    "/etc/ssl/ca-bundle.pem",
    "/etc/pki/tls/cacert.pem",
    "/etc/ssl/cert.pem",
}

-- proxyError returns the reason a configured proxy makes a request
-- unsafe, or nil. It is checked before any socket is opened.
Transport.PROXY_ERROR = "an HTTP proxy is switched on for KOReader, so this device's token "
    .. "would go to the proxy instead of to your server. Turn the proxy off in KOReader's network "
    .. "settings, then try again"

function Transport.proxyError(http)
    local proxy = http and http.PROXY
    if proxy == nil or proxy == false or proxy == "" then return nil end
    return Transport.PROXY_ERROR
end

local function readable(path)
    local f = io.open(path, "rb")
    if f then f:close() return true end
    return false
end

function Transport.findCA(custom)
    if custom and custom ~= "" then
        return readable(custom) and custom or nil
    end
    for _i, p in ipairs(Transport.CA_PATHS) do
        if readable(p) then return p end
    end
end

-- nameMatches compares one certificate name with a host, allowing a
-- single left-most wildcard label as RFC 6125 does.
function Transport.nameMatches(pattern, host)
    if type(pattern) ~= "string" then return false end
    pattern, host = pattern:lower(), host:lower()
    if pattern == host then return true end
    local rest = pattern:match("^%*%.(.+)$")
    if not rest or not rest:find("%.") then return false end
    local first, tail = host:match("^([^.]+)%.(.+)$")
    return first ~= nil and tail == rest
end

local function ipv4(s)
    local parts = { s:match("^(%d+)%.(%d+)%.(%d+)%.(%d+)$") }
    if #parts ~= 4 then return nil end
    for i, p in ipairs(parts) do
        parts[i] = tonumber(p)
        if parts[i] > 255 then return nil end
    end
    return parts
end

-- normalizeIP returns a canonical form of an IPv4 or IPv6 literal (with or
-- without brackets), or nil for anything else, so "::1" and
-- "0:0:0:0:0:0:0:1" compare equal.
function Transport.normalizeIP(s)
    if type(s) ~= "string" then return nil end
    local v4 = ipv4(s)
    if v4 then return table.concat(v4, ".") end
    s = (s:match("^%[(.*)%]$") or s):lower()
    if not s:find(":", 1, true) or not s:match("^[%x:%.]+$") then return nil end
    local head, tail = s:match("^(.*:)(%d+%.%d+%.%d+%.%d+)$")
    if head then
        local q = ipv4(tail)
        if not q then return nil end
        s = head .. string.format("%x:%x", q[1] * 256 + q[2], q[3] * 256 + q[4])
    end
    local function groups(part)
        local out = {}
        if part == "" then return out end
        for g in (part .. ":"):gmatch("([^:]*):") do
            if not g:match("^%x%x?%x?%x?$") then return nil end
            out[#out + 1] = tonumber(g, 16)
        end
        return out
    end
    local left, right = s:match("^(.-)::(.*)$")
    local g
    if left then
        local l, r = groups(left), groups(right)
        if not l or not r or #l + #r > 7 then return nil end
        g = l
        for _i = 1, 8 - #l - #r do g[#g + 1] = 0 end
        for _i, v in ipairs(r) do g[#g + 1] = v end
    else
        g = groups(s)
        if not g or #g ~= 8 then return nil end
    end
    for i, v in ipairs(g) do g[i] = string.format("%x", v) end
    return table.concat(g, ":")
end

-- certificateMatches checks subjectAltName DNS names, and the common
-- name only when the certificate has no DNS names. An IP address host
-- matches only an iPAddress entry, never a name.
function Transport.certificateMatches(names, common_name, host, ips)
    local ip = Transport.normalizeIP(host)
    if ip then
        for _i, a in ipairs(ips or {}) do
            if Transport.normalizeIP(a) == ip then return true end
        end
        return false
    end
    if names and #names > 0 then
        for _i, n in ipairs(names) do
            if Transport.nameMatches(n, host) then return true end
        end
        return false
    end
    return Transport.nameMatches(common_name, host)
end

local function cert_names(cert)
    local names, ips, cn = {}, {}, nil
    local ok, ext = pcall(function() return cert:extensions() end)
    local san = ok and ext and ext["2.5.29.17"]
    if san and san.dNSName then
        for _i, n in ipairs(san.dNSName) do names[#names + 1] = n end
    end
    if san and san.iPAddress then
        for _i, a in ipairs(san.iPAddress) do ips[#ips + 1] = a end
    end
    local sok, subject = pcall(function() return cert:subject() end)
    if sok and subject then
        for _i, entry in ipairs(subject) do
            if entry.name == "commonName" or entry.oid == "2.5.4.3" then cn = entry.value end
        end
    end
    return names, cn, ips
end

local function tls_create(socket, ssl, http, params, max_chunk)
    local function reg(conn)
        local mt = getmetatable(conn.sock).__index
        for name, method in pairs(mt) do
            if type(method) == "function" then
                conn[name] = function(self, ...) return method(self.sock, ...) end
            end
        end
    end
    return function()
        local conn = {}
        conn.sock = socket.try(socket.tcp())
        local st = getmetatable(conn.sock).__index.settimeout
        function conn:settimeout()
            return st(self.sock, http.TIMEOUT)
        end
        function conn:connect(host, port)
            socket.try(self.sock:connect(host, port))
            self.sock = socket.try(ssl.wrap(self.sock, params))
            self.sock:sni(host)
            self.sock:settimeout(http.TIMEOUT)
            socket.try(self.sock:dohandshake())
            local cert = self.sock:getpeercertificate()
            local names, cn, ips
            if cert then names, cn, ips = cert_names(cert) end
            if not cert or not Transport.certificateMatches(names, cn, host, ips) then
                self.sock:close()
                socket.try(nil, "the server certificate does not match " .. host)
            end
            reg(self)
            -- LuaSocket's chunked decoder asks for whatever size the peer
            -- announces; refuse absurd chunks before allocating them.
            local receive = self.receive
            function self:receive(pattern, ...)
                if type(pattern) == "number" and pattern > max_chunk then
                    return nil, "response chunk too large"
                end
                return receive(self, pattern, ...)
            end
            return 1
        end
        return conn
    end
end

-- new returns transport(req) for liseur_api. opts: ca_file, block,
-- total (seconds).
function Transport.new(opts)
    opts = opts or {}
    local http = require("socket.http")
    local ltn12 = require("ltn12")
    local socket = require("socket")
    local socketutil = require("socketutil")
    local function request(req)
        local perr = Transport.proxyError(http)
        if perr then return nil, perr end
        local headers = {}
        for k, v in pairs(req.headers or {}) do headers[k] = v end
        headers["User-Agent"] = socketutil.USER_AGENT
        local reqt = {
            url = req.url,
            method = req.method,
            headers = headers,
            redirect = false,
        }
        if req.body then
            reqt.source = ltn12.source.string(req.body)
            headers["Content-Length"] = tostring(#req.body)
        elseif req.method == "POST" or req.method == "PUT" then
            headers["Content-Length"] = "0"
        end
        local start = os.time()
        local total = opts.total or 30
        local chunks, size = {}, 0
        local failure
        local inner = req.sink
        reqt.sink = function(chunk, err)
            if chunk and os.time() - start > total then
                failure = "timed out"
                return nil, failure
            end
            if inner then return inner(chunk, err) end
            if chunk then
                size = size + #chunk
                if size > (req.max_body or 4 * 1024 * 1024) then
                    failure = "response too large"
                    return nil, failure
                end
                chunks[#chunks + 1] = chunk
            end
            return 1
        end
        if req.url:match("^https://") then
            local cafile = Transport.findCA(opts.ca_file)
            if not cafile then
                return nil, "no trusted certificate bundle was found on this device; set one in Liseur settings"
            end
            local ssl = require("ssl")
            reqt.create = tls_create(socket, ssl, http, {
                mode = "client",
                protocol = "any",
                options = { "all", "no_sslv2", "no_sslv3", "no_tlsv1" },
                verify = "peer",
                cafile = cafile,
            }, math.max(req.max_body or 0, 1024 * 1024))
        end
        socketutil:set_timeout(opts.block or 10, total)
        local ok, r1, r2, r3 = pcall(http.request, reqt)
        socketutil:reset_timeout()
        if not ok then return nil, tostring(r1) end
        if r1 == nil then return nil, failure or tostring(r2) end
        local code, rheaders = r2, r3
        if type(code) ~= "number" then return nil, failure or tostring(code) end
        if failure then return nil, failure end
        return { status = code, headers = rheaders or {}, body = table.concat(chunks) }
    end
    return function(req)
        if opts.background and not req.sink then
            return require("liseur_network").worker(function() return request(req) end)
        end
        return request(req)
    end
end

return Transport
