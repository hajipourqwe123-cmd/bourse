# فرهنگ داده پژوهش «کشف سهام مستعد رشد» (فاز ۴)

- وضعیت: نسخه ۱، ۱۳ مهر ۱۴۰۵ (2026-10-05). پیش‌نویس پیش از ساخت مجموعه داده تاریخی؛ هیچ ستونی هنوز روی داده واقعی سنجیده نشده.
- دامنه: همه ویژگی‌هایی که از منابع در دسترس قابل استخراج‌اند، به‌علاوه برچسب‌ها (خروجی‌ها) که هرگز ویژگی نیستند.
- مرجع تعریف‌ها: هر ویژگی‌ای که در `docs/signal-engine-spec.md` تعریف شده، با کد همان‌جا (مثلاً F2، P1) آمده و تعریفش این‌جا تکرار نمی‌شود؛ فقط خلاصه یک‌خطی دارد. تعریف رسمی همان مشخصات است (اصل E-2: یک تعریف و یک پیاده‌سازی).
- منبع ابزارهای تابلوخوانی: `docs/tablokhani_inventory.md`.

## ۱. قرارداد رکورد و واحدها

هر مقدار ذخیره‌شده (خام یا مشتق) این پوشش را دارد. این همان قرارداد موجود مخزن است (ADR-0003 و مشخصات ۴-۲) با نام‌هایی که مأموریت خواسته:

| فیلد | تعریف |
| --- | --- |
| `source` | کد منبع (جدول پایین) |
| `timestamp` | دو زمان جدا: `source_time` (زمان داده نزد منبع؛ اگر منبع نداشته باشد زمان دریافت با `source_time_estimated=true`) و `ingest_time` (زمان دریافت ما). برای ویژگی مشتق: `as_of` (آخرین `source_time` مصرف‌شده) و `computed_at` |
| `symbol` | `ins_code` (کد داخلی TSETMC، کلید پایدار)؛ `l18` فقط برای نمایش |
| `field` | نام ستون همین سند |
| `value` | مقدار؛ پول به **ریال int64**، قیمت ریال صحیح، شاخص‌ها float با دقت ثابت |
| `quality` | `ok`، یا کد رخداد کیفیت (`docs/data-quality.md` و کدهای SG مشخصات ۴-۳)، یا `missing_reason` |
| `config_hash` | برای هر ویژگی مشتق (مشخصات E-4) |

**زمان:** منطقه زمانی بازار `Asia/Tehran` (اکنون ثابت ‎+03:30؛ ایران از ۱۴۰۲ ساعت تابستانی ندارد، ولی تبدیل همیشه با پایگاه منطقه زمانی انجام می‌شود، نه با عدد ثابت). ClickHouse زمان را `DateTime64(3, 'UTC')` نگه می‌دارد (قرارداد موجود `001_schema.sql`). هر خروجی فایلی برای پژوهش (Parquet/CSV) زمان را **ISO-8601 با افست** (`2026-10-05T12:30:00+03:30`) و روز معاملاتی را تاریخ ISO میلادی می‌نویسد؛ تاریخ جلالی فقط ستون نمایشی است. شمارش روز معاملاتی فقط از `internal/calendar` است.

**کدهای منبع:**

| کد | منبع | تاریخچه | یادداشت |
| --- | --- | --- | --- |
| `SA` | سورس‌آرنا `all&type=0` (زنده) | ندارد | فروشنده اصلی زنده؛ سقف واقعی پلن تأییدنشده (حدود ۴۵ درخواست در روز دیده شد) |
| `SA-H` | سورس‌آرنا `name=&days=`، `power=` | روزانه، بدون جریان حقیقی/حقوقی | فقط تطبیق قیمت و حجم (D2) |
| `BA` | BrsApi `AllSymbols` (زنده) | ندارد | پلن رایگان ۱۰۰ در روز؛ ارزش حقیقی/حقوقی ندارد |
| `BA-H0`، `BA-H1` | BrsApi `History.php` نوع ۰ و ۱ | **روزانه از ۱۳۸۵ و ۱۳۸۷** | منبع اصلی تاریخچه (DL-03b)؛ سقف درخواست نامعلوم (Q-SG1) |
| `REC` | ضبط عکس‌های لحظه‌ای پروژه (`recordings/`، R-01) | فقط از روز شروع ضبط | تنها منبع **تاریخچه درون‌روزی** ما |
| `TK-P`، `TK-A` | تابلوخوانی عمومی و اشتراکی | فقط ماتریس ۱۰ دقیقه‌ای (از 2026-01-31)، امتیاز نماد (از 2026-01-04)، سرانه ۳۰۰ روزه، سهامداران ۲۰ روزه؛ بقیه فقط امروز (`archive_feasibility`) | مشتق از سورس‌آرنا؛ مستقل نیست |
| `CODAL` | کدال (از BrsApi `Codal/Announcement.php`) | دارد | زمان انتشار با رقم فارسی |
| `DER` | محاسبه در کد ما | — | فقط از ورودی‌های یک فروشنده (ADR-0006) |
| `LBL` | برچسب آینده‌نگر | — | **هرگز ورودی مدل نیست** |

**اولویت پژوهشی:** `P0` لازم برای نخستین مطالعه رویداد پایان روز؛ `P1` در دور اول؛ `P2` پس از شواهد اولیه؛ `P3` مسدود یا بیرون از افق ۱ تا ۱۰ روز.

