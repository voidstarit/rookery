package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/config"
	"github.com/voidstarit/rookery/internal/pki"
	"github.com/voidstarit/rookery/internal/store"
)

func runCreateClientCert(args []string) error {
	file, err := config.Load(config.Path(args))
	if err != nil {
		return err
	}
	apiCfg := config.API{}
	if file.API != nil {
		apiCfg = *file.API
	}

	fs := flag.NewFlagSet("create-client-cert", flag.ContinueOnError)
	fs.String("config", config.Path(args), "YAML configuration file (or ROOKERY_CONFIG)")
	dbPath := fs.String("db", config.EnvString(apiCfg.DB, "ROOKERY_DB", "rookery.db"), "SQLite database path (must match the running server)")
	pkiDir := fs.String("pki-dir", config.EnvString(apiCfg.PKIDir, "ROOKERY_PKI_DIR", "/var/lib/rookery/pki"), "directory containing the existing Rookery CA")
	days := fs.Int("days", 365, "certificate validity in days")
	outDir := fs.String("out-dir", ".", "directory for the generated certificate files")
	force := fs.Bool("force", false, "overwrite existing output files")

	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: rookery create-client-cert [flags] <name>\n\n"+
			"Issue an mTLS client certificate signed by the Rookery CA. Writes\n"+
			"<name>.crt, <name>.key, and <name>-ca.crt. The private key is written\n"+
			"with mode 0600 and is never stored by Rookery. The command must use\n"+
			"the same database and PKI directory as the running server.\n\nFlags:\n")
		fs.PrintDefaults()
	}

	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(os.Stdout)
			fs.Usage()
		}
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: rookery create-client-cert [flags] <name>")
	}
	name := strings.TrimSpace(fs.Arg(0))
	if name == "" {
		return errors.New("client name is required")
	}
	if *days <= 0 {
		return errors.New("-days must be positive")
	}
	if *days > 3650 {
		return errors.New("-days cannot exceed 3650")
	}
	if strings.TrimSpace(*outDir) == "" {
		return errors.New("-out-dir must not be empty")
	}

	outPath, err := filepath.Abs(strings.TrimSpace(*outDir))
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	stem := clientCertificateStem(name)
	certPath := filepath.Join(outPath, stem+".crt")
	keyPath := filepath.Join(outPath, stem+".key")
	caPath := filepath.Join(outPath, stem+"-ca.crt")
	for _, path := range []string{certPath, keyPath, caPath} {
		if _, err := os.Stat(path); err == nil {
			if !*force {
				return fmt.Errorf("%s already exists; use -force to overwrite", path)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("check output %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(outPath, 0o700); err != nil {
		return fmt.Errorf("create output directory %s: %w", outPath, err)
	}
	dbInfo, err := os.Stat(*dbPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("database %s does not exist; pass -db matching the running server", *dbPath)
		}
		return fmt.Errorf("check database %s: %w", *dbPath, err)
	}
	if dbInfo.IsDir() {
		return fmt.Errorf("database path %s is a directory", *dbPath)
	}
	if dbInfo.Size() == 0 {
		return fmt.Errorf("database %s is empty; pass -db matching the running server", *dbPath)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	issuer, err := pki.NewClientIssuer(*pkiDir, st, zerolog.Nop())
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	issued, err := issuer.IssueClientCertificate(ctx, name, *days)
	if err != nil {
		return fmt.Errorf("issue client certificate: %w", err)
	}

	outputs := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{certPath, issued.CertificatePEM, 0o644},
		{keyPath, issued.PrivateKeyPEM, 0o600},
		{caPath, issued.CAPEM, 0o644},
	}
	for _, output := range outputs {
		if err := writeClientCertificateFile(output.path, output.data, output.mode, *force); err != nil {
			return fmt.Errorf("write %s: %w", output.path, err)
		}
	}

	fmt.Printf("Issued client certificate %q\n", issued.Name)
	fmt.Printf("  serial:  %s\n", issued.Serial)
	fmt.Printf("  expires: %s\n\n", issued.NotAfter.Format(time.RFC3339))
	fmt.Printf("  client certificate: %s\n", certPath)
	fmt.Printf("  client private key: %s\n", keyPath)
	fmt.Printf("  CA certificate:     %s\n", caPath)
	fmt.Println("\nThe private key is not stored by Rookery; keep it secret.")
	return nil
}

func writeClientCertificateFile(path string, data []byte, mode os.FileMode, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	file, err := os.OpenFile(path, flags, mode)
	if err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		if !force {
			_ = os.Remove(path)
		}
		return err
	}
	return file.Close()
}

func clientCertificateStem(name string) string {
	var stem strings.Builder
	lastDash := false
	for _, char := range strings.TrimSpace(name) {
		safe := char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' ||
			char == '.' || char == '_' || char == '-'
		if safe {
			stem.WriteRune(char)
			lastDash = false
			continue
		}
		if !lastDash {
			stem.WriteByte('-')
			lastDash = true
		}
	}
	value := strings.Trim(stem.String(), ".-_")
	if value == "" {
		return "client"
	}
	return value
}
