//go:build ignore

package main

import (
	"flag"
	"time"

	odb "github.com/TreyVanderpool/oliver-golib/db"
	oinit "github.com/TreyVanderpool/oliver-golib/init"
	ol "github.com/TreyVanderpool/oliver-golib/logging"
	osch "github.com/TreyVanderpool/oliver-golib/schwab"
	osql "github.com/TreyVanderpool/oliver-golib/sql"
	ou "github.com/TreyVanderpool/oliver-golib/utils"
)

var (
  Log          ol.ILogger
  DB           *odb.DB
  Schwab       *osch.SCHWAB
  SQLs         osql.SQLs
  gsCurrDate   string = time.Now().Format( ou.YYYY_MM_DD )
)

//------------------------------------------------------------------------------
// Function: main
//------------------------------------------------------------------------------
func main() {
  lsDBName := flag.String( "db", "stocks_test", "Database name" )
	lsLogLevel := flag.String( "lvl", "info", "Log level (debug, info, warn, error)" )
  lsOrdersDate := flag.String( "od", time.Now().Format( ou.YYYY_MM_DD ), "Orders date to retrieve (YYYY-MM-DD)" )
	flag.Parse()

  Log = oinit.Init( oinit.INIT_LOG, lsLogLevel ).(ol.ILogger)
  Log.SetPatterns( "%M\n", "%D %-5L %T:%F:%# %M\n" )
  DB = oinit.Init( oinit.INIT_DB, lsDBName ).(*odb.DB)
  Schwab = oinit.Init( oinit.INIT_SCHWAB, Log, DB ).(*osch.SCHWAB)
  SQLs = oinit.Init( oinit.INIT_SQLS, Log, DB ).(osql.SQLs)

  lcAccts, err := Schwab.GetAllAccounts()

  if err != nil {
    Log.Error( "Error getting accounts: %s", err )
    return
  }

  // Loop through all the accounts and retrieve the Account Info which contains
  // the current and initial balances for the day. Insert the balances into the database.
  for _, lAcct := range lcAccts {
    _ProcessBalances( lAcct )
  }

  lcStartDate, err := time.Parse( ou.TIMESTAMPFORMAT, *lsOrdersDate + " 00:00:00" )

  if err != nil {
    Log.Error( "Error parsing orders date: %s", err )
    return
  }

  lcEndDate, _ := time.Parse( ou.TIMESTAMPFORMAT, *lsOrdersDate + " 23:59:59" )

  // Walk through each of the accounts and process all their orders for the day.
  for _, lAcct := range lcAccts {
    _ProcessOrders( lAcct, &lcStartDate, &lcEndDate )
  }

  // Walk through each of the accounts and process all their transactions for the day.
  for _, lAcct := range lcAccts {
    _ProcessTransactions( lAcct, &lcStartDate, &lcEndDate )
  }
}

