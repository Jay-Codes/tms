// Command seed builds tenant data straight against Postgres for the Phase 8
// load pass and the JJnE Rentals demo.
//
// Usage:
//
//	seed                     # load-test org, default size (5 properties / 50 units / 40 renters)
//	seed -units 20 -renters 15
//	seed -reset              # delete the load-test org's data, then re-seed
//	seed -demo               # top the JJnE Rentals demo org up instead
//
// The seeder talks to the database the API talks to (DATABASE_URL) and uses
// the same rules the handlers use — contract.Generate for schedules,
// payment.Allocate for money, notify.Render for message bodies — so a seeded
// org is indistinguishable from one built through the API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"tms/backend/internal/config"
	"tms/backend/internal/db"
	"tms/backend/internal/seed"
	"tms/backend/internal/validate"
)

func main() {
	var (
		orgName   = flag.String("org-name", seed.DefaultLoadTestOptions().OrgName, "load-test organisation name")
		units     = flag.Int("units", seed.DefaultLoadTestOptions().Units, "number of units to seed")
		renters   = flag.Int("renters", seed.DefaultLoadTestOptions().Renters, "number of renters to seed")
		props     = flag.Int("properties", seed.DefaultLoadTestOptions().Properties, "number of properties to seed")
		reset     = flag.Bool("reset", false, "delete the target org's data first (that org only)")
		demoOnly  = flag.Bool("demo", false, "seed the JJnE Rentals demo org instead of the load-test org")
		resetOnly = flag.Bool("reset-only", false, "with -reset: delete and stop, do not re-seed")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		logger.Error("cannot reach the database", "dsn", redact(cfg.DatabaseURL), "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	s := seed.New(pool, cfg, logger)

	if *demoOnly {
		sum, err := s.Demo(ctx)
		if err != nil {
			logger.Error("demo seed failed", "error", err)
			os.Exit(1)
		}
		printSummary(sum, "JJnE Rentals demo")
		return
	}

	opts := seed.DefaultLoadTestOptions()
	opts.OrgName = *orgName
	opts.Units = *units
	opts.Renters = *renters
	opts.Properties = *props
	if opts.Contracts > opts.Renters {
		opts.Contracts = opts.Renters
	}
	if opts.Contracts > opts.Units {
		opts.Contracts = opts.Units
	}

	if *reset {
		res, err := s.Reset(ctx, validate.Slugify(opts.OrgName))
		switch {
		case err != nil && strings.Contains(err.Error(), "no org with slug"):
			logger.Info("nothing to reset", "org", opts.OrgName)
		case err != nil:
			logger.Error("reset failed", "error", err)
			os.Exit(1)
		default:
			logger.Info("reset complete", "org", res.OrgName, "rows", res.Rows, "users", res.Users)
		}
		if *resetOnly {
			return
		}
	}

	sum, err := s.LoadTest(ctx, opts)
	if err != nil {
		logger.Error("seed failed", "error", err)
		os.Exit(1)
	}
	printSummary(sum, "load-test fixture")
}

func printSummary(sum *seed.Summary, title string) {
	fmt.Printf("\n=== %s ===\n", title)
	fmt.Printf("org           %s (%s)\n", sum.OrgName, sum.OrgID)
	if sum.OwnerEmail != "" {
		fmt.Printf("owner         %s\n", sum.OwnerEmail)
	}
	fmt.Printf("created       %d propert(ies), %d unit(s), %d renter(s)\n",
		sum.Properties, sum.Units, sum.Renters)
	fmt.Printf("              %d contract(s), %d schedule(s), %d payment(s) (%d reversed)\n",
		sum.Contracts, sum.Schedules, sum.Payments, sum.Reversed)
	fmt.Printf("              %d link request(s), %d notification row(s)\n",
		sum.LinkRequests, sum.Notifications)
	// Part 2 (PLAN2 Phase 15): the rows the expense ledger, the reports and
	// the credit screens are read from.
	fmt.Printf("              %d expense(s) over %d months (%d voided)\n",
		sum.Expenses, seed.ExpenseMonths, sum.ExpensesVoided)
	fmt.Printf("              %d SMS credit(s) on the org, %d message(s) held for credit\n",
		sum.Credits, sum.Held)
	for _, n := range sum.Notes {
		fmt.Printf("note          %s\n", n)
	}
	if len(sum.UnitCodes) > 0 {
		fmt.Printf("\nunit codes (scan URL: {BASE}/enduser/u/{code})\n")
		for _, c := range sum.UnitCodes {
			who := c.Renter
			if who == "" {
				who = c.Status
			}
			fmt.Printf("  %-28s %-10s %-12s %s\n", c.Property, c.Unit, c.Code, who)
		}
	}
	fmt.Println()
}

// redact hides the password in a DSN before it reaches a log line.
func redact(dsn string) string {
	at := strings.LastIndex(dsn, "@")
	scheme := strings.Index(dsn, "://")
	if at < 0 || scheme < 0 || at < scheme {
		return dsn
	}
	return dsn[:scheme+3] + "***" + dsn[at:]
}
