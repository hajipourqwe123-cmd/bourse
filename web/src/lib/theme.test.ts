import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { nextMode } from "./theme";

const css = readFileSync(new URL("../styles/tokens.css", import.meta.url), "utf8");

function block(re: RegExp): Map<string, string> {
  const m = css.match(re);
  expect(m, String(re)).not.toBeNull();
  return new Map([...m![1].matchAll(/(--[a-z0-9-]+):\s*([^;]+);/g)].map((x) => [x[1], x[2].trim()]));
}

describe("theme tokens", () => {
  const dark = block(/^:root \{([\s\S]*?)\n\}/m);
  const lightMedia = block(/:root:not\(\[data-theme="dark"\]\) \{([\s\S]*?)\n {2}\}/);
  const lightAttr = block(/:root\[data-theme="light"\] \{([\s\S]*?)\n\}/);

  it("the two light blocks are identical", () => {
    expect([...lightMedia]).toEqual([...lightAttr]);
  });
  it("light redefines every colour role except map/breadth (which keep the dark values)", () => {
    const colourRoles = [...dark.keys()].filter((k) => dark.get(k)!.startsWith("#") && k !== "--map-text");
    const expected = colourRoles.filter((k) => !k.startsWith("--map-") && !k.startsWith("--breadth-"));
    expect([...lightAttr.keys()].sort()).toEqual(expected.sort());
  });
  it("dark values are the owner's (spot check)", () => {
    expect(dark.get("--bg")).toBe("#0B1016");
    expect(dark.get("--map-1")).toBe("#1F7A4D");
    expect(lightAttr.get("--bg")).toBe("#F6F7F9");
    expect(lightAttr.get("--down-chip")).toBe("#A61F26");
  });
  it("toggle cycles auto → light → dark → auto", () => {
    expect(nextMode("auto")).toBe("light");
    expect(nextMode("light")).toBe("dark");
    expect(nextMode("dark")).toBe("auto");
  });
});