**رفتار مقدار ناموجود (همه ردیف‌ها):** قاعده ۱ مخزن. هیچ صفرگذاری، تکرار مقدار قبل یا درون‌یابی نیست. مقدار ثبت نمی‌شود و `missing_reason` می‌گیرد. ستون `missing_value_behavior` فقط علت‌های خاص هر ویژگی را می‌گوید.

## ۲. ویژگی‌های خام

### ۲-الف. بار روزانه

| feature_name | source | definition | unit | frequency | raw/calculated | formula_if_known | expected_range | missing_value_behavior | possible_bias | research_priority |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `open` | BA-H0 `pf`؛ SA `first_price` | اولین قیمت روز | ریال | روزانه | raw | — | > 0 | روز بی‌معامله **ردیف دارد** با `tvol = 0` و `pf = pmin = pmax = 0` ← ناموجود (`archive_feasibility` بخش ۳) | — | P0 |
| `high`، `low` | BA-H0 `pmax`، `pmin` | بیشینه و کمینه قیمت معامله‌شده | ریال | روزانه | raw | — | low ≤ high | قیمت ≤ ۱ ریال جانشین است ← ناموجود | — | P0 |
| `last` | BA-H0 `pl`؛ SA `close_price` | آخرین معامله | ریال | روزانه | raw | — | [low, high] | — | نام `close_price` سورس‌آرنا گمراه‌کننده است | P0 |
| `final` | BA-H0 `pc`؛ SA `final_price` | قیمت پایانی (میانگین وزنی رسمی) | ریال | روزانه | raw | — | > 0 | — | — | P0 |
| `prev` | BA-H0 `py` | قیمت مرجع دیروز (پس از تعدیل رویداد) | ریال | روزانه | raw | — | > 0 | — | منبع تشخیص رویداد شرکتی (D4) | P0 |
| `volume` | `tvol` | حجم کل | سهم | روزانه | raw | — | ≥ 0 | — | شامل بلوکی و توافقی (M4 بی‌داده) | P0 |
| `value` | `tval` | ارزش کل | ریال | روزانه | raw | — | ≥ 0 | — | همان | P0 |
| `trade_count` | `tno` | تعداد معاملات | عدد | روزانه | raw | — | ≥ 0 | — | — | P1 |
| `limit_up`، `limit_down` | زنده `tmax`/`tmin` و `daily_price_high/low`؛ تاریخچه ندارد | دامنه مجاز | ریال | روزانه | raw | — | low ≥ limit_down | مقدار ۱ یا ۹۹۹۹۹۹۹۹۹ = «بدون دامنه» ← ناموجود؛ تاریخچه تا DL-03d با جانشین D5 | جانشین قفل بی‌دامنه، روزهای بازگشایی را نمی‌شناسد | P0 |
| `base_volume` | زنده `bvol`؛ تاریخچه ندارد | حجم مبنا | سهم | روزانه | raw | — | > 0 | تاریخچه ندارد | فقط تولید | P3 |
| `adj_factor` | DER از D4 | ضریب رویداد شرکتی | نسبت | رویداد | calculated | `f(t) = py(t) ÷ pc(t−1)` | (0.05, 1] | بیرون از بازه ← صف بازبینی | ضریب > ۱ مشکوک | P0 |
| `adj_*` | DER | قیمت تعدیل‌شده با **لنگر t** | ریال | روزانه | calculated | مشخصات D4 | > 0 | پس از بازبینی ضریب | استفاده از سری تعدیل امروز = نگاه به آینده | P0 |
| `is_lock_up/down/flat` | DER از D5 | روز قفل و جهت آن | پرچم | روزانه | calculated | `tvol > 0` و `pmin = pmax > 0`؛ جهت با `pmin` در برابر `py` (نه `pc`)؛ ردیف `tvol = 0` قفل نیست و `UNKNOWN`/توقف است (`trading_state_model.md`) | — | — | با جانشین، قفل غیر روی حد را نمی‌شناسد | P0 |
| `halted_days` | DER (ردیف با `tvol = 0`، و روز بی‌ردیف در برابر تقویم) | طول توقف | روز | روزانه | calculated | — | ≥ 0 | تقویم تأییدنشده (D9) | ردیف `tvol = 0` نباید قفل جانشین D5 شمرده شود | P0 |
| `final_outside_range` | DER | `pc` بیرون از [`pmin`، `pmax`] (قاعده حجم مبنا) | پرچم | روزانه | calculated | `pc < pmin` یا `pc > pmax` با `tvol > 0` | — | — | پایانی آن روز قابل معامله نبوده؛ برچسب‌های مبتنی بر `final` حساسیت می‌خواهند | P0 |

### ۲-ب. حقیقی و حقوقی

| feature_name | source | definition | unit | frequency | raw/calculated | formula_if_known | expected_range | missing_value_behavior | possible_bias | research_priority |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `buy_count_i`، `sell_count_i` | BA-H1 `Buy_CountI`/`Sell_CountI`؛ SA `real_buy/sell_count` | تعداد خریدار و فروشنده حقیقی **متمایز در روز** | نفر | روزانه | raw | — | ≥ 0 | صفر واقعی است | تعداد متمایز روزانه است؛ تفاضل درون‌روزی آن کم‌شمار است (ADR-0004) | P0 |
| `buy_count_n`، `sell_count_n` | BA-H1 `*_CountN` | همان برای حقوقی | نفر | روزانه | raw | — | ≥ 0 | — | — | P1 |
| `buy_vol_i`، `sell_vol_i`، `buy_vol_n`، `sell_vol_n` | BA-H1 `*_Volume` | حجم خرید و فروش هر گروه | سهم | روزانه | raw | — | حقیقی + حقوقی = `volume` | ناسازگاری جمع ← رخداد کیفیت | کد به کد حقوقی/حقیقی | P0 |
| `buy_val_i`، `sell_val_i`، … | BA-H1 `*_Value`؛ SA `real/co_*_value` | ارزش خرید و فروش هر گروه | ریال | روزانه | raw | — | جمع = `value` | BA زنده ندارد ← مبنای حجمی (Q-SG12) | — | P0 |

