import { describe, expect, it } from "vitest";
import { mapBucket, squarify } from "./treemap";

describe("squarify", () => {
  it("areas proportional to weight, inside the rect, no overlap", () => {
    const items = [6, 6, 4, 3, 2, 2, 1].map((w, i) => ({ weight: w, item: i }));
    const tiles = squarify(items, { x: 0, y: 0, w: 6, h: 4 });
    expect(tiles).toHaveLength(7);
    const total = tiles.reduce((a, t) => a + t.w * t.h, 0);
    expect(total).toBeCloseTo(24, 6);
    for (const t of tiles) {
      expect(t.w * t.h).toBeCloseTo((items[t.item].weight / 24) * 24, 6);
      expect(t.x).toBeGreaterThanOrEqual(-1e-9);
      expect(t.y).toBeGreaterThanOrEqual(-1e-9);
      expect(t.x + t.w).toBeLessThanOrEqual(6 + 1e-9);
      expect(t.y + t.h).toBeLessThanOrEqual(4 + 1e-9);
    }
    for (let i = 0; i < tiles.length; i++)
      for (let j = i + 1; j < tiles.length; j++) {
        const a = tiles[i], b = tiles[j];
        const ox = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x);
        const oy = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y);
        expect(ox > 1e-9 && oy > 1e-9).toBe(false);
      }
  });
  it("drops non-positive weights", () => {
    expect(squarify([{ weight: 0, item: 1 }, { weight: -1, item: 2 }], { x: 0, y: 0, w: 1, h: 1 })).toEqual([]);
  });
});

describe("map colour buckets", () => {
  it("edges", () => {
    expect(mapBucket(-3)).toBe(5);
    expect(mapBucket(-2.99)).toBe(4);
    expect(mapBucket(-1)).toBe(4);
    expect(mapBucket(-0.99)).toBe(3);
    expect(mapBucket(0)).toBe(3);
    expect(mapBucket(1)).toBe(2);
    expect(mapBucket(2.99)).toBe(2);
    expect(mapBucket(3)).toBe(1);
    expect(mapBucket(null)).toBeNull();
  });
});
