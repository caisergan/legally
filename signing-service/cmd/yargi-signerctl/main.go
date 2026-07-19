// Command yargi-signerctl is the local administrative tool for exact PKCS#11
// credential enrollment (plan §5.2). A PIN is read only from a no-echo
// interactive TTY; it is never accepted via flag, environment, file, or pipe.
package main

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/caisergan/legally/signing-service/internal/config"
	"github.com/caisergan/legally/signing-service/internal/credentials"
	"github.com/caisergan/legally/signing-service/internal/persistence"
	"github.com/caisergan/legally/signing-service/internal/token"
)

const version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "version", "-version", "--version":
		fmt.Println(version)
	case "modules":
		modulesCommand(args[1:])
	case "credentials":
		credentialsCommand(args[1:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `yargi-signerctl <command>

  modules list [--local-details]
  credentials probe   --module <alias> [--local-details]
  credentials enroll  --module <alias> --slot <id> --certificate-sha256 <hex> --key-id <hex>
  credentials list    [--local-details]
  credentials verify  <service-credential-id>
  credentials disable <service-credential-id>
  credentials enable  <service-credential-id>
  credentials remove  <service-credential-id>

Environment: SIGNERD_DATABASE, SIGNERD_CONFIG (module allowlist), SIGNERD_MODE.`)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func modulesCommand(args []string) {
	if len(args) == 0 || args[0] != "list" {
		usage()
		os.Exit(2)
	}
	flags := flag.NewFlagSet("modules list", flag.ExitOnError)
	localDetails := flags.Bool("local-details", false, "show full module paths and digests")
	_ = flags.Parse(args[1:])

	allowlist := mustAllowlist()
	for _, entry := range allowlist.Modules {
		if *localDetails {
			fmt.Printf("%s\tmodes=%s\tpath=%s\tsha256=%s\n", entry.Alias, joinModes(entry.Modes), entry.Path, entry.SHA256)
		} else {
			fmt.Printf("%s\tmodes=%s\n", entry.Alias, joinModes(entry.Modes))
		}
	}
}

func credentialsCommand(args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "probe":
		credentialsProbe(args[1:])
	case "enroll":
		credentialsEnroll(args[1:])
	case "list":
		credentialsList(args[1:])
	case "verify":
		credentialsVerify(args[1:])
	case "disable":
		credentialsSetEnabled(args[1:], false)
	case "enable":
		credentialsSetEnabled(args[1:], true)
	case "remove":
		credentialsRemove(args[1:])
	default:
		usage()
		os.Exit(2)
	}
}

func credentialsProbe(args []string) {
	flags := flag.NewFlagSet("credentials probe", flag.ExitOnError)
	moduleAlias := flags.String("module", "", "allowlisted module alias")
	localDetails := flags.Bool("local-details", false, "show full token serials and CKA_IDs")
	_ = flags.Parse(args)
	if *moduleAlias == "" {
		fatalf("--module is required")
	}

	backend := mustBackend(*moduleAlias)
	defer backend.Close()

	slots, err := backend.Slots()
	if err != nil {
		fatalf("enumerate slots: %v", err)
	}
	for _, slot := range slots {
		serial := maskTail(slot.TokenSerial, 4, *localDetails)
		fmt.Printf("slot=%d serial=%s label=%s\n", slot.ID, serial, slot.TokenLabel)
		objects, err := backend.Objects(slot.ID)
		if err != nil {
			fmt.Printf("  objects: error: %v\n", err)
			continue
		}
		for _, cert := range objects.Certificates {
			fp := cert.Fingerprint()
			fmt.Printf("  certificate sha256=%s cka_id=%s\n", hex.EncodeToString(fp[:]), maskTail(hex.EncodeToString(cert.CKAID), 4, *localDetails))
		}
		for _, key := range objects.PrivateKeys {
			fmt.Printf("  private-key cka_id=%s\n", maskTail(hex.EncodeToString(key.CKAID), 4, *localDetails))
		}
	}
}

