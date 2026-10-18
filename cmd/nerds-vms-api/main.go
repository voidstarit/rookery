// Command nerds-vms-api runs the VM management API and embedded web UI.
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

	// Keep `nerds-vms-api -addr ...` working as an alias for serve.
	return runServe(args)
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  nerds-vms-api [serve] [flags]
  nerds-vms-api init [flags]

Commands:
  serve   Start the API and web UI. This is the default command.
  init    Idempotently create/verify the thin pool and ping the VPP API socket.
  help    Show this help.

The server starts even when prerequisites are missing. Check the UI or
GET /api/status; VM creation is blocked until both prerequisites are ready.

Examples:
  sudo nerds-vms-api init -vg vg0 -thin-pool nerds-vms -vpp-socket /run/vpp/api.sock
  nerds-vms-api serve -vg vg0 -thin-pool nerds-vms -vpp-socket /run/vpp/api.sock
  nerds-vms-api serve

Flags are command-specific. Use "nerds-vms-api serve -h" or
"nerds-vms-api init -h" for details.
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
