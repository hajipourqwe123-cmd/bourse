"use client";
import { useId, useState } from "react";
import { CLASS_LABEL, clock, num, signedNum, UNAVAILABLE } from "../lib/format";
import { axisPos, phase, rowSession, SESSION_ROWS, statusText } from "../lib/session";
import type { ClassFlow, ClassName, KPI } from "../lib/types";
import { useMarket, useNow } from "./hooks";
import { Age, ClassChip, MethodLink, Money, NA, Unavailable } from "./ui";

const AXIS_TICKS = ["۸:۰۰", "۱۰:۰۰", "۱۲:۰۰", "۱۴:۰۰", "۱۶:۰۰", "۱۸:۰۰"];
const FAMILY: Record<string, string> = { fixed: "fam-fixed", stock: "fam-stock", metals: "fam-gold" };

/** «جلسه‌های امروز»: one row per session family, from the gateway's calendar sessions. */
export function SessionStrip({ compact = false }: { compact?: boolean }) {
  const s = useMarket();
  const now = useNow() + s.offsetMs;
  const sessions = s.state?.sessions ?? [];
  const id = useId();
  return (
    <section className="card" aria-labelledby={id}>
      <div className="card-head">
        <h2 className="t-card" id={id}>جلسه‌های امروز</h2>
        {s.state?.calendar_unverified && <span className="chip chip-warn">ساعت‌ها تأییدنشده</span>}
        {!compact && <span className="end t-label desktop-only">هر ابزار با ساعت جلسه خودش سنجیده می‌شود</span>}
      </div>
      <div className="sessions">
        {SESSION_ROWS.map((r) => {
          const rs = rowSession(sessions, r.classes);
          const st = statusText(rs, now);
          const p = phase(rs, now);
          const pre = [axisPos(rs.preOpen), axisPos(rs.start)];
          const main = [axisPos(rs.start), axisPos(rs.close)];
          return (
            <div key={r.id} style={{ display: "contents" }}>
              <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span className={`dot ${FAMILY[r.id]}`} aria-hidden="true" />
                {r.label}
              </span>
              <div className="track" role="img" aria-label={`${r.label}: ${st.text}`}>
                {rs.open && (
                  <>
                    <span className={`seg seg-pre ${FAMILY[r.id]}`} style={{ left: `${pre[0] * 100}%`, width: `${(pre[1] - pre[0]) * 100}%` }} />
                    <span className={`seg ${FAMILY[r.id]}`} style={{ left: `${main[0] * 100}%`, width: `${(main[1] - main[0]) * 100}%`, opacity: p === "ended" ? 0.45 : 1 }} />
                  </>
                )}
                <span className="now" style={{ left: `calc(${axisPos(now) * 100}% - 1px)` }} aria-hidden="true" />
              </div>
              <span className={st.warn ? "warn t-label" : "t-label"} style={st.warn ? { color: "var(--warn-text)", fontWeight: 700 } : undefined}>
                {st.text}
              </span>
            </div>
          );
        })}
        <div className="axis" aria-hidden="true">
          {AXIS_TICKS.map((t) => (
            <span key={t}>{t}</span>
          ))}
        </div>
      </div>
    </section>
  );
}

const KPI_TITLE: Record<KPI["id"], string> = {
  index_total: "شاخص کل",
  value_stock: "ارزش معاملات خرد سهام",
  value_fixed: "ارزش معاملات درآمد ثابت",
  value_metals: "ارزش معاملات طلا و نقره",
};

function KpiCard({ k }: { k: KPI }) {
  const s = useMarket();
  const now = useNow() + s.offsetMs;
  if (!k.available) {
    return (
      <section className="card" aria-label={KPI_TITLE[k.id]}>
        <h2 className="t-label" style={{ margin: 0 }}>{KPI_TITLE[k.id]}</h2>
        <Unavailable reason={k.reason ?? ""} />
        <MethodLink anchor="kpi" />
      </section>
    );
  }
  const rs = rowSession(s.state?.sessions ?? [], k.classes ?? []);
  const st = statusText(rs, now);
  const bars = k.series.slice(-8);
  const max = Math.max(1, ...bars.map((b) => b.value ?? 0));
  return (
    <section className="card" aria-label={KPI_TITLE[k.id]} data-testid={`kpi-${k.id}`}>
      <div className="card-head">
        <h2 className="t-label" style={{ margin: 0 }}>{KPI_TITLE[k.id]}</h2>
        <span className="end t-label">{st.text}</span>
      </div>
      <div className="kpi-value">
        {k.value === null ? <NA /> : <span className="t-kpi"><Money rial={k.value} /></span>}
      </div>
      {k.secondary && (
        <span className="t-label">
          {CLASS_LABEL[k.secondary.class]} (جدا): <Money rial={k.secondary.value} />
          {k.secondary.awaiting > 0 && <> · در انتظار بازنشانی منبع: <span className="num">{num(k.secondary.awaiting)}</span></>}
        </span>
      )}
      <div className="bars desktop-only" role="img" aria-label="ارزش معاملات هر ۱۰ دقیقه">
        {bars.map((b, i) =>
          b.value === null ? (
            <span key={b.at} className="na-bar" title={`${clock(new Date(b.at), false)} · ${UNAVAILABLE}`} />
          ) : (
            <span key={b.at} className={b.partial ? "partial" : i === bars.length - 1 ? "last" : undefined} style={{ height: `${Math.max(4, (b.value / max) * 100)}%` }} title={`${clock(new Date(b.at), false)}${b.partial ? " · ناقص" : ""}`} />
          ),
        )}
      </div>
      <div className="card-foot">
        <Age at={k.as_of} />
        {k.missing > 0 && <span className="chip chip-muted">داده ناقص: {num(k.missing)} نماد</span>}
        {k.awaiting > 0 && <span className="chip chip-warn">در انتظار بازنشانی منبع: {num(k.awaiting)} نماد</span>}
        {k.note && <span className="chip chip-warn">{k.note}</span>}
      </div>
    </section>
  );
}

