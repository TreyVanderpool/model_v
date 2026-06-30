//go:build ignore

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"
  "sort"
	"time"

	odb "github.com/TreyVanderpool/oliver-golib/db"
	oinit "github.com/TreyVanderpool/oliver-golib/init"
	ol "github.com/TreyVanderpool/oliver-golib/logging"
	osch "github.com/TreyVanderpool/oliver-golib/schwab"
	osql "github.com/TreyVanderpool/oliver-golib/sql"
	otxt "github.com/TreyVanderpool/oliver-golib/text"
	ou "github.com/TreyVanderpool/oliver-golib/utils"

	oimg "github.com/TreyVanderpool/oliver-golib/image"
)

const (
  MODEL_VERSION          string = "v"
  REPORT_NAME            string = "eod"
  TEXT_MAX_LEN           float64 = 215
)

var (
  Log               ol.ILogger
  DB                *odb.DB
  Schwab            *osch.SCHWAB
  SQLs              osql.SQLs
  gsCurrDate        *string
  gbSendText        *bool
  gcFont            *oimg.Font
  gsTestTextNbr     *string
  gcSendText        *otxt.SendText
  gbUpdateBalances  *bool
  gcEquityTotal     map[string]float64 = make( map[string]float64 )
  gcCoveredTotal    map[string]float64 = make( map[string]float64 )
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
  gsCurrDate = flag.String( "rd", time.Now().Format( ou.YYYY_MM_DD ), "Run date" )
  lsOrdersDate := flag.String( "od", time.Now().Format( ou.YYYY_MM_DD ), "Orders date to retrieve (YYYY-MM-DD)" )
  gbSendText = flag.Bool( "sendtext", false, "send text" )
  gsTestTextNbr = flag.String( "testtext", "", "test phone number to text for testing" )
  gbUpdateBalances = flag.Bool( "updatebalances", false, "Update daily balances")
  flag.Parse()

  // Init main components, Log, DB, Schwab, SQLs...
  Log = oinit.Init( oinit.INIT_LOG, lsLogLevel ).(ol.ILogger)
  Log.SetPatterns( "%M\n", "%D %-5L %T:%-20.20F:%# %M\n" )
  Log.SetTag( TAG{ PgmName: "dlybal" } )
  DB = oinit.Init( oinit.INIT_DB, lsDBName ).(*odb.DB)
  Schwab = oinit.Init( oinit.INIT_SCHWAB, Log, DB ).(*osch.SCHWAB)
  SQLs = oinit.Init( oinit.INIT_SQLS, Log, DB ).(osql.SQLs)
  gcSendText = oinit.Init( oinit.INIT_TEXT ).(*otxt.SendText)


  // Use this to try and calculate text sizes so I can align the text message
  // with left/right alignments for numbers.
  lcImg := oimg.NewImage( 1, 1, oimg.BLACK )
  lcImg.LoadFont( "sans", "GoogleSans-Regular.ttf", 14 )
  gcFont = lcImg.GetFont( "sans" )

  lcAPIAcct, err := Schwab.GetAPIAccounts()

  if err != nil {
    Log.Error( "Error getting accounts: %s", err )
    return
  }

  Log.Info( "Starting: Run Date: %s  Update Balances: %t", *gsCurrDate, *gbUpdateBalances )

  // Loop through all the accounts and retrieve the Account Info which contains
  // the current and initial balances for the day. Insert the balances into the database.
  // No report generated here, just retrieves the data and writes it to the database.
  if *gbUpdateBalances {
    for _, lAcct := range lcAPIAcct {
      for _, lSchwab := range lAcct.SchwabAccounts {
        _ProcessBalances( lSchwab, lAcct.Owners, true )
      }
    }
  } else {
    Log.Info( "Update Balances flag not set, skipping pulling values.")
  }

  lcStartDate, err := time.Parse( ou.TIMESTAMPFORMAT, *lsOrdersDate + " 00:00:00" )

  if err != nil {
    Log.Error( "Error parsing orders date: %s", err )
    return
  }

  lcEndDate, _ := time.Parse( ou.TIMESTAMPFORMAT, *lsOrdersDate + " 23:59:59" )

  // Walk through each of the accounts and process all their orders for the day.
  for _, lAcct := range lcAPIAcct {
    for _, lSchwab := range lAcct.SchwabAccounts {
      _ProcessOrders( lSchwab, &lcStartDate, &lcEndDate, lAcct.Owners )
    }
  }

  for _, lAcct := range lcAPIAcct {
    _GenerateDailyReport( lAcct )
  }

  _GenerateCoveredCallReport()
}

