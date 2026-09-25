# نگاشت فیلدهای سورس‌آرنا

منبع: نمونه خروجی مستند اندپوینت «اطلاعات همه نمادها» در مستندات عمومی سورس‌آرنا. ستون «تأیید» یعنی کلید در مستندات عمومی دیده شد؛ «تأییدنشده» یعنی نام کلید حدس است و باید با پاسخ زنده بررسی شود (وظیفه D-03).

| فیلد استاندارد | کلید منبع | تأیید |
| --- | --- | --- |
| ins_code | instance_code | ✓ |
| symbol | name | ✓ |
| price_last (آخرین) | close_price | ✓ (در نمونه مستند، تغییر ۵٪ برای آخرین در برابر ۰.۳۹٪ برای پایانی) |
| price_close (پایانی) | final_price | ✓ |
| price_first، price_max، price_min | first_price، highest_price، lowest_price | ✓ |
| trade_count، volume، value | trade_number، trade_volume، trade_value | ✓ |
| ind_buy_vol، inst_buy_vol، ind_sell_vol، inst_sell_vol | real_buy_volume، co_buy_volume، real_sell_volume، co_sell_volume | ✓ |
| ind_buy_count | real_buy_count | ✓ |
| ind_sell_count | real_sell_count | ✗ تأییدنشده — **لازم برای پول داغ فروش** |
| inst_buy_count، inst_sell_count | co_buy_count، co_sell_count | ✗ تأییدنشده |
| price_yesterday | yesterday_price | ✗ تأییدنشده |
| source_time | — | در نمونه مستند نیست؛ زمان دریافت استفاده و علامت‌گذاری می‌شود |
| book (پنج سطح) | — | در این اندپوینت نیست؛ منبع جدا لازم است |

تا تأیید `real_sell_count`، موتور برای داده این منبع هیچ شاخص جریان پولی تولید نمی‌کند (قاعده INCOMPLETE). این رفتار عمدی است.
