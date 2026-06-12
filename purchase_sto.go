//go:build ignore

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	odb "github.com/TreyVanderpool/oliver-golib/db"
	oinit "github.com/TreyVanderpool/oliver-golib/init"
	ol "github.com/TreyVanderpool/oliver-golib/logging"
	osch "github.com/TreyVanderpool/oliver-golib/schwab"
	osql "github.com/TreyVanderpool/oliver-golib/sql"
	otxt "github.com/TreyVanderpool/oliver-golib/text"
	ou "github.com/TreyVanderpool/oliver-golib/utils"
)

const (
  MODEL_VERSION          string = "v"
)

var (
  Log               ol.ILogger
  Schwab            *osch.SCHWAB
  SQLs              osql.SQLs
  DB                *odb.DB
  gbUseTestData     *bool
  gbSendText        *bool
  gbSchwabLive      *bool
)

var (
  gcAcctMap           map[string]*osch.Account = make( map[string]*osch.Account )
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
  gbUseTestData = flag.Bool( "testdata", false, "use test data for account info" )
  gbSendText = flag.Bool( "sendtext", false, "send text on orders or warnings" )
  gbSchwabLive = flag.Bool( "schwab", false, "send live requests to Schwab, production mode")
  flag.Parse()

  if *gbUseTestData {
    _LoadTestData()
  }

  Log = oinit.Init( oinit.INIT_LOG, lsLogLevel ).(ol.ILogger)
  Log.SetPatterns( "%M\n", "%D %-5L %T:%-20.20F:%3# %M\n" )
  Log.SetTag( TAG{ PgmName: "pursto" } )
  DB = oinit.Init( oinit.INIT_DB, Log, lsDBName ).(*odb.DB)
  defer Log.Info( "Exiting Program" )

  Schwab = oinit.Init( oinit.INIT_SCHWAB, Log, DB ).(*osch.SCHWAB)
  SQLs = oinit.Init( oinit.INIT_SQLS, Log, DB ).(osql.SQLs)

  // Get all the covered calls to process for.
  lcCC, err := SQLs.S_VCoveredCallsAll()

  if err != nil {
    Log.Exception( err )
    return
  }

  if len( lcCC ) == 0 {
    Log.Info( "No symbols to process for, exiting program." )
    return
  }

  for _, lCC := range lcCC {
    Log.Info( "Processing for *****%s: %-6s  ExpireDays: %2d  MaxContracts: %3d  PctAbove: %5.2f",
              lCC.AccountNbr[len(lCC.AccountNbr)-3:],
              lCC.Symbol,
              lCC.ExpireDays,
              lCC.MaxContracts,
              lCC.PctAboveSymbolPrice )
    _ProcessCoveredCall( lCC )
  }
}

//--------------------------------------------------------------
// Function: _GetAccountInfo
//--------------------------------------------------------------
func _GetAccountInfo( acCC osql.VCoveredCall ) ( *osch.Account, error ) {
  if *gbUseTestData { return gcAcctMap["..."], nil }
  if lcMap, lbFnd := gcAcctMap[acCC.AccountNbr]; lbFnd { return lcMap, nil }

  Schwab.SetAccountNbr( acCC.AccountNbr )
  lcAcctInfo, err := Schwab.GetAccountInfo()

  if err != nil { return nil, Log.Exception( err ) }
  gcAcctMap[acCC.AccountNbr] = &lcAcctInfo
  return &lcAcctInfo, nil
}

