export const defaults = Object.freeze({mode: 'bilingual', fontSize: 32, opacity: 0.82});

export function normalizeSettings(value = {}) {
  return {
    mode: value.mode === 'chinese' ? 'chinese' : 'bilingual',
    fontSize: Number.isFinite(value.fontSize) ? Math.min(60, Math.max(18, Math.round(value.fontSize))) : defaults.fontSize,
    opacity: Number.isFinite(value.opacity) ? Math.min(0.95, Math.max(0.15, value.opacity)) : defaults.opacity,
  };
}

// Keep a saved window reachable even after a monitor has been disconnected.
export function fitBounds(saved, area) {
  const width = Math.min(area.width, Math.max(480, Number.isFinite(saved?.width) ? Math.round(saved.width) : 900));
  const height = Math.min(area.height, Math.max(180, Number.isFinite(saved?.height) ? Math.round(saved.height) : 260));
  const x = Number.isFinite(saved?.x) ? Math.round(saved.x) : area.x + Math.round((area.width - width) / 2);
  const y = Number.isFinite(saved?.y) ? Math.round(saved.y) : area.y + area.height - height - 70;
  return {width, height, x: Math.min(area.x + area.width - width, Math.max(area.x, x)), y: Math.min(area.y + area.height - height, Math.max(area.y, y))};
}