//-------------------------------------------------------------
// Function: _ProcessBalances
//-------------------------------------------------------------
func _ProcessBalances( acAcct osch.APISchwabAccount, asOwnerName string, abProcessTotals bool ) {
  Log.Info( "Retrieving balances for account %s : %s", acAcct.GetMaskedNbr(), asOwnerName )

  Schwab.SetAccountNbr( acAcct.AccountNbr )
  lcAcctInfo, err := Schwab.GetAccountInfo()

  if err != nil {
    Log.Exception( err )
    return
  }

  // Save the full account info data structure from Schwab
  err = SQLs.I_DailyBalances( *gsCurrDate, acAcct.AccountNbr, string(Schwab.HTTP.ResponseBody) )

  if err != nil {
    Log.Exception( err )
  }

  // Walk through the data structure and extract indivual values and total cash values
  lcDV := &osql.DailyValues{}
  lcDV.TranDate = *gsCurrDate
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
      lcDV.TodaysPctChg = lcDV.TodaysGainLoss / lcAcctInfo.CurrBalance.LongMarketValue
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
      lcDV.TodaysPctChg = lcDV.TodaysGainLoss / lcAcctInfo.CurrBalance.LiquidationValue
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

  if lcAcctInfo.CurrBalance.LiquidationValue != 0 {
    lcDV.Symbol = "..total"
    lcDV.TotalValue = lcAcctInfo.CurrBalance.LiquidationValue
    lcDV.TodaysGainLoss = lcAcctInfo.CurrBalance.LiquidationValue - lcAcctInfo.InitBalance.LiquidationValue
    if lcDV.TodaysGainLoss != 0 {
      lcDV.TodaysPctChg = lcDV.TodaysGainLoss / lcAcctInfo.CurrBalance.LiquidationValue
    }
    err = SQLs.I_DailyValues( lcDV )
    if err != nil { Log.Exception( err ) }
    lcDV.TodaysGainLoss = 0
    lcDV.TodaysPctChg = 0
  }

  for _, lPos := range lcAcctInfo.Positions {
    _ProcessPositions( lPos, lcDV.AccountNbr, abProcessTotals )
  }
}

//-------------------------------------------------------------
// Function: _ProcessPositions
//-------------------------------------------------------------
func _ProcessPositions( acPosition osch.Position, asAcctNbr string, abProcessTotals bool ) {
  lcDV := &osql.DailyValues{}
  lcDV.TranDate = *gsCurrDate
  lcDV.AccountNbr = asAcctNbr
  lcDV.TypeText = "position"
  lcDV.Symbol = acPosition.Instrument.Symbol
  
  if acPosition.ShortQuantity != 0 {
    if acPosition.IsCoveredCall() {
      // This must be a covered call position, nothing to post as values here.
      // This should have been posted as a type=cov_call when originally executed.
      return
    }
    lcDV.Shares = acPosition.ShortQuantity
  } else {
    lcDV.Shares = acPosition.LongQuantity
    lcDV.PurchasePrice = acPosition.AveragePrice
    lcDV.TodaysPrice = acPosition.MarketValue / acPosition.LongQuantity
    lcDV.TodaysGainLoss = acPosition.CurrDayProfitLoss
    lcDV.TodaysPctChg = acPosition.CurrDayProfitLossPct
    lcDV.OverallGainLoss = acPosition.LongOpenProfitLoss
    lcDV.OverallPctChg = ou.PctChg( acPosition.MarketValue, lcDV.Shares * acPosition.AveragePrice )
    lcDV.TotalValue = acPosition.MarketValue
  }

  if abProcessTotals {
    _AddPositionToTotalValue( acPosition )
  }

  err := SQLs.I_DailyValues( lcDV )
  if err != nil { Log.Exception( err ) }
}