//--------------------------------------------------------------
// Function: _ProcessCoveredCall
// 1) Check current open positions for any existing SELL_TO_OPEN
//    covered calls. Best way is to look in the Positions
//    and find an SettledShortQuantity with negative value
//    matching the symbol.
// 2) Get existing number of shares currently owned. Need this
//    total count to see if there are enough shares to support
//    a contract. Contract = 100 shares.
// 3) Get current equity ask/bid price to figure out which
//    strike price to use. _GetExpirationDate()
// 4) Get current option chain for this symbol to find the
//    strike price to set covered all for. _GetExpirationDate()
// 5) Create & Place Schwab order, only do this if -schwab
//    flag is present. In test mode it will execute the
//    PreviewOrder API to make sure it's good.
//--------------------------------------------------------------
func _ProcessCoveredCall( acCC osql.VCoveredCall ) ( error ) {
  lcAcct, err := _GetAccountInfo( acCC )
  if err != nil { return Log.Exception( err ) }

  // Check for any existing covered call entries on this equity.
  liExistingContractCount := _GetOpenCoveredCalls( acCC, lcAcct )

  // Get the current equity share count. Need to check and see if we
  // have enough shares to play.
  lfExistingShares := _GetCurrentSymbolShareCount( acCC, lcAcct )

  lfAvailableShares := lfExistingShares - float64(liExistingContractCount * 100)
  liAvailableContracts := int(lfAvailableShares / 100)

  Log.Info( "  -- Exist Contracts/Shares: %3d / %7.2f  Avail Shares/Contracts: %7.2f / %3d  Check: %t",
            liExistingContractCount, lfExistingShares, lfAvailableShares, liAvailableContracts, acCC.CheckValues )

  if acCC.CheckValues {
    liAvailableContracts = 1
  } else {
    if lfExistingShares <= 0 {
      Log.Info( "  ## %-6s equity not found in this account. Skipping this equity.", acCC.Symbol )
      return nil
    }

    if liAvailableContracts < 1 {
      Log.Info( "  ## No contracts available for processing... Skipping this equity." )
      return nil
    }

    if acCC.MaxContracts > 0 && liAvailableContracts > acCC.MaxContracts {
      liAvailableContracts = acCC.MaxContracts
      Log.Info( "  ## Max contract value reset available contracts to: %d", liAvailableContracts )
    }
  }

  // Find the appropiate Expire/Strike price to use for this execution.
  lcExpireDate, lcStrikePrice, err := _GetExpirationDate( acCC, liAvailableContracts )

  if lcExpireDate == nil || lcStrikePrice == nil { return nil }

  Log.Info( "USING %-6s Expire Date: %s / Strike Price: %7.2f  Est Value: %7.2f",
            acCC.Symbol,
            lcExpireDate.ExpireDate,
            lcStrikePrice.StrikePrice,
            _EstimatedValue( lcStrikePrice, liAvailableContracts ) )

  _CreateAndPlaceOrder( acCC, lcExpireDate, lcStrikePrice, liAvailableContracts )

  return nil
}

//--------------------------------------------------------------
// Function: _GetOpenCoveredCalls
//--------------------------------------------------------------
func _GetOpenCoveredCalls( acCC osql.VCoveredCall, acAcct *osch.Account ) ( int ) {
  liContractCount := 0

  for _, lPos := range acAcct.Positions {
    if lPos.Instrument.UnderlyingSymbol == acCC.Symbol &&
       lPos.Instrument.PutCall == "CALL" &&
       lPos.Instrument.AssetType == "OPTION" &&
       lPos.SettledShortQuantity < 0 {
        liContractCount += int(lPos.ShortQuantity)
       }
  }

  return liContractCount
}

//--------------------------------------------------------------
// Function: _GetCurrentSymbolShareCount
//--------------------------------------------------------------
func _GetCurrentSymbolShareCount( acCC osql.VCoveredCall, acAcct *osch.Account ) ( float64 ) {
  lfShares := 0.0

  for _, lPos := range acAcct.Positions {
    if lPos.Instrument.Symbol == acCC.Symbol &&
       lPos.Instrument.AssetType == "EQUITY" {
        lfShares += lPos.LongQuantity
       }
  }

  return lfShares
}

