-- Stand-in for LuaSocket's http module outside KOReader: it records the
-- requests the transport would have sent instead of sending them.
local http = { TIMEOUT = 60, requests = {} }

-- Mirrors LuaSocket's return shape: a status code with a sink, the body
-- on its own without one.
function http.request(reqt)
    http.requests[#http.requests + 1] = reqt
    local body = "{}"
    if reqt.sink then
        if not reqt.sink(body) then return nil, "sink refused the body" end
        reqt.sink(nil)
        return 1, 200, {}
    end
    return body, 200, {}
end

function http.reset()
    http.requests = {}
    http.PROXY = nil
end

return http