//-------------------------------------------------------------
// Function: _ProcessOrders
//-------------------------------------------------------------
func _ProcessOrders( acAcct osch.APISchwabAccount, acStartDate, acEndDate *time.Time, asOwnerName string ) {
  Log.Info( "Retrieving orders on %s for account %s : %s", acStartDate.Format( ou.YYYY_MM_DD ), acAcct.GetMaskedNbr(), asOwnerName )

  Schwab.SetAccountNbr( acAcct.AccountNbr )
  lcOrders, err := Schwab.GetOrders( acStartDate, acEndDate, "" )

  if err != nil {
    Log.Exception( err )
    return
  }

  lcOrders = osch.FlattenOrders( lcOrders )

  for _, lOrder := range lcOrders {
    if lOrder.Status != "FILLED" { continue }
    lcDV := &osql.DailyValues{}
    lcDV.TranDate = lOrder.CloseTime[0:10]
    lcDV.AccountNbr = acAcct.AccountNbr
    lcDV.OrderId = lOrder.OrderId

    for _, lLeg := range lOrder.OrderLegCollection {
      lcDV.Symbol = lLeg.Instrument.Symbol
      lcDV.Instruction = lLeg.Instruction
      switch lLeg.Instruction {
        case "BUY":
          lcDV.TypeText = "buy"
        case "SELL":
          lcDV.TypeText = "sell"
      }
      if lLeg.OrderLegType == "OPTION" && lLeg.Instruction == "SELL_TO_OPEN" {
        lcDV.TypeText = "cov_call"
      }
    }

    for _, lOAC := range lOrder.OrderActivityCollection {
      _, lfShares, lfPrice := lOAC.GetPriceAndQty()
      lcDV.Shares += lfShares
      lcDV.PurchasePrice += lfPrice
    }

    if len(lOrder.OrderActivityCollection) > 1 {
      lcDV.PurchasePrice /= float64(len(lOrder.OrderActivityCollection))
    }

    if lcDV.TypeText == "cov_call" {
      lcDV.TotalValue = ( lcDV.Shares * 100 ) * lcDV.PurchasePrice
    } else {
      lcDV.TotalValue = lcDV.Shares * lcDV.PurchasePrice
    }

    Log.Info( "Order: %d : %-21s : %s -- Qty: %9.2f  Price: %9.2f : %-10s : %s",
              lcDV.OrderId, lcDV.Symbol, lcDV.TranDate, lcDV.Shares, lcDV.PurchasePrice, lcDV.TypeText, lcDV.Instruction )

    err = SQLs.I_DailyValues( lcDV )

    if err != nil {
      Log.Exception( err )
      continue
    }
  }
}

//-------------------------------------------------------------
// Function: _GenerateDailyReport
// https://en.wikipedia.org/wiki/List_of_emojis
//-------------------------------------------------------------
func _GenerateDailyReport( acAPIAcct osch.APIAccount ) {
  lbEOW := Schwab.IsEndOfWeek( *gsCurrDate )
  lbEOM := Schwab.IsEndOfMonth( *gsCurrDate )

  lsPhoneNbrs, err := Schwab.GetAllVerionTypePhoneNbrs( MODEL_VERSION, REPORT_NAME )

  for _, lPhoneNbr := range lsPhoneNbrs {
    lfWeekBeg := 0.0
    lfWeekEnd := 0.0
    lfMonthBeg := 0.0
    lfMonthEnd := 0.0
    lbTotalsRpt := true
    liAcctCount := 0
    lfTotalValue := 0.0
    lfTotalGL := 0.0
    lfTotalCC := 0.0
    lfWeekCC := 0.0
    lfMonthCC := 0.0

    for _, lAcct := range acAPIAcct.SchwabAccounts {
      Schwab.SetAccountNbr( lAcct.AccountNbr )
      if err != nil {
        Log.Exception( err )
        continue
      }

      lbPhoneOnReport, _ := Schwab.IsPhoneOnNotification( MODEL_VERSION, lPhoneNbr, REPORT_NAME )
      if ! lbPhoneOnReport { continue }
      if *gsTestTextNbr > "" && lPhoneNbr != *gsTestTextNbr { continue }

      liAcctCount++

      if liAcctCount == 1 {
        lsLine := "--- END OF DAY ---"
        lfLen := gcFont.GetTextWidth( lsLine )
        lfSpace := gcFont.GetCharWidth( ' ' )
        lfOff := ( (TEXT_MAX_LEN + 10) / 2 ) - ( lfLen / 2 )
        lfSpace = lfOff / lfSpace
        lsLine = fmt.Sprintf( ".%*s%s", int(lfSpace+0.5)-1, "", lsLine )
        gcSendText.AddLine( lPhoneNbr, lsLine )
      } else {
        gcSendText.AddLine( lPhoneNbr, "" )
      }

      lfValue, lfGL, lfCoveredCalls := _GenerateReportByPhoneNbr( lAcct, lPhoneNbr, acAPIAcct.Owners, lbTotalsRpt )
      lfTotalValue += lfValue
      lfTotalGL += lfGL
      lfTotalCC += lfCoveredCalls

      if lbEOW {
        lfBeg, lfEnd, lfCC := _GetBegEndValues( lAcct.AccountNbr, lPhoneNbr, lbEOM )
        lfWeekBeg += lfBeg
        lfWeekEnd += lfEnd
        lfWeekCC += lfCC
      }
      if lbEOM {
        lfBeg, lfEnd, lfCC := _GetBegEndValues( lAcct.AccountNbr, lPhoneNbr, lbEOM )
        lfMonthBeg += lfBeg
        lfMonthEnd += lfEnd
        lfMonthCC += lfCC
      }
    }

    if liAcctCount > 1 {
      gcSendText.AddLine( lPhoneNbr, "" )
      gcSendText.AddLine( lPhoneNbr, gcFont.AppendRightJustified( "Total Value:", ou.Commas( "$%.0f", lfTotalValue ), TEXT_MAX_LEN ) )
      lsGLLine := gcFont.AppendRightJustified( "Total G/L:", ou.Commas( "$%.0f", lfTotalGL ), TEXT_MAX_LEN )
      if lfTotalGL < 0 {
        lsGLLine += otxt.EMOJI_RED_DOT + " "
      } else {
        lsGLLine += otxt.EMOJI_GREEN_DOT + " "
      }
      gcSendText.AddLine( lPhoneNbr, lsGLLine )
      if lfTotalCC > 0 {
        lsGLLine = gcFont.AppendRightJustified( "Total Covered Calls:", ou.Commas( "$%.0f", lfTotalCC ), TEXT_MAX_LEN ) + otxt.EMOJI_SMILE_WITH_TEETH
        gcSendText.AddLine( lPhoneNbr, lsGLLine )
        lfWeekCC += lfTotalCC
        lfMonthCC += lfTotalCC
      }
    }

    if lfWeekBeg != 0 {
      _AddOtherValues( lPhoneNbr, "Week", lfWeekBeg, lfWeekEnd, lfWeekCC )
    }

    if lfMonthBeg != 0 {
      _AddOtherValues( lPhoneNbr, "Month", lfMonthBeg, lfMonthEnd, lfMonthCC )
    }
    lbTotalsRpt = false
  }

  _SendText()

}

