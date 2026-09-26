# مقایسه فروشندگان داده: BrsApi و سورس‌آرنا

خروجی `go run ./cmd/vendorcmp` (فقط محلی). این گزارش فقط آمار و چند مقدار نمونه دارد: هیچ کلید، توکن یا خروجی خامی در آن نیست. این **هم‌خوانی دو خوراک** است (که احتمالاً هر دو از TSETMC می‌آیند)، نه اثبات درستی داده، و فقط برای همین دو پاسخ معتبر است.

| | BrsApi (`AllSymbols.php?type=1`) | سورس‌آرنا (`all&type=0`) |
| --- | --- | --- |
| زمان فایل یا دریافت | فایل ذخیره‌شده، زمان فایل 2026-09-25 20:18 UTC | فایل ذخیره‌شده، زمان فایل 2026-09-26 03:27 UTC |
| آخرین زمان درون داده | 18:46:12 (`time`، بدون تاریخ) | 1405/7/1 17:59:43 (`last_trade_date/time`) |
| تعداد ردیف | 1608 | 1102 |

**زمینه:** هر دو فایل داده پایانی روز معاملاتی ۱۴۰۵/۰۷/۰۱ (۲۰۲۶-۰۹-۲۳) هستند و در بازار بسته گرفته شده‌اند (BrsApi: جمعه ۲۳:۴۸ تهران؛ سورس‌آرنا: شنبه ۰۷:۰۰ تهران)، پس دو خوراک در یک لحظه مقایسه می‌شوند. در بازار باز، سورس‌آرنا در دو لحظه گشایش ۱۴۰۵/۰۷/۰۴ عقب‌تر بود ولی اندازه تأخیرش اندازه‌گیری نشده است (`docs/source-mapping.md`). کلاس صندوق‌ها از نام به دست می‌آید و نام کامل صندوق در دو فروشنده فرق دارد (`l30` در برابر `full_name`)، پس نگاشت کلاس باید با `ins_code` ذخیره شود، نه با نام.

**پیوند ردیف‌ها:** 1101 ردیف مشترک (1101 با کد داخلی TSETMC و 0 با نماد، وقتی یک طرف کد نداشت). 507 ردیف فقط در BrsApi و 1 ردیف فقط در سورس‌آرنا. کد تکراری: BrsApi 0 و سورس‌آرنا 0. نماد تکراری (پس از یکسان‌سازی): BrsApi 0 و سورس‌آرنا 0.

## تعداد ردیف به تفکیک کلاس (قاعده پیشنهادی `docs/source-mapping.md`)

«مشترک هم‌کلاس» یعنی ردیف‌های مشترکی که هر دو فروشنده در همین کلاس می‌گذارند. کلاس صندوق‌ها از نام است و نام دو فروشنده فرق دارد.

| کلاس | BrsApi | سورس‌آرنا | مشترک هم‌کلاس |
| --- | ---: | ---: | ---: |
| سهام بورس | 363 | 363 | 363 |
| سهام فرابورس | 233 | 233 | 233 |
| تابلو غیرعادی (…0003) | 384 | 1 | 1 |
| بازار پایه/دیگر | 153 | 153 | 153 |
| صندوق سهامی | 159 | 126 | 126 |
| صندوق درآمد ثابت | 92 | 92 | 92 |
| صندوق طلا/کالا | 50 | 50 | 50 |
| تابلو غیرعادی (…0002) | 95 | 0 | 0 |
| سایر صندوق‌ها | 30 | 63 | 30 |
| انرژی (IRE9) | 18 | 7 | 7 |
| حق تقدم | 11 | 11 | 11 |
| تابلو غیرعادی (…0004) | 18 | 0 | 0 |
| صندوق نقره | 2 | 2 | 2 |
| اوراق | 0 | 1 | 0 |

ردیف‌های مشترک با کلاس متفاوت (BrsApi → سورس‌آرنا): صندوق سهامی → سایر صندوق‌ها: 33.

## مقایسه فیلدها روی ردیف‌های مشترک

هر ردیف مشترک در یکی از این ستون‌ها شمرده می‌شود: «تطابق» (برابری دقیق؛ متن پس از یکسان‌سازی ی/ک و فاصله)، «ناهمخوان» (دو مقدار متفاوت، یا مقدار غیرقابل‌تجزیه کنار مقدار سالم)، «فقط یک طرف»، «هیچ‌کدام» (نبود، null، "" یا «-» در هر دو) و «هر دو نامعتبر». «تطابق غیرصفر» تطابق‌های صفر را کنار می‌گذارد، چون صفر برای هر دو فروشنده «هیچ» هم هست (سطح خالی دفتر، نماد بی‌معامله). نرخ = تطابق ÷ ردیف‌هایی که هر دو مقدار سالم دارند.