func credentialsEnroll(args []string) {
	flags := flag.NewFlagSet("credentials enroll", flag.ExitOnError)
	moduleAlias := flags.String("module", "", "allowlisted module alias")
	slot := flags.Uint("slot", 0, "slot ID")
	certSHA := flags.String("certificate-sha256", "", "certificate SHA-256 fingerprint (hex)")
	keyID := flags.String("key-id", "", "private key CKA_ID (hex)")
	_ = flags.Parse(args)
	slotSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "slot" {
			slotSet = true
		}
	})
	if *moduleAlias == "" || *certSHA == "" || *keyID == "" || !slotSet {
		fatalf("--module, --slot, --certificate-sha256, and --key-id are all required")
	}

	fingerprint, err := hexToArray(*certSHA)
	if err != nil {
		fatalf("invalid --certificate-sha256: %v", err)
	}
	ckaID, err := hex.DecodeString(*keyID)
	if err != nil {
		fatalf("invalid --key-id: %v", err)
	}

	entry := mustModuleEntry(*moduleAlias)
	backend := mustBackendFromEntry(entry)
	defer backend.Close()

	db := mustJournal()
	defer db.Close()
	registry := credentials.NewRegistry(db.DB)

	params := credentials.EnrollParams{
		ModuleAlias:       entry.Alias,
		ModuleSHA256:      entry.SHA256,
		SlotID:            *slot,
		CertificateSHA256: fingerprint,
		KeyCKAID:          ckaID,
	}
	cred, err := registry.Enroll(context.Background(), backend, params, readPIN)
	if err != nil {
		fatalf("enroll failed: %v", err)
	}
	fmt.Printf("enrolled credential %s (fingerprint %s)\n", cred.ServiceCredentialID, cred.CertificateSHA256)
}

func credentialsList(args []string) {
	flags := flag.NewFlagSet("credentials list", flag.ExitOnError)
	localDetails := flags.Bool("local-details", false, "show full token serials and subjects")
	_ = flags.Parse(args)

	db := mustJournal()
	defer db.Close()
	creds, err := credentials.NewRegistry(db.DB).List(context.Background())
	if err != nil {
		fatalf("list credentials: %v", err)
	}
	for _, cred := range creds {
		status := "available"
		if !cred.Enabled {
			status = "disabled"
		}
		fmt.Printf("%s\t%s\tserial=%s\tsubject=%s\tnot_after=%s\n",
			cred.ServiceCredentialID, status,
			maskTail(cred.TokenSerial, 4, *localDetails),
			subjectDisplay(cred.CertificateDER, *localDetails),
			cred.NotAfter.Format("2006-01-02"))
	}
}

func credentialsVerify(args []string) {
	id := singleID(args, "verify")
	db := mustJournal()
	defer db.Close()
	registry := credentials.NewRegistry(db.DB)

	cred, err := registry.Get(context.Background(), id)
	if err != nil {
		fatalf("verify: %v", err)
	}
	entry := mustModuleEntry(cred.ModuleAlias)
	backend := mustBackendFromEntry(entry)
	defer backend.Close()

	if err := registry.Verify(context.Background(), id, backend, readPIN); err != nil {
		fatalf("verification failed: %v", err)
	}
	fmt.Printf("credential %s verified\n", id)
}

func credentialsSetEnabled(args []string, enabled bool) {
	action := "enable"
	if !enabled {
		action = "disable"
	}
	id := singleID(args, action)
	db := mustJournal()
	defer db.Close()
	if err := credentials.NewRegistry(db.DB).SetEnabled(context.Background(), id, enabled); err != nil {
		fatalf("%s failed: %v", action, err)
	}
	fmt.Printf("credential %s %sd\n", id, action)
}

