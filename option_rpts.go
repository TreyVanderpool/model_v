//go:build ignore

package main

import (
	"flag"
	"fmt"
	// "os"
	"sort"

	// "fmt"
	"strconv"
	"strings"
	"sync"

	"time"

	odb "github.com/TreyVanderpool/oliver-golib/db"
	oinit "github.com/TreyVanderpool/oliver-golib/init"
	ol "github.com/TreyVanderpool/oliver-golib/logging"
	osch "github.com/TreyVanderpool/oliver-golib/schwab"
	osql "github.com/TreyVanderpool/oliver-golib/sql"

	olst "github.com/TreyVanderpool/oliver-golib/list"
	orpt "github.com/TreyVanderpool/oliver-golib/report"
	osyn "github.com/TreyVanderpool/oliver-golib/syntax"
	ou "github.com/TreyVanderpool/oliver-golib/utils"
)

const (
	MODEL_VERSION     string = "v"
	THREAD_COUNT      int    = 1
  DEFAULT_AWHERE    string = "strike.offset between 1 and 5 and expire.day in (thursday,friday) and expire.days <= 7 and call.sell_to_open.pct > highest.call.sto.pct; clear(); highest.call.sto.pct = call.sell_to_open.pct"
  // DEFAULT_AWHERE    string = "strike.offset between 1 and 5 and expire.day in (thursday,friday) and expire.days <= 7 and call.bid > highest.call.bid; clear(); highest.call.bid = call.bid"
)

var (
	Log                   ol.ILogger
	Schwab                *osch.SCHWAB
	SQLs                  osql.SQLs
	DB                    *odb.DB
	gfEquityLowPrice      *float64
	gfEquityHighPrice     *float64
	gsRunDate             *string
	gcRptList             *olst.SafeList[string]
	giStrikeOffset        *int
	gfStrikePctOffset     *float64
	giExpireDays          *int
	gsSymbolRange         *string
	gbExcludeZeroBids     *bool
	gcPrintLock           sync.Mutex
	gbRptAHeadingsPrinted bool = false
	gbRptBHeadingsPrinted bool = false
  gcExecutorB           *osyn.Executor
  gcExecutorA           *osyn.Executor
  gcRPTA                *orpt.RPT
  gcRPTB                *orpt.RPT
)

var (
// gcAcctMap           map[string]*osch.Account = make( map[string]*osch.Account )
)

type TAG struct {
	ol.ILogTag
	PgmName string
}

func (t TAG) GetTag() string {
	return t.PgmName
}

