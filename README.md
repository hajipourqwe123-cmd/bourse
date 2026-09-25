# سامانه تحلیل لحظه‌ای بازار سرمایه — مخزن فاز ۱

مرجع الزامات: سند PRD (Claude Docs). این مخزن خروجی **اسپرینت ۰ (پایه)** است: قرارداد داده، هسته محاسبه جریان پول، قواعد کیفیت داده، آداپتورهای منبع، و زیرساخت محلی.

## وضعیت

| جزء | وضعیت |
| --- | --- |
| قرارداد داده (`internal/model`، `contracts/`) | آماده، پوشش آزمون |
| موتور جریان پول: پول داغ، پول داغ پلاس، بازی بازار، ماتریس ۱۰ دقیقه‌ای (`internal/flow`) | آماده، ۱۱ آزمون |
| قواعد کیفیت داده (`internal/quality`) | آماده، پوشش از طریق آزمون موتور |
| ردیف ۱ هوش مصنوعی: رادار ناهنجاری و واگرایی (`internal/anomaly`) | آماده، ۵ آزمون |
| آداپتور بازپخش (`replay`) | آماده، آزمون شده |
| آداپتور سورس‌آرنا | **موقت**: نگاشت ۴ فیلد تأیید نشده؛ بدون توکن زنده آزمون نشده (وظیفه D-03) |
| گذرگاه NATS JetStream (`BUS=nats`، `internal/bus`) | آماده (P-01)؛ بازیابی حالت پس از راه‌اندازی دوباره، آزمون با سرور NATS درون‌فرایندی |
| اجاره تک‌نمونه engine (JetStream KV) | آماده (P-02)؛ engine دوم از شروع سر باز می‌زند |
| تقویم جلسه‌های معاملاتی هر نماد (`internal/calendar`) | آماده (DL-01)؛ همه ساعت‌ها و تعطیلات **تأییدنشده**؛ `docs/sessions.md` |
| دروازه Centrifugo و وضعیت بازار (`cmd/gateway`، `internal/market`) | آماده (G-01)؛ فقط روی loopback؛ توکن بی‌نام فقط برای توسعه؛ فرمول‌ها در `docs/market-metrics.md` |
| نویسنده ClickHouse | اسپرینت ۱ (W-01) |
| `infra/docker-compose.yml` و DDL کلیک‌هاوس | اجرا و آزموده شده (I-01): هر ۵ سرویس سالم، ۴ جدول ساخته می‌شود |
| رابط کاربری: داشبورد بازار (`web/`، Next.js، راست‌به‌چپ، تم تیره) | آماده (UI-01)؛ Gate 2 با ۱۵۰۰ نماد: p95 تأخیر منبع تا مرورگر ۲٫۸ ثانیه (`make gate2`) |

## اجرای محلی

```bash
make test          # همه آزمون‌ها
make synth         # تولید یک روز معاملاتی ساختگی (فقط برای توسعه)
make demo          # collector (بازپخش) | engine → out.ndjson
```

زیرساخت محلی (NATS، ClickHouse، Redis، PostgreSQL، Centrifugo؛ همه فقط روی 127.0.0.1):

```bash
make env           # یک بار: ساخت .env از .env.example با رمزهای تصادفی محلی (چاپ نمی‌شوند)
make up            # بالا آوردن و صبر تا سالم شدن همه سرویس‌ها
make ddl           # اعمال (دوباره) DDL کلیک‌هاوس؛ تکرارپذیر
make down
```

اجرا روی NATS (پس از `make up`؛ پیش‌فرض همچنان NDJSON است):

```bash
make demo-nats     # روز ساختگی → یک NATS دورریختنی جدا (نه پشته مشترک)؛ engine تا انتها اجرا و خارج می‌شود
make build
BUS=nats bin/engine &                                                  # مصرف‌کننده پایدار engine (اجاره تک‌نمونه)
SOURCE=sourcearena BUS=nats bin/collector                             # فقط وقتی جلسه معاملاتی یکی از گروه‌ها باز است (docs/sessions.md)
go run ./cmd/syngen -n 300 > /tmp/syn300.ndjson                        # بار آزمایشی ۳۰۰ نماد
```

داشبورد روی پشته محلی (پس از `make up` و `make web`). داده ساختگی فقط روی پشته محلی و با برچسب «داده نمایشی» نمایش داده می‌شود:

```bash
set -a; . ./.env; set +a
export BUS=nats ALLOW_SYNTHETIC_ON_BUS=1
bin/engine &
GATEWAY_DEV_TOKEN=1 bin/gateway &                                      # http://127.0.0.1:8080 (فقط loopback)
SOURCE=replay REPLAY_REBASE=now REPLAY_AT=10:00 bin/collector          # ضبط، جابه‌جا به اکنون، با سرعت واقعی
make gate2                                                             # Gate 2: ۱۵۰۰ نماد، NATS و Centrifugo دورریختنی، Chromium
```

وقتی بازار بسته است: `make demo-day` یک روز ساختگی را روی ساعت نمایشی بازپخش می‌کند (پیش‌فرض: آخرین روز معاملاتی، از ۱۱:۴۰، سرعت ۵ برابر، `http://127.0.0.1:8090/`؛ توقف با `make demo-day-stop`). فقط داده ساختگی و فقط loopback؛ جزئیات در `docs/demo-clock.md` (از جمله نکته‌های ویندوز).

برای توسعه رابط: `cd web && NEXT_PUBLIC_GATEWAY_URL=http://127.0.0.1:8080 npm run dev`. گزارش Gate 2 در `gate2-out/report.json` است و تصاویر مرجع طراحی در `docs/design/`.

اگر dockerd سقف فایل باز کمتر از 262144 دارد (خطای `error setting rlimit type 7`)، در `.env` مقدار `CLICKHOUSE_NOFILE` را برابر `ulimit -Hn` بگذارید.

اتصال به منبع زنده (پس از دریافت توکن):

```bash
cp .env.example .env    # SOURCEARENA_TOKEN را در .env بگذارید؛ .env هرگز commit نمی‌شود
SOURCE=sourcearena bin/collector | tee recordings/$(date +%F).ndjson | bin/engine
```

## اصول غیرقابل‌مذاکره

1. داده ناقص یا ناسازگار → بدون خروجی، همراه با رخداد کیفیت. هرگز صفر یا مقدار برآوردی.
2. پول به ریال (int64). زمان منبع و زمان دریافت همیشه جدا.
3. هیچ کلید یا توکنی در کد یا لاگ.
4. هر شاخص فرمول منتشرشده دارد (`docs/hot-money-method.md`).
5. داده ساختگی (`SYN*`) هرگز به کاربر نمایش داده نمی‌شود و در بک‌تست استفاده نمی‌شود.

مستندات: `docs/claude-code-guide.md` (مدل، افورت و روال کار با Claude Code)، `docs/phase1-plan.md` (برنامه و دروازه‌ها)، `docs/adr/` (تصمیم‌های معماری)، `docs/data-quality.md`، `docs/source-mapping.md`.
