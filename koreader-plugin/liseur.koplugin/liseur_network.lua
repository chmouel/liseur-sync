local Trapper = require("ui/trapper")

local Network = {}
local in_child = false

function Network.run(fn)
    if coroutine.running() then return fn() end
    return Trapper:wrap(fn)
end

function Network.worker(fn)
    -- Downloads already run their stream and hash together in one child
    if in_child or not coroutine.running() then return fn() end
    local completed, result, err = Trapper:dismissableRunInSubprocess(function()
        in_child = true
        return fn()
    end)
    if not completed then return nil, "network operation cancelled" end
    return result, err
end

return Network