// ------------------------------------------------------------------------------
// Function: main
// "strike.offset between 1 and 3 and expire.day in (thursday,friday) and expire.days <= 7"
// ------------------------------------------------------------------------------
func main() {
	lsDBName := flag.String( "db", "stocks_test", "database name" )
	lsLogLevel := flag.String( "lvl", "info", "Log level (debug, info, warn, error)" )
	gfEquityLowPrice = flag.Float64( "elp", 0.0, "Equity low price" )
	gfEquityHighPrice = flag.Float64( "ehp", 0.0, "Equity high price" )
	gsRunDate = flag.String( "rd", time.Now().Format(ou.YYYY_MM_DD), "Run date" )
	lsSymbols := flag.String( "s", "mktcap_1B", "Symbols list, can be symbol or symbols_to_process name" )
	giStrikeOffset = flag.Int( "so", 1, "strike offset from current equity price" )
	gfStrikePctOffset = flag.Float64( "spo", 0, "strike offset by percent from current equity price" )
	giExpireDays = flag.Int( "edays", 1, "Number of days expire date is from specified date" )
	gsSymbolRange = flag.String( "range", "0,999999999", "Equity ask price range, comma separted values" )
	gbExcludeZeroBids = flag.Bool( "excludezerobid", false, "Exclude zero dollar CALL bids" )
	liThreads := flag.Int( "threads", THREAD_COUNT, "Thread count to use" )
  lsRptBWhere := flag.String( "bwhere", "", "where clause for RPT B")
  lsRptAWhere := flag.String( "awhere", DEFAULT_AWHERE, "where clause for RPT A")
	flag.Parse()

	Log = oinit.Init( oinit.INIT_LOG, lsLogLevel ).(ol.ILogger)
	Log.SetPatterns( "%M\n", "%D %-5L %T:%-20.20F:%3# %M\n" )
	Log.SetTag( TAG{ PgmName: "optrpt" } )
	DB = oinit.Init( oinit.INIT_DB, Log, lsDBName ).(*odb.DB)
	defer Log.Info( "Exiting Program" )

	Schwab = oinit.Init( oinit.INIT_SCHWAB, Log, DB ).(*osch.SCHWAB)
	SQLs = oinit.Init( oinit.INIT_SQLS, Log, DB ).(osql.SQLs)

  if *lsRptBWhere == "" {
    fmt.Printf( "ERROR: No RPTB where clause provided" )
    return
  }

  lcSyntax := osyn.CreateSyntax()
  var err     error
  gcExecutorB, err = osyn.CreateExecutor( lcSyntax, *lsRptBWhere )
  gcExecutorA, err = osyn.CreateExecutor( lcSyntax,
                                          *lsRptAWhere )

  if err != nil {
    fmt.Printf( "ERROR: %s\n", err )
    return
  }

	lsSymbolsList := make( []string, 0 )

	if *lsSymbols == "" {
		lsSymbolsList, _ = SQLs.S_OpenCloseAllSymbols()
	} else {
		lsSymbolsList, _ = SQLs.X_BuildSymbolsList( *lsSymbols )
	}

	lsSymbolsList, _ = SQLs.S_ExcludeNoOptionList( lsSymbolsList )

	Log.Info( "Starting: Looking at %d symbols", len(lsSymbolsList) )

  if len(lsSymbolsList) == 0 {
    fmt.Printf( "No symbol names found.\n" )
    return
  }

	_PutinSaveList( lsSymbolsList )

	if *gsRunDate == time.Now().Format( ou.YYYY_MM_DD ) {
		gcRPTA = _CreateReportA()
		gcRPTB = _CreateReportB()
		lcQuotes, err := Schwab.GetSymbolQuotes( lsSymbolsList, "" )
		if err != nil {
      Log.Error( "ERROR: %s", string(Schwab.HTTP.ResponseBody) )
			Log.Exception( err )
			return
		}
		lcWaitGroup := &sync.WaitGroup{}
		lcWaitGroup.Add( *liThreads )
		for range *liThreads {
      go _RetrieveOptionChains( lcQuotes, lcWaitGroup )
		}
		lcWaitGroup.Wait()
	}
}

// ------------------------------------------------------------------------------
// Function: _PutinSaveList
// ------------------------------------------------------------------------------
func _PutinSaveList(asList []string) {
	gcRptList = olst.NewSafeList[string]()

	for i := range asList {
		asList[i] = strings.ToUpper( asList[i] )
		gcRptList.PushBack( asList[i] )
	}
}

// ------------------------------------------------------------------------------
// Function: _ReportALiveData
// ------------------------------------------------------------------------------
func _RetrieveOptionChains( acQuotes map[string]osch.Quote, acWaitGroup *sync.WaitGroup ) {
	lsRange := strings.Split( *gsSymbolRange, "," )
	lfLowRange, _ := strconv.ParseFloat( lsRange[0], 64 )
	lfHighRange, _ := strconv.ParseFloat( lsRange[1], 64 )
	defer acWaitGroup.Done()

	if len(lsRange) != 2 {
		panic("Symbol range value '%s' must be 2 values separated by comma")
	}

	gcPrintLock.Lock()
	if ! gbRptAHeadingsPrinted {
		fmt.Printf( "RA: Report: Range: %.0f/%.0f  %s\n",	lfLowRange, lfHighRange, gcExecutorA.GetOriginalText() )
		gbRptAHeadingsPrinted = true
	}
	if ! gbRptBHeadingsPrinted {
		fmt.Printf( "RB: Report: Range: %.0f/%.0f  %s\n", lfLowRange, lfHighRange, gcExecutorB.GetOriginalText() )
		gbRptBHeadingsPrinted = true
	}
	gcPrintLock.Unlock()

	for {
		lcElement := gcRptList.RemoveFront()
		if lcElement == nil {
			break
		}

		lsSymbol := lcElement.Value.(string)
		lcQuote, lbFnd := acQuotes[lsSymbol]
		if ! lbFnd {
			continue
		}

		if lcQuote.Quote.AskPrice < lfLowRange || lcQuote.Quote.AskPrice > lfHighRange {
			continue
		}

		lcChain, err := Schwab.GetOptionChain( lsSymbol, nil )
		if err != nil {
			gcRPTA.PrintLine( lsSymbol, err )
			continue
		}

  	// gcPrintLock.Lock()
    lcVars := make( map[string]any )
    lcVars["highest.call.bid"] = 0.0
    lcVars["highest.call.sto.pct"] = 0.0
    lcESa, err := lcChain.FindUsing( gcExecutorA, &lcVars )
    lcESb, err := lcChain.FindUsing( gcExecutorB, nil )
	//   gcPrintLock.Unlock()

    // if len(lcES) == 0 { continue }

    if len(lcESa) > 0 { _ReportALiveData( lcQuote, &lcChain, lcESa ) }
    if len(lcESb) > 0 { _ReportBLiveData( lcQuote, &lcChain, lcESb ) }
  }
}