export function KpiRow() {
  const s = useMarket();
  const kpis = s.summary?.kpis ?? [];
  return (
    <div className="grid-kpi">
      {kpis.map((k) => (
        <KpiCard key={k.id} k={k} />
      ))}
    </div>
  );
}

const FLOW_TABS: ClassName[] = ["stock", "equity_etf", "gold", "silver", "fixed_income", "other_fund"];

/** «جریان پول حقیقی امروز»: bands per class, diverging bars, rule-based pattern note. */
export function FlowCard({ fixedClass }: { fixedClass?: ClassName }) {
  const s = useMarket();
  const flows = s.summary?.flows ?? [];
  const tabs = FLOW_TABS.filter((c) => c === "stock" || (flows.find((f) => f.class === c)?.instruments ?? 0) > 0);
  const [sel, setSel] = useState<ClassName>("stock");
  const cls = fixedClass ?? sel;
  const f = flows.find((x) => x.class === cls);
  const mt = (rial: number) => num(rial / 10 / 1e6);
  const hot = s.state?.hot_threshold ?? 0;
  const plus = s.state?.plus_threshold ?? 0;
  const id = useId();
  return (
    <section className="card" aria-labelledby={id}>
      <div className="card-head">
        <h2 className="t-card" id={id}>جریان پول حقیقی {fixedClass ? `· ${CLASS_LABEL[fixedClass]}` : "امروز"}</h2>
        <span className="end">
          <MethodLink anchor="flows" />
        </span>
      </div>
      {!fixedClass && (
        <div className="tabs" role="tablist" aria-label="گروه ابزار">
          {tabs.map((c) => (
            <button key={c} role="tab" type="button" className="tab" aria-selected={c === cls} onClick={() => setSel(c)}>
              {CLASS_LABEL[c]}
            </button>
          ))}
        </div>
      )}
      {!f || f.net_hot === null ? (
        <Unavailable reason={f && f.missing > 0 ? `جمع جریان ${num(f.missing)} نماد هنوز نرسیده است` : "هنوز نمادی از این گروه داده نداده است"} />
      ) : (
        <FlowBands f={f} labels={[`درشت ≥ ${mt(hot)} م.ت`, `متوسط ${mt(plus)}–${mt(hot)} م.ت`, `خرد < ${mt(plus)} م.ت`, "غیرقابل انتساب"]} />
      )}
      {f && f.net_hot !== null && (
        <div className="pattern">
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <strong className="warn">الگوی توزیع</strong>
            <span className="chip chip-muted">قاعده‌محور</span>
          </div>
          <span>{f.pattern ? f.pattern.text : "الگو به دلیل داده ناقص محاسبه نمی‌شود."}</span>
        </div>
      )}
      {f && (
        <div className="card-foot">
          <Age at={f.as_of} />
          {f.partial && <span className="chip chip-warn">روز ناقص</span>}
          {f.missing > 0 && <span className="chip chip-muted">داده ناقص: {num(f.missing)} نماد</span>}
          {f.lagging > 0 && <span className="chip chip-stale">بیات: {num(f.lagging)} نماد</span>}
          {f.awaiting > 0 && <span className="chip chip-warn">در انتظار بازنشانی منبع: {num(f.awaiting)} نماد</span>}
          {f.value_missing > 0 && <span className="chip chip-muted">بدون ارزش: {num(f.value_missing)} نماد</span>}
        </div>
      )}
    </section>
  );
}

function FlowBands({ f, labels }: { f: ClassFlow; labels: string[] }) {
  const values = [f.net_hot, f.net_hot_plus, f.net_retail, f.net_unattributed];
  const max = Math.max(1, ...values.map((v) => Math.abs(v ?? 0)));
  return (
    <div className="flow-rows">
      {values.map((v, i) => {
        const w = v === null ? 0 : (Math.abs(v) / max) * 50;
        return (
          <div key={labels[i]} style={{ display: "contents" }}>
            <span className="t-label" style={{ color: "var(--text-2)" }}>{labels[i]}</span>
            <div className="diverge" aria-hidden="true">
              {v !== null && v !== 0 && (
                <span className={v > 0 ? "bar-up" : "bar-down"} style={v > 0 ? { left: "50%", width: `${w}%` } : { right: "50%", width: `${w}%` }} />
              )}
            </div>
            <span style={{ fontWeight: 700, fontSize: 12 }}>
              <Money rial={v} signed unit="hemmat" />
            </span>
          </div>
        );
      })}
    </div>
  );
}

