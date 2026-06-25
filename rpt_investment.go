//go:build ignore

package main

import (
	"flag"
	"fmt"

	ol "github.com/TreyVanderpool/oliver-golib/logging"

	odb "github.com/TreyVanderpool/oliver-golib/db"
	oinit "github.com/TreyVanderpool/oliver-golib/init"
	osch "github.com/TreyVanderpool/oliver-golib/schwab"
	osql "github.com/TreyVanderpool/oliver-golib/sql"
	orpt "github.com/TreyVanderpool/oliver-golib/report"
	// otxt "github.com/TreyVanderpool/oliver-golib/text"
	// ou "github.com/TreyVanderpool/oliver-golib/utils"
	// oimg "github.com/TreyVanderpool/oliver-golib/image"
)

const (
//   MODEL_VERSION          string = "v"
//   REPORT_NAME            string = "eod"
//   TEXT_MAX_LEN           float64 = 215
)

var (
  Log               ol.ILogger
  DB                *odb.DB
  Schwab            *osch.SCHWAB
  SQLs              osql.SQLs
  gcRPT             *orpt.RPT
  gcTracker         map[string]*_track = make( map[string]*_track )
  gcTotalCCAmt      float64
)

type _track struct {
  Symbol                string
  CCAmount              float64
  CCPlays               int
  Shares                float64
  EquityValue           float64
  EquityGL              float64
  Lots                  []_lot
}

type _lot struct {
  Shares                float64
  PurchasePrice         float64
}

//------------------------------------------------------------------------------
// Function: main
//------------------------------------------------------------------------------
func main() {
  lsDBName := flag.String( "db", "stocks_prod", "Database name" )
  lsAcctNbr := flag.String( "a", "", "account number" )
  lsLogLevel := flag.String( "lvl", "info", "logging level" )
  flag.Parse()

  // Init main components, Log, DB, Schwab, SQLs...
  Log = oinit.Init( oinit.INIT_LOG, lsLogLevel ).(ol.ILogger)
  Log.SetPatterns( "%M\n", "%D %-5L %T:%-20.20F:%# %M\n" )
  // Log.SetTag( TAG{ PgmName: "dlybal" } )
  DB = oinit.Init( oinit.INIT_DB, lsDBName ).(*odb.DB)
  Schwab = oinit.Init( oinit.INIT_SCHWAB, Log, DB ).(*osch.SCHWAB)
  SQLs = oinit.Init( oinit.INIT_SQLS, Log, DB ).(osql.SQLs)

  fmt.Printf( "Investment Report\n" )

  if *lsAcctNbr == "" {
    Schwab.SetAnyAccount()
    *lsAcctNbr = Schwab.GetAccountNbr()
  }

  lcValues, err := SQLs.S_DailyValuesAcctNbr( *lsAcctNbr )
  if err != nil {
    fmt.Printf( "ERROR: %s\n", err )
    return
  }

  if len(lcValues) == 0 {
    fmt.Printf( "No records found.\n" )
    return
  }

  _CreateRpt()
  lsHoldDate := lcValues[0].TranDate
  lcDayValues := make( []osql.DailyValues, 0 )

  for i, lV := range lcValues {
    if lV.TranDate == lsHoldDate {
      lcDayValues = append( lcDayValues, lcValues[i] )
      continue
    }
    liPrintedRows := _ReportInvestment( lcDayValues )
    lsHoldDate = lV.TranDate
    lcDayValues = make( []osql.DailyValues, 0 )
    lcDayValues = append( lcDayValues, lcValues[i] )
    if liPrintedRows > 0 {
      fmt.Printf( "\n" )
    }
  }

  _ReportInvestment( lcDayValues )
}

