package uninstall

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
)

// movedStore keeps the items of keychain.Service in a test service, as the keychain of a
// test build with MAI_TEST_KEYCHAIN_SERVICE does.
type movedStore struct {
	*keychain.Memory
	to string
}

func (m movedStore) ServiceFor(service string) string {
	if service == keychain.Service {
		return m.to
	}
	return service
}

func (m movedStore) Set(service, account, secret string) error {
	return m.Memory.Set(m.ServiceFor(service), account, secret)
}

func (m movedStore) Get(service, account string) (string, error) {
	return m.Memory.Get(m.ServiceFor(service), account)
}

func (m movedStore) Delete(service, account string) error {
	return m.Memory.Delete(m.ServiceFor(service), account)
}

// The plan shows the keychain service from which the uninstall deletes the keys.
func TestThePlanShowsTheKeychainServiceThatTheStoreUses(t *testing.T) {
	e := setup(t)
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Keychain = movedStore{Memory: e.kc, to: "dev.mai.test.plan"}
	p, err := u.MakePlan()
	if err != nil {
		t.Fatal(err)
	}
	u.Show(context.Background(), p)
	text := out.String()
	if !strings.Contains(text, "keychain   dev.mai.test.plan / owner@mai") || strings.Contains(text, "keychain   dev.mai / ") {
		t.Fatalf("the plan does not show the test service:\n%s", text)
	}
}
