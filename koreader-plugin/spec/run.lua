-- Runs the plugin specs without KOReader: a busted-compatible subset of
-- describe/it/before_each/assert, enough for pure-Lua module tests.
-- Usage: luajit koreader-plugin/spec/run.lua [spec_file ...]

local here = debug.getinfo(1, "S").source:sub(2):match("(.*/)") or "./"
package.path = table.concat({
    here .. "support/?.lua",
    here .. "../liseur.koplugin/?.lua",
    package.path,
}, ";")

local function deep_equal(a, b)
    if a == b then return true end
    if type(a) ~= "table" or type(b) ~= "table" then return false end
    for k, v in pairs(a) do
        if not deep_equal(v, b[k]) then return false end
    end
    for k in pairs(b) do
        if a[k] == nil then return false end
    end
    return true
end

local function show(v, depth)
    depth = depth or 0
    if type(v) == "string" then return string.format("%q", v) end
    if type(v) ~= "table" or depth > 3 then return tostring(v) end
    local keys = {}
    for k in pairs(v) do keys[#keys + 1] = k end
    table.sort(keys, function(x, y) return tostring(x) < tostring(y) end)
    local parts = {}
    for _, k in ipairs(keys) do
        parts[#parts + 1] = tostring(k) .. "=" .. show(v[k], depth + 1)
    end
    return "{" .. table.concat(parts, ", ") .. "}"
end

local function fail(msg, level)
    error(msg, (level or 2) + 1)
end

local assert_ = {}
assert_.are = {
    same = function(expected, actual, msg)
        if not deep_equal(expected, actual) then
            fail((msg and msg .. ": " or "") .. "expected " .. show(expected) .. ", got " .. show(actual))
        end
    end,
    equal = function(expected, actual, msg)
        if expected ~= actual then
            fail((msg and msg .. ": " or "") .. "expected " .. show(expected) .. ", got " .. show(actual))
        end
    end,
}
assert_.are.equals = assert_.are.equal
assert_.same = assert_.are.same
assert_.equals = assert_.are.equal
assert_.is_true = function(v, msg) if v ~= true then fail((msg or "expected true") .. ", got " .. show(v)) end end
assert_.is_false = function(v, msg) if v ~= false then fail((msg or "expected false") .. ", got " .. show(v)) end end
assert_.is_nil = function(v, msg) if v ~= nil then fail((msg or "expected nil") .. ", got " .. show(v)) end end
assert_.is_not_nil = function(v, msg) if v == nil then fail(msg or "expected a value") end end
assert_.truthy = function(v, msg) if not v then fail((msg or "expected truthy") .. ", got " .. show(v)) end end
assert_.falsy = function(v, msg) if v then fail((msg or "expected falsy") .. ", got " .. show(v)) end end
assert_.matches = function(pattern, s, msg)
    if type(s) ~= "string" or not s:find(pattern) then
        fail((msg and msg .. ": " or "") .. show(s) .. " does not match " .. pattern)
    end
end
assert_.has_error = function(fn, msg)
    if pcall(fn) then fail(msg or "expected an error") end
end
setmetatable(assert_, { __call = function(_, v, msg, ...)
    if not v then fail(msg or "assertion failed") end
    return v, msg, ...
end })

local suites = {}
local stack = {}

function describe(name, body)
    local suite = { name = name, tests = {}, before = {}, parent = stack[#stack] }
    if suite.parent then
        table.insert(suite.parent.tests, suite)
    else
        table.insert(suites, suite)
    end
    table.insert(stack, suite)
    body()
    table.remove(stack)
end

function it(name, body)
    table.insert(stack[#stack].tests, { name = name, body = body })
end

function before_each(fn)
    table.insert(stack[#stack].before, fn)
end

_G.assert = assert_

local passed, failed = 0, 0
local failures = {}

local function befores(suite, out)
    if suite.parent then befores(suite.parent, out) end
    for _, fn in ipairs(suite.before) do out[#out + 1] = fn end
    return out
end

local function run_suite(suite, prefix)
    local label = prefix .. suite.name
    for _, t in ipairs(suite.tests) do
        if t.tests then
            run_suite(t, label .. " / ")
        else
            local ok, err = pcall(function()
                for _, fn in ipairs(befores(suite, {})) do fn() end
                t.body()
            end)
            if ok then
                passed = passed + 1
            else
                failed = failed + 1
                failures[#failures + 1] = label .. " / " .. t.name .. "\n    " .. tostring(err)
            end
        end
    end
end

local files = { ... }
if #files == 0 then
    local p = io.popen('ls "' .. here .. '"*_spec.lua')
    for line in p:lines() do files[#files + 1] = line end
    p:close()
end

for _, file in ipairs(files) do
    suites = {}
    local chunk, err = loadfile(file)
    if not chunk then
        failed = failed + 1
        failures[#failures + 1] = file .. "\n    " .. err
    else
        local ok, lerr = pcall(chunk)
        if not ok then
            failed = failed + 1
            failures[#failures + 1] = file .. "\n    " .. tostring(lerr)
        end
        for _, suite in ipairs(suites) do run_suite(suite, "") end
    end
end

for _, f in ipairs(failures) do io.stderr:write("FAIL ", f, "\n") end
print(string.format("%d passed, %d failed", passed, failed))
os.exit(failed == 0 and 0 or 1)
