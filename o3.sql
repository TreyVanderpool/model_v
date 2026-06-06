select k.symbol,
       d.tran_time,
       k.expire_date as exp_date,
       k.strike_price as strike,
       v.expire_days as edays,
       v.offset_from_symbol as offset,
       d.symbol_buy as sym_buy,
       cast( d.symbol_buy * 100 as decimal(7,0) ) as sym_val,
       v.call_buy as cbuy,
       v.call_sell as csell,
       cast( ((v.call_buy + v.call_sell) / 2) as decimal(7,2) ) as callamt,
       cast( ((v.call_buy + v.call_sell) / 2) * 100 as decimal(7,0) ) as sto_amt,
       ( cast( ((v.call_buy + v.call_sell) / 2) * 100 as decimal(7,2) ) / cast( d.symbol_buy * 100 as decimal(7,0) ) ) * 100 as pct_sto,
       cast( ((v.call_buy + v.call_sell) / 2) * 100 as decimal(7,2) ) as value
from   option_keys k,
       option_dates d,
       option_values v
where  d.symbol = k.symbol
and    d.tran_date = '2026-06-02'
and    d.tran_time < '13:00:00'
and    v.opt_sym_id = k.opt_sym_id
and    v.opt_time_id = d.opt_time_id
and    v.expire_days between 2 and 5
and    v.offset_from_symbol between 4 and 4
order by pct_sto desc
