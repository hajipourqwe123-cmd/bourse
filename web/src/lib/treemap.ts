// Squarified treemap (Bruls, Huizing, van Wijk 2000) and the market-map colour buckets
// (docs/market-metrics.md «نقشه بازار»).

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Tile<T> extends Rect {
  item: T;
}

function worst(row: number[], side: number): number {
  const s = row.reduce((a, b) => a + b, 0);
  const max = Math.max(...row);
  const min = Math.min(...row);
  return Math.max((side * side * max) / (s * s), (s * s) / (side * side * min));
}

/** Lays out items (weight > 0, any order) in rect; tile areas are proportional to weight. */
export function squarify<T>(items: { weight: number; item: T }[], rect: Rect): Tile<T>[] {
  const list = items.filter((i) => i.weight > 0).sort((a, b) => b.weight - a.weight);
  const total = list.reduce((a, b) => a + b.weight, 0);
  if (total <= 0 || rect.w <= 0 || rect.h <= 0) return [];
  const scale = (rect.w * rect.h) / total;
  const areas = list.map((i) => i.weight * scale);
  const out: Tile<T>[] = [];
  let { x, y, w, h } = rect;
  let i = 0;
  while (i < areas.length) {
    const side = Math.min(w, h);
    const row = [areas[i]];
    let j = i + 1;
    while (j < areas.length && worst([...row, areas[j]], side) <= worst(row, side)) {
      row.push(areas[j]);
      j++;
    }
    const sum = row.reduce((a, b) => a + b, 0);
    if (w >= h) {
      // column on the left edge of the remaining rect
      const cw = sum / h;
      let cy = y;
      row.forEach((a, k) => {
        const th = a / cw;
        out.push({ x, y: cy, w: cw, h: th, item: list[i + k].item });
        cy += th;
      });
      x += cw;
      w -= cw;
    } else {
      const rh = sum / w;
      let cx = x;
      row.forEach((a, k) => {
        const tw = a / rh;
        out.push({ x: cx, y, w: tw, h: rh, item: list[i + k].item });
        cx += tw;
      });
      y += rh;
      h -= rh;
    }
    i = j;
  }
  return out;
}

/**
 * Colour bucket of a price change (percent): 5 = ≤ −3, 4 = (−3, −1], 3 = (−1, +1), 2 = [+1, +3),
 * 1 = ≥ +3 (tokens --map-5 … --map-1); null = change missing (grey tile).
 */
export function mapBucket(chg: number | null): 1 | 2 | 3 | 4 | 5 | null {
  if (chg === null || !Number.isFinite(chg)) return null;
  if (chg <= -3) return 5;
  if (chg <= -1) return 4;
  if (chg < 1) return 3;
  if (chg < 3) return 2;
  return 1;
}
