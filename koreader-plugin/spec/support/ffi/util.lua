-- The parts of KOReader's ffi/util that the plugin uses outside a device.
local util = {}
function util.template(str, ...)
    local args = { ... }
    return (tostring(str):gsub("%%(%d)", function(k)
        local v = args[tonumber(k)]
        if v == nil then return "%" .. k end
        return tostring(v)
    end))
end
function util.getTimestamp() return os.time() end
return util
