-- KOReader byte transport for liseur_api: LuaSocket over LuaSec with
-- certificate and host-name verification, no redirects, bounded
-- response bodies and timeouts.
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

-- certificateMatches checks subjectAltName DNS names, and the common
-- name only when the certificate has no DNS names.
function Transport.certificateMatches(names, common_name, host)
    if names and #names > 0 then
        for _i, n in ipairs(names) do
            if Transport.nameMatches(n, host) then return true end
        end
        return false
    end
    return Transport.nameMatches(common_name, host)
end

local function cert_names(cert)
    local names, cn = {}, nil
    local ok, ext = pcall(function() return cert:extensions() end)
    local san = ok and ext and ext["2.5.29.17"]
    if san and san.dNSName then
        for _i, n in ipairs(san.dNSName) do names[#names + 1] = n end
    end
    local sok, subject = pcall(function() return cert:subject() end)
    if sok and subject then
        for _i, entry in ipairs(subject) do
            if entry.name == "commonName" or entry.oid == "2.5.4.3" then cn = entry.value end
        end
    end
    return names, cn
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
            local names, cn
            if cert then names, cn = cert_names(cert) end
            if not cert or not Transport.certificateMatches(names, cn, host) then
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
