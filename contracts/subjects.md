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

## جریان‌های JetStream (P-01)

روی NATS، موضوع پیام همان موضوع بالا است و محتوای پیام **دقیقاً** JSON نوع Go (همان `data` در NDJSON، بدون پاکت). انتخاب گذرگاه با `BUS=ndjson|nats` (پیش‌فرض `ndjson`) و آدرس با `NATS_URL`. تعریف جریان‌ها فقط در `bus.DefaultStreams()`.

| جریان | موضوع‌ها | نگهداری | سقف حجم | هنگام پر شدن |
| --- | --- | --- | --- | --- |
| `MD` | `md.snap.>` | ۴۸ ساعت | 8 GiB | حذف قدیمی‌ترین (`DiscardOld`) + ثبت در لاگ |
| `FLOW` | `flow.>` | ۴۸ ساعت | 4 GiB | همان |
| `AI` | `ai.signal.>` | ۴۸ ساعت | 1 GiB | همان |
| `QUALITY` | `quality.>` | ۴۸ ساعت | 1 GiB | همان |

بایگانی بلندمدت ClickHouse (W-01) و ضبط روزانه (R-01) است، نه گذرگاه. پنجره حذف تکراری: ۵ دقیقه.

مصرف‌کننده پایدار `engine` روی `md.snap.>`:
- حداکثر یک پیام تأییدنشده (`MaxAckPending=1`) تا ترتیب snapshotها حفظ شود؛ `AckWait` ده ثانیه.
- پیام خراب (غیرقابل رمزگشایی): `Term` + لاگ + رخداد کیفیت `UNDECODABLE`؛ هرگز تکرار نمی‌شود و پیام بعدی را قفل نمی‌کند.
- خطای گذرا: تحویل دوباره با تأخیر افزایشی، حداکثر ۵ بار، سپس `Term`.
- پس از اجرای `Process()` روی یک snapshot هرگز `Nak` نمی‌شود: انتشار همان خروجی‌ها با تأخیر محدود تکرار می‌شود و در صورت شکست، engine با کد غیرصفر خارج می‌شود (پیام تأییدنشده به اجرای بعدی می‌رسد). شناسه پیام خروجی‌ها قطعی است (`eng:<زمان ساخت MD>:<شماره>:<ردیف>`)، پس انتشار دوباره حذف تکراری می‌شود.
- بازیابی حالت در شروع: بازپخش `md.snap.>` از ابتدای روز معاملاتی تهران (بر پایه آخرین snapshot تأییدشده) تا کف تأیید، **بدون انتشار**، سپس ادامه مصرف. حالت رادار AI-01 فقط از همان روز گرم می‌شود.