//-------------------------------------------------------------
// Function: _ProcessBalances
//-------------------------------------------------------------
func _ProcessBalances( acAcct osch.AcctInfo ) {
  Log.Info( "Retrieving balances for account *****%s : %s", acAcct.AccountNbr[len(acAcct.AccountNbr)-3:], acAcct.Owners )

  Schwab.SetAccountNbr( acAcct.AccountNbr )
  lcAcctInfo, err := Schwab.GetAccountInfo()

  if err != nil {
    Log.Exception( err )
    return
  }

  // Save the full account info data structure from Schwab
  err = SQLs.I_DailyBalances( gsCurrDate, acAcct.AccountNbr, string(Schwab.HTTP.ResponseBody) )

  if err != nil {
    Log.Exception( err )
  }

  // Walk through the data structure and extract indivual values and total cash values
  lcDV := &osql.DailyValues{}
  lcDV.TranDate = gsCurrDate
  lcDV.AccountNbr = acAcct.AccountNbr
  lcDV.TypeText = "value"

  if lcAcctInfo.CurrBalance.CashBalance != 0 {
    lcDV.Symbol = "..cash"
    lcDV.TotalValue = lcAcctInfo.CurrBalance.CashBalance
    lcDV.TodaysGainLoss = lcAcctInfo.CurrBalance.CashBalance - lcAcctInfo.InitBalance.CashBalance
    if lcDV.TodaysGainLoss != 0 {
      lcDV.TodaysPctChg = lcDV.TodaysGainLoss / lcAcctInfo.CurrBalance.CashBalance
    }
    err = SQLs.I_DailyValues( lcDV )
    if err != nil { Log.Exception( err ) }
    lcDV.TodaysGainLoss = 0
    lcDV.TodaysPctChg = 0
  }

  if lcAcctInfo.CurrBalance.LongMarketValue != 0 {
    lcDV.Symbol = "..longvalue"
    lcDV.TotalValue = lcAcctInfo.CurrBalance.LongMarketValue
    lcDV.TodaysGainLoss = lcAcctInfo.CurrBalance.LongStockValue - lcAcctInfo.InitBalance.LongMarketValue
    if lcDV.TodaysGainLoss != 0 {
      lcDV.TodaysPctChg = lcDV.TodaysGainLoss / lcAcctInfo.CurrBalance.CashBalance
    }
    err = SQLs.I_DailyValues( lcDV )
    if err != nil { Log.Exception( err ) }
    lcDV.TodaysGainLoss = 0
    lcDV.TodaysPctChg = 0
  }

  if lcAcctInfo.CurrBalance.MutualFundValue != 0 {
    lcDV.Symbol = "..mutualvalue"
    lcDV.TotalValue = lcAcctInfo.CurrBalance.MutualFundValue
    lcDV.TodaysGainLoss = lcAcctInfo.CurrBalance.MutualFundValue - lcAcctInfo.InitBalance.MutualFundValue
    if lcDV.TodaysGainLoss != 0 {
      lcDV.TodaysPctChg = lcDV.TodaysGainLoss / lcAcctInfo.CurrBalance.CashBalance
    }
    err = SQLs.I_DailyValues( lcDV )
    if err != nil { Log.Exception( err ) }
    lcDV.TodaysGainLoss = 0
    lcDV.TodaysPctChg = 0
  }

  if lcAcctInfo.CurrBalance.PendingDeposits != 0 {
    lcDV.Symbol = "..pendingdeposit"
    lcDV.TotalValue = lcAcctInfo.CurrBalance.PendingDeposits
    err = SQLs.I_DailyValues( lcDV )
    if err != nil { Log.Exception( err ) }
  }

  if lcAcctInfo.CurrBalance.TotalCash != 0 {
    lcDV.Symbol = "..totalcash"
    lcDV.TotalValue = lcAcctInfo.CurrBalance.TotalCash
    err = SQLs.I_DailyValues( lcDV )
    if err != nil { Log.Exception( err ) }
  }

  for _, lPos := range lcAcctInfo.Positions {
    _ProcessPositions( lPos, lcDV.AccountNbr )
  }
}

//-------------------------------------------------------------
// Function: _ProcessPositions
//-------------------------------------------------------------
func _ProcessPositions( acPosition osch.Position, asAcctNbr string ) {
  lcDV := &osql.DailyValues{}
  lcDV.TranDate = gsCurrDate
  lcDV.AccountNbr = asAcctNbr
  lcDV.TypeText = "position"
  lcDV.Symbol = acPosition.Instrument.Symbol
  
  if acPosition.ShortQuantity != 0 {
    if acPosition.SettledShortQuantity < 0 {
      // This must be a covered call position, nothing to post as values here.
      // This should have been posted as a type=cov_call when originally executed.
      return
    }
    lcDV.Shares = acPosition.ShortQuantity
  } else {
    lcDV.Shares = acPosition.LongQuantity
    lcDV.TodaysGainLoss = acPosition.CurrDayProfitLoss
    lcDV.TodaysPctChg = acPosition.CurrDayProfitLossPct
    lcDV.OverallGainLoss = acPosition.LongOpenProfitLoss
    lcDV.OverallPctChg = ou.PctChg( acPosition.MarketValue, lcDV.Shares * acPosition.AveragePrice )
  }

  err := SQLs.I_DailyValues( lcDV )
  if err != nil { Log.Exception( err ) }
}

