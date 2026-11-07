-- Test stand-in for KOReader's `json` module (luajson). Like it, a JSON
-- null decodes to a function, so code under test must cope with that.

local json = {}

json.null = function() end

local function encode_string(s)
    return '"' .. s:gsub('[%c"\\]', function(c)
        local map = { ['"'] = '\\"', ['\\'] = '\\\\', ['\n'] = '\\n', ['\r'] = '\\r', ['\t'] = '\\t' }
        return map[c] or string.format("\\u%04x", c:byte())
    end) .. '"'
end

local function is_array(t)
    local n = 0
    for _ in pairs(t) do n = n + 1 end
    for i = 1, n do
        if t[i] == nil then return false end
    end
    return n > 0
end

local function encode(v)
    local tv = type(v)
    if v == nil or v == json.null then return "null" end
    if tv == "boolean" then return tostring(v) end
    if tv == "number" then
        if v ~= v or v == math.huge or v == -math.huge then error("cannot encode " .. tostring(v)) end
        if v == math.floor(v) and math.abs(v) < 2^53 then return string.format("%d", v) end
        return string.format("%.17g", v)
    end
    if tv == "string" then return encode_string(v) end
    if tv == "table" then
        if is_array(v) then
            local out = {}
            for i = 1, #v do out[i] = encode(v[i]) end
            return "[" .. table.concat(out, ",") .. "]"
        end
        local keys = {}
        for k in pairs(v) do keys[#keys + 1] = tostring(k) end
        table.sort(keys)
        if #keys == 0 then return "[]" end
        local out = {}
        for _, k in ipairs(keys) do out[#out + 1] = encode_string(k) .. ":" .. encode(v[k]) end
        return "{" .. table.concat(out, ",") .. "}"
    end
    error("cannot encode " .. tv)
end
json.encode = encode

function json.decode(s)
    local pos = 1
    local function ws() pos = s:find("[^ \t\r\n]", pos) or #s + 1 end
    local value
    local function str()
        local out = {}
        pos = pos + 1
        while true do
            local c = s:sub(pos, pos)
            if c == "" then error("unterminated string") end
            if c == '"' then pos = pos + 1; break end
            if c == "\\" then
                local e = s:sub(pos + 1, pos + 1)
                local map = { b = "\b", f = "\f", n = "\n", r = "\r", t = "\t", ['"'] = '"', ["\\"] = "\\", ["/"] = "/" }
                if e == "u" then
                    local cp = tonumber(s:sub(pos + 2, pos + 5), 16)
                    if cp < 0x80 then out[#out + 1] = string.char(cp)
                    elseif cp < 0x800 then out[#out + 1] = string.char(0xC0 + math.floor(cp / 64), 0x80 + cp % 64)
                    else out[#out + 1] = string.char(0xE0 + math.floor(cp / 4096), 0x80 + math.floor(cp / 64) % 64, 0x80 + cp % 64) end
                    pos = pos + 6
                else
                    out[#out + 1] = map[e] or error("bad escape")
                    pos = pos + 2
                end
            else
                out[#out + 1] = c
                pos = pos + 1
            end
        end
        return table.concat(out)
    end
    function value()
        ws()
        local c = s:sub(pos, pos)
        if c == "{" then
            local t = {}
            pos = pos + 1; ws()
            if s:sub(pos, pos) == "}" then pos = pos + 1; return t end
            while true do
                ws()
                local k = str()
                ws()
                if s:sub(pos, pos) ~= ":" then error("expected : at " .. pos) end
                pos = pos + 1
                t[k] = value()
                ws()
                local d = s:sub(pos, pos); pos = pos + 1
                if d == "}" then return t end
                if d ~= "," then error("expected , or } at " .. pos) end
            end
        elseif c == "[" then
            local t = {}
            pos = pos + 1; ws()
            if s:sub(pos, pos) == "]" then pos = pos + 1; return t end
            while true do
                t[#t + 1] = value()
                ws()
                local d = s:sub(pos, pos); pos = pos + 1
                if d == "]" then return t end
                if d ~= "," then error("expected , or ] at " .. pos) end
            end
        elseif c == '"' then
            return str()
        elseif s:sub(pos, pos + 3) == "true" then pos = pos + 4; return true
        elseif s:sub(pos, pos + 4) == "false" then pos = pos + 5; return false
        elseif s:sub(pos, pos + 3) == "null" then pos = pos + 4; return json.null
        else
            local num = s:match("^-?%d+%.?%d*[eE]?[-+]?%d*", pos)
            if not num or num == "" then error("unexpected " .. c .. " at " .. pos) end
            pos = pos + #num
            return tonumber(num)
        end
    end
    local v = value()
    ws()
    if pos <= #s then error("trailing data at " .. pos) end
    return v
end

return json