// ------------------------------------------------------------------------------
// Function: _ReportALiveData
// ------------------------------------------------------------------------------
func _ReportALiveData( acQuote osch.Quote, acChain *osch.Chain, acES []osch.CStrikePrice ) {
	lsCurrTime := time.Now().Format( ou.HH_MM_SS )

  if len(acES) == 0 {
    return
  }

  lfCallEstimateValue := ( acES[0].Price.Call.Ask + acES[0].Price.Call.Bid ) / 2
  lfSTOValue := lfCallEstimateValue * 100
  lfSTOPct := (lfSTOValue / ( acQuote.Quote.AskPrice * 100 )) * 100

  if acES[0].Price.Call.Bid == 0 {
    if *gbExcludeZeroBids {
      return
    }
    lfSTOPct = 0
    lfSTOValue = 0
    lfCallEstimateValue = 0
  }

  gcPrintLock.Lock()
  gcRPTA.PrintLine( acQuote.Symbol,
    lsCurrTime,
    acQuote.Quote.AskPrice,
    acES[0].Strike.ExpireDate,
    acES[0].Price.StrikePrice,
    acES[0].Strike.ExpireDays,
    acES[0].Price.OffsetFromSymbol,
    ou.PctChg( acQuote.Quote.AskPrice, acES[0].Price.StrikePrice ),
    acQuote.Quote.AskPrice*100,
    acES[0].Price.Call.Ask,
    acES[0].Price.Call.Bid,
    lfCallEstimateValue,
    lfSTOValue,
    lfSTOPct )
  gcPrintLock.Unlock()
}

// ------------------------------------------------------------------------------
// Function: _CreateReportA
// ------------------------------------------------------------------------------
func _CreateReportA() *orpt.RPT {
	lcRpt := orpt.NewRPT()
	lcRpt.SetReportName("RA:")
	lcRpt.AddColumn("Symbol", "%s", 6, orpt.RPT_ALGN_LEFT)
	lcRpt.AddColumn("TranTime", "%s", 8, orpt.RPT_ALGN_LEFT)
	lcRpt.AddColumn("Sym Ask", "%.2f", 7, orpt.RPT_ALGN_RIGHT)
	lcRpt.AddColumn("ExpireDate", "%s", 10, orpt.RPT_ALGN_LEFT)
	lcRpt.AddColumn("Strike$", "%.1f", 7, orpt.RPT_ALGN_RIGHT)
	lcRpt.AddColumn("EDays", "%d", 5, orpt.RPT_ALGN_RIGHT)
	lcRpt.AddColumn("Off", "%d", 3, orpt.RPT_ALGN_RIGHT)
	lcRpt.AddColumn("SymAsk%%", "%.2f%%", 7, orpt.RPT_ALGN_RIGHT)
	lcRpt.AddColumn("Sym Value", "%.0f", 9, orpt.RPT_ALGN_RIGHT).SetCommas(true)
	lcRpt.AddColumn("CallAsk", "%.2f", 7, orpt.RPT_ALGN_RIGHT).SetBWZ(true)
	lcRpt.AddColumn("CallBid", "%.2f", 7, orpt.RPT_ALGN_RIGHT).SetBWZ(true)
	lcRpt.AddColumn("CallEst", "%.2f", 7, orpt.RPT_ALGN_RIGHT).SetBWZ(true)
	lcRpt.AddColumn("STO Amt", "%.0f", 7, orpt.RPT_ALGN_RIGHT).SetCommas(true).SetBWZ(true)
	lcRpt.AddColumn("STO Pct", "%.2f%%", 7, orpt.RPT_ALGN_RIGHT).SetBWZ(true)

	return lcRpt
}

