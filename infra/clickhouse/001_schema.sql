-- Canonical storage. Money in rial (Int64), volumes/counts Int64. Times in UTC (DateTime64(3, 'UTC')).
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
    missing Array(LowCardinality(String))
)
ENGINE = ReplacingMergeTree(ingest_time)
PARTITION BY toYYYYMMDD(source_time)
ORDER BY (ins_code, source_time, source);

CREATE TABLE IF NOT EXISTS market.flow_events
(
    ins_code LowCardinality(String), symbol LowCardinality(String),
    side Enum8('buy' = 1, 'sell' = 2),
    band Enum8('hot' = 1, 'hot_plus' = 2, 'retail' = 3),
    attribution Enum8('attributed' = 1, 'unattributed' = 2),
    interval_from DateTime64(3, 'UTC'), interval_to DateTime64(3, 'UTC'),
    volume Int64, value Int64, participants Int64, avg_ticket Int64, vwap Int64, price_last Int64
)
ENGINE = MergeTree
PARTITION BY toYYYYMMDD(interval_to)
ORDER BY (ins_code, interval_to, side);

CREATE TABLE IF NOT EXISTS market.flow_10m
(
    ins_code LowCardinality(String), window_start DateTime64(0, 'UTC'),
    net_hot Int64, price_open Int64, price_last Int64,
    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY toYYYYMMDD(window_start)
ORDER BY (ins_code, window_start);

CREATE TABLE IF NOT EXISTS market.quality_issues
(
    ins_code LowCardinality(String), code LowCardinality(String), detail String, at DateTime64(3, 'UTC')
)
ENGINE = MergeTree
PARTITION BY toYYYYMMDD(at)
ORDER BY (code, at);