//-------------------------------------------------------------
// Function: _ProcessOrders
//-------------------------------------------------------------
func _ProcessOrders( acAcct osch.AcctInfo, acStartDate, acEndDate *time.Time ) {
  Log.Info( "Retrieving orders on %s for account *****%s : %s", acStartDate.Format( ou.YYYY_MM_DD ), acAcct.AccountNbr[len(acAcct.AccountNbr)-3:], acAcct.Owners )

  Schwab.SetAccountNbr( acAcct.AccountNbr )
  lcOrders, err := Schwab.GetOrders( acStartDate, acEndDate, "" )

  if err != nil {
    Log.Exception( err )
    return
  }

  lcOrders = osch.FlattenOrders( lcOrders )

  for _, lOrder := range lcOrders {
    lcDV := &osql.DailyValues{}
    lcDV.TranDate = lOrder.CloseTime[0:10]
    lcDV.AccountNbr = acAcct.AccountNbr
    lcDV.OrderId = lOrder.OrderId

    for _, lLeg := range lOrder.OrderLegCollection {
      lcDV.Symbol = lLeg.Instrument.Symbol
      lcDV.Instruction = lLeg.Instruction
      if lLeg.Instruction == "BUY" {
        lcDV.TypeText = "buy"
      } else if lLeg.Instruction == "SELL" {
        lcDV.TypeText = "sell"
      }
      if lLeg.OrderLegType == "OPTION" && lLeg.Instruction == "SELL_TO_OPEN" {
        lcDV.TypeText = "cov_call"
      }
    }

    for _, lOAC := range lOrder.OrderActivityCollection {
      _, lcDV.Shares, lcDV.PurchasePrice = lOAC.GetPriceAndQty()
    }

    Log.Info( "Order: %d : %-20s : %s -- Qty: %9.2f  Price: %9.2f : %-10s : %s",
              lcDV.OrderId, lcDV.Symbol, lcDV.TranDate, lcDV.Shares, lcDV.PurchasePrice, lcDV.TypeText, lcDV.Instruction )

    err = SQLs.I_DailyValues( lcDV )

    if err != nil {
      Log.Exception( err )
      continue
    }
  }
}

//-------------------------------------------------------------
// Function: _ProcessTransactions
//-------------------------------------------------------------
func _ProcessTransactions( acAcct osch.AcctInfo, acStartDate, acEndDate *time.Time ) {
  Log.Info( "Retrieving transactions   on %s for account *****%s : %s", acStartDate.Format( ou.YYYY_MM_DD ), acAcct.AccountNbr[len(acAcct.AccountNbr)-3:], acAcct.Owners )

  Schwab.SetAccountNbr( acAcct.AccountNbr )

  for _, lType := range []string{"TRADE","RECEIVE_AND_DELIVER","DIVIDEND_OR_INTEREST","ACH_RECEIPT","ACH_DISBURSEMENT","CASH_RECEIPT","CASH_DISBURSEMENT","ELECTRONIC_FUND","WIRE_OUT","WIRE_IN","JOURNAL","MEMORANDUM","MARGIN_CALL","MONEY_MARKET","SMA_ADJUSTMENT"} {
    lcTrans, err := Schwab.GetTransactions( acStartDate, acEndDate, "", lType )

    if err != nil {
      Log.Error( "Error getting transactions for %s", lType )
      Log.Exception( err )
      Log.Error( "%s : %d : %s", lType, len(lcTrans), string(Schwab.HTTP.ResponseBody) )
      continue
    }

    if( Log.IsDebug() ) {
      Log.Debug( "%s : %d : %s", lType, len(lcTrans), string(Schwab.HTTP.ResponseBody) )
    }

    if len(lcTrans) > 0 {
      Log.Info( "  -- %d transactions for %s", len(lcTrans), lType )

      // Save the full transaction info data structure from Schwab
      err = SQLs.I_DailyTransactions( gsCurrDate, acAcct.AccountNbr, lType, string(Schwab.HTTP.ResponseBody) )

      if err != nil {
        Log.Exception( err )
      }
    }
  }
}