### ۲-پ. دفتر سفارش (فقط زنده و ضبط‌شده)

| feature_name | source | definition | unit | frequency | raw/calculated | formula_if_known | expected_range | missing_value_behavior | possible_bias | research_priority |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `bid_px_k`، `bid_vol_k`، `bid_cnt_k` (k=1..5) | SA `{k}_buy_*`؛ BA `pd/qd/zd` | پنج سطح خرید | ریال، سهم، نفر | عکس لحظه‌ای | raw | — | — | نبود هر یک از ۳۰ کلید ← کل دفتر ناموجود | سفارش‌های پیش‌گشایش قابل لغو | P2 |
| `ask_px_k`، `ask_vol_k`، `ask_cnt_k` | SA `{k}_sell_*`؛ BA `po/qo/zo` | پنج سطح فروش | همان | عکس لحظه‌ای | raw | — | — | همان | — | P2 |
| `queue_buy_value`، `queue_sell_value` | DER از دفتر | ارزش صف در قیمت حد | ریال | عکس لحظه‌ای | calculated | `bid_vol_1 × bid_px_1` وقتی `bid_px_1 = limit_up` | ≥ 0 | دامنه نامعلوم ← ناموجود | — | P2 |

### ۲-ت. داده مرجع نماد

| feature_name | source | definition | unit | frequency | raw/calculated | formula_if_known | expected_range | missing_value_behavior | possible_bias | research_priority |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `industry_code` | SA `industry_code`؛ BA `cs_id` | گروه رسمی | کد | جاری | raw | — | — | — | **فقط مقدار جاری**؛ نسبت دادن به گذشته نگاه به آینده و سوگیری بقاست | P1 |
| `driver_group` | فایل نگاشت (D7) | گروه محرک اقتصادی | کد | نسخه‌دار | raw | — | — | ساخته نشده | — | P1 |
| `asset_class` | `internal/classmap` | سهم، حق تقدم، کلاس صندوق | برچسب | جاری | calculated | قواعد classmap | — | `unknown` بیرون از جمع‌ها | تأییدنشده | P0 |
| `board` | چهار رقم آخر ISIN | تابلو عادی، صدور/ابطال، قیمت ثابت، بلوکی | کد | جاری | raw | — | 0001..0004 | — | — | P0 |
| `shares_out` | SA `all_stocks`؛ فیلتر `z` | تعداد سهام | سهم | جاری | raw | — | > 0 | — | افزایش سرمایه گذشته را نشان نمی‌دهد | P2 |
| `free_float_pct` | SA `free_float` | درصد شناوری | ٪ | جاری | raw | — | [0, 100] | — | فقط تولید (U3، M1) | P3 |
| `state` | SA `state` | مجاز، ممنوع-متوقف، مجاز-متوقف | برچسب | لحظه‌ای | raw | — | — | — | — | P1 |
| `eps`، `pe` | BA | سود هر سهم و P/E جاری | ریال، نسبت | جاری | raw | — | — | صندوق‌ها `null` | بدون زمان انتشار؛ تاریخچه ندارد | P3 |
| `has_market_maker` | — | بازارگردان | پرچم | — | — | — | — | منبع ندارد (Q-SG3) | — | P3 |

### ۲-ث. سطح بازار

| feature_name | source | definition | unit | frequency | raw/calculated | formula_if_known | expected_range | missing_value_behavior | possible_bias | research_priority |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `tepix`، `tepix_ew` | SA `market=market_bourse`؛ BA `Index.php`؛ TK-P `market-indices` | شاخص کل و هم‌وزن بورس | واحد شاخص | لحظه‌ای | raw | — | > 0 | — | تاریخچه شاخص در منابع فعلی کاوش نشده | P1 |
| `ifb_index` | همان | شاخص فرابورس | واحد شاخص | لحظه‌ای | raw | — | > 0 | — | همان | P1 |
| `ew_index_univ` | DER (R3) | شاخص هم‌وزن جهان نرمال‌سازی خودمان | واحد شاخص | روزانه | calculated | مشخصات R3 | > 0 | روز با مقطع کم ← ناموجود | جهان امروز = سوگیری بقا تا Q-SG11 | P0 |
| `breadth_ma50` | DER (R1) | سهم نمادهای بالای MA50 | ٪ | روزانه | calculated | مشخصات R1 | [0, 100] | — | همان | P0 |
| `breadth_range` | DER (`internal/market`) | مثبت/منفی نسبت به دامنه هر نماد | شمار | لحظه‌ای | calculated | `docs/market-metrics.md` | — | `no_limits` جدا | — | P1 |
| `market_nrf_n` | DER (R2) | جریان حقیقی کل بازار n روز ÷ MDV | نسبت | روزانه | calculated | مشخصات R2 | — | — | — | P0 |
| `queue_counts` | TK-P `queue-trend` | تعداد و ارزش صف خرید و فروش کل بازار | شمار، ارزش | حدود ۱ دقیقه | raw (نزد ما) | تعریف صف نزد سایت نامعلوم | ≥ 0 | — | تعریف و واحد نامعلوم؛ تا تطبیق با دفتر خودمان LOW CONFIDENCE | P1 |
| `market_per_capita` | TK-A `market-percapita-history` | سرانه خرید و فروش حقیقی بازار | میلیون تومان | روزانه/درون‌روزی | calculated (نزد سایت) | بدون صندوق درآمد ثابت، طلا، نقره | > 0 | — | تعریف سایت | P2 |

