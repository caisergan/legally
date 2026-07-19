//go:build pkcs11

// Command yargi-softhsm-provision is a TEST-ONLY helper that writes an
// RSA private-key object (viewable pre-login, CKA_PRIVATE=false, but
// CKA_SENSITIVE material) and its X.509 certificate into a SoftHSM token via
// C_CreateObject, so the reviewed enrollment path (token.Resolve, which
// enumerates objects without login) can bind the credential. It never touches
// the production signing path and only runs under the pkcs11 build tag. Real
// qualified tokens that hide private-key objects pre-login are out of scope.
package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"

	"github.com/miekg/pkcs11"
)

func main() {
	module := flag.String("module", "", "PKCS#11 module path")
	tokenLabel := flag.String("token-label", "", "target token label")
	pin := flag.String("pin", "", "user PIN")
	keyDER := flag.String("key-der", "", "PKCS#8 RSA private key (DER) path")
	certDER := flag.String("cert-der", "", "X.509 certificate (DER) path")
	idHex := flag.String("id", "", "CKA_ID (hex)")
	flag.Parse()
	if *module == "" || *tokenLabel == "" || *pin == "" || *keyDER == "" || *certDER == "" || *idHex == "" {
		log.Fatal("all of --module --token-label --pin --key-der --cert-der --id are required")
	}
	ckaID, err := hex.DecodeString(*idHex)
	if err != nil {
		log.Fatalf("invalid --id: %v", err)
	}

	priv, err := loadRSAKey(*keyDER)
	if err != nil {
		log.Fatalf("load key: %v", err)
	}
	certBytes, err := os.ReadFile(*certDER)
	if err != nil {
		log.Fatalf("read cert: %v", err)
	}
	cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		log.Fatalf("parse cert: %v", err)
	}

	ctx := pkcs11.New(*module)
	if ctx == nil {
		log.Fatal("load pkcs11 module")
	}
	if err := ctx.Initialize(); err != nil {
		log.Fatalf("initialize: %v", err)
	}
	defer func() { _ = ctx.Finalize(); ctx.Destroy() }()

	slot, err := findSlot(ctx, *tokenLabel)
	if err != nil {
		log.Fatalf("find token: %v", err)
	}
	session, err := ctx.OpenSession(slot, pkcs11.CKF_SERIAL_SESSION|pkcs11.CKF_RW_SESSION)
	if err != nil {
		log.Fatalf("open session: %v", err)
	}
	defer ctx.CloseSession(session)
	if err := ctx.Login(session, pkcs11.CKU_USER, *pin); err != nil {
		log.Fatalf("login: %v", err)
	}
	defer ctx.Logout(session)

	priv.Precompute()
	keyTemplate := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_PRIVATE_KEY),
		pkcs11.NewAttribute(pkcs11.CKA_KEY_TYPE, pkcs11.CKK_RSA),
		pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
		pkcs11.NewAttribute(pkcs11.CKA_PRIVATE, false),
		pkcs11.NewAttribute(pkcs11.CKA_SENSITIVE, true),
		pkcs11.NewAttribute(pkcs11.CKA_EXTRACTABLE, false),
		pkcs11.NewAttribute(pkcs11.CKA_SIGN, true),
		pkcs11.NewAttribute(pkcs11.CKA_ID, ckaID),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, "yargi-test-key"),
		pkcs11.NewAttribute(pkcs11.CKA_MODULUS, priv.N.Bytes()),
		pkcs11.NewAttribute(pkcs11.CKA_PUBLIC_EXPONENT, big.NewInt(int64(priv.E)).Bytes()),
		pkcs11.NewAttribute(pkcs11.CKA_PRIVATE_EXPONENT, priv.D.Bytes()),
		pkcs11.NewAttribute(pkcs11.CKA_PRIME_1, priv.Primes[0].Bytes()),
		pkcs11.NewAttribute(pkcs11.CKA_PRIME_2, priv.Primes[1].Bytes()),
		pkcs11.NewAttribute(pkcs11.CKA_EXPONENT_1, priv.Precomputed.Dp.Bytes()),
		pkcs11.NewAttribute(pkcs11.CKA_EXPONENT_2, priv.Precomputed.Dq.Bytes()),
		pkcs11.NewAttribute(pkcs11.CKA_COEFFICIENT, priv.Precomputed.Qinv.Bytes()),
	}
	if _, err := ctx.CreateObject(session, keyTemplate); err != nil {
		log.Fatalf("create private key object: %v", err)
	}

	certTemplate := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_CERTIFICATE),
		pkcs11.NewAttribute(pkcs11.CKA_CERTIFICATE_TYPE, pkcs11.CKC_X_509),
		pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
		pkcs11.NewAttribute(pkcs11.CKA_PRIVATE, false),
		pkcs11.NewAttribute(pkcs11.CKA_ID, ckaID),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, "yargi-test-cert"),
		pkcs11.NewAttribute(pkcs11.CKA_SUBJECT, cert.RawSubject),
		pkcs11.NewAttribute(pkcs11.CKA_VALUE, certBytes),
	}
	if _, err := ctx.CreateObject(session, certTemplate); err != nil {
		log.Fatalf("create certificate object: %v", err)
	}

	fmt.Printf("provisioned key+cert on token %q (CKA_ID=%s)\n", *tokenLabel, *idHex)
}

func loadRSAKey(path string) (*rsa.PrivateKey, error) {
	der, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not RSA")
	}
	return key, nil
}

func findSlot(ctx *pkcs11.Ctx, label string) (uint, error) {
	slots, err := ctx.GetSlotList(true)
	if err != nil {
		return 0, err
	}
	for _, slot := range slots {
		info, err := ctx.GetTokenInfo(slot)
		if err != nil {
			continue
		}
		if strings.TrimSpace(info.Label) == label {
			return slot, nil
		}
	}
	return 0, fmt.Errorf("token %q not found", label)
}
