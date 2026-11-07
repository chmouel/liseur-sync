-- Cases ported from Liseur Android ReadingStateMergeTest.kt (8d78094c).
local Merge = require("liseur_merge")

local S = Merge.state
local function base(p, status) return { progression = p, status = status or Merge.statusFor(p) } end

describe("liseur_merge", function()
    it("nothing anywhere is nothing to do", function()
        assert.are.equal("in_sync", Merge.reconcile(nil, nil, nil, false).kind)
    end)

    it("a book only the server knows is taken", function()
        local d = Merge.reconcile(nil, S(0.4), nil, false)
        assert.are.equal("pull", d.kind)
        assert.are.equal(0.4, d.state.progression)
    end)

    it("a book only this device knows is sent only if it owes it", function()
        assert.are.equal("push", Merge.reconcile(S(0.4), nil, nil, true).kind)
        assert.are.equal("in_sync", Merge.reconcile(S(0.4), nil, nil, false).kind)
    end)

    it("positions within a page of each other are the same place", function()
        assert.are.equal("in_sync", Merge.reconcile(S(0.5), S(0.502), base(0.5), true).kind)
    end)

    it("different exact anchors inside the tolerance are pulled", function()
        assert.are.equal("pull", Merge.reconcile(S(0.071), S(0.074), nil, false, false).kind)
    end)

    it("different exact anchors keep two local changes in conflict", function()
        assert.are.equal("conflict", Merge.reconcile(S(0.072), S(0.074), base(0.071), true, false).kind)
    end)

    it("the same exact place agrees despite layout differences", function()
        assert.are.equal("in_sync", Merge.reconcile(S(0.071), S(0.078), nil, true, true).kind)
    end)

    it("only the server moved, so its position is taken", function()
        local d = Merge.reconcile(S(0.3), S(0.7), base(0.3), false)
        assert.are.equal("pull", d.kind)
        assert.are.equal(0.7, d.state.progression)
    end)

    it("only this device moved, so its position is sent", function()
        assert.are.equal("push", Merge.reconcile(S(0.7), S(0.3), base(0.3), true).kind)
    end)

    it("going back to reread is sent, not overwritten by the further server", function()
        local d = Merge.reconcile(S(0.02), S(0.9), base(0.9), true)
        assert.are.equal("push", d.kind)
        assert.are.equal(0.02, d.state.progression)
    end)

    it("both sides moved, so neither is chosen", function()
        assert.are.equal("conflict", Merge.reconcile(S(0.6), S(0.8), base(0.3), true).kind)
    end)

    it("one local page in a long book is not erased by a remote move", function()
        assert.are.equal("conflict", Merge.reconcile(S(0.58635), S(0.57646), base(0.58434), true).kind)
    end)

    it("the further position does not win a conflict on its own", function()
        assert.are.equal("conflict", Merge.reconcile(S(0.1), S(0.95), base(0.05), true).kind)
    end)

    it("no baseline and a divergent server is a conflict, not a guess", function()
        assert.are.equal("conflict", Merge.reconcile(S(0.6), S(0.8), nil, true).kind)
    end)

    it("no baseline and nothing owed takes the server's position", function()
        assert.are.equal("pull", Merge.reconcile(S(0.6), S(0.8), nil, false).kind)
    end)

    it("a status with no position is adopted without moving", function()
        local d = Merge.reconcile(S(0.42), { status = Merge.FINISHED }, base(0.42), false)
        assert.are.equal("adopt_status", d.kind)
        assert.are.equal(Merge.FINISHED, d.status)
        assert.are.equal("in_sync", Merge.reconcile(S(0.42), { status = Merge.READING }, base(0.42), false).kind)
    end)

    it("a status change on its own is a move", function()
        local d = Merge.reconcile(S(0.99, Merge.READING), S(0.99, Merge.FINISHED), base(0.99, Merge.READING), false)
        assert.are.equal("pull", d.kind)
    end)

    it("derives the status from the position", function()
        assert.are.equal(Merge.FINISHED, Merge.statusFor(0.995))
        assert.are.equal(Merge.READING, Merge.statusFor(0.5))
        assert.are.equal(Merge.READY, Merge.statusFor(0))
        assert.are.equal(Merge.READY, Merge.statusFor(nil))
    end)
end)