//------------------------------------------------------------------------------
// Function: _ReportInvestment
//------------------------------------------------------------------------------
func _ReportInvestment( acValues []osql.DailyValues ) ( int ) {
  liRows := 0
  var lcTracker   *_track

  for _, lV := range acValues {
    lfTotValue := 0.0
    switch lV.TypeText {
      case "position", "value", "expired":
        continue
      case "buy", "sell":
        lcTracker = _UpdateTracker( &lV )
        lfTotValue = lV.Shares * lV.PurchasePrice
      case "cov_call":
        lcTracker = _UpdateTracker( &lV )
        lfTotValue = (lV.Shares * 100) * lV.PurchasePrice
        gcTotalCCAmt += lfTotValue
      case "assigned":
        lcTracker = _UpdateTracker( &lV )
        lfTotValue = (lV.Shares * 100) * lV.PurchasePrice
      default:
        fmt.Printf( "*** Handle type: %s : %s : %s\n", lV.TypeText, lV.TranDate, lV.Symbol )
        continue
    }

    lsComment := ""

    if lcTracker.Shares == 0 { lsComment = "Closed equity" }

    gcRPT.PrintLine( lV.TranDate,
                     lV.Symbol,
                     lV.TypeText,
                     lV.Shares,
                     lV.PurchasePrice,
                     lfTotValue,
                     lcTracker.CCPlays,
                     lcTracker.CCAmount,
                     lcTracker.Shares,
                     lcTracker.EquityValue,
                     lcTracker.EquityGL,
                     gcTotalCCAmt,
                     lsComment )
    liRows++
  }

  return liRows
}

//------------------------------------------------------------------------------
// Function: _CreateRpt
//------------------------------------------------------------------------------
func _CreateRpt() {
	gcRPT = orpt.NewRPT()
  gcRPT.AddColumn( "Tran Date", "%s", 10, orpt.RPT_ALGN_LEFT )
	gcRPT.AddColumn( "Symbol", "%s", 21, orpt.RPT_ALGN_LEFT )
  gcRPT.AddColumn( "Activity", "%s", 10, orpt.RPT_ALGN_LEFT )
  gcRPT.AddColumn( "Shares", "%.0f", 6, orpt.RPT_ALGN_RIGHT ).SetCommas( true )
  gcRPT.AddColumn( "Price", "%.2f", 8, orpt.RPT_ALGN_RIGHT ).SetCommas( true )
  gcRPT.AddColumn( "Tot Value", "%.0f", 10, orpt.RPT_ALGN_RIGHT ).SetCommas( true )
  gcRPT.AddColumn( "CC Plays", "%d", 8, orpt.RPT_ALGN_RIGHT ).SetBWZ( true )
  gcRPT.AddColumn( "CC Amount", "%.0f", 9, orpt.RPT_ALGN_RIGHT ).SetCommas( true ).SetBWZ( true )
  gcRPT.AddColumn( "EquShares", "%.0f", 9, orpt.RPT_ALGN_RIGHT ).SetCommas( true ).SetBWZ( true )
  gcRPT.AddColumn( "Equ Value", "%.0f", 10, orpt.RPT_ALGN_RIGHT ).SetCommas( true )
  gcRPT.AddColumn( "EquityG/L", "%.0f", 9, orpt.RPT_ALGN_RIGHT ).SetCommas( true ).SetBWZ( true )
  gcRPT.AddColumn( "Total CC", "%.0f", 8, orpt.RPT_ALGN_RIGHT ).SetCommas( true ).SetBWZ( true )
  gcRPT.AddColumn( "Comments", "%s", 25, orpt.RPT_ALGN_LEFT )
}