## ۳. ویژگی‌های مشتق

### ۳-الف. جریان پول (خوشه‌های K-FLOW و K-SIZE، بخش ۴)

| feature_name | source | definition | unit | frequency | raw/calculated | formula_if_known | expected_range | missing_value_behavior | possible_bias | research_priority |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `nrf_day` | DER | خالص جریان حقیقی روز | ریال | روزانه | calculated | مشخصات بخش ۲: با `volume`، `(buy_vol_i − sell_vol_i) × (value ÷ volume)` | منفی تا مثبت | `volume = 0` ← ناموجود | مبنای حجمی تقریب VWAP روز است، نه ارزش واقعی | P0 |
| `F1_nrf_5d` | DER (SM-01) | جمع `nrf_day` در پنجره ÷ MDV20 | نسبت | روزانه | calculated | مشخصات F1 | عموماً [−3, 3] | روز معتبر کمتر از حد ← ناموجود | — | P0 |
| `F2_buyer_power` | DER (SM-01) | قدرت خریدار: سرانه خرید حقیقی ÷ سرانه فروش حقیقی | نسبت (ذخیره لگاریتمی) | روزانه | calculated | مشخصات F2 | (0, ∞)، معمولاً [0.2, 5] | تعداد یا حجم صفر ← ناموجود | در روز قفل، ترکیب جلوی صف تصادفی است (ادعای خود تابلوخوانی) | P0 |
| `per_capita_buy_i`، `per_capita_sell_i` | DER | ارزش (یا حجم × VWAP) هر خریدار/فروشنده حقیقی | ریال | روزانه | calculated | `buy_val_i ÷ buy_count_i` | > 0 | تعداد صفر ← ناموجود | تورم: مقایسه بین سال‌ها فقط نسبی (نسبت به میانه خود نماد) | P0 |
| `F3_flow_persistence` | DER (SM-01) | شمار یا تداوم روزهای «قوی» | روز | روزانه | calculated | مشخصات F3 | [0, window] | — | — | P0 |
| `F5_ownership_shift` | DER | `nrf_day ÷ value` (نمایشی) | نسبت | روزانه | calculated | مشخصات F5 | [−1, 1] | — | با F1 هم‌خوشه؛ در امتیاز نمی‌آید | P1 |
| `ret_vs_flow_divergence` | DER | همان `mic_class` مشخصات | طبقه | روزانه | calculated | مشخصات P5 | absorption، thin_supply، no_real_flow، neutral | — | — | P0 |
| `sell_count_growth_i` | DER (M3) | تعداد فروشنده حقیقی ÷ میانگین پایه | نسبت | روزانه | calculated | مشخصات M3 | > 0 | — | — | P1 |
| `inst_net_share` | DER | (`buy_vol_n − sell_vol_n`) ÷ `volume` | نسبت | روزانه | calculated | — | [−1, 1] | — | کد به کد حقوقی/حقیقی؛ صندوق‌های بازارگردان | P1 |
| `hot_net`، `hot_plus_net`، `retail_net`، `unattributed` (خانواده **HotMoneyMetric**) | REC + DER (`internal/flow`) | خالص پول هر باند اندازه، با باند بر اساس **میانگین** ارزش بازه بر افزایش تعداد؛ «پول هوشمند» یا «خریدار بزرگ» نیست (`source_independence_matrix` بخش ۵) | ریال | بازه و جمع روز | calculated | ADR-0004 | — | روز ناقص ← `partial` | رقیق‌شدن در بازه؛ وابسته به طول بازه | P2 (فقط رو به جلو) |
| `flow_10m_matrix` | REC + DER | خالص هر باند در پنجره ۱۰ دقیقه + تغییر قیمت | ریال، ٪ | ۱۰ دقیقه | calculated | `internal/flow` | — | پنجره ناقص ← `partial` | همان | P2 |

### ۳-ب. واکنش قیمت و حجم (خوشه‌های K-VOL و K-LOC)

