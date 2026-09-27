// Excel-compatible CSV export (owner decision: CSV with a UTF-8 BOM; button «خروجی CSV (سازگار با اکسل)»).
// Numbers are ASCII (Excel parses them as numbers); money in rial; missing values are empty cells
// with the status column saying why (never 0).
import { CLASS_LABEL } from "./format";
import { STATUS_LABEL, type RowStatus } from "./status";
import type { Row } from "./types";

export const BOM = "﻿";

function cell(v: string | number | null): string {
  if (v === null) return "";
  const s = String(v);
  return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

export function toCSV(rows: Row[], status: (r: Row) => RowStatus): string {
  const header = ["نماد", "کد", "کلاس", "آخرین (ریال)", "تغییر (٪)", "ارزش (ریال)", "پول درشت (ریال)", "وضعیت داده", "زمان منبع"];
  const lines = [header.map(cell).join(",")];
  for (const r of rows) {
    lines.push([
      r.sym, r.ins, CLASS_LABEL[r.class], r.last, r.chg === null ? null : r.chg.toFixed(2), r.value, r.net_hot,
      STATUS_LABEL[status(r)], r.src,
    ].map(cell).join(","));
  }
  return BOM + lines.join("\r\n") + "\r\n";
}