| گروه | فیلد | کلید BrsApi | کلید سورس‌آرنا | هر دو سالم | تطابق | نرخ | تطابق غیرصفر | ناهمخوان | فقط BrsApi | فقط سورس‌آرنا | هیچ‌کدام | نامعتبر B/S | قالب BrsApi | قالب سورس‌آرنا | نمونه ناهمخوانی (BrsApi ≠ سورس‌آرنا) |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- |
| شناسه | symbol | `l18` | `name` | 1101 | 1101 | 100.0٪ | 1101 | 0 | 0 | 0 | 0 | 0/0 | text ×1101 | text ×1101 |  |
| شناسه | isin | `isin` | `namad_code` | 1101 | 1101 | 100.0٪ | 1101 | 0 | 0 | 0 | 0 | 0/0 | text ×1101 | text ×1101 |  |
| شناسه | sector_code | `cs_id` | `industry_code` | 1101 | 1101 | 100.0٪ | 1101 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| قیمت | price_last | `pl` | `close_price` | 1101 | 1101 | 100.0٪ | 1101 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| قیمت | price_close | `pc` | `final_price` | 1101 | 1101 | 100.0٪ | 1101 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| قیمت | price_first | `pf` | `first_price` | 1101 | 1101 | 100.0٪ | 1091 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| قیمت | price_yesterday | `py` | `yesterday_price` | 1101 | 1101 | 100.0٪ | 1101 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| قیمت | price_min | `pmin` | `lowest_price` | 1101 | 1101 | 100.0٪ | 1090 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| قیمت | price_max | `pmax` | `highest_price` | 1101 | 1101 | 100.0٪ | 1090 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دامنه | price_limit_min | `tmin` | `daily_price_low` | 1101 | 1101 | 100.0٪ | 1101 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | decimal string ×1101 |  |
| دامنه | price_limit_max | `tmax` | `daily_price_high` | 1101 | 1101 | 100.0٪ | 1101 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | decimal string ×1101 |  |
| جمع روز | trade_count | `tno` | `trade_number` | 1101 | 1101 | 100.0٪ | 1090 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| جمع روز | volume | `tvol` | `trade_volume` | 1101 | 1101 | 100.0٪ | 1090 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| جمع روز | value | `tval` | `trade_value` | 1101 | 1101 | 100.0٪ | 1090 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| حقیقی/حقوقی | ind_buy_vol | `Buy_I_Volume` | `real_buy_volume` | 1101 | 1101 | 100.0٪ | 1086 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| حقیقی/حقوقی | inst_buy_vol | `Buy_N_Volume` | `co_buy_volume` | 1101 | 1101 | 100.0٪ | 842 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| حقیقی/حقوقی | ind_sell_vol | `Sell_I_Volume` | `real_sell_volume` | 1101 | 1101 | 100.0٪ | 1080 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| حقیقی/حقوقی | inst_sell_vol | `Sell_N_Volume` | `co_sell_volume` | 1101 | 1101 | 100.0٪ | 898 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| حقیقی/حقوقی | ind_buy_count | `Buy_CountI` | `real_buy_count` | 1101 | 1101 | 100.0٪ | 1087 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| حقیقی/حقوقی | inst_buy_count | `Buy_CountN` | `co_buy_count` | 1101 | 1101 | 100.0٪ | 842 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| حقیقی/حقوقی | ind_sell_count | `Sell_CountI` | `real_sell_count` | 1101 | 1101 | 100.0٪ | 1081 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| حقیقی/حقوقی | inst_sell_count | `Sell_CountN` | `co_sell_count` | 1101 | 1101 | 100.0٪ | 898 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid1_price | `pd1` | `1_buy_price` | 1101 | 1101 | 100.0٪ | 1049 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid1_vol | `qd1` | `1_buy_volume` | 1101 | 1101 | 100.0٪ | 1049 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid1_count | `zd1` | `1_buy_count` | 1101 | 1101 | 100.0٪ | 1049 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask1_price | `po1` | `1_sell_price` | 1101 | 1101 | 100.0٪ | 956 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask1_vol | `qo1` | `1_sell_volume` | 1101 | 1101 | 100.0٪ | 956 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask1_count | `zo1` | `1_sell_count` | 1101 | 1101 | 100.0٪ | 956 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid2_price | `pd2` | `2_buy_price` | 1101 | 1101 | 100.0٪ | 996 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid2_vol | `qd2` | `2_buy_volume` | 1101 | 1101 | 100.0٪ | 996 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid2_count | `zd2` | `2_buy_count` | 1101 | 1101 | 100.0٪ | 996 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask2_price | `po2` | `2_sell_price` | 1101 | 1101 | 100.0٪ | 909 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask2_vol | `qo2` | `2_sell_volume` | 1101 | 1101 | 100.0٪ | 909 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask2_count | `zo2` | `2_sell_count` | 1101 | 1101 | 100.0٪ | 909 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid3_price | `pd3` | `3_buy_price` | 1101 | 1101 | 100.0٪ | 959 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid3_vol | `qd3` | `3_buy_volume` | 1101 | 1101 | 100.0٪ | 959 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid3_count | `zd3` | `3_buy_count` | 1101 | 1101 | 100.0٪ | 959 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask3_price | `po3` | `3_sell_price` | 1101 | 1101 | 100.0٪ | 880 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask3_vol | `qo3` | `3_sell_volume` | 1101 | 1101 | 100.0٪ | 880 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask3_count | `zo3` | `3_sell_count` | 1101 | 1101 | 100.0٪ | 880 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid4_price | `pd4` | `4_buy_price` | 1101 | 1101 | 100.0٪ | 920 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid4_vol | `qd4` | `4_buy_volume` | 1101 | 1101 | 100.0٪ | 920 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid4_count | `zd4` | `4_buy_count` | 1101 | 1101 | 100.0٪ | 920 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask4_price | `po4` | `4_sell_price` | 1101 | 1101 | 100.0٪ | 828 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask4_vol | `qo4` | `4_sell_volume` | 1100 | 1100 | 100.0٪ | 828 | 0 | 1 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1100، empty string ×1 |  |
| دفتر | ask4_count | `zo4` | `4_sell_count` | 1101 | 1101 | 100.0٪ | 828 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid5_price | `pd5` | `5_buy_price` | 1101 | 1101 | 100.0٪ | 891 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid5_vol | `qd5` | `5_buy_volume` | 1101 | 1101 | 100.0٪ | 891 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | bid5_count | `zd5` | `5_buy_count` | 1101 | 1101 | 100.0٪ | 891 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask5_price | `po5` | `5_sell_price` | 1101 | 1101 | 100.0٪ | 789 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| دفتر | ask5_vol | `qo5` | `5_sell_volume` | 1100 | 1100 | 100.0٪ | 789 | 0 | 1 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1100، empty string ×1 |  |
| دفتر | ask5_count | `zo5` | `5_sell_count` | 1101 | 1101 | 100.0٪ | 789 | 0 | 0 | 0 | 0 | 0/0 | integer number ×1101 | numeric string ×1101 |  |
| سایر | market_value | `mv` | `market_value` | 1101 | 1100 | 99.9٪ | 1100 | 1 | 0 | 0 | 0 | 0/0 | integer number ×1101 | integer number ×1101 | هامون: 29910000000000 ≠ 20937000000000 |
| سایر | eps | `eps` | `eps` | 747 | 747 | 100.0٪ | 746 | 0 | 21 | 2 | 331 | 0/0 | integer number ×768، null ×333 | numeric string ×749، empty string ×352 |  |

## فیلدهایی که فقط یکی از فروشنده‌ها دارد

- **فقط BrsApi:** `bvol`، `cs`، `l30`، `pcc`، `pcp`، `pe`، `plc`، `plp`، `time`، `z`
- **فقط سورس‌آرنا:** `P:E`، `all_stocks`، `avg_month`، `basis_volume`، `close_price_change`، `close_price_change_percent`، `co_buy_value`، `co_sell_value`، `final_price_change`، `final_price_change_percent`، `free_float`، `full_name`، `industry`، `last_trade_date`، `last_trade_time`، `market`، `real_buy_value`، `real_sell_value`، `state`

## ردیف‌های بی‌جفت (نمونه)

- **فقط BrsApi** (507): تابان3، ومعادن3، وبملت3، فن افزار3، فزر3، شپنا3، پاکشو3، شستا3، ذوب3، کیان2، منجیل3، خگستر3 — به تفکیک کلاس: انرژی (IRE9): 11؛ تابلو غیرعادی (…0002): 95؛ تابلو غیرعادی (…0003): 383؛ تابلو غیرعادی (…0004): 18
- **فقط سورس‌آرنا** (1): گواهی ظرفیت — به تفکیک کلاس: اوراق: 1