//-------------------------------------------------------------
// Function: _GenerateReportByPhoneNbr
// https://en.wikipedia.org/wiki/List_of_emojis
//-------------------------------------------------------------
func _GenerateReportByPhoneNbr( acAPIAcct osch.APISchwabAccount, asPhoneNbr, asOwnerName string, abProcessTotals bool ) ( rTotal, rGL, rfCoveredCalls float64 ) {
  lsText := ""

  lcDV, err := SQLs.S_DailyValues( *gsCurrDate, acAPIAcct.AccountNbr )

  if err != nil {
    Log.Exception( err )
    return
  }

  lsText = fmt.Sprintf( "%s: %s", asOwnerName, acAPIAcct.GetMaskedNbr() )
  gcSendText.AddLine( asPhoneNbr, lsText )
  lsTotalLine := ""
  lsTotalLine2 := ""
  lsCoveredLine := ""
  lsCashLine := ""
  rfCoveredCalls = 0.0

  // Sum the covered call entries
  for _, lDV := range lcDV {
    switch( lDV.TypeText ) {
      case "cov_call":
        rfCoveredCalls += (lDV.Shares * lDV.PurchasePrice) * 100
        if abProcessTotals {
          _AddCoveredToTotalValue( lDV )
        }
      }
  }

  if rfCoveredCalls > 0 {
    lsCoveredLine = gcFont.AppendRightJustified( "  Covered Call:", ou.Commas( "$%.0f", rfCoveredCalls ), TEXT_MAX_LEN )
    lsCoveredLine += otxt.EMOJI_SMILE_WITH_TEETH
  }
  
  for _, lDV := range lcDV {
    switch( lDV.TypeText ) {
      case "value":
        switch( lDV.Symbol ) {
          case "..totalcash":
            lsCashLine = gcFont.AppendRightJustified( "  Total Cash:", ou.Commas( "$%.0f", lDV.TotalValue ), TEXT_MAX_LEN )
          case "..total":
            lsTotalLine = gcFont.AppendRightJustified( "  Account Balance:", ou.Commas( "$%.0f", lDV.TotalValue ), TEXT_MAX_LEN )
            lsTotalLine2 = gcFont.AppendRightJustified( "  -- Todays G/L", ou.Commas( "$%.0f", lDV.TodaysGainLoss ), TEXT_MAX_LEN )
            if lDV.TodaysGainLoss < 0 {
              lsTotalLine2 += otxt.EMOJI_RED_DOT + " "
            } else {
              lsTotalLine2 += otxt.EMOJI_GREEN_DOT + " "
            }
            rTotal = lDV.TotalValue
            rGL = lDV.TodaysGainLoss
        }
    }
  }

  if lsCoveredLine > "" { gcSendText.AddLine( asPhoneNbr, lsCoveredLine ) }
  if lsCashLine > "" { gcSendText.AddLine( asPhoneNbr, lsCashLine ) }
  if lsTotalLine > "" { gcSendText.AddLine( asPhoneNbr, lsTotalLine ) }
  if lsTotalLine2 > "" { gcSendText.AddLine( asPhoneNbr, lsTotalLine2 ) }

  return rTotal, rGL, rfCoveredCalls
}

