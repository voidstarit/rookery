// Command rookery runs the VM management API and embedded web UI.
//
// It also has init and create-client-cert subcommands that prepare host
// prerequisites and issue mTLS client certificates.
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
		case "create-client-cert", "issue-client-cert":
			return runCreateClientCert(args[1:])
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
  rookery create-client-cert [flags] <name>

Commands:
  serve               Start the API and web UI. This is the default command.
  init                Idempotently create/verify the thin pool and ping the VPP API socket.
  create-client-cert  Issue an mTLS client certificate signed by the Rookery CA.
                      The alias issue-client-cert is also accepted.
  help                Show this help.

The server starts even when prerequisites are missing. Check the UI or
GET /api/status; VM creation is blocked until both prerequisites are ready.

Examples:
  sudo rookery init -vg vg0 -thin-pool rookery -vpp-socket /run/vpp/api.sock
  rookery serve -vg vg0 -thin-pool rookery -vpp-socket /run/vpp/api.sock
  rookery serve
  sudo rookery create-client-cert -config /etc/rookery/config.yaml \
    -out-dir ./ops-laptop ops-laptop

Flags are command-specific. Use "rookery serve -h",
"rookery init -h", or "rookery create-client-cert -h" for details.
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
