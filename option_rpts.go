//go:build ignore

package main

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	"time"

	odb "github.com/TreyVanderpool/oliver-golib/db"
	oinit "github.com/TreyVanderpool/oliver-golib/init"
	ol "github.com/TreyVanderpool/oliver-golib/logging"
	osch "github.com/TreyVanderpool/oliver-golib/schwab"
	osql "github.com/TreyVanderpool/oliver-golib/sql"

	olst "github.com/TreyVanderpool/oliver-golib/list"
	orpt "github.com/TreyVanderpool/oliver-golib/report"
	ou "github.com/TreyVanderpool/oliver-golib/utils"
)

const (
  MODEL_VERSION          string = "v"
)

var (
  Log                     ol.ILogger
  Schwab                  *osch.SCHWAB
  SQLs                    osql.SQLs
  DB                      *odb.DB
  gfEquityLowPrice      	*float64
  gfEquityHighPrice       *float64
  gsRunDate               *string
  gcRptList               *olst.SafeList[string]
  giStrikeOffset          *int
  gfStrikePctOffset       *float64
  giExpireDays            *int
  gsSymbolRange           *string
  gbExcludeZeroBids       *bool
)

var (
//   gcAcctMap           map[string]*osch.Account = make( map[string]*osch.Account )
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
  lsDBName := flag.String( "db", "stocks_test", "database name" )
  lsLogLevel := flag.String( "lvl", "info", "Log level (debug, info, warn, error)" )
  gfEquityLowPrice = flag.Float64( "elp", 0.0, "Equity low price" )
  gfEquityHighPrice = flag.Float64( "ehp", 0.0, "Equity high price" )
  gsRunDate = flag.String( "rd", time.Now().Format( ou.YYYY_MM_DD ), "Run date" )
  lsSymbols := flag.String( "s", "mktcap_1B", "Symbols list, can be symbol or symbols_to_process name" )
  giStrikeOffset = flag.Int( "so", 1, "strike offset from current equity price" )
  gfStrikePctOffset = flag.Float64( "spo", 0, "strike offset by percent from current equity price" )
  giExpireDays = flag.Int( "edays", 1, "Number of days expire date is from specified date" )
  gsSymbolRange = flag.String( "range", "0,999999999", "Equity ask price range, comma separted values" )
  gbExcludeZeroBids = flag.Bool( "excludezerobid", false, "Exclude zero dollar CALL bids" )
  flag.Parse()

  Log = oinit.Init( oinit.INIT_LOG, lsLogLevel ).(ol.ILogger)
  Log.SetPatterns( "%M\n", "%D %-5L %T:%-20.20F:%3# %M\n" )
  Log.SetTag( TAG{ PgmName: "optrpt" } )
  DB = oinit.Init( oinit.INIT_DB, Log, lsDBName ).(*odb.DB)
  defer Log.Info( "Exiting Program" )

  Schwab = oinit.Init( oinit.INIT_SCHWAB, Log, DB ).(*osch.SCHWAB)
  SQLs = oinit.Init( oinit.INIT_SQLS, Log, DB ).(osql.SQLs)

  lsSymbolsList := make( []string, 0 )

  if *lsSymbols == "" {
    lsSymbolsList, _ = SQLs.S_OpenCloseAllSymbols()
  } else {
    lsSymbolsList, _ = SQLs.X_BuildSymbolsList( *lsSymbols )
  }

  lsSymbolsList, _ = SQLs.S_ExcludeOptionList( lsSymbolsList )

  Log.Info( "Starting: Looking at %d symbols", len(lsSymbolsList) )

  _PutinSaveList( lsSymbolsList )

  if *gsRunDate == time.Now().Format( ou.YYYY_MM_DD ) {
    lcRpt := _CreateReportA()
    _ReportALiveData( lcRpt, lsSymbolsList )
  }
}

//------------------------------------------------------------------------------
// Function: _PutinSaveList
//------------------------------------------------------------------------------
func _PutinSaveList( asList []string ) {
  gcRptList = olst.NewSafeList[string]()

  for i := range asList {
    asList[i] = strings.ToUpper( asList[i] )
    gcRptList.PushBack( asList[i] )
  }
}