//--------------------------------------------------------------
// Function: _GetBegEndValues
//--------------------------------------------------------------
func _GetBegEndValues( asAcctNbr, asPhoneNbr string, abEOM bool ) ( float64, float64, float64 ) {
  lcSDate := time.Time{}

  if abEOM {
    lcSDate, _ = time.Parse( ou.YYYY_MM_DD, (*gsCurrDate)[0:8] + "01" )
  } else {
    lcSDate, _ = time.Parse( ou.YYYY_MM_DD, *gsCurrDate )
    lcSDate = lcSDate.AddDate( 0, 0, -5 )
  }

  lsBegData := ""

  for {
    lsDate := lcSDate.Format( ou.YYYY_MM_DD )
    if lsDate > *gsCurrDate {
      return 0, 0, 0
    }
    lsInfo, err := SQLs.S_DailyBalances( lsDate, asAcctNbr )
    if err == nil && lsInfo > "" {
      lsBegData = lsInfo
      break
    }
    lcSDate = lcSDate.AddDate( 0, 0, 1 )
  }

  if lsBegData == "" { return 0, 0, 0 }

  lsEndData, err := SQLs.S_DailyBalances( *gsCurrDate, asAcctNbr )

  if err != nil {
    Log.Exception( err )
    return 0, 0, 0
  }

  lcBegAcctInfo := osch.SecuritiesHeader{}
  err = json.Unmarshal( []byte(lsBegData), &lcBegAcctInfo )

  if err != nil {
    Log.Exception( err )
    return 0, 0, 0
  }

  lcEndAcctInfo := osch.SecuritiesHeader{}
   err = json.Unmarshal( []byte(lsEndData), &lcEndAcctInfo )

  if err != nil {
    Log.Exception( err )
    return 0, 0, 0
  }

  lfCC := 0.0
  lfBegBalance := lcBegAcctInfo.Account.InitBalance.LiquidationValue
  lfEndBalance := lcEndAcctInfo.Account.CurrBalance.LiquidationValue
  lcDV, err := SQLs.S_DailyValuesSumByType( lcSDate.Format( ou.YYYY_MM_DD ), *gsCurrDate, asAcctNbr, "cov_call" )
  if err == nil && len(lcDV) > 0 { lfCC = lcDV[0].TotalValue }
  return lfBegBalance, lfEndBalance, lfCC
}

//--------------------------------------------------------------
// Function: _SendText
//--------------------------------------------------------------
func _AddOtherValues( asPhoneNbr, asEndOfText string, afBegValue, afEndValue, afCovCalls float64 ) {
  lfGL := afEndValue - afBegValue
  gcSendText.AddLine( asPhoneNbr, "" )
  gcSendText.AddLine( asPhoneNbr, fmt.Sprintf( "End Of %s:", asEndOfText ) )
  gcSendText.AddLine( asPhoneNbr, gcFont.AppendRightJustified( "  Covered Calls:", ou.Commas( "$%.0f", afCovCalls ), TEXT_MAX_LEN ) + otxt.EMOJI_SMILE_WITH_TEETH )
  gcSendText.AddLine( asPhoneNbr, gcFont.AppendRightJustified( "  Beg Balance:", ou.Commas( "$%.0f", afBegValue ), TEXT_MAX_LEN ) )
  gcSendText.AddLine( asPhoneNbr, gcFont.AppendRightJustified( "  End Balance:", ou.Commas( "$%.0f", afEndValue ), TEXT_MAX_LEN ) )
  // gcSendText.AddLine( asPhoneNbr, gcFont.AppendRightJustified( "  Total Value:", ou.Commas( "$%.0f", afEndValue ), TEXT_MAX_LEN ) )
  lsGLLine := gcFont.AppendRightJustified( fmt.Sprintf( "  -- %sly G/L:", asEndOfText ), ou.Commas( "$%.0f", lfGL ), TEXT_MAX_LEN )

  if lfGL < 0 {
    lsGLLine += otxt.EMOJI_RED_DOT + " "
  } else {
    lsGLLine += otxt.EMOJI_GREEN_DOT + " "
  }

  gcSendText.AddLine( asPhoneNbr, lsGLLine )
}

