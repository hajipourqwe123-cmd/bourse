# راهنمای تولید با Claude Code: مدل، افورت، مصرف توکن

منبع: [مستندات پیکربندی مدل Claude Code](https://code.claude.com/docs/en/model-config) (مطالعه‌شده ۱ مهر ۱۴۰۵). نام مستعارها با گذر زمان به نسخه‌های جدیدتر اشاره می‌کنند؛ پیش از شروع هر فاز یک بار `/model` را بررسی کنید.

## اصل
مدل گران برای تصمیم و محاسبه، مدل ارزان برای جستجو و اجرای آزمون. افورت را بر اساس وظیفه تنظیم کنید، نه یک بار برای همیشه.

## جدول انتخاب

| نوع کار | مدل | افورت | نمونه در این پروژه |
| --- | --- | --- | --- |
| معماری، ADR، طراحی مدل‌های هوش مصنوعی، ریشه‌یابی خطای داده، اجرای خودکار یک اسپرینت کامل | Fable 5.1 (`/model fable`) اگر در پلن شما فعال است؛ وگرنه Opus 5.5 | `xhigh` | ADR-0005، طراحی AI-03، ریشه‌یابی اختلاف پایانی‌ها |
| هسته محاسبه و فرمول‌ها، هم‌روندی، سندباکس فیلتر، امنیت | Opus 5.5 | `high` | internal/flow، internal/anomaly، F-03 |
| پیاده‌سازی عادی: سرویس، API، صفحات رابط | Opus 5.5 | `medium` (پیش‌فرض همین مدل) | W-01، صفحات اسپرینت ۲ و ۳ |
| جستجو در کد، اجرای آزمون، خلاصه لاگ | Haiku (زیرعامل‌های `explorer` و `test-runner`) | `low` | از قبل در `.claude/agents` تعریف شده |
| بازبینی فرمول و سیگنال پیش از ادغام | Opus 5.5 (زیرعامل `quant-reviewer`) | `high` | هر تغییر در flow، anomaly، quality |

نکته‌ها:
- پیش‌فرض Opus 5.5 افورت `medium` است؛ برای کار حساس به دقت دست‌کم `high` بگذارید (`/effort high`).
- `max` فقط برای مسئله‌ای که با `xhigh` حل نشد؛ طبق مستندات ممکن است بازده کاهنده داشته باشد.
- برای یک پرسش سخت در میانه کار، کلمه `ultrathink` در همان پیام کافی است و افورت جلسه را عوض نمی‌کند.
- Fable بسته به پلن و سطح صندلی ممکن است از «اعتبار مصرف» (usage credits) کسر شود؛ Claude Code پیش از آن تأیید می‌گیرد.
- گزینه `opusplan` (Opus در حالت برنامه‌ریزی، Sonnet در اجرا) برای کارهای بزرگ ولی کم‌ریسک رابط کاربری، مصرف را کم می‌کند.

## صرفه‌جویی در توکن
1. **CLAUDE.md کوتاه و انگلیسی:** در هر جلسه کامل بارگذاری می‌شود؛ متن فارسی توکن بیشتری مصرف می‌کند. مستندات انسانی فارسی در `docs/` می‌ماند و فقط در صورت نیاز خوانده می‌شود.
2. **یک وظیفه در هر جلسه** و `/clear` بین وظیفه‌ها؛ شناسه وظیفه در اولین پیام.
3. **فایل‌های حجیم ممنوع:** `testdata/*.ndjson` و `recordings/` در `.claude/settings.json` مسدود شده‌اند.
4. **زیرعامل ارزان برای کار پرحجم:** جستجو و اجرای آزمون روی Haiku، تا متن آن‌ها زمینه جلسه اصلی را پر نکند.
5. **حالت برنامه‌ریزی** برای تغییرات بیش از ۳ فایل: یک برنامه تأییدشده از چند دور اصلاح ارزان‌تر است.

## شروع کار در Claude Code
```bash
unzip bourse-platform.zip && cd bourse-platform
git init && git add -A && git commit -m "Sprint 0 foundation"
claude --model opus
```
در جلسه: `/effort high` و سپس این پیام (انگلیسی برای صرفه‌جویی در توکن):

```text
Task I-01 + P-01 (docs/phase1-plan.md, Sprint 1). Read CLAUDE.md first.
1) Bring up infra/docker-compose.yml, apply ClickHouse DDL, fix anything that fails.
2) Implement a NATS JetStream publisher satisfying bus.Publisher, wire collector and engine to it
   behind a BUS=ndjson|nats env switch, keep NDJSON as default for tests.
Use plan mode. Run test-runner after changes and quant-reviewer before finishing.
```

وظیفه D-03 (تأیید فیلدهای سورس‌آرنا) به توکن زنده نیاز دارد؛ توکن را فقط در `.env` بگذارید و هرگز در گفتگو ننویسید.
