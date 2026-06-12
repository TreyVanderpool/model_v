//go:build ignore

package main

import (
  "flag"
  "time"
  "encoding/json"

  odb "github.com/TreyVanderpool/oliver-golib/db"
  oinit "github.com/TreyVanderpool/oliver-golib/init"
  ol "github.com/TreyVanderpool/oliver-golib/logging"
  osch "github.com/TreyVanderpool/oliver-golib/schwab"
  osql "github.com/TreyVanderpool/oliver-golib/sql"
  ou "github.com/TreyVanderpool/oliver-golib/utils"
)

const (
  MODEL_VERSION          string = "v"
)

var (
  Log                 ol.ILogger
  DB                  *odb.DB
  Schwab              *osch.SCHWAB
  SQLs                osql.SQLs
  gsCurrDate          string = time.Now().Format( ou.YYYY_MM_DD )
  gbSendText          *bool
  // gcFont       *oimg.Font
  TRANSACTION_TYPES   []string = []string{"TRADE","RECEIVE_AND_DELIVER","DIVIDEND_OR_INTEREST","ACH_RECEIPT","ACH_DISBURSEMENT","CASH_RECEIPT","CASH_DISBURSEMENT","ELECTRONIC_FUND","WIRE_OUT","WIRE_IN","JOURNAL","MEMORANDUM","MARGIN_CALL","MONEY_MARKET","SMA_ADJUSTMENT"}
)

type TAG struct {
  ol.ILogTag
  PgmName               string
}
func (t TAG) GetTag() (string) {
  return t.PgmName
}

//------------------------------------------------------------------------------
// Function: main
//------------------------------------------------------------------------------
func main() {
  lsDBName := flag.String( "db", "stocks_test", "Database name" )
  lsLogLevel := flag.String( "lvl", "info", "Log level (debug, info, warn, error)" )
  lsTranStartDate := flag.String( "ts", time.Now().Format( ou.YYYY_MM_DD ), "Orders date to retrieve (YYYY-MM-DD)" )
  lsTranEndDate := flag.String( "te", time.Now().Format( ou.YYYY_MM_DD ), "Orders date to retrieve (YYYY-MM-DD)" )
  // gbSendText = flag.Bool( "sendtext", false, "send text" )
  flag.Parse()

  Log = oinit.Init( oinit.INIT_LOG, lsLogLevel ).(ol.ILogger)
  Log.SetPatterns( "%M\n", "%D %-5L %T:%-20.20F:%# %M\n" )
  Log.SetTag( TAG{ PgmName: "dlytran" } )
  DB = oinit.Init( oinit.INIT_DB, lsDBName ).(*odb.DB)
  Schwab = oinit.Init( oinit.INIT_SCHWAB, Log, DB ).(*osch.SCHWAB)
  SQLs = oinit.Init( oinit.INIT_SQLS, Log, DB ).(osql.SQLs)

  lcAccts, err := Schwab.GetAllAccounts()

  if err != nil {
    Log.Error( "Error getting accounts: %s", err )
    return
  }

  lcStartDate, err := time.Parse( ou.TIMESTAMPFORMAT, *lsTranStartDate + " 00:00:00" )

  if err != nil {
    Log.Error( "Error parsing transaction {ts} date: %s", err )
    return
  }

  lcEndDate, _ := time.Parse( ou.TIMESTAMPFORMAT, *lsTranEndDate + " 23:59:59" )

  // Walk through each of the accounts and process all their transactions for the day.
  for _, lAcct := range lcAccts {
    _ProcessTransactions( lAcct, &lcStartDate, &lcEndDate )
  }
}

//-------------------------------------------------------------
// Function: _ProcessTransactions
//-------------------------------------------------------------
func _ProcessTransactions( acAcct osch.AcctInfo, acStartDate, acEndDate *time.Time ) {
  Log.Info( "Retrieving transactions   on %s for account %s : %s", acStartDate.Format( ou.YYYY_MM_DD ), acAcct.GetMaskedNbr(), acAcct.Owners )

  Schwab.SetAccountNbr( acAcct.AccountNbr )

  // Remove any existing entries incase they've been deleted...
  err := SQLs.D_DailyTransactionsDateRange( acAcct.AccountNbr, acStartDate.Format( ou.YYYY_MM_DD ), acEndDate.Format( ou.YYYY_MM_DD ) )

  if err != nil {
    Log.Exception( err )
  }

  for _, lType := range TRANSACTION_TYPES {
    lcTrans, err := _GetTransactionsByDate( acAcct, acStartDate, acEndDate, lType )

    if err != nil {
      Log.Error( "Error getting transactions for %s", lType )
      Log.Exception( err )
      Log.Error( "%s : %d : %s", lType, len(lcTrans), string(Schwab.HTTP.ResponseBody) )
      continue
    }

    if len(lcTrans) > 0 {
      for lDate, lAcct := range lcTrans {
        Log.Info( "  -- %d transactions for %s : %s", len(lAcct), lDate, lType )

        lbData, err := json.Marshal( lAcct )

        // Save the full transaction info data structure from Schwab
        err = SQLs.I_DailyTransactions( lDate, acAcct.AccountNbr, lType, string(lbData) )

        if err != nil {
          Log.Exception( err )
        }
      }
    }
  }
}

//-------------------------------------------------------------
// Function: _ProcessTransactions
//-------------------------------------------------------------
func _GetTransactionsByDate( acAcct osch.AcctInfo, acStartDate, acEndDate *time.Time, asType string ) ( map[string][]osch.Activity, error ) {
  lcMap := make( map[string][]osch.Activity )

  lcTrans, err := Schwab.GetTransactions( acStartDate, acEndDate, "", asType )

  if err != nil {
    Log.Error( "Error getting transactions for %s", asType )
    Log.Exception( err )
    Log.Error( "%s : %d : %s", asType, len(lcTrans), string(Schwab.HTTP.ResponseBody) )
    return lcMap, err
  }

  if( Log.IsDebug() ) {
    Log.Debug( "%s : %d : %s", asType, len(lcTrans), string(Schwab.HTTP.ResponseBody) )
  }

  if len(lcTrans) == 0 { return lcMap, nil }

  for i, lTran := range lcTrans {
    lsDate := lTran.Time[0:10]
    lcAcct, lbFnd := lcMap[lsDate]

    if ! lbFnd {
      lcAcct = make( []osch.Activity, 0 )
    }

    lcAcct = append( lcAcct, lcTrans[i] )
    lcMap[lsDate] = lcAcct
  }

  return lcMap, nil
}