| feature_name | source | definition | unit | frequency | raw/calculated | formula_if_known | expected_range | missing_value_behavior | possible_bias | research_priority |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `P1_rvol_day` (= `relative_volume`) | DER (TA-01) | حجم روز ÷ میانه حجم ۲۰ روز پیش از t | نسبت | روزانه | calculated | مشخصات P1 | (0, ∞)، معمولاً [0.2, 10] | پایه کوتاه ← ناموجود | حجم بلوکی | P0 |
| `volume_zscore` | DER (پیشنهاد) | انحراف لگاریتم حجم از میانگین ۲۰ روز پیش، بر انحراف معیار | z | روزانه | calculated | `(ln vol_t − mean(ln vol_{t−20..t−1})) ÷ sd(…)` | معمولاً [−3, 6] | sd = 0 یا پایه کوتاه ← ناموجود | دم‌پهن؛ لگاریتم برای پایداری | P1 |
| `P2_clv` | DER (TA-01) | جای آخرین قیمت در دامنه روز | [−1, 1] | روزانه | calculated | مشخصات P2 | [−1, 1] | `high = low` ← ناموجود + وضعیت قفل | — | P0 |
| `P4_ad_rating` | DER (TA-01) | میانگین وزنی CLV با حجم | [−1, 1] | روزانه | calculated | مشخصات P4 | [−1, 1] | — | — | P0 |
| `last_vs_final` | DER | `(last − final) ÷ final` | ٪ | روزانه | calculated | مشخصات P3 (بدون شرط حجم مبنا) | معمولاً [−5, 5] | — | P3 فقط نمایشی؛ در ماشه G1 جهتش به کار رفته | P0 |
| `gap_open` | DER | `open ÷ prev − 1` | ٪ | روزانه | calculated | — | در دامنه | — | — | P1 |
| `intraday_recovery` | DER (پیشنهاد) | بازگشت از کمینه روز تا پایانی، نسبت به افت تا کمینه | نسبت | روزانه | calculated | `(final − low) ÷ (prev − low)` اگر `low < prev` | [0, ∞) | `low ≥ prev` ← ناموجود (افتی نبوده) | نسخه پایان روز «منفی به مثبت»؛ مسیر درون‌روزی را نمی‌بیند | P0 |
| `vwap_day` | DER | `value ÷ volume` | ریال | روزانه | calculated | — | [low, high] | `volume = 0` ← ناموجود | شامل بلوکی | P1 |
| `VWAP_distance` | DER (پیشنهاد) | `final ÷ vwap_day − 1` | ٪ | روزانه | calculated | — | معمولاً [−3, 3] | — | همان | P1 |
| `ATR_normalized_move` | DER (پیشنهاد) | `(final − prev) ÷ ATR14(t−1)` | ATR | روزانه | calculated | — | معمولاً [−3, 3] | ATR گرم‌نشده ← ناموجود | — | P0 |
| `P6_quiet_volume` | DER (SM-01) | حجم نسبی بالا با بازده کم، یا در روز قفل | پرچم | روزانه | calculated | مشخصات P6 | — | — | — | P0 |

### ۳-پ. روند، قدرت نسبی و نوسان (خوشه‌های K-TREND و K-VOLA)

| feature_name | source | definition | unit | frequency | raw/calculated | formula_if_known | expected_range | missing_value_behavior | possible_bias | research_priority |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `MAn`، `ROCn` | DER (TA-01) | میانگین ساده و نرخ تغییر `adj_final` | ریال، ٪ | روزانه | calculated | مشخصات بخش ۲ | — | گرم‌شدن ← ناموجود | — | P0 |
| `ATRn` | DER (TA-01) | دامنه واقعی با هموارسازی وایلدر | ریال | روزانه | calculated | مشخصات بخش ۲ | > 0 | — | روزهای قفل دامنه را کم نشان می‌دهند | P0 |
| `K1_atr_pct`، `down_lock_days` | DER | `ATR14 ÷ adj_final`؛ شمار قفل پایین | ٪، روز | روزانه | calculated | مشخصات K1 | — | — | — | P0 |
| `T1_rs_rating` | DER | جمع وزنی ROCها، به صدک | صدک | روزانه | calculated | مشخصات T1 | [0, 100] | تاریخچه کوتاه ← `SG_HISTORY_SHORT` | — | P0 |
| `market_relative_strength` | DER | همان `er_5d` (P5) و T1 | ٪، صدک | روزانه | calculated | مشخصات P5 | — | — | — | P0 |
| `T2_trend_gate` | DER | دروازه روند S1/S2 | پرچم | روزانه | calculated | مشخصات T2 | — | — | — | P0 |
| `T3_atr_ratio`، `T3_vol_dryup` | DER | انقباض نوسان و خشک‌شدن حجم | نسبت | روزانه | calculated | مشخصات T3 | > 0 | — | — | P0 |
| `T4_dist_high` | DER | فاصله تا سقف n روزه | ٪ | روزانه | calculated | مشخصات T4 | ≤ 0 | — | — | P0 |
| `breakout_level_L` | DER | بیشینه `adj_high` در `level_days` روز تا t−1 | ریال (مقیاس خام t) | روزانه | calculated | مشخصات ۱۰-۱ | > 0 | — | — | P0 |
| `sr_levels`، `distance_from_support`، `distance_from_resistance` | DER (TA-01) | نزدیک‌ترین سطح قله/دره خوشه‌بندی‌شده زیر و بالای قیمت؛ فاصله بر حسب ATR14 (و درصد برای نمایش) | ATR | روزانه | calculated | برنامه TA-01: قله و دره محلی، تحمل `k·ATR` | ≥ 0 | سطحی نیست ← ناموجود | پنجره قله محلی به داده بعد از قله نیاز دارد: قله فقط پس از N روز تأیید می‌شود (آزمون نابستگی به آینده) | P1 |
| `realized_vol_n` | DER | انحراف معیار بازده روزانه n روز | ٪ | روزانه | calculated | — | > 0 | — | روزهای قفل نوسان را کم نشان می‌دهند | P1 |

### ۳-ت. شاخص‌های فنی (لایه تأیید، فاز ۱۴؛ همه در خوشه K-TREND مگر خلافش گفته شود)