func credentialsRemove(args []string) {
	id := singleID(args, "remove")
	fmt.Printf("Remove credential %s? This cannot be undone. Type 'yes' to confirm: ", id)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	if strings.TrimSpace(line) != "yes" {
		fatalf("aborted")
	}
	db := mustJournal()
	defer db.Close()
	if err := credentials.NewRegistry(db.DB).Remove(context.Background(), id); err != nil {
		if errors.Is(err, credentials.ErrCredentialReferenced) {
			fatalf("cannot remove: credential is referenced by unreconciled jobs")
		}
		fatalf("remove failed: %v", err)
	}
	fmt.Printf("credential %s removed\n", id)
}

// readPIN reads a PIN from a no-echo interactive terminal only.
func readPIN() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("PIN must be entered on an interactive terminal, not a pipe or file")
	}
	fmt.Fprint(os.Stderr, "Token PIN: ")
	pin, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(pin), nil
}

func mustAllowlist() config.ModuleAllowlist {
	path := strings.TrimSpace(os.Getenv("SIGNERD_CONFIG"))
	if path == "" {
		fatalf("SIGNERD_CONFIG (module allowlist path) is required")
	}
	allowlist, err := config.LoadModuleAllowlist(path)
	if err != nil {
		fatalf("load module allowlist: %v", err)
	}
	return allowlist
}

func mustModuleEntry(alias string) config.ModuleEntry {
	entry, ok := mustAllowlist().Lookup(alias)
	if !ok {
		fatalf("module alias %q is not allowlisted", alias)
	}
	if err := token.VerifyModule(entry, requiredMode()); err != nil {
		fatalf("module verification failed: %v", err)
	}
	return entry
}

func mustBackend(alias string) token.Backend {
	return mustBackendFromEntry(mustModuleEntry(alias))
}

func mustBackendFromEntry(entry config.ModuleEntry) token.Backend {
	backend, err := token.NewBackend(entry.Path)
	if err != nil {
		fatalf("open pkcs11 module: %v", err)
	}
	return backend
}

func mustJournal() *persistence.DB {
	path := strings.TrimSpace(os.Getenv("SIGNERD_DATABASE"))
	if path == "" {
		fatalf("SIGNERD_DATABASE is required")
	}
	db, err := persistence.Open(path)
	if err != nil {
		fatalf("open signer journal: %v", err)
	}
	return db
}

func requiredMode() config.ModuleMode {
	if strings.EqualFold(os.Getenv("SIGNERD_MODE"), string(config.ModeHardware)) {
		return config.ModuleModeHardware
	}
	return config.ModuleModeTest
}

func singleID(args []string, command string) string {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fatalf("credentials %s requires exactly one <service-credential-id>", command)
	}
	return args[0]
}

func maskTail(value string, keep int, reveal bool) string {
	if reveal || value == "" {
		if value == "" {
			return "-"
		}
		return value
	}
	if len(value) <= keep {
		return strings.Repeat("*", len(value))
	}
	return "***" + value[len(value)-keep:]
}

func subjectDisplay(der []byte, reveal bool) string {
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return "***"
	}
	if reveal {
		return certificate.Subject.String()
	}
	fields := strings.Fields(certificate.Subject.CommonName)
	if len(fields) == 0 {
		return "***"
	}
	masked := make([]string, 0, len(fields))
	for _, field := range fields {
		masked = append(masked, string([]rune(field)[0])+"***")
	}
	return strings.Join(masked, " ")
}

func joinModes(modes []config.ModuleMode) string {
	parts := make([]string, len(modes))
	for i, mode := range modes {
		parts[i] = string(mode)
	}
	return strings.Join(parts, ",")
}

func hexToArray(value string) ([32]byte, error) {
	var out [32]byte
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return out, err
	}
	if len(decoded) != 32 {
		return out, fmt.Errorf("expected 32 bytes, got %d", len(decoded))
	}
	copy(out[:], decoded)
	return out, nil
}