// ------------------------------------------------------------------------------
// Function: _ReportBLiveData
// ------------------------------------------------------------------------------
func _ReportBLiveData( acQuote osch.Quote, acChain *osch.Chain, acES []osch.CStrikePrice ) {

  lbPrinted := false

  sort.Slice( acES, func( i, j int ) (bool) {
    return ( acES[i].Strike.ExpireDate < acES[j].Strike.ExpireDate ) ||
           ( acES[i].Strike.ExpireDate == acES[j].Strike.ExpireDate &&
             acES[i].Price.StrikePrice < acES[j].Price.StrikePrice )
  })

  gcPrintLock.Lock()
  defer gcPrintLock.Unlock()

  for _, lES := range acES {
		lfCallEstimateValue := ( lES.Price.Call.Ask + lES.Price.Call.Bid ) / 2
		lfSTOValue := lfCallEstimateValue * 100
		lfSTOPct := (lfSTOValue / ( acQuote.Quote.AskPrice * 100 )) * 100

		if lES.Price.Call.Bid == 0 {
			// if *gbExcludeZeroBids {
			// 	continue
			// }
			lfSTOPct = 0
			lfSTOValue = 0
			lfCallEstimateValue = 0
		}

		gcRPTB.PrintLine( acChain.Symbol,
			acQuote.Quote.AskPrice,
			lES.Strike.ExpireDate,
			lES.Price.StrikePrice,
			lES.Strike.ExpireDays,
			lES.Price.OffsetFromSymbol,
			ou.PctChg( acQuote.Quote.AskPrice, lES.Price.StrikePrice ),
			acQuote.Quote.AskPrice*100,
			lES.Price.Call.Ask,
			lES.Price.Call.Bid,
			lfCallEstimateValue,
			lfSTOValue,
			lfSTOPct)
      lbPrinted = true
  }

  if lbPrinted {
    fmt.Printf( "RB:\n" )
  }
}

// ------------------------------------------------------------------------------
// Function: _CreateReportB
// ------------------------------------------------------------------------------
func _CreateReportB() *orpt.RPT {
	lcRpt := orpt.NewRPT()
	lcRpt.SetReportName("RB:")
	lcRpt.AddColumn("Symbol", "%s", 6, orpt.RPT_ALGN_LEFT)
	lcRpt.AddColumn("Sym Ask", "%.2f", 7, orpt.RPT_ALGN_RIGHT)
	lcRpt.AddColumn("ExpireDate", "%s", 10, orpt.RPT_ALGN_LEFT)
	lcRpt.AddColumn("Strike$", "%.1f", 7, orpt.RPT_ALGN_RIGHT)
	lcRpt.AddColumn("EDays", "%d", 5, orpt.RPT_ALGN_RIGHT)
	lcRpt.AddColumn("Off", "%d", 3, orpt.RPT_ALGN_RIGHT)
	lcRpt.AddColumn("SymAsk%%", "%.2f%%", 7, orpt.RPT_ALGN_RIGHT)
	lcRpt.AddColumn("Sym Value", "%.0f", 9, orpt.RPT_ALGN_RIGHT).SetCommas(true)
	lcRpt.AddColumn("CallAsk", "%.2f", 7, orpt.RPT_ALGN_RIGHT).SetBWZ(true).SetBWZ(true)
	lcRpt.AddColumn("CallBid", "%.2f", 7, orpt.RPT_ALGN_RIGHT).SetBWZ(true).SetBWZ(true)
	lcRpt.AddColumn("CallEst", "%.2f", 7, orpt.RPT_ALGN_RIGHT).SetBWZ(true).SetBWZ(true)
	lcRpt.AddColumn("STO Amt", "%.0f", 7, orpt.RPT_ALGN_RIGHT).SetCommas(true).SetBWZ(true)
	lcRpt.AddColumn("STO Pct", "%.2f%%", 7, orpt.RPT_ALGN_RIGHT).SetBWZ(true).SetBWZ(true)

	return lcRpt
}