| feature_name | source | definition | unit | frequency | raw/calculated | formula_if_known | expected_range | missing_value_behavior | possible_bias | research_priority |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `rsi14` | DER (TA-01) | RSI با میانگین وایلدر | [0, 100] | روزانه | calculated | استاندارد | [0, 100] | گرم‌شدن | — | P1 |
| `mfi14` | DER (TA-01) | RSI وزن‌دار با «قیمت نوعی × حجم» | [0, 100] | روزانه | calculated | استاندارد | [0, 100] | — | خوشه K-TREND و K-VOL | P1 |
| `macd`، `macd_signal`، `macd_hist` | DER (TA-01) | MACD(12، 26، 9) | ریال | روزانه | calculated | استاندارد | — | — | — | P1 |
| `stoch_k`، `stoch_d` | DER (TA-01) | Stochastic(14، 3، 3) | [0, 100] | روزانه | calculated | استاندارد | [0, 100] | — | خوشه K-LOC هم | P1 |
| `cci14`، `williams_r14` | DER | CCI و Williams %R | — | روزانه | calculated | استاندارد | WR در [−100, 0] | — | ورودی امتیاز تکنیکالی تابلوخوانی | P2 |
| `adx14`، `di_plus`، `di_minus` | DER | قدرت روند | [0, 100] | روزانه | calculated | استاندارد (وایلدر) | [0, 100] | — | — | P1 |
| `ema_5/10/20/30`، `ema_alignment` | DER | چیدمان میانگین‌های نمایی | پرچم سه‌حالته | روزانه | calculated | صعودی، نزولی، آمیخته | — | — | — | P2 |
| `bb_width`، `bb_pct_b` | DER | باند بولینگر (۲۰، ۲) | نسبت | روزانه | calculated | استاندارد | — | — | خوشه K-VOLA | P1 |
| `obv_slope_n` | DER | شیب OBV در n روز | — | روزانه | calculated | استاندارد | — | — | خوشه K-VOL | P2 |
| `ichimoku_*` | DER | تنکان، کیجون، ابر | ریال | روزانه | calculated | استاندارد (۹، ۲۶، ۵۲) | — | — | ابر جلوانداخته‌شده نباید به گذشته نسبت داده شود | P2 |
| `candle_pattern` | DER (TA-01) | چکش، پوشا، دوجی، ستاره | برچسب | روزانه | calculated | تعریف عددی TA-01 | — | روز قفل الگو حساب نمی‌شود | — | P2 |

### ۳-ث. ویژگی‌های پیشنهادی مأموریت (فاز ۱۳)

| feature_name | source | definition | unit | frequency | raw/calculated | formula_if_known | expected_range | missing_value_behavior | possible_bias | research_priority |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `buyer_power_change` | DER | تغییر لگاریتم قدرت خریدار نسبت به میانه ۵ روز پیش | لگاریتم | روزانه | calculated | `ln F2(t) − median(ln F2, t−5..t−1)` | معمولاً [−2, 2] | کمتر از ۳ روز معتبر ← ناموجود | قفل‌ها | P0 |
| `buyer_power_acceleration` | DER | تغییر `buyer_power_change` | لگاریتم | روزانه | calculated | `chg(t) − chg(t−1)` | — | — | نویزی | P1 |
| `price_response_to_money_flow` | DER | واکنش قیمت به جریان: اختلاف صدک بازده مازاد ۵ روزه و صدک F1 | واحد صدک | روزانه | calculated | `pct(er_5d) − pct(F1_nrf_5d)` | [−100, 100] | — | منفی بزرگ = جذب (absorption)، مثبت بزرگ = حرکت بی‌پول؛ پیوسته‌ساز `mic_class` | P0 |
| `industry_relative_strength` | DER | ROC20 نماد منهای ROC20 شاخص هم‌وزن گروه محرک | ٪ | روزانه | calculated | — | — | `industry.enabled: false` ← ناموجود | عضویت جاری = سوگیری | P2 |
| `smart_money_persistence` | DER | همان F3 | — | — | — | — | — | — | تعریف دوم ساخته نمی‌شود (E-2) | P0 |
| `filter_frequency` | DER | شمار غربال‌های پیش‌ثبت‌شده‌ای که نماد در روز t در آن‌هاست | عدد | روزانه | calculated | — | [0, N] | — | **شمارش تکراری** خوشه‌ها (بخش ۴) | P1 |
| `filter_diversity` | DER | شمار **خوشه‌های مستقل** در میان غربال‌های برقرار | عدد | روزانه | calculated | — | [0, ۷] | — | — | P1 |
| `signal_confluence` | DER | شمار خوشه‌هایی که صدک نماینده‌شان ≥ آستانه است | عدد | روزانه | calculated | سطح خوشه، نه سطح ویژگی | [0, ۷] | خوشه با نماینده ناقص شمرده نمی‌شود | — | P0 |
| `regime_score` و `regime_state` | DER (R) | امتیاز و وضعیت مشخصات (مثبت، خنثی، منفی) | [−1, 1]، برچسب | روزانه | calculated | مشخصات بخش ۶ | — | ورودی ناقص ← `SG_REGIME_UNKNOWN` | — | P0 |
| `regime6_label` | DER (پژوهش) | BULL، RECOVERY، RANGE، DISTRIBUTION، BEAR، PANIC | برچسب | روزانه | calculated | چند تعریف رقیب (`research_plan` H-R) | — | — | تعریف با نگاه به آینده (مثلاً قله‌یابی) فقط برچسب تحلیل است، نه ورودی | P1 |
| `IGR` | DER (I1) | امتیاز گروه | صدک | روزانه | calculated | مشخصات I1 | [0, 100] | گروه کم‌عضو ← ناموجود (I3) | — | P2 |

### ۳-ج. ویژگی‌های تابلوخوانی (برای آزمون ادعا و مقایسه؛ همه وابسته به سورس‌آرنا)

همه این‌ها **فقط** برای آزمون ادعاهای سایت (H-T*) یا بایگانی رویداد به کار می‌روند و وارد امتیاز ما نمی‌شوند (قاعده ۶). برداشت هر چیزی جز `TK-P` به تصمیم مالک وابسته است.