//------------------------------------------------------------------------------
// Function: _ReportALiveData
//------------------------------------------------------------------------------
func _ReportALiveData( acRpt *orpt.RPT, asSymbolList []string ) {
  lcDate := time.Now().AddDate( 0, 0, *giExpireDays )
  lsExpireDate := lcDate.Format( ou.YYYY_MM_DD )
  lcOptionParms := make( map[string]string )
  lcOptionParms["toDate"] = lsExpireDate
  lsRange := strings.Split( *gsSymbolRange, "," )
  lsCurrTime := time.Now().Format( ou.HH_MM_SS )

  if len(lsRange) != 2 {
    panic( "Symbol range value '%s' must be 2 values separated by comma" )
  }

  lcQuotes, err := Schwab.GetSymbolQuotes( asSymbolList, "" )

  if err != nil {
    Log.Exception( err )
    return
  }

  lfLowRange, _ := strconv.ParseFloat( lsRange[0], 64 )
  lfHighRange, _ := strconv.ParseFloat( lsRange[1], 64 )

  fmt.Printf( "RA: Report: Range: %.0f/%.0f  ExpireDate: %d : %s\n", lfLowRange, lfHighRange, *giExpireDays, lsExpireDate )
  
  for {
    lcElement := gcRptList.RemoveFront()
    if lcElement == nil { break }

    lsSymbol := lcElement.Value.(string)
    lcQuote, lbFnd := lcQuotes[lsSymbol]
    if ! lbFnd { continue }

    if lcQuote.Quote.AskPrice < lfLowRange || lcQuote.Quote.AskPrice > lfHighRange { continue }

    lcChain, err := Schwab.GetOptionChain( lsSymbol, lcOptionParms )
    if err != nil {
      acRpt.PrintLine( lsSymbol, err )
      continue
    }

    lcExpireDate, lcStrikePrice := lcChain.FindStrikePriceAbove( lsSymbol, lsExpireDate, lcQuote.Quote.AskPrice )

    if lcExpireDate == nil || lcStrikePrice == nil { continue }

    lfCallEstimateValue := ( lcStrikePrice.Call.Ask + lcStrikePrice.Call.Bid ) / 2
    lfSTOValue := lfCallEstimateValue * 100
    lfSTOPct := ( lfSTOValue / ( lcQuote.Quote.AskPrice * 100 ) ) * 100

    if lcStrikePrice.Call.Bid == 0 {
      if *gbExcludeZeroBids { continue }
      lfSTOPct = 0
      lfSTOValue = 0
      lfCallEstimateValue = 0
    }

    acRpt.PrintLine( lsSymbol,
                     lsCurrTime,
                     lcExpireDate.ExpireDate,
                     lcStrikePrice.StrikePrice,
                     lcExpireDate.ExpireDays,
                     lcStrikePrice.OffsetFromSymbol,
                     lcQuote.Quote.AskPrice,
                     lcQuote.Quote.AskPrice * 100,
                     lcStrikePrice.Call.Ask,
                     lcStrikePrice.Call.Bid,
                     lfCallEstimateValue,
                     lfSTOValue,
                     lfSTOPct )
  }
}

//------------------------------------------------------------------------------
// Function: _CreateReportA
//------------------------------------------------------------------------------
func _CreateReportA() ( *orpt.RPT ) {
  lcRpt := orpt.NewRPT()
  lcRpt.SetReportName( "RA:" )
  lcRpt.AddColumn( "Symbol", "%s", 6, orpt.RPT_ALGN_LEFT )
  lcRpt.AddColumn( "TranTime", "%s", 8, orpt.RPT_ALGN_LEFT )
  lcRpt.AddColumn( "ExpireDate", "%s", 10, orpt.RPT_ALGN_LEFT )
  lcRpt.AddColumn( "Strike$", "%.1f", 7, orpt.RPT_ALGN_RIGHT )
  lcRpt.AddColumn( "EDays", "%d", 5, orpt.RPT_ALGN_RIGHT )
  lcRpt.AddColumn( "Off", "%d", 3, orpt.RPT_ALGN_RIGHT )
  lcRpt.AddColumn( "Sym Ask", "%.2f", 7, orpt.RPT_ALGN_RIGHT )
  lcRpt.AddColumn( "Sym Value", "%.0f", 9, orpt.RPT_ALGN_RIGHT ).SetCommas( true )
  lcRpt.AddColumn( "CallAsk", "%.2f", 7, orpt.RPT_ALGN_RIGHT ).SetBWZ( true )
  lcRpt.AddColumn( "CallBid", "%.2f", 7, orpt.RPT_ALGN_RIGHT ).SetBWZ( true )
  lcRpt.AddColumn( "CallEst", "%.2f", 7, orpt.RPT_ALGN_RIGHT ).SetBWZ( true )
  lcRpt.AddColumn( "STO Amt", "%.0f", 7, orpt.RPT_ALGN_RIGHT ).SetCommas( true ).SetBWZ( true )
  lcRpt.AddColumn( "STO Pct", "%.2f", 7, orpt.RPT_ALGN_RIGHT ).SetBWZ( true )

  return lcRpt
}