/** «ارزش صف‌ها» (unavailable by contract) + «پهنای بازار سهام». */
export function QueueBreadthCard() {
  const s = useMarket();
  const b = s.summary?.breadth;
  const q = s.summary?.queues;
  const covered = b ? b.instruments - b.missing - b.untraded - b.awaiting : 0;
  const segs = b
    ? [
        { n: b.floor, color: "var(--breadth-1)", label: "در کف دامنه (≤ −۳٪)" },
        { n: b.down, color: "var(--breadth-2)", label: "−۳٪ تا ۰" },
        { n: b.flat, color: "var(--map-3)", label: "بدون تغییر" },
        { n: b.up, color: "var(--breadth-3)", label: "۰ تا +۳٪" },
        { n: b.ceil, color: "var(--breadth-4)", label: "در سقف دامنه (≥ +۳٪)" },
      ]
    : [];
  const id = useId();
  return (
    <section className="card" aria-labelledby={id}>
      <div className="card-head">
        <h2 className="t-card" id={id}>ارزش صف‌ها</h2>
      </div>
      <Unavailable reason={q?.reason ?? "سطح یک دفتر سفارش در قرارداد داده فعلی نیست (D-03)"} />
      <div className="card-head" style={{ marginTop: 8 }}>
        <span className="t-label">
          پهنای بازار سهام · <span className="num">{num(covered)}</span> نماد معامله‌شده
        </span>
        <span className="end">
          <MethodLink anchor="breadth" />
        </span>
      </div>
      {!b || covered <= 0 ? (
        <NA />
      ) : (
        <>
          <div className="breadth-bar" role="img" aria-label={segs.map((x) => `${x.label}: ${num(x.n)}`).join("، ")}>
            {segs.map((x) => (x.n > 0 ? <span key={x.label} style={{ width: `${(x.n / covered) * 100}%`, background: x.color }} /> : null))}
          </div>
          <div className="breadth-legend">
            {segs.map((x) => (
              <span key={x.label}>
                {x.label}: <span className="num">{num(x.n)}</span>
              </span>
            ))}
          </div>
        </>
      )}
      {b && (
        <div className="card-foot">
          <Age at={b.as_of} />
          {b.missing > 0 && <span className="chip chip-muted">بدون قیمت: {num(b.missing)} نماد</span>}
          {b.untraded > 0 && <span className="chip chip-muted">بی‌معامله: {num(b.untraded)} نماد</span>}
          {b.awaiting > 0 && <span className="chip chip-warn">در انتظار بازنشانی منبع: {num(b.awaiting)} نماد</span>}
        </div>
      )}
    </section>
  );
}

/** «رادار سیگنال»: time, symbol, class, z or «واگرایی», Reason, footnote. */
export function RadarCard({ limit = 3 }: { limit?: number }) {
  const s = useMarket();
  const [all, setAll] = useState(false);
  const items = all ? s.radar : s.radar.slice(0, limit);
  const id = useId();
  return (
    <section className="card" aria-labelledby={id}>
      <div className="card-head">
        <h2 className="t-card" id={id}>رادار سیگنال</h2>
        {s.connection === "live" && <span className="chip chip-brand">زنده</span>}
        <span className="end">
          {s.radar.length > limit && (
            <button type="button" className="link-btn" onClick={() => setAll(!all)} aria-expanded={all}>
              {all ? "کمتر" : "همه سیگنال‌ها"}
            </button>
          )}
        </span>
      </div>
      {items.length === 0 ? (
        <span className="t-label">هنوز سیگنالی امروز ثبت نشده است.</span>
      ) : (
        <ul className="radar-list">
          {items.map((x) => (
            <li className="radar-item" key={`${x.ins}-${x.at}-${x.kind}`}>
              <div className="top">
                <span className="num t-label">{clock(new Date(x.at), false)}</span>
                <strong>{x.sym || x.ins}</strong>
                <ClassChip cls={x.class} />
                {x.syn && <span className="chip chip-demo">نمایشی</span>}
                {x.kind === "anomaly" ? (
                  <span className={`score num ${(x.z ?? 0) >= 0 ? "up" : "down"}`}>z = {signedNum(x.z ?? 0, 1, false)}</span>
                ) : (
                  <span className="score warn">واگرایی</span>
                )}
              </div>
              <span style={{ fontSize: 13, color: "var(--text-2)" }}>{x.reason}</span>
            </li>
          ))}
        </ul>
      )}
      <span className="t-label">سیگنال‌ها شاخص تحلیلی‌اند، نه توصیه خرید یا فروش.</span>
    </section>
  );
}
