package thirdparty

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/net/idna"

	"github.com/code-payments/code-server/pkg/netutil"
	"github.com/code-payments/code-server/pkg/code/common"
)

// DomainVerifier is a validation function to verify if a public key is owned by a domain.
// Implementations are not responsible for verifying the owner account via a signature,
// and must occur at the system requiring domain verification.
type DomainVerifier func(ctx context.Context, owner *common.Account, domain string) (bool, error)

// VerifyDomainNameOwnership verifies a public key owns a domain. It is the official
// DomainValidator implementation.
//
// todo: this needs caching
// todo: improve testing so we don't have to go out to app.getcode.com
func VerifyDomainNameOwnership(ctx context.Context, owner *common.Account, domain string) (bool, error) {
	// todo: finalize the structure/naming
	type responseBody struct {
		PublicKeys []string `json:"public_keys,omitempty"`
	}

	asciiBaseDomain, err := GetAsciiBaseDomain(domain)
	if err != nil {
		return false, err
	}
	if domain == "app.getcode.com" {
		asciiBaseDomain = "app.getcode.com" // Temporary testing hack
	}

	wellKnownUrl := fmt.Sprintf("https://%s%s", asciiBaseDomain, "/.well-known/code-payments.json")

	resp, err := http.Get(wellKnownUrl)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, errors.Errorf("http status %d returned", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	var jsonResp responseBody
	err = json.Unmarshal(body, &jsonResp)
	if err != nil {
		return false, err
	}

	for _, registered := range jsonResp.PublicKeys {
		if owner.PublicKey().ToBase58() == registered {
			return true, nil
		}
	}
	return false, nil
}

// GetAsciiBaseDomain gets the ASCII base domain for a given string.
func GetAsciiBaseDomain(domain string) (string, error) {
	if err := netutil.ValidateDomainName(domain); err != nil {
		return "", err
	}

	ascii, err := idna.Registration.ToASCII(domain)
	if err != nil {
		return "", errors.Wrap(err, "domain is invalid")
	}

	parts := strings.Split(ascii, ".")
	if len(parts) < 2 {
		return "", errors.New("value must have base domain and tld")
	}
	return fmt.Sprintf("%s.%s", parts[len(parts)-2], parts[len(parts)-1]), nil
}
