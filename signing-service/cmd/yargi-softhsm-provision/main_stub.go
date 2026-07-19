//go:build !pkcs11

package main

import "log"

func main() {
	log.Fatal("yargi-softhsm-provision requires the pkcs11 build tag (go run -tags pkcs11 ./cmd/yargi-softhsm-provision ...)")
}
