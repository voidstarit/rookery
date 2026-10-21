// Command rookery runs the VM management API and embedded web UI.
//
// It also has an init subcommand that prepares the host prerequisites.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "init":
			return runInit(args[1:])
		case "serve":
			return runServe(args[1:])
		case "help", "-h", "--help":
			printUsage(os.Stdout)
			return nil
		default:
			if !strings.HasPrefix(args[0], "-") {
				printUsage(os.Stderr)
				return fmt.Errorf("unknown command %q", args[0])
			}
		}
	}

	// Keep `rookery -addr ...` working as an alias for serve.
	return runServe(args)
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  rookery [serve] [flags]
  rookery init [flags]

Commands:
  serve   Start the API and web UI. This is the default command.
  init    Idempotently create/verify the thin pool and ping the VPP API socket.
  help    Show this help.

The server starts even when prerequisites are missing. Check the UI or
GET /api/status; VM creation is blocked until both prerequisites are ready.

Examples:
  sudo rookery init -vg vg0 -thin-pool nerds-vms -vpp-socket /run/vpp/api.sock
  rookery serve -vg vg0 -thin-pool nerds-vms -vpp-socket /run/vpp/api.sock
  rookery serve

Flags are command-specific. Use "rookery serve -h" or
"rookery init -h" for details.
`)
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(os.Stdout)
			fs.Usage()
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return nil
}
