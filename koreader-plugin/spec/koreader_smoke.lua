-- Loads the plugin inside a real KOReader tree, without a screen, and
-- checks that every KOReader module and API it relies on exists there.
-- Given an HTTPS URL it also drives the real TLS transport, against
-- that server and against badssl.com's broken certificates.
--
-- Usage, from an unpacked KOReader (for example the linux-x86_64 build):
--   cd koreader && KO_HOME=$(mktemp -d) SDL_VIDEODRIVER=dummy \
--     ./luajit /path/to/spec/koreader_smoke.lua /path/to/liseur.koplugin [https://host]

local plugin_dir, tls_url = arg[1], arg[2]
assert(plugin_dir, "usage: koreader_smoke.lua PLUGIN_DIR [HTTPS_URL]")

require("setupkoenv")
G_defaults = require("luadefaults"):open()
local DataStorage = require("datastorage")
G_reader_settings = require("luasettings"):open(DataStorage:getDataDir() .. "/settings.reader.lua")
package.path = plugin_dir .. "/?.lua;" .. package.path

local failures = 0
local function check(name, fn)
    local ok, err = pcall(fn)
    print((ok and "ok   " or "FAIL ") .. name .. (ok and "" or (": " .. tostring(err))))
    if not ok then failures = failures + 1 end
end

einkfb = require("ffi/framebuffer") -- luacheck: ignore
einkfb.dummy = true -- luacheck: ignore

check("Device boots headless", function()
    local Device = require("device")
    Device.screen:init()
    require("document/canvascontext"):init(Device)
    Device.input.dummy = true
end)

local Liseur
check("main.lua loads", function()
    Liseur = dofile(plugin_dir .. "/main.lua")
    assert(type(Liseur) == "table" and Liseur.name == "liseur")
end)

check("_meta.lua loads", function()
    local meta = dofile(plugin_dir .. "/_meta.lua")
    assert(meta.fullname)
end)

check("ffi/sha2 streams sha256 and md5", function()
    local sha2 = require("ffi/sha2")
    local u = sha2.sha256()
    u("hello ")
    u("world")
    assert(u() == "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9")
    assert(sha2.md5("abc") == "900150983cd24fb0d6963f7d28e17f72")
end)

check("util.partialMD5 and ffi/util helpers exist", function()
    local util = require("util")
    local ffiUtil = require("ffi/util")
    assert(type(util.partialMD5) == "function")
    assert(type(util.makePath) == "function")
    assert(type(ffiUtil.fsyncOpenedFile) == "function")
    assert(type(ffiUtil.getTimestamp()) == "number")
    assert(ffiUtil.template("%1-%2", "a", "b") == "a-b")
end)

check("the luajson null decodes to something the plugin ignores", function()
    local json = require("json")
    local Api = require("liseur_api")
    local v = json.decode('{"a":null}')
    assert(Api.present(v.a) == nil)
    assert(json.decode(json.encode({ progression = 0.25 })).progression == 0.25)
end)

check("plugin instance builds its menus", function()
    local registered
    local ui = { menu = { registerToMainMenu = function(_, p) registered = p end } }
    local p = Liseur:new{ ui = ui }
    assert(registered == p)
    local items = {}
    p:addToMainMenu(items)
    local sub = items.liseur.sub_item_table_func()
    assert(#sub >= 5)
    local settings = sub[#sub].sub_item_table
    for _, item in ipairs(settings) do
        if item.text_func then assert(type(item.text_func()) == "string") end
    end
    assert(p:positionsState() == "off")
    assert(p:positionsActive() == false)
end)

check("UI widgets the plugin uses exist", function()
    for _, m in ipairs({ "ui/widget/menu", "ui/widget/buttondialog", "ui/widget/confirmbox", "ui/widget/inputdialog",
        "ui/widget/textviewer", "ui/widget/infomessage", "ui/network/manager", "apps/filemanager/filemanagerutil",
        "apps/filemanager/filemanager", "dispatcher", "socketutil", "ssl", "socket.http", "ltn12" }) do
        assert(require(m), m)
    end
    local NetworkMgr = require("ui/network/manager")
    assert(NetworkMgr.isConnected and NetworkMgr.runWhenConnected)
    assert(require("apps/filemanager/filemanagerutil").showChooseDialog)
end)

if tls_url then
    local Api = require("liseur_api")
    local Transport = require("liseur_transport")
    check("TLS to " .. tls_url .. " verifies the certificate", function()
        local api = Api.new{ base = assert(Api.parseBase(tls_url)), transport = Transport.new{ block = 10, total = 20 } }
        local _, err = api:get("/v1/token")
        assert(err == nil or err.kind ~= "transport", err and err.message)
    end)
    -- badssl.com serves deliberately broken certificates. Each must be
    -- refused for the right reason: a DNS failure or a missing CA bundle
    -- is also a transport error and would hide a certificate that was
    -- accepted.
    for _i, bad in ipairs({
        { url = "https://wrong.host.badssl.com", want = "certificate does not match" },
        { url = "https://self-signed.badssl.com", want = "certificate" },
        { url = "https://expired.badssl.com", want = "certificate" },
    }) do
        check("TLS refuses " .. bad.url .. " for a certificate reason", function()
            local api = Api.new{ base = assert(Api.parseBase(bad.url)), transport = Transport.new{ block = 10, total = 20 } }
            local _, err = api:get("/")
            assert(err and err.kind == "transport", "a broken certificate was accepted")
            print("     refused with: " .. tostring(err.message))
            local message = tostring(err.message)
            assert(not message:find("bundle"), "no CA bundle was found, so this proves nothing: " .. message)
            assert(not message:find("resolve"), "the host name did not resolve, so this proves nothing: " .. message)
            assert(message:find(bad.want, 1, true), "not refused for the expected reason (" .. bad.want .. "): " .. message)
        end)
    end
end

print(failures == 0 and "all checks passed" or (failures .. " check(s) failed"))
os.exit(failures == 0 and 0 or 1)