//--------------------------------------------------------------
// Function: _GetExpirationDate
//--------------------------------------------------------------
func _GetExpirationDate( acCC osql.VCoveredCall, aiContractCount int ) ( *osch.CStrike, *osch.CPrice, error ) {
  // Get current equity ask/bid values
  lcQuote, err := Schwab.GetSymbolQuote( acCC.Symbol )
  if err != nil { return nil, nil, Log.Exception( err ) }

  lcDate := time.Now().AddDate( 0, 0, acCC.ExpireDays + 7 )
  lcParms := make( map[string]string )
  lcParms["contractType"] = "CALL"
  // lcParms["strikeCount"] = "24"
  lcParms["includeUnderlyingQuote"] = "true"
  lcParms["toDate"] = lcDate.Format( ou.YYYY_MM_DD )

  // Get current option prices for this equity.
  lcOptions, err := Schwab.GetOptionChain( acCC.Symbol, lcParms )
  if err != nil { return nil, nil, Log.Exception( err ) }

  lfStrikePrice := lcQuote.Quote.AskPrice * ( 1 + ( acCC.PctAboveSymbolPrice / 100 ) )
  Log.Info( "  -- Equity Ask: %7.2f  Pct Above: %5.2f%%  Estimated Strike Price: %7.2f {%d}",
            lcQuote.Quote.AskPrice, acCC.PctAboveSymbolPrice, lfStrikePrice, int(lfStrikePrice) )

  Log.Info( "  -- Options: Calls: %3d  Puts: %3d  Strikes: %3d",
            len(lcOptions.Calls), len(lcOptions.Puts), len(lcOptions.Strikes) )

  lcExpireDate := &osch.CStrike{}
  lcStrikePrice := &osch.CPrice{}
  lcExpireDate = nil
  lcStrikePrice = nil

  for lExpireDate, lStrike := range lcOptions.Strikes {
    if lStrike.ExpireDays == acCC.ExpireDays {
      lcEDate := lcOptions.Strikes[lExpireDate]
      lcExpireDate = &lcEDate
    }
  }

  if lcExpireDate == nil {
    Log.Info( "  ## No Expiration Date found %d days out. Skipping this equity.", acCC.ExpireDays )
    return nil, nil, nil
  }

  // Copy each of the strike prices into an array so we can sort
  lfSPrices := make( []float64, 0 )

  for lPrice := range lcExpireDate.Prices {
    lfSPrices = append( lfSPrices, lPrice )
  }

  sort.Float64Slice( lfSPrices ).Sort()

  for i := len(lfSPrices) - 1; i >= 0; i-- {
    if lfSPrices[i] < lfStrikePrice {
      lcSP := lcExpireDate.Prices[lfSPrices[i]]
      lcStrikePrice = &lcSP
      lfStrikePrice = lfSPrices[i]
      break
    }
  }

  if lcStrikePrice == nil {
    Log.Info( "  ## No Strike Price found for %.2f. Skipping this equity.", lfStrikePrice )
    return nil, nil, nil
  }

  lfABPctDiff := ou.PctChg( lcStrikePrice.Call.Bid, lcStrikePrice.Call.Ask )

  Log.Info( "  -- CALL Ask/Bid price difference, Ask: %5.2f  Bid: %5.2f  DiffPct: %6.2f",
            lcStrikePrice.Call.Ask, lcStrikePrice.Call.Bid, lfABPctDiff )

  if ! acCC.CheckValues {
    if lfABPctDiff > 10 {
      lsText := fmt.Sprintf( "Purchase Covered Calls:\n--- Warning - Ask/Bid Spread too high.\n%-6s : %s : %.2f\nAsk: %5.2f  Bid: %5.2f  Diff:%6.2f%%\nAccount: *****%s\nContract Count: %d\nReview and manually place order!",
                            acCC.Symbol, 
                            lcExpireDate.ExpireDate, 
                            lcStrikePrice.StrikePrice, 
                            lcStrikePrice.Call.Ask, 
                            lcStrikePrice.Call.Bid, 
                            lfABPctDiff,
                            acCC.AccountNbr[len(acCC.AccountNbr)-3:],
                            aiContractCount )
      _SendText( "ask_bid_pct", lsText )
      return nil, nil, nil
    }
  }

  return lcExpireDate, lcStrikePrice, nil
}