| feature_name | source | definition | unit | frequency | raw/calculated | formula_if_known | expected_range | missing_value_behavior | possible_bias | research_priority |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `tk_hot_alert` (رویداد، HotMoneyMetric) | TK-A `hot-money/hot` (فقط امروز؛ بایگانی ندارد) | هشدار پول داغ با `delta_count`، `alert_per_capita`، `first_time` | رویداد | درون‌روزی | calculated (نزد سایت) | میانگین ارزش بازه ÷ افزایش تعداد (inventory C-02) | — | — | رقیق‌شدن؛ هشدارهای پس از جلسه (C-03) | P1 |
| `tk_alert_count_buy/sell`، `tk_avg_hot_buys/sells`، `tk_proceeds` | TK-A | تجمیع روزانه هشدارها | عدد، میلیون تومان | روزانه | calculated | — | ≥ 0 | — | همان | P2 |
| `tk_symbol_score` و چهار جزء | TK-A `symbol-scores` | امتیاز ۰ تا ۱۰۰ | امتیاز | روزانه (تاریخچه) | calculated | inventory C-01 و T09 | [0, 100] | — | ساعت مرورگر؛ حجم خطی | P1 |
| `tk_technical_score` | TK-A `indicators` | ۰ تا ۱۰ از EMA، RSI، CCI، WR، ADX | امتیاز | لحظه‌ای | calculated | inventory T09 | [0, 10] | — | — | P2 |
| `tk_golden_number`، `tk_plusD`، `tk_plusM`، `tk_mToD`، `tk_cross_time/type`، `tk_buy_slope`، `tk_sell_slope`، `tk_ratio_to_avg` | TK-A `tablocastic-history` | ستون‌های تابلوکستیک | نامعلوم | ۶۰ ثانیه | calculated (نزد سایت) | **نامعلوم** | نامعلوم | — | همه از سرانه درون‌روزی (K-SIZE) | P2 |
| `tk_multidim_hits` | TK-A `multidimensional-analysis` | شمار حضور در ۸ فیلتر | عدد | ۶۰ ثانیه | calculated | — | [0, 8] | — | شمارش تکراری | P1 |
| `tk_special_alert` (۲۱ نوع) | TK-A `stock-alerts` (تعریف) | رویداد هشدار ویژه با دسته اثر | رویداد | درون‌روزی | calculated | منتشرشده (`authenticated_audit` بخش ۴) | — | — | بایگانی ندارد؛ فقط با بازسازی خودمان | **P1** (۹ هشدار پایان روز) |
| `large_holder_change` (خانواده **TrueLargeBuyerEvidence**) | TSETMC / TK-A `shareholders` | تغییر روزانه تعداد سهم دارندگان ≥ ۱٪ | سهم، ٪ | روزانه | raw | — | — | تاریخچه فقط ۲۰ روز؛ پیش از آن ناموجود | فقط بالای ۱٪؛ بیشتر نهادها؛ با تأخیر | P1 (ضبط رو به جلو) |
| `tk_smart_money_score` | TK-A صفحه اصلی | ترکیب خطی سه نسبت | امتیاز | لحظه‌ای | calculated | inventory T04 | ≥ 0 | — | تعریف ورود و خروج یکسان (C-04) | P3 |
| `tk_price_movement_ratio` | TK-P `smart-money-averages` | میانگین پایانی ۳ روز ÷ ۱۴ روز | نسبت | روزانه | calculated | inventory T04 | حدود [0.7, 1.4] | — | با ROC هم‌خوشه | P3 |
| `tk_avg_per_capita_*_10d` | TK-P `smart-money-averages` | میانگین ۱۰ روزه سرانه و تعداد | میلیون تومان، نفر | روزانه | calculated | — | > 0 | — | نمودار سرانه سایت مقدار پرشده دارد (C-05) | P3 |
| `tk_queue_trend` | TK-P | صف‌های کل بازار | — | ۱ دقیقه | raw | — | — | — | تعریف نامعلوم | P1 |
| `tk_hall_signal` | TK-A `signals/closed` | ورود، درصد پرتفوی، خروج‌ها | ریال، ٪ | رویداد | raw | — | — | — | سوگیری انتخاب ناشر | P1 |

## ۴. استقلال ویژگی‌ها

**اصل:** شمار سیگنال ≠ شمار اطلاعات مستقل. چند ویژگی که تابعی از یک متغیر پایه‌اند، یک تأیید حساب می‌شوند.

### ۴-الف. خوشه‌های وابستگی ساختاری (از روی فرمول؛ فرضیه تا سنجش)