//------------------------------------------------------------------------------
// Function: _UpdateTracker
//------------------------------------------------------------------------------
func _UpdateTracker( acValues *osql.DailyValues ) ( *_track ) {
  lsSymbol := acValues.Symbol

  if acValues.RootSymbol > "" { lsSymbol = acValues.RootSymbol }

  lcTracker, lbFnd := gcTracker[lsSymbol]

  if ! lbFnd {
    lcTracker = &_track{}
    lcTracker.Lots = make( []_lot, 0 )
    lcTracker.Symbol = lsSymbol
    gcTracker[lsSymbol] = lcTracker
  }

  switch acValues.TypeText {
    case "buy":
      lNew := _lot{}
      lNew.Shares = acValues.Shares
      lNew.PurchasePrice = acValues.PurchasePrice
      lcTracker.Lots = append( lcTracker.Lots, lNew )
      lcTracker.EquityValue += ( acValues.Shares * acValues.PurchasePrice )
      lcTracker.Shares += acValues.Shares
    case "sell":
      lfShares := acValues.Shares
      lcTracker.EquityValue = 0
      lfGL := 0.0
      for i, lLot := range lcTracker.Lots {
        if lLot.Shares > 0 {
          if lLot.Shares >= lfShares {
            lcTracker.Lots[i].Shares -= lfShares
            lfGL += ( lfShares * acValues.PurchasePrice ) - lfShares * lcTracker.Lots[i].PurchasePrice
            lcTracker.EquityValue += lcTracker.Lots[i].Shares * lcTracker.Lots[i].PurchasePrice
            lfShares = 0
            break
          } else {
            lfShares -= lLot.Shares
            lcTracker.Lots[i].Shares = 0
            lfGL += ( lfShares * acValues.PurchasePrice ) - lfShares * lcTracker.Lots[i].PurchasePrice
          }
        }
      }
      if lfShares > 0 {
        panic( fmt.Sprintf( "Problem calculating shares...%s : %s", acValues.TranDate, lsSymbol ) )
      }
      lcTracker.Lots = _RemoveEmptyLots( &lcTracker.Lots )
      // lcTracker.EquityValue = ( acValues.Shares * acValues.PurchasePrice ) - lcTracker.EquityValue
      // lcTracker.EquityValue = ( lcTracker.EquityValue * -1 ) + ( acValues.Shares * acValues.PurchasePrice )
      lcTracker.Shares -= acValues.Shares
      lcTracker.EquityGL += lfGL
    case "cov_call":
      lcTracker.CCAmount += acValues.TotalValue
      lcTracker.CCPlays += int(acValues.Shares)
    case "assigned":
      lfShares := acValues.Shares * 100
      _, _, _, lfPrice := osch.SplitOptionSymbol( acValues.Symbol )
      acValues.PurchasePrice = lfPrice
      lcTracker.EquityValue = 0
      lfGL := 0.0
      for i, lLot := range lcTracker.Lots {
        if lLot.Shares > 0 {
          if lLot.Shares >= lfShares {
            lcTracker.Lots[i].Shares -= lfShares
            lfGL += ( lfShares * lfPrice ) - lfShares * lcTracker.Lots[i].PurchasePrice
            lcTracker.EquityValue += lcTracker.Lots[i].Shares * lcTracker.Lots[i].PurchasePrice
            lfShares = 0
            break
          } else {
            lfShares -= lLot.Shares
            lcTracker.Lots[i].Shares = 0
            lfGL += ( lfShares * lfPrice ) - lfShares * lcTracker.Lots[i].PurchasePrice
          }
        }
      }
      if lfShares > 0 {
        panic( fmt.Sprintf( "Problem calculating shares...%s : %s", acValues.TranDate, lsSymbol ) )
      }
      lfShares = acValues.Shares * 100
      lcTracker.Lots = _RemoveEmptyLots( &lcTracker.Lots )
      lcTracker.Shares -= lfShares
      lcTracker.EquityGL += lfGL
      // lcTracker.EquityValue = ( lcTracker.EquityValue * -1 ) + ( lfShares * lfPrice )
  }

  return lcTracker
}

//------------------------------------------------------------------------------
// Function: _RemoveEmptyLots
//------------------------------------------------------------------------------
func _RemoveEmptyLots( acLots *[]_lot ) ( []_lot ) {
  lcLots := make( []_lot, 0 )

  for i, lLot := range *acLots {
    if lLot.Shares > 0 {
      lcLots = append( lcLots, (*acLots)[i] )
    }
  }

  return lcLots
}