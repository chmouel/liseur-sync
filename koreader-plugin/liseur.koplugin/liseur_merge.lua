-- Three-way reading-position reconciliation, ported from Liseur
-- Android's domain/ReadingStateMerge.kt (liseur 8d78094c). The further
-- position never wins on its own and clocks are never compared: only
-- which side moved away from the last agreed baseline decides.

local Merge = {}

Merge.EPSILON = 0.005
Merge.LOCAL_MOVE_EPSILON = 0.000000001
Merge.FINISHED_PROGRESSION = 0.97

Merge.READY = "ready_to_read"
Merge.READING = "reading"
Merge.FINISHED = "finished"

function Merge.statusFor(progression)
    if progression == nil or progression <= 0 then return Merge.READY end
    if progression >= Merge.FINISHED_PROGRESSION then return Merge.FINISHED end
    return Merge.READING
end

-- state builds a ReadingState from a progression; status is derived,
-- because KOReader carries no separate reading status.
function Merge.state(progression, status)
    return { progression = progression, status = status or Merge.statusFor(progression) }
end

local function moved_from(baseline, state, tolerance)
    if baseline == nil then return true end
    if baseline.status ~= state.status then return true end
    local agreed = baseline.progression
    if agreed == nil then return state.progression ~= nil end
    local now = state.progression
    if now == nil then return true end
    return math.abs(agreed - now) >= tolerance
end

local function same_spot(l, r, exact)
    if l.status ~= r.status then return false end
    if exact ~= nil then return exact end
    if l.progression == nil then return r.progression == nil end
    if r.progression == nil then return false end
    return math.abs(l.progression - r.progression) < Merge.EPSILON
end

-- reconcile returns one of:
--   {kind="in_sync"}, {kind="pull", state=remote}, {kind="push", state=local},
--   {kind="adopt_status", status=...}, {kind="conflict", ["local"]=l, remote=r}
-- exact: true/false when both positions resolve exactly in this
-- document (same rendered page or not), nil when only percentages exist.
function Merge.reconcile(l, r, baseline, dirty, exact)
    if l == nil and r == nil then return { kind = "in_sync" } end
    if l == nil then return { kind = "pull", state = r } end
    if r == nil then
        if dirty then return { kind = "push", state = l } end
        return { kind = "in_sync" }
    end
    if same_spot(l, r, exact) then return { kind = "in_sync" } end
    if r.progression == nil then
        if r.status ~= l.status then return { kind = "adopt_status", status = r.status } end
        if dirty and l.progression ~= nil then return { kind = "push", state = l } end
        return { kind = "in_sync" }
    end
    if exact == false then
        if dirty then return { kind = "conflict", ["local"] = l, remote = r } end
        return { kind = "pull", state = r }
    end
    local local_moved = dirty and moved_from(baseline, l, Merge.LOCAL_MOVE_EPSILON)
    local remote_moved = moved_from(baseline, r, Merge.EPSILON)
    if not local_moved and remote_moved then return { kind = "pull", state = r } end
    if local_moved and not remote_moved then return { kind = "push", state = l } end
    if local_moved and remote_moved then return { kind = "conflict", ["local"] = l, remote = r } end
    return { kind = "in_sync" }
end

return Merge
