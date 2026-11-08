-- Stand-in for LuaSocket outside KOReader: no socket can be opened in the
-- specs, so anything that tries fails loudly.
local socket = { _VERSION = "LuaSocket spec stub" }

function socket.try(v, message)
    if not v then error(message or "socket error", 2) end
    return v
end

function socket.tcp()
    error("a spec opened a TCP socket", 2)
end

return socket
