// Media Session plumbing shared by the book page's player and the
// reader's (#114). Everything is feature-detected and best-effort: an
// engine without Media Session, or one that rejects a value, must not
// break playback. `session` is a MediaSession-like object so the caller
// can hand in the real one or a test stub.

export const MEDIA_ACTIONS = [
  "play", "pause", "seekbackward", "seekforward", "seekto", "previoustrack", "nexttrack",
];

export function mediaMetadata(Metadata, { title, artist, album, artwork } = {}) {
  if (typeof Metadata !== "function") return null;
  const data = {};
  if (title) data.title = String(title);
  if (artist) data.artist = String(artist);
  if (album) data.album = String(album);
  const art = (Array.isArray(artwork) ? artwork : [])
    .filter((entry) => entry && entry.src)
    .map(({ src, sizes, type }) => ({ src, ...(sizes ? { sizes } : {}), ...(type ? { type } : {}) }));
  if (art.length) data.artwork = art;
  return new Metadata(data);
}

// installMediaSession writes metadata when one is given and registers
// every handler in `handlers`; a handler that is not a function clears
// the action, which is how a player says it can no longer do it.
export function installMediaSession(session, { Metadata, metadata, handlers } = {}) {
  if (!session) return false;
  if (metadata) {
    try {
      const built = mediaMetadata(Metadata, metadata);
      if (built) session.metadata = built;
    } catch {}
  }
  for (const [action, handler] of Object.entries(handlers || {})) {
    try { session.setActionHandler(action, typeof handler === "function" ? handler : null); } catch {}
  }
  return true;
}

export function setMediaPlaybackState(session, state) {
  if (!session) return false;
  try {
    session.playbackState = state;
    return true;
  } catch {
    return false;
  }
}

// WebKit throws when position > duration or duration <= 0, so the value
// is clamped and a refusal is swallowed: the next timeupdate fixes it.
export function updateMediaPosition(session, { duration, position, playbackRate = 1 } = {}) {
  if (!session || typeof session.setPositionState !== "function") return false;
  if (!(duration > 0) || !Number.isFinite(position)) return false;
  try {
    session.setPositionState({
      duration,
      playbackRate: playbackRate > 0 ? playbackRate : 1,
      position: Math.max(0, Math.min(duration, position)),
    });
    return true;
  } catch {
    return false;
  }
}

export function clearMediaSession(session, actions = MEDIA_ACTIONS) {
  if (!session) return false;
  try { session.metadata = null; } catch {}
  try { session.playbackState = "none"; } catch {}
  for (const action of actions) {
    try { session.setActionHandler(action, null); } catch {}
  }
  return true;
}
