// Committing an audiobook seek without depending on a `change` event
// (#109). WebKit fires `input` and `pointerup` for a tap on a range
// input but often no `change`, so a player that waits for `change`
// keeps its "scrubbing" latch on forever and the clock stops. Every
// gesture end commits or cancels here, so the latch is always cleared.

export function seekScrubber(seek, { onScrub, onCommit, onCancel } = {}) {
  if (!seek) return { isScrubbing: () => false, commit() {}, cancel() {}, detach() {} };
  let scrubbing = false;
  const value = () => Number(seek.value);
  const start = () => {
    scrubbing = true;
    onScrub?.(value());
  };
  const commit = () => {
    if (!scrubbing) return false;
    scrubbing = false;
    onCommit?.(value());
    return true;
  };
  const cancel = () => {
    if (!scrubbing) return false;
    scrubbing = false;
    onCancel?.();
    return true;
  };
  const listeners = [
    ["input", start],
    ["change", commit],
    ["pointerup", commit],
    ["touchend", commit],
    ["keyup", commit],
    ["pointercancel", cancel],
    ["touchcancel", cancel],
    ["blur", cancel],
  ];
  for (const [event, listener] of listeners) seek.addEventListener(event, listener);
  return {
    isScrubbing: () => scrubbing,
    commit,
    cancel,
    detach() {
      for (const [event, listener] of listeners) seek.removeEventListener(event, listener);
    },
  };
}