| خوشه | متغیرهای پایه | اعضا (این سند و تابلوخوانی) |
| --- | --- | --- |
| **K-SIZE** (اندازه خریدار) | `buy_val_i`، `buy_count_i`، `sell_val_i`، `sell_count_i` | `per_capita_buy/sell_i`، `F2_buyer_power`، `buyer_power_change`، `buyer_power_acceleration`، `F3` (بخش قدرت)، `tk_buy_to_sell_ratio`، `tk_golden_number`؟، `tk_plusD/plusM/mToD`؟، `tk_cross_*`، `tk_buy/sell_slope`، `tk_ratio_to_avg`، دو جزء سرانه و نسبت `tk_symbol_score`، `tk_alert_per_capita`، `smart_money_power` هشدارها، `power=` سورس‌آرنا |
| **K-FLOW** (خالص جریان حقیقی) | `buy_*_i − sell_*_i` | `nrf_day`، `F1`، `F5`، `F3` (بخش جریان)، `market_nrf_n`، `hot/plus/retail_net` (جمعشان ≈ `nrf_day`)، `tk_proceeds`، «ورود/خروج پول» تابلوخوانی، «برآیند پول داغ» |
| **K-VOL** (فعالیت حجمی) | `volume`، پایه حجم | `P1_rvol_day`، `volume_zscore`، `P6`، جزء حجم `tk_symbol_score`، «حجم به ۳۰ روز»، «حجم مشکوک»، `obv_slope`، بخش حجم `mfi14` |
| **K-LOC** (جای قیمت در روز) | `last`، `final`، `high`، `low`، `prev` | `P2_clv`، `P4`، `last_vs_final`، `intraday_recovery`، `VWAP_distance`، جزء «هیجان» `tk_symbol_score`، `stoch_k` |
| **K-TREND** (روند و تکانه) | سری `adj_final` | `ROCn`، `MAn`، `T1`، `T2`، `T4`، `er_5d`، `rsi14`، `macd`، `cci14`، `williams_r14`، `adx14`، `ema_alignment`، `tk_technical_score`، `tk_price_movement_ratio` |
| **K-VOLA** (نوسان) | دامنه روزانه | `ATRn`، `K1`، `T3_atr_ratio`، `bb_width`، `realized_vol_n` |
| **K-BOOK** (دفتر و صف) | دفتر سفارش | `queue_*`، `tk_queue_trend`، پیش‌گشایش، «اردرهای حمایتی» |

**هم‌پوشانی میان خوشه‌ها (ساختاری):**
- **K-SIZE و K-FLOW** از همان چهار فیلد حقیقی ساخته می‌شوند. با مبنای حجمی، `F1` و `F2` هر دو از `buy_vol_i` و `sell_vol_i` می‌آیند؛ تفاوت در تقسیم بر تعداد است. انتظار: هم‌بستگی مثبت متوسط تا زیاد. پیش از سنجش، **دو خوشه جدا ولی نه کاملاً مستقل** شمرده می‌شوند.
- **K-VOL و K-FLOW:** `F1` بر MDV20 تقسیم می‌شود؛ روزهای پرحجم قدر مطلق `nrf` بزرگ‌تری دارند.
- **K-LOC و K-TREND:** CLV روز شکست با ROC1 هم‌بسته است.
- **K-SIZE و K-BOOK در روز قفل:** در صف خرید، سرانه خرید تصادفی است و نسبت خرید به فروش بی‌معناست. در روز قفل، اعضای K-SIZE نماینده خوشه نیستند.

**پیامد برای شمارش تأیید:** ابزار «تحلیل چندبعدی» تابلوخوانی (۸ فیلتر) در بهترین حالت ۴ تا ۵ خوشه را پوشش می‌دهد: پول داغ، پول هوشمند و کد به کد همه K-FLOW/K-SIZE هستند؛ حجم K-VOL؛ تحرک قیمت و حرکت امروز K-TREND/K-LOC. نمادی با «۶ از ۸» ممکن است فقط ۳ شاهد مستقل داشته باشد.

### ۴-ب. سنجش تجربی (بخشی از MI-04 در `research_plan`)

روی مجموعه داده پایان روز (پس از DL-03b):
1. ماتریس هم‌بستگی **Spearman درون‌مقطعی** هر روز، سپس میانه و صدک‌های ۵ و ۹۵ در طول زمان (هم‌بستگی تجمیعی سری‌های زمانی گمراه‌کننده است، چون سطح بازار مشترک است).
2. خوشه‌بندی سلسله‌مراتبی روی `1 − |ρ|` میانه؛ مرز ادغام `|ρ| ≥ 0.7`.
3. VIF درون هر مدل خطی (مرز ۵).
4. پایداری خوشه‌ها به تفکیک رژیم و اندازه نماد.
5. خروجی: جدول نهایی خوشه‌ها که جای جدول ساختاری بالا را می‌گیرد، و انتخاب **یک نماینده** برای هر خوشه در `signal_confluence`.

تا این سنجش، جدول ۴-الف **فرضیه** است.

## ۵. خلأهای داده که پژوهش را محدود می‌کنند

| خلأ | اثر | راه حل |
| --- | --- | --- |
| تاریخچه درون‌روزی (دفتر، جریان بازه‌ای، سرانه درون‌روزی) نداریم | خانواده‌های پول داغ، تابلوکستیک، منفی به مثبت درون‌روزی، صف‌ها **فقط رو به جلو** پژوهش‌پذیرند | ضبط روزانه R-01 از همین حالا؛ هر روز ضبط‌نشده برای همیشه از دست می‌رود |
| تاریخچه دامنه مجاز و حجم مبنا | واقع‌گرایی صف با جانشین D5 | DL-03d |
| عضویت صنعت نقطه‌ای در زمان | چرخش صنعت بدون سوگیری | نگاشت نسخه‌دار گروه محرک (D7) |
| نمادهای حذف‌شده | سوگیری بقا | Q-SG11 |
| رویدادهای شرکتی با زمان اعلام | وتوی مجمع و افزایش سرمایه | Q-SG4 |
| شناوری تاریخی | U3 و M1 فقط تولید | — |
| تاریخچه شاخص کل و هم‌وزن رسمی | مقایسه با شاخص رسمی | شاخص هم‌وزن جهان خودمان (R3) جایگزین است |
| نرخ ارز و بازده اوراق | R5 و رژیم کلان | منبع ندارد |
