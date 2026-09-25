import { describe, expect, it } from "vitest";
import { age, arrow, CLASS_LABEL, classFamily, clock, direction, jalaliDate, money, moneyUnit, num, pct, signedNum, UNAVAILABLE } from "./format";

describe("numbers", () => {
  it("Persian digits, «٬» thousands, «٫» decimal", () => {
    expect(num(3412850)).toBe("۳٬۴۱۲٬۸۵۰");
    expect(num(10.4, 1)).toBe("۱۰٫۴");
    expect(num(-5)).toBe("۵"); // sign is separate
  });
  it("sign and arrow", () => {
    expect(signedNum(-2.9, 1)).toBe("−۲٫۹");
    expect(signedNum(3.1, 1)).toBe("+۳٫۱");
    expect(signedNum(-0.04, 1)).toBe("۰٫۰"); // rounds to zero: no sign
    expect(pct(2.1)).toBe("+۲٫۱۰٪");
    expect(pct(-0.18)).toBe("−۰٫۱۸٪");
    expect(direction(2.1)).toBe("up");
    expect(direction(-0.004)).toBe("flat");
    expect(arrow("up")).toBe("▲");
    expect(arrow("down")).toBe("▼");
    expect(arrow("flat")).toBe("");
  });
});

describe("money (rial in, toman out)", () => {
  it("همت from 0.1 همت (1e12 rial), else م.ت", () => {
    expect(moneyUnit(1e12)).toBe("hemmat");
    expect(moneyUnit(9.99e11)).toBe("mt");
    expect(money(104e12)).toBe("۱۰٫۴ همت"); // 10.4e12 toman
    expect(money(-29e12, true)).toBe("−۲٫۹ همت");
    expect(money(31e12, true)).toBe("+۳٫۱ همت");
    expect(money(7_390_000_000)).toBe("۷۳۹ م.ت"); // 739 million toman
    expect(money(-420_000_000, true)).toBe("−۴۲ م.ت");
    expect(money(0, true)).toBe("۰ م.ت");
  });
});

describe("time", () => {
  it("Tehran clock and Jalali date", () => {
    const t = new Date("2026-09-27T08:48:42Z"); // 12:18:42 Tehran, 5 Mehr 1405
    expect(clock(t)).toBe("۱۲:۱۸:۴۲");
    expect(clock(t, false)).toBe("۱۲:۱۸");
    expect(jalaliDate(t)).toBe("یکشنبه ۵ مهر ۱۴۰۵");
  });
  it("data age", () => {
    expect(age(400)).toBe("هم‌اکنون");
    expect(age(-3000)).toBe("هم‌اکنون");
    expect(age(2500)).toBe("۲ ثانیه پیش");
    expect(age(125_000)).toBe("۲ دقیقه پیش");
    expect(age(3 * 3600_000)).toBe("۳ ساعت پیش");
  });
});

describe("labels", () => {
  it("unavailable text and class families", () => {
    expect(UNAVAILABLE).toBe("داده در دسترس نیست");
    expect(classFamily("equity_etf")).toBe("stock");
    expect(classFamily("other_fund")).toBe("stock");
    expect(classFamily("gold")).toBe("gold");
    expect(CLASS_LABEL.fixed_income).toBe("درآمد ثابت");
  });
});
