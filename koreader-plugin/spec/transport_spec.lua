local Transport = require("liseur_transport")

describe("liseur_transport host names", function()
    it("matches exact names case-insensitively", function()
        assert.is_true(Transport.nameMatches("Books.Example.org", "books.example.org"))
        assert.is_false(Transport.nameMatches("books.example.org", "evil.example.org"))
    end)

    it("allows one left-most wildcard label only", function()
        assert.is_true(Transport.nameMatches("*.example.org", "books.example.org"))
        assert.is_false(Transport.nameMatches("*.example.org", "a.books.example.org"))
        assert.is_false(Transport.nameMatches("*.example.org", "example.org"))
        assert.is_false(Transport.nameMatches("*.org", "example.org"))
        assert.is_false(Transport.nameMatches("b*.example.org", "books.example.org"))
    end)

    it("prefers DNS names over the common name", function()
        assert.is_false(Transport.certificateMatches({ "other.example.org" }, "books.example.org", "books.example.org"))
        assert.is_true(Transport.certificateMatches({}, "books.example.org", "books.example.org"))
        assert.is_false(Transport.certificateMatches(nil, nil, "books.example.org"))
    end)

    it("matches an IP address host against iPAddress entries only", function()
        assert.is_true(Transport.certificateMatches({}, nil, "192.168.1.10", { "192.168.1.10" }))
        assert.is_true(Transport.certificateMatches({}, nil, "[::1]", { "0:0:0:0:0:0:0:1" }))
        assert.is_true(Transport.certificateMatches({}, nil, "[fd00::a]", { "FD00:0:0:0:0:0:0:A" }))
        assert.is_false(Transport.certificateMatches({ "192.168.1.10" }, "192.168.1.10", "192.168.1.10", {}))
        assert.is_false(Transport.certificateMatches({}, nil, "192.168.1.10", { "192.168.1.11" }))
        assert.is_nil(Transport.normalizeIP("books.example.org"))
        assert.is_nil(Transport.normalizeIP("1.2.3.256"))
    end)

    it("uses only a readable configured bundle", function()
        assert.is_nil(Transport.findCA("/nonexistent/ca.pem"))
    end)
end)

-- A configured HTTP proxy makes LuaSocket rewrite the host and port of
-- every request to the proxy while keeping our TLS `create`: the
-- certificate checked would be the proxy's and the bearer token would be
-- sent to it. The transport refuses before opening anything.
describe("liseur_transport proxy", function()
    local http = require("socket.http")

    before_each(function() http.reset() end)

    it("refuses an HTTPS request, before it reaches the socket", function()
        local transport = require("liseur_transport").new{ block = 1, total = 1 }
        http.PROXY = "http://127.0.0.1:3128"
        local resp, err = transport({ method = "GET", url = "https://books.example.org/v1/token",
            headers = { ["Authorization"] = "Bearer canary" } })
        assert.is_nil(resp)
        assert.matches("proxy", err)
        assert.are.equal(0, #http.requests, "a request was handed to LuaSocket")
    end)

    it("refuses the loopback http exception too", function()
        local transport = require("liseur_transport").new{ block = 1, total = 1 }
        http.PROXY = "http://127.0.0.1:3128"
        local resp, err = transport({ method = "GET", url = "http://localhost:8080/v1/token" })
        assert.is_nil(resp)
        assert.matches("proxy", err)
        assert.are.equal(0, #http.requests, "a request was handed to LuaSocket")
    end)

    it("sends normally once the proxy is off", function()
        local transport = require("liseur_transport").new{ block = 1, total = 1 }
        local resp = transport({ method = "GET", url = "http://localhost:8080/healthz" })
        assert.is_not_nil(resp)
        assert.are.equal(1, #http.requests)
    end)
end)