//--------------------------------------------------------------
// Function: _SendText
//--------------------------------------------------------------
func _SendText() {
  var lsPhoneList   []string

  gcSendText.ClearPhoneList()

  if *gsTestTextNbr > "" {
    lsPhoneList = append( lsPhoneList, *gsTestTextNbr )
    gcSendText.AddPhoneList( lsPhoneList )
  }

  if *gbSendText || *gsTestTextNbr > "" {
    err := gcSendText.SendSavedMessages()
    if err != nil {
      Log.Exception( err )
    }
  }
}

//--------------------------------------------------------------
// Function: _AddPositionToTotalValue
//--------------------------------------------------------------
func _AddPositionToTotalValue( acPosition osch.Position ) {
  if acPosition.Instrument.AssetType != "EQUITY" { return }

  lfValue := acPosition.MarketValue

  if lfTotal, lbFnd := gcEquityTotal[acPosition.Instrument.Symbol]; lbFnd {
    lfValue += lfTotal
  }

  gcEquityTotal[acPosition.Instrument.Symbol] = lfValue
}

//--------------------------------------------------------------
// Function: _AddCoveredToTotalValue
//--------------------------------------------------------------
func _AddCoveredToTotalValue( acDV osql.DailyValues ) {
  if len(acDV.Symbol) < 6 { return }

  lfValue := (acDV.Shares * acDV.PurchasePrice) * 100
  lsSymbol := strings.Trim( acDV.Symbol[0:6], " " )

  if lfTotal, lbFnd := gcCoveredTotal[lsSymbol]; lbFnd {
    lfValue += lfTotal
  }

  gcCoveredTotal[lsSymbol] = lfValue
}

//--------------------------------------------------------------
// Function: _AddCoveredToTotalValue
//--------------------------------------------------------------
func _GenerateCoveredCallReport() {
  type _item struct {
    Symbol       string
    PctOfEquity  float64
    Equity       float64
    Covered      float64
  }

  lcItems := make( []_item, 0 )

  for lSym, lValue := range gcEquityTotal {
    if lfCovered, lbFnd := gcCoveredTotal[lSym]; lbFnd {
      lItem := _item{ Symbol: lSym, Equity: lValue, Covered: lfCovered }
      lItem.PctOfEquity = (lItem.Covered / lItem.Equity) * 100
      lcItems = append( lcItems, lItem )
    }
  }

  // Test items...
  // lcItems = append( lcItems, _item{ Symbol: "OCUL", PctOfEquity: 7.01, Covered: 693 } )
  // lcItems = append( lcItems, _item{ Symbol: "FLNC", PctOfEquity: 4.75, Covered: 230 } )
  // lcItems = append( lcItems, _item{ Symbol: "FRMI", PctOfEquity: 4.01, Covered: 150 } )

  if len(lcItems) == 0 { return }

  // Sort in descending order
  sort.Slice( lcItems, func( i, j int ) bool {
    return lcItems[i].PctOfEquity > lcItems[j].PctOfEquity
  })

  lsLines := make( []string, len(lcItems) + 1 )
  lsLines[0] = "Covered Call Breakdown\n"

  for i, lItem := range lcItems {
    lsLines[i+1] = fmt.Sprintf( "%s : %s%%  %s", 
                                gcFont.MinWidthLeft( lItem.Symbol, 40 ), 
                                gcFont.MinWidthRight( fmt.Sprintf( "%.2f", lItem.PctOfEquity ), 40 ),
                                gcFont.MinWidthRight( ou.Commas( "$%.0f", lItem.Covered ), 40 ) )
  }

  lsPhoneNbrs, _ := Schwab.GetPhoneNumbers( "cov_call_report" )
  gcSendText.ClearPhoneList()
  gcSendText.AddPhoneList( lsPhoneNbrs )
  gcSendText.SendMsg( strings.Join( lsLines, "\n" ) )
}