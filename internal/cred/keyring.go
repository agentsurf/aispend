package cred

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"

	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/dbg"
)

// service is the name aispend's entries appear under in the OS credential
// store. A user auditing their keychain should be able to find and delete every
// one of them by searching for this.
const service = "aispend"

// Store writes a credential to the OS credential store.
//
// The keychain is encrypted at rest by the operating system, audited by people
// far better at it than we are, and — the part that matters commercially — it
// is an answer a security reviewer already accepts. Nothing here writes to the
// database, a config file, or a log.
func Store(vendor, secret string) error {
	if secret == "" {
		return errors.New("refusing to store an empty credential")
	}
	if err := keyring.Set(service, vendor, secret); err != nil {
		return fmt.Errorf("could not save to the %s: %w", storeName(), err)
	}
	dbg.Printf("%s: credential saved to the %s", vendor, storeName())
	return nil
}

// Delete removes a vendor's entry. A missing entry is not an error: the caller
// wanted it gone, and it is.
func Delete(vendor string) (removed bool, err error) {
	switch err := keyring.Delete(service, vendor); {
	case err == nil:
		return true, nil
	case errors.Is(err, keyring.ErrNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("could not remove from the %s: %w", storeName(), err)
	}
}

// fromKeyring reads a vendor's stored credential.
//
// A keychain read can fail because the user declined the OS prompt, which is a
// choice rather than a fault, so it is reported at debug level and treated as
// "no credential" — the tool then says the vendor is not connected, which is
// true from its point of view.
func fromKeyring(vendor string) (Credential, bool) {
	secret, err := keyring.Get(service, vendor)
	switch {
	case err == nil && secret != "":
		return New(vendor, SourceKeyring, KeyringRef(vendor), secret), true
	case err != nil && !errors.Is(err, keyring.ErrNotFound):
		dbg.Printf("%s: could not read the %s: %v", vendor, storeName(), err)
	}
	return Credential{Vendor: vendor}, false
}

// Stored reports whether a vendor has an entry, without reading the secret.
// purge uses it to say exactly what it removed.
func Stored(vendor string) bool {
	_, ok := fromKeyring(vendor)
	return ok
}

// storeName is what the OS calls its credential store, so error messages say
// something the user recognises rather than "the keyring".
func storeName() string {
	switch config.GOOS() {
	case "darwin":
		return "macOS Keychain"
	case "windows":
		return "Windows Credential Manager"
	default:
		return "system keyring"
	}
}