//--------------------------------------------------------------
// Function: _CreateAndPlaceOrder
//--------------------------------------------------------------
func _CreateAndPlaceOrder( acCC osql.VCoveredCall, acExpireDate *osch.CStrike, acStrikePrice *osch.CPrice, aiContractCount int ) ( error ) {
  lsSymbol := osch.CreateOptionSymbol( acCC.Symbol, acExpireDate.ExpireDate, "C", acStrikePrice.StrikePrice )
  lcOrder := Schwab.NewMarketOrder( lsSymbol, float64(aiContractCount), osch.Instruction(osch.SELL_TO_OPEN), osch.Duration(osch.DAY) )
  lcOrder.OrderLegCollection[0].Instrument.AssetType = "OPTION"
  lsMsg := ""
  lfEstValue := _EstimatedValue( acStrikePrice, aiContractCount )
  // lfEstValue = ( acStrikePrice.Call.Ask + acStrikePrice.Call.Bid ) / 2
  // lfEstValue *= ( float64(aiContractCount) * 100 )

  if acCC.CheckValues {
      lsMsg = fmt.Sprintf( "Purchase Covered Calls:\nCHECK VALUE...\n%-6s : %s : %.2f\nEstimated Value: $%s",
                           acCC.Symbol, 
                           acExpireDate.ExpireDate, 
                           acStrikePrice.StrikePrice, 
                           ou.Commas( "%.0f", lfEstValue ) )
      _SendText( "preview_order", lsMsg )
      return nil
  }

  // Not LIVE, issue a Preview Order and send text...
  if ! *gbSchwabLive {
    lcResp := Schwab.PreviewOrder( lcOrder )
    if lcResp.Error != nil {
      Log.Info( "REQ : %s", Schwab.HTTP.RequestBody )
      Log.Info( "RESP: %+v", lcResp )
    } else {
      lsMsg = fmt.Sprintf( "Purchase Covered Calls:\nPreview Order was successful...\n%-6s : %s : %.2f\nAccount: *****%s\nContract Count: %d\nEstimated Value: $%s",
                           acCC.Symbol, 
                           acExpireDate.ExpireDate, 
                           acStrikePrice.StrikePrice, 
                           acCC.AccountNbr[len(acCC.AccountNbr)-3:],
                           aiContractCount,
                           ou.Commas( "%.0f", lfEstValue ) )
      _SendText( "preview_order", lsMsg )
      Log.Info( "RESP: %s", string(Schwab.HTTP.ResponseBody) )
    }

    return nil
  }

  lcResp := Schwab.PlaceOrder( lcOrder )

  if lcResp.Error != nil {
    Log.Info( "REQ : %s", Schwab.HTTP.RequestBody )
    Log.Info( "RESP: %+v", lcResp )
    lsMsg = fmt.Sprintf( "Purchase Covered Calls:\nPlace Order FAILED!!!\n%-6s : %s : %.2f\nAccount: *****%s\nContract Count: %d\n%s",
                         acCC.Symbol, 
                         acExpireDate.ExpireDate, 
                         acStrikePrice.StrikePrice, 
                         acCC.AccountNbr[len(acCC.AccountNbr)-3:],
                         aiContractCount,
                         lcResp.Error )
  } else {
    lsMsg = fmt.Sprintf( "Purchase Covered Calls:\nPlace Order SUCCESSFUL!!!\n%-6s : %s : %.2f\nAccount: *****%s\nContract Count: %d",
                         acCC.Symbol, 
                         acExpireDate.ExpireDate, 
                         acStrikePrice.StrikePrice, 
                         acCC.AccountNbr[len(acCC.AccountNbr)-3:],
                         aiContractCount )
  }

  _SendText( "place_order", lsMsg )

  return nil
}

//--------------------------------------------------------------
// Function: _SendText
//--------------------------------------------------------------
func _SendText( asTextName, asTextMsg string ) {

  // We assume 'Schwab' object has already been initialized and
  // the account number is previously set.
  lsPhoneList, err := Schwab.GetVersionPhoneNumbers( MODEL_VERSION, asTextName )

  if err != nil {
    Log.Exception( err )
    return
  }

  if len( lsPhoneList ) == 0 {
    Log.Error( "Unable to send text message, no phone numbers found for '%s:%s'", MODEL_VERSION, asTextName )
    Log.Error( strings.Replace( asTextMsg, "\n", "\\n", -1 ) )
    return
  }

  lcText := oinit.Init( oinit.INIT_TEXT ).(*otxt.SendText)
  lcText.ClearPhoneList()
  lcText.AddPhoneList( lsPhoneList )

  if *gbSendText {
    lcText.SendMsg( asTextMsg )
  }
}

