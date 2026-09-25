// Inline stroke icons (no icon font or CDN; rule 7). Decorative: aria-hidden.
type P = { size?: number };
const base = (size: number) => ({
  width: size, height: size, viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.8,
  strokeLinecap: "round" as const, strokeLinejoin: "round" as const, "aria-hidden": true,
});

export const Logo = ({ size = 32 }: P) => (
  <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
    <circle cx="16" cy="16" r="14" fill="none" stroke="var(--brand)" strokeWidth="2.2" />
    <circle cx="16" cy="16" r="8" fill="none" stroke="var(--brand)" strokeWidth="1.6" opacity="0.6" />
    <path d="M16 16 L25 8" stroke="var(--cls-gold)" strokeWidth="2.4" strokeLinecap="round" />
    <circle cx="16" cy="16" r="2.2" fill="var(--brand)" />
  </svg>
);
export const IconGrid = ({ size = 20 }: P) => (
  <svg {...base(size)}><rect x="4" y="4" width="6" height="6" rx="1.5" /><rect x="14" y="4" width="6" height="6" rx="1.5" /><rect x="4" y="14" width="6" height="6" rx="1.5" /><rect x="14" y="14" width="6" height="6" rx="1.5" /></svg>
);
export const IconTrend = ({ size = 20 }: P) => (
  <svg {...base(size)}><path d="M4 17l6-6 4 4 6-7" /><path d="M15 8h5v5" /></svg>
);
export const IconBriefcase = ({ size = 20 }: P) => (
  <svg {...base(size)}><rect x="4" y="7" width="16" height="12" rx="2" /><path d="M9 7V5h6v2M4 12h16" /></svg>
);
export const IconBuilding = ({ size = 20 }: P) => (
  <svg {...base(size)}><path d="M4 20h16M6 20V9l6-4 6 4v11M10 20v-6h4v6" /></svg>
);
export const IconTarget = ({ size = 20 }: P) => (
  <svg {...base(size)}><circle cx="12" cy="12" r="8" /><circle cx="12" cy="12" r="4" /><path d="M12 12l6-6" /></svg>
);
export const IconFilter = ({ size = 20 }: P) => (
  <svg {...base(size)}><path d="M4 5h16l-6 8v6l-4-2v-4z" /></svg>
);
export const IconBell = ({ size = 20 }: P) => (
  <svg {...base(size)}><path d="M6 16V11a6 6 0 1 1 12 0v5l1.5 2h-15z" /><path d="M10 20a2 2 0 0 0 4 0" /></svg>
);
export const IconPie = ({ size = 20 }: P) => (
  <svg {...base(size)}><circle cx="12" cy="12" r="8" /><path d="M12 4v8l6 5" /></svg>
);
export const IconUser = ({ size = 20 }: P) => (
  <svg {...base(size)}><circle cx="12" cy="8" r="4" /><path d="M4 20c1.5-4 5-5 8-5s6.5 1 8 5" /></svg>
);
