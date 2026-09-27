-- Canonical storage. Money in rial (Int64), volumes/counts Int64. Times in UTC (DateTime64(3, 'UTC')).
--
-- W-01: every table is written by cmd/writer and is IDEMPOTENT. A redelivered bus message, a crash
-- between the insert and the ack, or an engine republish after the 30-min JetStream dedup window
-- all produce rows with the same sorting key, which ReplacingMergeTree collapses. The version is
-- the bus stream sequence (bus_seq): the latest publication of a key wins, a stale redelivery never
-- overwrites a newer one. Collapsing happens at merge time: READ WITH `FINAL` (or argMax by
-- bus_seq) to see exactly one row per key.
--
-- `day` is the Tehran trading day. `class` is the instrument class from the session calendar
-- (`unknown` if unmapped; per-class aggregates exclude it). `partial` must reach every consumer
-- as «ناقص» (rule 1; contracts/subjects.md).
CREATE TABLE IF NOT EXISTS market.snapshots
(
    ins_code        LowCardinality(String),
    symbol          LowCardinality(String),
    source          LowCardinality(String),
    source_time     DateTime64(3, 'UTC'),
    ingest_time     DateTime64(3, 'UTC'),
    source_time_estimated Bool,
    price_last Int64, price_close Int64, price_first Int64, price_yesterday Int64, price_min Int64, price_max Int64,
    trade_count Int64, volume Int64, value Int64,
    ind_buy_vol Int64, ind_sell_vol Int64, inst_buy_vol Int64, inst_sell_vol Int64,
    ind_buy_count Int64, ind_sell_count Int64, inst_buy_count Int64, inst_sell_count Int64,
    missing Array(LowCardinality(String)),
    bus_seq UInt64
)
ENGINE = ReplacingMergeTree(bus_seq)
PARTITION BY toYYYYMMDD(source_time)
ORDER BY (ins_code, source_time, source);

-- One hot / hot-plus interval per instrument and side. Band and values may change if the engine
-- recomputes an interval (restart), so they are not in the key.
CREATE TABLE IF NOT EXISTS market.flow_events
(
    ins_code LowCardinality(String), symbol LowCardinality(String), class LowCardinality(String),
    side Enum8('buy' = 1, 'sell' = 2),
    band Enum8('hot' = 1, 'hot_plus' = 2, 'retail' = 3),
    attribution Enum8('attributed' = 1, 'unattributed' = 2),
    interval_from DateTime64(3, 'UTC'), interval_to DateTime64(3, 'UTC'),
    volume Int64, value Int64, participants Int64, avg_ticket Int64, vwap Int64, price_last Int64,
    bus_seq UInt64
)
ENGINE = ReplacingMergeTree(bus_seq)
PARTITION BY toYYYYMMDD(interval_to)
ORDER BY (ins_code, interval_to, side, interval_from);

-- The engine republishes a window as it fills: the latest publication is the window's value.
CREATE TABLE IF NOT EXISTS market.flow_10m
(
    ins_code LowCardinality(String), class LowCardinality(String), window_start DateTime64(0, 'UTC'),
    net_hot Int64, price_open Int64, price_last Int64,
    partial Bool,
    bus_seq UInt64,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(bus_seq)
PARTITION BY toYYYYMMDD(window_start)
ORDER BY (ins_code, window_start);

-- Market game: one row per instrument and day (the latest totals published that day).
CREATE TABLE IF NOT EXISTS market.game_totals
(
    ins_code LowCardinality(String), class LowCardinality(String), day Date,
    as_of DateTime64(3, 'UTC'), volume Int64,
    net_hot Int64, net_hot_plus Int64, net_retail Int64, net_unattributed Int64,
    partial Bool,
    bus_seq UInt64
)
ENGINE = ReplacingMergeTree(bus_seq)
PARTITION BY toYYYYMM(day)
ORDER BY (ins_code, day);

-- dedup_at/dedup_detail = at/detail, except for the once-per-instrument-and-day codes
-- (DAY_START_MISSED, PREV_DAY_CARRYOVER): the day's midnight (Tehran) and '', so a re-emission
-- (engine recovery) collapses onto (code, ins_code, day).
CREATE TABLE IF NOT EXISTS market.quality_issues
(
    ins_code LowCardinality(String), code LowCardinality(String), detail String, at DateTime64(3, 'UTC'),
    day Date,
    dedup_at DateTime64(3, 'UTC'), dedup_detail String,
    bus_seq UInt64
)
ENGINE = ReplacingMergeTree(bus_seq)
PARTITION BY toYYYYMM(day) -- by day, not at: rows collapse only within a partition
ORDER BY (code, ins_code, day, dedup_at, dedup_detail);
