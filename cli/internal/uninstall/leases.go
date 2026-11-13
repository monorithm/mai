package uninstall

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// KeySource gives the key of an account. The key file of the CLI (keys.File) is one.
type KeySource interface {
	Get(account string) (string, error)
}

// EndLeases ends each lease of the server through the owner API. It never fails: it gives a
// line of text that tells what it did. The bridges stop at their next call.
func EndLeases(ctx context.Context, hc *http.Client, base string, ks KeySource, instance string) string {
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Second}
	}
	if ks == nil {
		return "Leases: not ended (no key file)."
	}
	key, err := ks.Get(keys.OwnerAccount(instance))
	if err != nil {
		return "Leases: not ended (no owner key in the key file)."
	}
	do := func(method, path string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, base+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		return hc.Do(req)
	}
	resp, err := do(http.MethodGet, "/api/sessions")
	if err != nil {
		return "Leases: not ended (the server gives no answer)."
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Leases: not ended (the server gave HTTP %d).", resp.StatusCode)
	}
	var body struct {
		Sessions []struct {
			Login  string `json:"login"`
			Leased bool   `json:"leased"`
		} `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "Leases: not ended (the answer of the server is not valid)."
	}
	ended := 0
	for _, s := range body.Sessions {
		if !s.Leased {
			continue
		}
		r, err := do(http.MethodDelete, "/api/sessions/"+url.PathEscape(s.Login)+"/lease")
		if err != nil {
			continue
		}
		r.Body.Close()
		if r.StatusCode == http.StatusNoContent || r.StatusCode == http.StatusOK {
			ended++
		}
	}
	return fmt.Sprintf("Leases: %d ended.", ended)
}
