package main

import (
	"flag"
	"fmt"
	"math/rand"

  ou "github.com/TreyVanderpool/oliver-golib/utils"
)

func main() {
  lfStartingPrice := flag.Float64( "sp", 118.25, "The starting price of the stock" )
  lfSTOPct := flag.Float64( "sto", 0.2332, "Estimated WMT STO percent" )
  liShares := flag.Int( "sh", 100, "Number of shares" )
  flag.Parse()

  fmt.Printf( "Starting Price: $%.2f   Shares: %d  STO Pct: %.4f\n\n", *lfStartingPrice, *liShares, *lfSTOPct )

  fmt.Printf( " Base $  Shares  Options  PctChg    Run $    Value  Gain/Loss  Value%%   Real G/L    STO $     Cash\n")
  fmt.Printf( "-------  ------  -------  ------  -------  -------  ---------  ------  ---------  -------  -------\n" )
  
  lfBasePrice := *lfStartingPrice
  lfRunningPrice := lfBasePrice
  lsRealizedGL := ""
  lfRealizedGL := 0.0
  lfCash := 0.0
  liOptions := 0
  liRunningShares := *liShares

  for range 52 {
    // Get random number between -6% and +6%
    lfPctChg := rand.Float64() * 0.12 - 0.06

    lfRunningPrice = lfRunningPrice * ( 1 + lfPctChg )
    lfGL := ( lfRunningPrice - *lfStartingPrice ) * float64(liRunningShares)
    lfValuePctChg := ( lfRunningPrice - lfBasePrice ) / lfBasePrice

    if lfRealizedGL != 0.0 {
      lsRealizedGL = fmt.Sprintf( "%.2f", lfRealizedGL )
    } else {
      lsRealizedGL = ""
    }

    liOptions = liRunningShares / 100
    lfSTOValue := (lfBasePrice * *lfSTOPct) * float64(liOptions)
    lfCash += lfSTOValue
    
    fmt.Printf( "%7.2f  %6d  %7d  %5.2f%%  %7.2f  %7s  %9s  %6.2f  %9s  %7.0f  %7.0f\n", 
                lfBasePrice,
                liRunningShares,
                liOptions,
                lfPctChg*100,
                lfRunningPrice,
                ou.Commas( "%.0f", lfRunningPrice*float64(liRunningShares) ),
                ou.Commas( "%.0f", lfGL ),
                lfValuePctChg*100,
                lsRealizedGL,
                lfSTOValue,
                lfCash )

    if lfValuePctChg < -0.01 {
      lfRealizedGL = (lfRunningPrice*float64(liRunningShares)) - (lfBasePrice*float64(liRunningShares))
      lfBasePrice = lfRunningPrice
    } else {
      lfRealizedGL = 0.0
    }

    liNewShares := 0
    liNewShares = int(lfCash / lfRunningPrice)
    lfCash -= float64(liNewShares) * lfRunningPrice
    liRunningShares += liNewShares
  }
}