//--------------------------------------------------------------
// Function: _EstimatedValue
//--------------------------------------------------------------
func _EstimatedValue( acStrikePrice *osch.CPrice, aiContractCount int ) ( float64 ) {
  lfEstValue := ( acStrikePrice.Call.Ask + acStrikePrice.Call.Bid ) / 2
  lfEstValue *= ( float64(aiContractCount) * 100 )
  return lfEstValue
}

//--------------------------------------------------------------
// Function: _LoadTestData
//--------------------------------------------------------------
func _LoadTestData() {
  lsText := `{ "securitiesAccount": { "type": "CASH", "accountNumber": "...", "roundTrips": 0, "isDayTrader": false, "isClosingOnlyRestricted": false, "pfcbFlag": false, "positions": [ { "shortQuantity": 1.0, "averagePrice": 0.9434, "currentDayProfitLoss": 6.03, "currentDayProfitLossPercentage": 63.27, "longQuantity": 0.0, "settledLongQuantity": 0.0, "settledShortQuantity": -1.0, "instrument": { "assetType": "OPTION", "cusip": "0WMT..F560121000", "symbol": "WMT 260605C00121000", "description": "WALMART INC 06/05/2026 $121 Call", "netChange": -0.0553, "type": "VANILLA", "putCall": "CALL", "underlyingSymbol": "WMT" }, "marketValue": -3.5, "maintenanceRequirement": 0.0, "averageShortPrice": 0.95, "taxLotAverageShortPrice": 0.9434, "shortOpenProfitLoss": 90.84, "previousSessionShortQuantity": 1.0, "currentDayCost": 0.0 }, { "shortQuantity": 0.0, "averagePrice": 120.1, "currentDayProfitLoss": -273.900000000001, "currentDayProfitLossPercentage": -1.44, "longQuantity": 166.0, "settledLongQuantity": 166.0, "settledShortQuantity": 0.0, "instrument": { "assetType": "EQUITY", "cusip": "931142103", "symbol": "WMT", "netChange": -1.7 }, "marketValue": 18749.7, "maintenanceRequirement": 0.0, "averageLongPrice": 120.1, "taxLotAverageLongPrice": 120.1, "longOpenProfitLoss": -1186.899999999999, "previousSessionLongQuantity": 166.0, "currentDayCost": 0.0 } ], "initialBalances": { "accruedInterest": 0.0, "cashAvailableForTrading": 11687.43, "cashAvailableForWithdrawal": 11687.43, "cashBalance": 11687.43, "bondValue": 0.0, "cashReceipts": 0.0, "liquidationValue": 30701.5, "longOptionMarketValue": 0.0, "longStockValue": 19023.6, "moneyMarketFund": 0.0, "mutualFundValue": 11687.43, "shortOptionMarketValue": -9.53, "shortStockValue": -9.53, "isInCall": false, "unsettledCash": 0.0, "cashDebitCallValue": 0.0, "pendingDeposits": 0.0, "accountValue": 30701.5 }, "currentBalances": { "accruedInterest": 0.0, "cashBalance": 11687.43, "cashReceipts": 0.0, "longOptionMarketValue": 0.0, "liquidationValue": 30433.63, "longMarketValue": 18749.7, "moneyMarketFund": 0.0, "savings": 0.0, "shortMarketValue": 0.0, "pendingDeposits": 0.0, "mutualFundValue": 0.0, "bondValue": 0.0, "shortOptionMarketValue": -3.5, "cashAvailableForTrading": 11687.43, "cashAvailableForWithdrawal": 11687.43, "cashCall": 0.0, "longNonMarginableMarketValue": 11687.43, "totalCash": 11687.43, "cashDebitCallValue": 0.0, "unsettledCash": 0.0 }, "projectedBalances": { "cashAvailableForTrading": 11687.43, "cashAvailableForWithdrawal": 11687.43 } }, "aggregatedBalance": { "currentLiquidationValue": 30433.63, "liquidationValue": 30433.63 }}`
  var lcData  osch.SecuritiesHeader

  err := json.Unmarshal( []byte(lsText), &lcData )
  if err != nil { panic( err ) }
  gcAcctMap["..."] = &lcData.Account
}
