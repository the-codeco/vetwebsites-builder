package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/config"
	"webbuilder/internal/platform"
)

const adoptUsage = `Usage: server -adopt-single-site -owner you@example.com [-media-from ./media] [-backups-from ./backups] [-yes]

Turns the database of a single website (the program run without PLATFORM=true) into a platform's, with that website as
the first customer's. Its tables move into their own schema (site_1); its users become platform accounts with the same
ids and passwords. The owner gets the website; administrators become platform administrators.

Without -yes nothing is changed: the report says what would happen. MAKE A BACKUP OF THE DATABASE FIRST
(docker compose exec db pg_dump -U webbuilder webbuilder > before.sql), stop the old program, and run this once. Then
start the program with PLATFORM=true, SECRET_KEY and BASE_URL set.
`

// adopt is "server -adopt-single-site": it returns the program's exit code.
func adopt(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, args []string) int {
	fs := flag.NewFlagSet("adopt-single-site", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, adoptUsage) }
	owner := fs.String("owner", "", "email of the user who becomes the website's owner")
	media := fs.String("media-from", "", "the old media folder, copied to DATA_DIR/media/1")
	backups := fs.String("backups-from", "", "the old backups folder, copied to DATA_DIR/backups/1")
	yes := fs.Bool("yes", false, "make the changes (without it, only report them)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *owner == "" {
		fs.Usage()
		return 2
	}
	rep, err := platform.Adopt(ctx, cfg, pool, platform.AdoptOptions{Owner: *owner, MediaFrom: *media, BackupsFrom: *backups, Apply: *yes})
	if rep.SiteName != "" {
		fmt.Printf("Website:        %s\n", rep.SiteName)
		fmt.Printf("Owner:          %s\n", rep.Owner)
		fmt.Printf("Administrators: %s\n", strings.Join(rep.Admins, ", "))
		if rep.PublicURL != "" {
			fmt.Printf("Address:        %s\n", rep.PublicURL)
		}
		fmt.Printf("Tables moved:   %d\n", rep.Tables)
		if rep.MediaFiles > 0 {
			fmt.Printf("Pictures:       %d file(s) copied\n", rep.MediaFiles)
		}
	}
	for _, n := range rep.Notes {
		fmt.Println("Note:", n)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Stopped:", err)
		return 1
	}
	if rep.Applied {
		fmt.Println("Done. Start the program with PLATFORM=true (and SECRET_KEY, BASE_URL set), log in with your old password, and set up publishing.")
	} else {
		fmt.Println("Nothing was changed. Run again with -yes to make these changes.")
	}
	return 0
}
