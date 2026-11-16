export function coalesceFrameHide(FrameManager) {
  const hide = FrameManager.prototype.hide;
  const setCSSProperties = FrameManager.prototype.setCSSProperties;
  const load = FrameManager.prototype.load;
  const show = FrameManager.prototype.show;
  FrameManager.prototype.hide = function () {
    if (this.pendingHide) return this.pendingHide;
    const pending = this.pendingShow ? this.pendingShow.then(() => hide.call(this)) : hide.call(this);
    this.pendingHide = pending;
    pending.finally(() => {
      if (this.pendingHide === pending) this.pendingHide = null;
    }).catch(() => {});
    return pending;
  };
  FrameManager.prototype.show = function (...args) {
    const pending = this.pendingHide ? this.pendingHide.then(() => show.apply(this, args)) : show.apply(this, args);
    this.pendingShow = pending;
    pending.finally(() => {
      if (this.pendingShow === pending) this.pendingShow = null;
    }).catch(() => {});
    return pending;
  };
  FrameManager.prototype.setCSSProperties = function (...args) {
    const pending = this.pendingShow || this.pendingHide;
    if (!pending) return setCSSProperties.apply(this, args);
    pending.then(() => setCSSProperties.apply(this, args)).catch(() => {});
  };
  FrameManager.prototype.load = async function (...args) {
    if (this.pendingHide) await this.pendingHide;
    if (this.pendingShow) await this.pendingShow;
    return load.apply(this, args);
  };
}
