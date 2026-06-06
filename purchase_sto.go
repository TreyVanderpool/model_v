//go:build ignore

package main

import (
	"flag"

  oinit "github.com/TreyVanderpool/oliver-golib/init"
  osch "github.com/TreyVanderpool/oliver-golib/schwab"
  ol "github.com/TreyVanderpool/oliver-golib/logging"
)

var (
  Log          ol.ILogger
  Schwab       *osch.SCHWAB
)

//------------------------------------------------------------------------------
// Function: main
//------------------------------------------------------------------------------
func main() {
	lsLogLevel := flag.String( "lvl", "info", "Log level (debug, info, warn, error)" )
  lsAcctHash := flag.String( "acct", "", "SCHWAB account hash number" )
	flag.Parse()

  Log = oinit.Init( oinit.INIT_LOG, lsLogLevel ).(ol.ILogger)
  Log.SetPatterns( "%M\n", "%D %-5L %T:%F:%# %M\n" )

  Schwab = oinit.Init( oinit.INIT_SCHWAB, Log ).(*osch.SCHWAB)

  Log.Info( "Starting for account *****%s : %s", "594", *lsAcctHash )
}