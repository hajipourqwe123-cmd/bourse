# قرارداد نام‌گذاری پیام‌ها

| موضوع (NATS subject) | محتوا (نوع Go) | تولیدکننده | مصرف‌کننده |
| --- | --- | --- | --- |
| `md.snap.<ins_code>` | `model.Snapshot` | collector | engine، writer ClickHouse |
| `flow.event.<ins_code>` | `model.FlowEvent` | engine | gateway، alerts، writer |
| `flow.game.<ins_code>` | `model.GameTotals` | engine | gateway، writer |
| `flow.10m.<ins_code>` | `model.TenMinute` | engine | gateway، writer |
| `ai.signal.<ins_code>` | `anomaly.Event` (ناهنجاری AI-01، واگرایی AI-02) | engine | gateway، alerts، writer |
| `quality.<ins_code>` | `model.QualityIssue` | engine، collector | پایش کیفیت، writer |

کانال‌های Centrifugo (مرورگر): `sym:<ins_code>` (وضعیت نماد)، `flow:hot` (همه رویدادهای پول داغ)، `mkt:summary` (خلاصه بازار).

قواعد: واحد پول ریال (int64)؛ زمان‌ها RFC3339 با منطقه زمانی؛ هر فیلد نبود داده در `missing` می‌آید و هیچ مصرف‌کننده‌ای مقدار صفر را به‌جای «نبود داده» تفسیر نمی‌کند.
