// inventrospect-ops supplies shell-free container health and offline maintenance.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"inventrospect/internal/backup"
	"inventrospect/internal/buildinfo"
	"inventrospect/internal/inventory"
	"inventrospect/internal/operational"
	"io"
	"os"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: inventrospect-ops health|backup|verify|restore|orphans|version [flags]")
	}
	command := os.Args[1]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	data := flags.String("data", "/data", "Data directory (must not be owned by a running application)")
	archive := flags.String("archive", "/backups/inventrospect.tar.gz", "Backup archive path")
	endpoint := flags.String("url", "http://127.0.0.1:8080/readyz", "Health endpoint")
	apply := flags.Bool("apply", false, "Remove reported orphan files older than 24 hours")
	if e := flags.Parse(os.Args[2:]); e != nil {
		return e
	}
	ctx := context.Background()
	if *archive == "-" && (command == "backup" || command == "verify" || command == "restore") {
		f, err := os.CreateTemp("", "inventrospect-stream-*")
		if err != nil {
			return err
		}
		name := f.Name()
		defer os.Remove(name)
		if command == "backup" {
			f.Close()
			if err = os.Remove(name); err != nil {
				return err
			}
			if err = backup.Create(ctx, *data, name); err != nil {
				return err
			}
			f, err = os.Open(name)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(os.Stdout, f)
			return err
		}
		_, err = io.Copy(f, os.Stdin)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		*archive = name
	}
	switch command {
	case "version":
		fmt.Println(buildinfo.String())
		return nil
	case "health":
		return operational.Probe(ctx, *endpoint)
	case "backup":
		return backup.Create(ctx, *data, *archive)
	case "verify":
		return backup.Verify(ctx, *archive)
	case "restore":
		return backup.Restore(ctx, *data, *archive)
	case "orphans":
		s, e := inventory.Open(*data)
		if e != nil {
			return e
		}
		defer s.Close()
		report, e := s.ReconcileOrphans(ctx, *apply)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(report)
	default:
		return fmt.Errorf("unknown maintenance command")
	}
}
